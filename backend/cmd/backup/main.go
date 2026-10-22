// Command backup is the portal's backup service. Run with no arguments it stays up and, every night,
// dumps the database and copies new CAD files, photos and videos to a second bucket. The other commands
// are for the day something has to be restored.
//
//	backup                  run the nightly schedule (the container's default)
//	backup once             take one backup now and exit
//	backup list             list the dumps in the backup bucket
//	backup fetch KEY OUT    download a dump to OUT (- for standard output), decrypting it with the secret key
//	backup restore-files    copy files the vault lacks back from the backup bucket
//	backup keygen           make the key pair that encrypts the dumps
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rpsoftech/cad-portal/backend/internal/backup"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

func main() {
	log.SetFlags(log.LstdFlags | log.LUTC)
	cmd := "run"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var err error
	switch cmd {
	case "run":
		err = runDaemon(ctx)
	case "once":
		err = runOnce(ctx)
	case "list":
		err = list(ctx)
	case "fetch":
		err = fetch(ctx, os.Args[2:])
	case "restore-files":
		err = restoreFiles(ctx)
	case "keygen":
		err = keygen()
	default:
		err = fmt.Errorf("unknown command %q (run, once, list, fetch, restore-files, keygen)", cmd)
	}
	if err != nil {
		log.Fatalf("backup: %v", err)
	}
}

func env(name, def string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return def
}

func atoi(name string, def int) (int, error) {
	v := env(name, "")
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > 1000 {
		return 0, fmt.Errorf("%s must be a whole number, got %q", name, v)
	}
	return n, nil
}

// destStore is the backup bucket. Its own credentials are required: a token that can write backups must
// not be the one that can delete the vault, so it is never inferred from the vault's.
func destStore() (*backup.S3, error) {
	missing := []string{}
	for _, n := range []string{"BACKUP_S3_BUCKET", "BACKUP_S3_ACCESS_KEY_ID", "BACKUP_S3_SECRET_ACCESS_KEY"} {
		if env(n, "") == "" {
			missing = append(missing, n)
		}
	}
	endpoint := env("BACKUP_S3_ENDPOINT", env("S3_ENDPOINT", ""))
	if endpoint == "" {
		missing = append(missing, "BACKUP_S3_ENDPOINT")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("the backup bucket is not configured: set %s", strings.Join(missing, ", "))
	}
	if strings.ContainsAny(endpoint, "<>") {
		return nil, fmt.Errorf("the endpoint still holds the placeholder from .env.example (%q)", endpoint)
	}
	if env("BACKUP_S3_BUCKET", "") == env("S3_BUCKET", "-") && endpoint == env("S3_ENDPOINT", "") {
		return nil, errors.New("BACKUP_S3_BUCKET is the same bucket as S3_BUCKET: a backup in the same bucket is lost with it")
	}
	return backup.NewS3(backup.S3Config{
		Endpoint: endpoint, Region: env("BACKUP_S3_REGION", env("S3_REGION", "auto")), Bucket: env("BACKUP_S3_BUCKET", ""),
		AccessKeyID: env("BACKUP_S3_ACCESS_KEY_ID", ""), SecretAccessKey: env("BACKUP_S3_SECRET_ACCESS_KEY", ""),
	})
}

// sourceStore is the CAD vault; nil when no bucket is configured (then only the database is backed up).
func sourceStore() (*backup.S3, error) {
	if env("S3_BUCKET", "") == "" {
		return nil, nil
	}
	return backup.NewS3(backup.S3Config{
		Endpoint: env("S3_ENDPOINT", ""), Region: env("S3_REGION", "auto"), Bucket: env("S3_BUCKET", ""),
		AccessKeyID: env("S3_ACCESS_KEY_ID", ""), SecretAccessKey: env("S3_SECRET_ACCESS_KEY", ""),
	})
}

// recipient is the public key that dumps are encrypted to. Dumps hold every password hash, the audit
// log, wastage and stone rates, so plain dumps need an explicit BACKUP_UNENCRYPTED=true.
func recipient() (age.Recipient, error) {
	pub := env("BACKUP_AGE_RECIPIENT", "")
	if pub == "" {
		if env("BACKUP_UNENCRYPTED", "") == "true" {
			log.Print("WARNING: database dumps are stored unencrypted (BACKUP_UNENCRYPTED=true)")
			return nil, nil
		}
		return nil, errors.New("BACKUP_AGE_RECIPIENT is not set: run `backup keygen`, keep the secret key offline, and put the public key in BACKUP_AGE_RECIPIENT (or set BACKUP_UNENCRYPTED=true to store dumps in the clear)")
	}
	r, err := age.ParseX25519Recipient(pub)
	if err != nil {
		return nil, fmt.Errorf("BACKUP_AGE_RECIPIENT is not a valid public key: %w", err)
	}
	return r, nil
}

func policy() (backup.Policy, error) {
	var p backup.Policy
	var err error
	if p.Daily, err = atoi("BACKUP_KEEP_DAILY", backup.DefaultPolicy.Daily); err != nil {
		return p, err
	}
	if p.Weekly, err = atoi("BACKUP_KEEP_WEEKLY", backup.DefaultPolicy.Weekly); err != nil {
		return p, err
	}
	p.Monthly, err = atoi("BACKUP_KEEP_MONTHLY", backup.DefaultPolicy.Monthly)
	return p, err
}

// dbRecorder writes each run into backup_runs, where the portal's setup checklist reads it.
type dbRecorder struct{ q *db.Queries }

