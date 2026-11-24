package main

import (
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/alert"
	"github.com/rpsoftech/cad-portal/backend/internal/backup"
)

func TestBackupMessageTellsGoodNewsAndAlertsOnFailureWithoutRepeating(t *testing.T) {
	now := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	cd := alert.Cooldown{Every: 6 * time.Hour}

	l, title, body, send := backupMessage(backup.Result{OK: true, DBBytes: 5 << 20, FilesCopied: 3, FilesBytes: 2 << 20, FilesTotal: 120}, true, &cd, now)
	if !send || l != alert.Info || title != "Backup finished" || !strings.Contains(body, "5.0 MB") || !strings.Contains(body, "3 new files") || !strings.Contains(body, "120 in the vault") {
		t.Errorf("good backup: %v %q %q %v", l, title, body, send)
	}
	if _, _, b, _ := backupMessage(backup.Result{OK: true, FilesTotal: 7}, true, &cd, now); !strings.Contains(b, "No new files (7") {
		t.Errorf("no new files: %q", b)
	}
	if _, _, b, _ := backupMessage(backup.Result{OK: true, FilesSkipped: true}, true, &cd, now); !strings.Contains(b, "not copied") {
		t.Errorf("files skipped: %q", b)
	}
	if _, _, _, send := backupMessage(backup.Result{OK: true}, false, &cd, now); send {
		t.Error("ALERT_BACKUP_OK=false must silence good backups")
	}

	bad := backup.Result{Err: "upload failed"}
	l, title, body, send = backupMessage(bad, false, &cd, now)
	if !send || l != alert.Critical || title != "Backup FAILED" || !strings.Contains(body, "upload failed") {
		t.Errorf("first failure: %v %q %q %v", l, title, body, send)
	}
	if _, _, _, send = backupMessage(bad, false, &cd, now.Add(30*time.Minute)); send {
		t.Error("a retry 30 minutes later must not message again")
	}
	if _, _, _, send = backupMessage(bad, false, &cd, now.Add(7*time.Hour)); !send {
		t.Error("still failing after 6 hours must message again")
	}
	backupMessage(backup.Result{OK: true}, false, &cd, now.Add(8*time.Hour)) // a good run ends the streak
	if _, _, _, send = backupMessage(bad, false, &cd, now.Add(9*time.Hour)); !send {
		t.Error("a new failure after a good run must message at once")
	}
	long := strings.Repeat("x", 2000)
	if _, _, b, _ := backupMessage(backup.Result{Err: long}, false, &alert.Cooldown{Every: time.Hour}, now); len(b) > 700 {
		t.Errorf("error text not cut: %d", len(b))
	}
}
