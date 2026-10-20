package handlers

import (
	"encoding/json"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

func TestImportDesignsOverHTTP(t *testing.T) {
	r, _, _ := newHTTPEnv(t)
	url := "/api/v1/designs/import"
	good := []byte(`{"rows":[{"code":"LR300","name":"Ring","category":"Rings","volume_mm3":"200"}]}`)

	if w := do(r, "POST", url, "", "application/json", good); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Production), "application/json", good); w.Code != 403 {
		t.Errorf("production: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", []byte("nope")); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", []byte(`{"rows":[]}`)); w.Code != 400 {
		t.Errorf("no rows: %d", w.Code)
	}

	// Dry run: 200 and nothing created.
	dry := []byte(`{"dry_run":true,"rows":[{"code":"LR300","category":"Rings","volume_mm3":"200"}]}`)
	w := do(r, "POST", url, token(t, auth.Designer), "application/json", dry)
	if w.Code != 200 {
		t.Fatalf("dry run: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/designs/LR300", token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("dry run created the design: %d", w.Code)
	}

	// Rejected: 422 with the list of problems, nothing created.
	bad := []byte(`{"rows":[{"line":2,"code":"LR300","category":"Nope"},{"line":3,"code":"LR301","category":"Rings","volume_mm3":"x"}]}`)
	w = do(r, "POST", url, token(t, auth.Designer), "application/json", bad)
	var rej struct {
		Errors []struct {
			Line    int
			Message string
		}
	}
	if w.Code != 422 || json.Unmarshal(w.Body.Bytes(), &rej) != nil || len(rej.Errors) != 2 || rej.Errors[0].Line != 2 {
		t.Fatalf("rejected: %d %s", w.Code, w.Body)
	}

	// Real import: 201, then the same file is refused because the design now exists.
	w = do(r, "POST", url, token(t, auth.Designer), "application/json", good)
	if w.Code != 201 {
		t.Fatalf("import: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/designs/LR300", token(t, auth.Production), "", nil); w.Code != 200 {
		t.Errorf("imported design missing: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", good); w.Code != 422 {
		t.Errorf("repeat import: %d", w.Code)
	}

	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=design.import")
	if a := findWhere(rows, func(a auditRow) bool { return a.Status == 201 }); a == nil || a.Detail != "1 designs, 1 variations" {
		t.Errorf("audit row for the import = %+v", a)
	}
	if findWhere(rows, func(a auditRow) bool { return a.Status == 403 }) == nil {
		t.Error("the refused production attempt should be audited")
	}
}
