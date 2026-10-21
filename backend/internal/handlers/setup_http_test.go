package handlers

import (
	"encoding/json"
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
