package service

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/backup"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// BackupBucket is the read-only view of the backup bucket that the portal gets (list dumps, make a
// download link). It never writes or deletes there; only the backup service holds a token that can.
type BackupBucket interface {
	List(ctx context.Context, prefix string) ([]backup.Object, error)
	PresignGet(ctx context.Context, key, filename string, ttl time.Duration) (string, error)
}

// BackupRunView is one run of the backup service as the Backups screen shows it.
type BackupRunView struct {
	ID           int64      `json:"id"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	OK           *bool      `json:"ok"` // null while running (or if the run was killed)
	DBBytes      int64      `json:"db_bytes"`
	FilesCopied  int32      `json:"files_copied"`
	FilesBytes   int64      `json:"files_bytes"`
	FilesTotal   int32      `json:"files_total"`
	FilesSkipped bool       `json:"files_skipped"`
	Error        string     `json:"error"`
}

// BackupDumpView is a database dump found in the backup bucket.
type BackupDumpView struct {
	Key       string    `json:"key"`
	Name      string    `json:"name"`
	TakenAt   time.Time `json:"taken_at"`
	Bytes     int64     `json:"bytes"`
	Encrypted bool      `json:"encrypted"`
}

type BackupsView struct {
	Runs []BackupRunView `json:"runs"`
	// Dumps is empty when the portal cannot see the bucket; DumpsNote then says why.
	Dumps     []BackupDumpView `json:"dumps"`
	DumpsNote string           `json:"dumps_note"`
	// CanDownload is true when the portal holds a read token for the backup bucket.
	CanDownload bool `json:"can_download"`
	// Pending: a "Back up now" is waiting for the backup service. Running: a backup is in progress.
	Pending bool `json:"pending"`
	Running bool `json:"running"`
}

const backupRunsShown = 30

func runView(r db.BackupRun) BackupRunView {
	v := BackupRunView{
		ID: r.ID, StartedAt: r.StartedAt.Time, DBBytes: r.DbBytes, FilesCopied: r.FilesCopied,
		FilesBytes: r.FilesBytes, FilesTotal: r.FilesTotal, FilesSkipped: r.FilesSkipped, Error: r.Error,
	}
	if r.FinishedAt.Valid {
		t := r.FinishedAt.Time
		v.FinishedAt = &t
	}
	if r.Ok.Valid {
		ok := r.Ok.Bool
		v.OK = &ok
	}
	return v
}

// Backups returns the run history and, when the portal can see the backup bucket, the stored dumps.
// Admin only: a dump holds every password hash and the rates.
func (s *Service) Backups(ctx context.Context) (BackupsView, error) {
	runs, err := s.Q.ListBackupRuns(ctx, backupRunsShown)
	if err != nil {
		return BackupsView{}, mapErr(err)
	}
	out := BackupsView{Runs: make([]BackupRunView, 0, len(runs)), Dumps: []BackupDumpView{}, CanDownload: s.BackupBucket != nil}
	for _, r := range runs {
		out.Runs = append(out.Runs, runView(r))
	}
	if out.Pending, err = s.Q.HasPendingBackupRequest(ctx); err != nil {
		return BackupsView{}, mapErr(err)
	}
	if out.Running, err = s.Q.BackupInProgress(ctx); err != nil {
		return BackupsView{}, mapErr(err)
	}
	if s.BackupBucket == nil {
		out.DumpsNote = "The portal cannot see the backup bucket: set BACKUP_S3_ENDPOINT, BACKUP_S3_BUCKET and a read-only token (BACKUP_VIEW_ACCESS_KEY_ID, BACKUP_VIEW_SECRET_ACCESS_KEY) on the api service to list and download dumps here."
		return out, nil
	}
	objs, err := s.BackupBucket.List(ctx, "db/")
	if err != nil {
		out.DumpsNote = "The backup bucket could not be read: " + err.Error()
		return out, nil
	}
	for _, o := range objs {
		if d, ok := backup.ParseDump(o.Key); ok {
			out.Dumps = append(out.Dumps, BackupDumpView{
				Key: o.Key, Name: path.Base(o.Key), TakenAt: d.Time, Bytes: o.Size, Encrypted: backup.IsEncrypted(o.Key),
			})
		}
	}
	sort.Slice(out.Dumps, func(i, j int) bool { return out.Dumps[i].TakenAt.After(out.Dumps[j].TakenAt) })
	return out, nil
}

// RequestBackup asks the backup service to take a backup now. It returns ErrConflict if one is already
// waiting or running, so a second click does nothing.
func (s *Service) RequestBackup(ctx context.Context, userID int64) error {
	running, err := s.Q.BackupInProgress(ctx)
	if err != nil {
		return mapErr(err)
	}
	if running {
		return fmt.Errorf("%w: a backup is already running", ErrConflict)
	}
	_, err = s.Q.RequestBackup(ctx, pgtype.Int8{Int64: userID, Valid: userID > 0})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) { // ON CONFLICT DO NOTHING returned no row: one is already waiting
			return fmt.Errorf("%w: a backup is already requested and waiting for the backup service", ErrConflict)
		}
		return mapErr(err)
	}
	return nil
}

// BackupDownload makes a short-lived link to one stored dump. Only keys that are dumps are accepted, so
// the link cannot be used to fetch anything else from the bucket. The file is still encrypted: opening
// it needs the secret key that is kept offline.
func (s *Service) BackupDownload(ctx context.Context, key string) (DownloadLink, error) {
	if s.BackupBucket == nil {
		return DownloadLink{}, ErrStorageDisabled
	}
	if _, ok := backup.ParseDump(key); !ok {
		return DownloadLink{}, invalid("key is not a database dump")
	}
	objs, err := s.BackupBucket.List(ctx, key)
	if err != nil {
		return DownloadLink{}, err
	}
	found := false
	for _, o := range objs {
		found = found || o.Key == key
	}
	if !found {
		return DownloadLink{}, ErrNotFound
	}
	ttl := s.PresignTTL
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	name := path.Base(key)
	url, err := s.BackupBucket.PresignGet(ctx, key, name, ttl)
	if err != nil {
		return DownloadLink{}, err
	}
	return DownloadLink{URL: url, Filename: name, ExpiresIn: int(ttl.Seconds())}, nil
}
