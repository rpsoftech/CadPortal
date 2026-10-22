package service

import (
	"context"
	"strings"
	"testing"
	"time"
)

func backupItem(t *testing.T, s *Service) *SetupItem {
	t.Helper()
	st, err := s.SetupStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range st.Items {
		if st.Items[i].Key == "backup" {
			return &st.Items[i]
		}
	}
	return nil
}

func TestSetupChecklistWarnsWhenBackupsStop(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	// Not watching: local development has no backup service.
	if backupItem(t, s) != nil {
		t.Fatal("the backup warning must be off unless BackupMaxAge is set")
	}

	s.BackupMaxAge = 36 * time.Hour
	if it := backupItem(t, s); it == nil || !strings.Contains(it.Title, "ever completed") {
		t.Fatalf("never backed up: %+v", it)
	}

	// A failed run: still a warning, and it says why.
	id, err := s.Q.StartBackupRun(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE backup_runs SET finished_at = now(), ok = false, error = 'files: boom' WHERE id = $1`, id); err != nil {
		t.Fatal(err)
	}
	if it := backupItem(t, s); it == nil || !strings.Contains(it.Detail, "files: boom") {
		t.Fatalf("failed run: %+v", it)
	}

	// A good run clears it.
	good, _ := s.Q.StartBackupRun(ctx)
	if _, err := s.Pool.Exec(ctx, `UPDATE backup_runs SET finished_at = now(), ok = true WHERE id = $1`, good); err != nil {
		t.Fatal(err)
	}
	if it := backupItem(t, s); it != nil {
		t.Fatalf("a fresh good backup must clear the warning: %+v", it)
	}

	// ...until it is older than the limit. A newer failure does not hide that.
	if _, err := s.Pool.Exec(ctx, `UPDATE backup_runs SET finished_at = now() - interval '2 days' WHERE id = $1`, good); err != nil {
		t.Fatal(err)
	}
	if it := backupItem(t, s); it == nil || it.Title != "Backups have stopped" || !strings.Contains(it.Detail, "files: boom") {
		t.Fatalf("stale backup: %+v", it)
	}

	// A run that was killed mid-way (never finished) is not a good backup either.
	if _, err := s.Pool.Exec(ctx, `UPDATE backup_runs SET ok = NULL WHERE id = $1`, good); err != nil {
		t.Fatal(err)
	}
	if it := backupItem(t, s); it == nil {
		t.Fatal("an unfinished run is not a backup")
	}
}
