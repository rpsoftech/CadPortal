package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
)

// Result is what one run did; it is stored in backup_runs for the portal to show.
type Result struct {
	OK           bool
	DBKey        string
	DBBytes      int64
	FilesCopied  int
	FilesBytes   int64
	FilesTotal   int
	FilesSkipped bool
	Err          string
}

// Recorder keeps the run history (the portal's backup_runs table).
type Recorder interface {
	Start(ctx context.Context) (int64, error)
	Finish(ctx context.Context, id int64, r Result) error
}

// Runner takes one backup. Dump writes a database dump (pg_dump -Fc) and Verify checks a dump file can
// be read back; both are plain functions so tests need no Postgres.
type Runner struct {
	Rec    Recorder
	Source Store // the CAD vault bucket; nil when there is none (local development): files are skipped
	Dest   Store // the backup bucket
	Dump   func(ctx context.Context, dst io.Writer) error
	Verify func(ctx context.Context, path string) error
	// Recipient encrypts dumps; nil writes them as they are.
	Recipient age.Recipient
	Policy    Policy
	Now       func() time.Time
	TmpDir    string
	Workers   int
	Logf      func(format string, a ...any)
}

func (r *Runner) logf(format string, a ...any) {
	if r.Logf != nil {
		r.Logf(format, a...)
	}
}

// Run takes a database dump, thins old dumps, and copies new files. The two halves are independent: a
// failed file copy never stops the database dump. Failures are recorded and returned in Result.Err.
func (r *Runner) Run(ctx context.Context) Result {
	id, recErr := r.Rec.Start(ctx)
	if recErr != nil {
		// The database being unreachable is exactly when the dump will fail too; say so and carry on.
		r.logf("could not record the start of the run: %v", recErr)
	}
	var res Result
	var errs []string

	key, n, err := r.backupDB(ctx)
	res.DBKey, res.DBBytes = key, n
	if err != nil {
		errs = append(errs, "database: "+err.Error())
		r.logf("database backup failed: %v", err)
	} else {
		r.logf("database dump stored as %s (%d bytes)", key, n)
		if err := r.prune(ctx); err != nil {
			r.logf("pruning old dumps failed (storage just grows until it works): %v", err)
		}
	}

	if r.Source == nil {
		res.FilesSkipped = true
		r.logf("no file storage configured: only the database is backed up")
	} else {
		copied, bytes, total, err := r.syncFiles(ctx)
		res.FilesCopied, res.FilesBytes, res.FilesTotal = copied, bytes, total
		if err != nil {
			errs = append(errs, "files: "+err.Error())
			r.logf("file backup failed: %v", err)
		} else {
			r.logf("files: %d new (%d bytes), %d in the backup", copied, bytes, total)
		}
	}

	res.OK = len(errs) == 0
	res.Err = strings.Join(errs, "; ")
	if recErr == nil {
		// The run may have been cancelled; the record of it should still be written.
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := r.Rec.Finish(fctx, id, res); err != nil {
			r.logf("could not record the end of the run: %v", err)
		}
	}
	return res
}