func (d dbRecorder) Start(ctx context.Context) (int64, error) { return d.q.StartBackupRun(ctx) }
func (d dbRecorder) Finish(ctx context.Context, id int64, r backup.Result) error {
	return d.q.FinishBackupRun(ctx, db.FinishBackupRunParams{
		ID: id, Ok: pgtype.Bool{Bool: r.OK, Valid: true}, DbKey: r.DBKey, DbBytes: r.DBBytes, FilesCopied: int32(r.FilesCopied),
		FilesBytes: r.FilesBytes, FilesTotal: int32(r.FilesTotal), FilesSkipped: r.FilesSkipped, Error: r.Err,
	})
}

func newRunner(ctx context.Context) (*backup.Runner, *pgxpool.Pool, error) {
	dbURL := env("DATABASE_URL", "")
	if dbURL == "" {
		return nil, nil, errors.New("DATABASE_URL is required")
	}
	dest, err := destStore()
	if err != nil {
		return nil, nil, err
	}
	rcpt, err := recipient()
	if err != nil {
		return nil, nil, err
	}
	pol, err := policy()
	if err != nil {
		return nil, nil, err
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, nil, err
	}
	r := &backup.Runner{
		Rec: dbRecorder{db.New(pool)}, Dest: dest, Dump: backup.PgDump(dbURL), Verify: backup.PgVerify,
		Recipient: rcpt, Policy: pol, TmpDir: env("BACKUP_TMP_DIR", os.TempDir()), Logf: log.Printf,
	}
	src, err := sourceStore()
	if err != nil {
		pool.Close()
		return nil, nil, err
	}
	if src != nil {
		r.Source = src
	}
	return r, pool, nil
}

func runOnce(ctx context.Context) error {
	r, pool, err := newRunner(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	if res := r.Run(ctx); !res.OK {
		return errors.New(res.Err)
	}
	return nil
}

func runDaemon(ctx context.Context) error {
	r, pool, err := newRunner(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	hour, min, err := backup.ParseTimeOfDay(env("BACKUP_AT", "02:00"))
	if err != nil {
		return err
	}
	loc, err := time.LoadLocation(env("BACKUP_TZ", "UTC"))
	if err != nil {
		return fmt.Errorf("BACKUP_TZ: %w", err)
	}
	q := db.New(pool)

	// After a restart or an outage, do not wait for the next night if the last good backup is old.
	last, err := q.LastGoodBackupRun(ctx)
	if err != nil || !last.FinishedAt.Valid || time.Since(last.FinishedAt.Time) >= 26*time.Hour {
		log.Print("no backup in the last 26 hours: taking one now")
		time.Sleep(10 * time.Second) // let the database finish starting
		r.Run(ctx)
	}
	for {
		next := backup.NextRun(time.Now(), hour, min, loc)
		log.Printf("next backup at %s", next.In(loc).Format("2006-01-02 15:04 MST"))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(next)):
			r.Run(ctx)
		}
	}
}

func list(ctx context.Context) error {
	dest, err := destStore()
	if err != nil {
		return err
	}
	objs, err := dest.List(ctx, "db/")
	if err != nil {
		return err
	}
	for _, o := range objs {
		fmt.Printf("%s\t%d bytes\n", o.Key, o.Size)
	}
	if len(objs) == 0 {
		fmt.Println("no dumps yet")
	}
	return nil
}

func fetch(ctx context.Context, args []string) error {
	if len(args) != 2 {
		return errors.New("usage: backup fetch KEY OUT_FILE   (KEY as shown by `backup list`)")
	}
	dest, err := destStore()
	if err != nil {
		return err
	}
	rc, _, err := dest.Get(ctx, args[0])
	if err != nil {
		return err
	}
	defer rc.Close()
	out := os.Stdout // OUT_FILE of - writes to standard output, for `docker compose run -T backup fetch KEY - > file`
	if args[1] != "-" {
		f, err := os.OpenFile(args[1], os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return err
		}
		defer f.Close()
		out = f
	}
	if backup.IsEncrypted(args[0]) {
		secret := env("BACKUP_AGE_IDENTITY", "")
		if secret == "" {
			if f := env("BACKUP_AGE_IDENTITY_FILE", ""); f != "" {
				b, err := os.ReadFile(f)
				if err != nil {
					return err
				}
				secret = string(b)
			}
		}
		if secret == "" {
			return errors.New("this dump is encrypted: set BACKUP_AGE_IDENTITY_FILE to the file holding the secret key")
		}
		plain, err := backup.Decrypt(rc, secret)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, plain)
		return err
	}
	_, err = io.Copy(out, rc)
	return err
}

func restoreFiles(ctx context.Context) error {
	dest, err := destStore()
	if err != nil {
		return err
	}
	vault, err := sourceStore()
	if err != nil {
		return err
	}
	if vault == nil {
		return errors.New("S3_BUCKET (the vault to restore into) is not set")
	}
	n, err := backup.RestoreFiles(ctx, dest, vault, log.Printf)
	if err != nil {
		return err
	}
	log.Printf("restored %d files", n)
	return nil
}

func keygen() error {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return err
	}
	fmt.Printf("# created: %s\n# public key: %s\n%s\n", time.Now().UTC().Format(time.RFC3339), id.Recipient(), id)
	fmt.Fprintln(os.Stderr, "\nKeep the AGE-SECRET-KEY line OFFLINE (password manager, printed copy). Without it no dump can be opened.")
	fmt.Fprintf(os.Stderr, "Put only the public key in .env:  BACKUP_AGE_RECIPIENT=%s\n", id.Recipient())
	return nil
}
