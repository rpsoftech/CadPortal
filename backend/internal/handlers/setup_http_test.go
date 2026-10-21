package handlers

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

func TestSetupStatusIsAdminOnly(t *testing.T) {
	r, _, _ := newHTTPEnv(t)
	url := "/api/v1/masters/setup-status"
	if w := do(r, "GET", url, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	// It talks about wastage and rates, which only admins may see.
	for _, role := range []auth.Role{auth.Designer, auth.Production} {
		if w := do(r, "GET", url, token(t, role), "", nil); w.Code != 403 {
			t.Errorf("%s: %d", role, w.Code)
		}
	}
	w := do(r, "GET", url, token(t, auth.Admin), "", nil)
	if w.Code != 200 {
		t.Fatalf("admin: %d %s", w.Code, w.Body)
	}
	var st struct {
		Complete bool
		Items    []struct{ Key, Severity, Title, Detail, Tab string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Complete || len(st.Items) == 0 {
		t.Fatalf("a fresh database is not set up: %s", w.Body)
	}
	for _, it := range st.Items {
		if it.Key == "" || it.Title == "" || it.Detail == "" || it.Tab == "" || it.Severity == "" {
			t.Errorf("incomplete item %+v", it)
		}
	}
}

func TestMasterDataExportIsAdminOnlyAndComplete(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	url := "/api/v1/masters/export"
	if w := do(r, "GET", url, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	// It holds wastage and rates, so only admins may download it.
	for _, role := range []auth.Role{auth.Designer, auth.Production} {
		if w := do(r, "GET", url, token(t, role), "", nil); w.Code != 403 {
			t.Errorf("%s: %d", role, w.Code)
		}
	}
	ringsPrefix(t, svc)
	w := do(r, "GET", url, token(t, auth.Admin), "", nil)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("admin: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "master-data-") || !strings.HasSuffix(cd, `.zip"`) {
		t.Errorf("disposition = %q", cd)
	}
	zr, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	for _, n := range []string{"alloys.csv", "stone_codes.csv", "stone_matrix.csv", "categories.csv", "tags.csv", "README.txt"} {
		if !names[n] {
			t.Errorf("missing %s", n)
		}
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=export.master_data")
	if findWhere(rows, func(a auditRow) bool { return a.Status == 200 }) == nil {
		t.Error("the export should be audited")
	}
	if findWhere(rows, func(a auditRow) bool { return a.Status == 403 }) == nil {
		t.Error("the refused attempts should be audited")
	}
}