func (r *Runner) backupDB(ctx context.Context) (key string, size int64, err error) {
	f, err := os.CreateTemp(r.TmpDir, "dump-*.partial")
	if err != nil {
		return "", 0, err
	}
	defer os.Remove(f.Name())
	defer f.Close()

	if err := r.Dump(ctx, f); err != nil {
		return "", 0, fmt.Errorf("pg_dump: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return "", 0, err
	}
	if st.Size() == 0 {
		return "", 0, errors.New("the dump is empty")
	}
	// A dump that cannot be read back is not a backup: check it before it replaces anything.
	if err := r.Verify(ctx, f.Name()); err != nil {
		return "", 0, fmt.Errorf("the dump is unreadable: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", 0, err
	}

	key = DumpKey(r.now(), r.Recipient != nil)
	var body io.Reader = f
	if r.Recipient != nil {
		pr, pw := io.Pipe()
		go func() {
			w, err := age.Encrypt(pw, r.Recipient)
			if err == nil {
				_, err = io.Copy(w, f)
			}
			if err == nil {
				err = w.Close()
			}
			pw.CloseWithError(err)
		}()
		defer pr.Close()
		body = pr
	}
	// pg_dump -Fc output is already compressed, so it is stored as it is.
	if err := r.Dest.Put(ctx, key, body, Meta{ContentType: "application/octet-stream"}); err != nil {
		return "", 0, fmt.Errorf("upload: %w", err)
	}
	objs, err := r.Dest.List(ctx, key)
	if err != nil {
		return "", 0, fmt.Errorf("checking the upload: %w", err)
	}
	for _, o := range objs {
		if o.Key == key && o.Size > 0 {
			return key, st.Size(), nil
		}
	}
	return "", 0, fmt.Errorf("%s is not in the backup bucket after uploading", key)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) prune(ctx context.Context) error {
	objs, err := r.Dest.List(ctx, dumpPrefix)
	if err != nil {
		return err
	}
	var dumps []Dump
	for _, o := range objs {
		if d, ok := ParseDump(o.Key); ok {
			dumps = append(dumps, d)
		}
	}
	_, drop := r.Policy.Prune(dumps)
	for _, d := range drop {
		if err := r.Dest.Delete(ctx, d.Key); err != nil {
			return err
		}
		r.logf("pruned %s", d.Key)
	}
	return nil
}

// skipPrefix is where the R2 integration tests write; those objects are not data.
const skipPrefix = "r2-test/"

// syncFiles copies every object of the vault that the backup lacks (or has with a different size). Stored
// objects have random names and are never rewritten, so this is incremental by construction. Nothing is
// ever deleted from the backup: a file removed in the portal stays in the backup.
func (r *Runner) syncFiles(ctx context.Context) (copied int, copiedBytes int64, total int, err error) {
	src, err := r.Source.List(ctx, "")
	if err != nil {
		return 0, 0, 0, fmt.Errorf("listing the vault: %w", err)
	}
	have, err := r.sizes(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	var todo []Object
	for _, o := range src {
		if strings.HasPrefix(o.Key, skipPrefix) {
			continue
		}
		total++
		if size, ok := have[o.Key]; !ok || size != o.Size {
			todo = append(todo, o)
		}
	}

	workers := r.Workers
	if workers < 1 {
		workers = 4
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		failed int
		first  error
	)
	sem := make(chan struct{}, workers)
	for _, o := range todo {
		if ctx.Err() != nil {
			break
		}
		sem <- struct{}{}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			err := r.copyOne(ctx, o.Key)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				failed++
				if first == nil {
					first = fmt.Errorf("%s: %w", o.Key, err)
				}
				return
			}
			copied++
			copiedBytes += o.Size
		}()
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return copied, copiedBytes, total, err
	}
	if failed > 0 {
		return copied, copiedBytes, total, fmt.Errorf("%d of %d new files could not be copied, first: %w", failed, len(todo), first)
	}
	return copied, copiedBytes, total, nil
}

func (r *Runner) sizes(ctx context.Context) (map[string]int64, error) {
	objs, err := r.Dest.List(ctx, filesPrefix)
	if err != nil {
		return nil, fmt.Errorf("listing the backup: %w", err)
	}
	m := make(map[string]int64, len(objs))
	for _, o := range objs {
		m[strings.TrimPrefix(o.Key, filesPrefix)] = o.Size
	}
	return m, nil
}

func (r *Runner) copyOne(ctx context.Context, key string) error {
	rc, meta, err := r.Source.Get(ctx, key)
	if err != nil {
		return err
	}
	defer rc.Close()
	return r.Dest.Put(ctx, filesPrefix+key, rc, meta)
}

// RestoreFiles copies every object of the backup that the vault lacks back into the vault, with the same
// type and encoding. It never overwrites or deletes anything in the vault.
func RestoreFiles(ctx context.Context, backup, vault Store, logf func(string, ...any)) (restored int, err error) {
	objs, err := backup.List(ctx, filesPrefix)
	if err != nil {
		return 0, err
	}
	live, err := vault.List(ctx, "")
	if err != nil {
		return 0, err
	}
	have := make(map[string]bool, len(live))
	for _, o := range live {
		have[o.Key] = true
	}
	for _, o := range objs {
		key := strings.TrimPrefix(o.Key, filesPrefix)
		if have[key] {
			continue
		}
		rc, meta, err := backup.Get(ctx, o.Key)
		if err != nil {
			return restored, err
		}
		err = vault.Put(ctx, key, rc, meta)
		rc.Close()
		if err != nil {
			return restored, fmt.Errorf("%s: %w", key, err)
		}
		restored++
		logf("restored %s", key)
	}
	return restored, nil
}

// Decrypt opens an age-encrypted dump with the secret key text (the AGE-SECRET-KEY-… line, possibly with
// comments around it, as age-keygen and "backup keygen" write it).
func Decrypt(r io.Reader, identity string) (io.Reader, error) {
	ids, err := age.ParseIdentities(strings.NewReader(identity))
	if err != nil {
		return nil, fmt.Errorf("the secret key is not valid: %w", err)
	}
	return age.Decrypt(r, ids...)
}
