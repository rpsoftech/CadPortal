package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/backup"
)

type backupsBody struct {
	Runs  []struct{ ID int64 }
	Dumps []struct {
		Key, Name string
		Encrypted bool
	}
	DumpsNote   string `json:"dumps_note"`
	CanDownload bool   `json:"can_download"`
	Pending     bool
	Running     bool
}

func TestBackupsAreAdminOnly(t *testing.T) {
	r, _, _ := newHTTPEnv(t)
	for _, c := range []struct{ method, url string }{
		{"GET", "/api/v1/backups"}, {"POST", "/api/v1/backups/run"},
		{"GET", "/api/v1/backups/download?key=db/cad_portal-20261022-020000.dump.age"},
	} {
		if w := do(r, c.method, c.url, "", "", nil); w.Code != 401 {
			t.Errorf("%s %s anonymous: %d", c.method, c.url, w.Code)
		}
		for _, role := range []auth.Role{auth.Designer, auth.Production} {
			if w := do(r, c.method, c.url, token(t, role), "", nil); w.Code != 403 {
				t.Errorf("%s %s as %s: %d", c.method, c.url, role, w.Code)
			}
		}
	}
}

func TestBackupsScreenListsRunsAndDumpsAndOffersOnlyDumps(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	admin := token(t, auth.Admin)

	// Without a view of the bucket: history only, with a reason.
	w := do(r, "GET", "/api/v1/backups", admin, "", nil)
	var b backupsBody
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil || b.CanDownload || b.DumpsNote == "" || b.Dumps == nil {
		t.Fatalf("no bucket: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/backups/download?key=db/cad_portal-20261022-020000.dump.age", admin, "", nil); w.Code != 503 && w.Code != 501 && w.Code != 404 {
		t.Errorf("download without a bucket should be refused, got %d", w.Code)
	}

	bk := backup.NewMemory()
	put := func(k string) { _ = bk.Put(ctx, k, strings.NewReader("data"), backup.Meta{}) }
	put("db/cad_portal-20261021-020000.dump.age")
	put("db/cad_portal-20261022-020000.dump.age")
	put("db/notes.txt")
	put("files/cad/secret.3dm.gz")
	svc.BackupBucket = bk
	id, _ := svc.Q.StartBackupRun(ctx)
	_, _ = svc.Pool.Exec(ctx, `UPDATE backup_runs SET finished_at = now(), ok = true, files_copied = 3 WHERE id = $1`, id)

	w = do(r, "GET", "/api/v1/backups", admin, "", nil)
	b = backupsBody{}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &b) != nil {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !b.CanDownload || len(b.Runs) != 1 || len(b.Dumps) != 2 || b.Dumps[0].Name != "cad_portal-20261022-020000.dump.age" || !b.Dumps[0].Encrypted {
		t.Fatalf("listing wrong (newest first, dumps only): %s", w.Body)
	}

	// A link for a real dump; nothing else in the bucket can be fetched through it.
	w = do(r, "GET", "/api/v1/backups/download?key=db/cad_portal-20261021-020000.dump.age", admin, "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "url") || !strings.Contains(w.Body.String(), "cad_portal-20261021-020000.dump.age") {
		t.Fatalf("download link: %d %s", w.Code, w.Body)
	}
	for _, key := range []string{"files/cad/secret.3dm.gz", "db/notes.txt", "../x", "", "db/cad_portal-20990101-000000.dump.age"} {
		w = do(r, "GET", "/api/v1/backups/download?key="+key, admin, "", nil)
		if w.Code != 400 && w.Code != 404 {
			t.Errorf("key %q must be refused, got %d %s", key, w.Code, w.Body)
		}
	}

	// The download is a sensitive read: it is in the audit log, with the dump's name.
	rows, _ := fetchAudit(t, r, admin, "?action=backup")
	if a := findWhere(rows, func(r auditRow) bool { return r.Action == "backup.download_link" && r.Status == 200 }); a == nil || !strings.Contains(a.Detail, "20261021") {
		t.Errorf("backup download was not audited: %+v", rows)
	}
}

func TestBackUpNowQueuesOneRequestAtATime(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	admin := token(t, auth.Admin)

	if w := do(r, "POST", "/api/v1/backups/run", admin, "", nil); w.Code != 202 {
		t.Fatalf("first request: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", "/api/v1/backups/run", admin, "", nil); w.Code != 409 {
		t.Errorf("a second request while one waits must be refused, got %d", w.Code)
	}
	var b backupsBody
	w := do(r, "GET", "/api/v1/backups", admin, "", nil)
	if json.Unmarshal(w.Body.Bytes(), &b) != nil || !b.Pending {
		t.Errorf("the screen should show the request as waiting: %s", w.Body)
	}

	// The backup service claims it; a new request is possible again.
	if n, err := svc.Q.ClaimBackupRequests(ctx); err != nil || n != 1 {
		t.Fatalf("claim: %d %v", n, err)
	}
	// ...unless a backup is running right now.
	if _, err := svc.Q.StartBackupRun(ctx); err != nil {
		t.Fatal(err)
	}
	if w := do(r, "POST", "/api/v1/backups/run", admin, "", nil); w.Code != 409 || !strings.Contains(w.Body.String(), "running") {
		t.Errorf("while a backup runs: %d %s", w.Code, w.Body)
	}
	_, _ = svc.Pool.Exec(ctx, `UPDATE backup_runs SET finished_at = now(), ok = true`)
	if w := do(r, "POST", "/api/v1/backups/run", admin, "", nil); w.Code != 202 {
		t.Errorf("after it finished: %d %s", w.Code, w.Body)
	}
	rows, _ := fetchAudit(t, r, admin, "?action=backup")
	if findAction(rows, "backup.request") == nil {
		t.Error("the request is not in the audit log")
	}
}
