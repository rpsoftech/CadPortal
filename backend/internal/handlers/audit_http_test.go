package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

type auditRow struct {
	ID         int64  `json:"id"`
	UserID     *int64 `json:"user_id"`
	UserEmail  string `json:"user_email"`
	Action     string `json:"action"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Status     int    `json:"status"`
	Detail     string `json:"detail"`
}

func fetchAudit(t *testing.T, r *gin.Engine, tok, query string) ([]auditRow, string) {
	t.Helper()
	w := do(r, "GET", "/api/v1/audit"+query, tok, "", nil)
	if w.Code != 200 {
		t.Fatalf("GET /audit%s: %d %s", query, w.Code, w.Body)
	}
	var rows []auditRow
	if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	return rows, w.Header().Get("X-Total-Count")
}

func findAction(rows []auditRow, action string) *auditRow {
	return findWhere(rows, func(r auditRow) bool { return r.Action == action })
}

// findWhere returns the newest row matching the predicate.
func findWhere(rows []auditRow, match func(auditRow) bool) *auditRow {
	for i := range rows {
		if match(rows[i]) {
			return &rows[i]
		}
	}
	return nil
}

func TestAuditRecordsActionsWithoutSecrets(t *testing.T) {
	r, svc := newAuthEnv(t)
	ctx := context.Background()
	adminUser := mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "production@example.com", "production")

	// Failed login (wrong password) then a real one.
	const badPW = "totally-wrong-password-x"
	login(t, r, "admin@example.com", badPW)
	adm := bearer(t, r, "admin@example.com")
	prod := bearer(t, r, "production@example.com")

	// An admin creates a user with a password in the request body.
	const newUserPW = "Sup3r-secret-new-user-pw"
	w := do(r, "POST", "/api/v1/users", adm, "application/json",
		jsonBody(map[string]string{"email": "new@example.com", "name": "N", "password": newUserPW, "role": "designer"}))
	if w.Code != 201 {
		t.Fatalf("create user: %d %s", w.Code, w.Body)
	}
	var created service.UserView
	json.Unmarshal(w.Body.Bytes(), &created) //nolint:errcheck

	// Edit a metal, create a design (and a variation), then something production may not do.
	m, _ := svc.Q.GetMetalByCode(ctx, "18KR")
	do(r, "PUT", "/api/v1/masters/metals/"+itoa(m.ID), adm, "application/json",
		jsonBody(map[string]any{"specific_gravity": "15.25", "wastage_pct": "12.5", "is_default_input": true, "active": true}))
	cats, _ := svc.ListCategories(ctx)
	dw := do(r, "POST", "/api/v1/designs", adm, "application/json", jsonBody(map[string]any{"code": "LR001", "name": "Ring", "category_id": cats[0].ID}))
	var design struct{ ID int64 }
	json.Unmarshal(dw.Body.Bytes(), &design) //nolint:errcheck
	vw := do(r, "POST", "/api/v1/designs/"+itoa(design.ID)+"/variations", adm, "application/json", jsonBody(map[string]string{"volume_mm3": "200"}))
	if vw.Code != 201 {
		t.Fatalf("create variation: %d %s", vw.Code, vw.Body)
	}
	denied := do(r, "POST", "/api/v1/designs", prod, "application/json", jsonBody(map[string]any{"code": "XX9", "category_id": cats[0].ID}))
	if denied.Code != 403 {
		t.Fatalf("production create design: %d", denied.Code)
	}
	// Reads and unauthenticated/unknown requests are not actions.
	do(r, "GET", "/api/v1/designs", adm, "", nil)
	do(r, "GET", "/api/v1/masters/metals", adm, "", nil)
	do(r, "POST", "/api/v1/designs", "", "application/json", []byte("{}")) // 401
	do(r, "GET", "/api/v1/no-such-route", adm, "", nil)                    // 404, unmatched

	rows, total := fetchAudit(t, r, adm, "?limit=200")

	if a := findAction(rows, "auth.login_failed"); a == nil || a.Status != 401 || a.UserID != nil || a.UserEmail != "admin@example.com" {
		t.Errorf("failed login = %+v", a)
	}
	if a := findWhere(rows, func(r auditRow) bool {
		return r.Action == "auth.login" && r.UserEmail == "admin@example.com"
	}); a == nil || a.Status != 200 || a.UserID == nil || *a.UserID != adminUser.ID {
		t.Errorf("admin login = %+v", a)
	}
	if a := findAction(rows, "user.create"); a == nil || a.EntityType != "user" || a.EntityID != itoa(created.ID) || !strings.Contains(a.Detail, "new@example.com") || a.UserEmail != "admin@example.com" {
		t.Errorf("user.create = %+v", a)
	}
	if a := findAction(rows, "metal.update"); a == nil || a.EntityID != itoa(m.ID) || !strings.Contains(a.Detail, "18KR") || !strings.Contains(a.Detail, "wastage=12.5") {
		t.Errorf("metal.update = %+v", a)
	}
	if a := findWhere(rows, func(r auditRow) bool { return r.Action == "design.create" && r.Status == 201 }); a == nil ||
		a.EntityID != itoa(design.ID) || a.Detail != "LR001" {
		t.Errorf("design.create = %+v", a)
	}
	if a := findAction(rows, "variation.create"); a == nil || a.EntityType != "variation" || a.Detail != "LR001-V1" {
		t.Errorf("variation.create = %+v", a)
	}
	// A refused attempt is recorded, with who tried it.
	refused := findWhere(rows, func(r auditRow) bool { return r.Action == "design.create" && r.Status == 403 })
	if refused == nil || refused.UserEmail != "production@example.com" || refused.UserID == nil {
		t.Errorf("refused attempt = %+v", refused)
	}

	// Not recorded: plain reads, the 401, the unmatched 404.
	for _, a := range rows {
		if strings.HasPrefix(a.Action, "get ") || a.Status == 401 && a.Action != "auth.login_failed" || strings.Contains(a.Action, "no-such-route") {
			t.Errorf("unexpected entry: %+v", a)
		}
	}
	if total == "" {
		t.Error("X-Total-Count missing")
	}

	// Secrets never appear anywhere in the table.
	for _, secret := range []string{badPW, newUserPW, "wastage_pct", "specific_gravity"} {
		var n int
		err := svc.Pool.QueryRow(ctx,
			`SELECT count(*) FROM audit_log WHERE user_email || action || entity_type || entity_id || detail LIKE '%' || $1 || '%'`, secret).Scan(&n)
		if err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%q found in %d audit rows", secret, n)
		}
	}
}

func TestAuditEndpointAccessFiltersAndPaging(t *testing.T) {
	r, svc := newAuthEnv(t)
	ctx := context.Background()
	mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "designer@example.com", "designer")
	adm := bearer(t, r, "admin@example.com")
	des := bearer(t, r, "designer@example.com")
	cats, _ := svc.ListCategories(ctx)
	for i := 1; i <= 5; i++ {
		do(r, "POST", "/api/v1/designs", des, "application/json",
			jsonBody(map[string]any{"code": "LR00" + itoa(int64(i)), "category_id": cats[0].ID}))
	}

	// Admin only.
	if w := do(r, "GET", "/api/v1/audit", des, "", nil); w.Code != 403 {
		t.Errorf("designer reading the audit log: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/audit", "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}

	rows, total := fetchAudit(t, r, adm, "?action=design.&limit=2")
	if len(rows) != 2 || total != "5" {
		t.Errorf("paged design entries: %d rows, total %s", len(rows), total)
	}
	rows, _ = fetchAudit(t, r, adm, "?action=design.&limit=2&offset=4")
	if len(rows) != 1 {
		t.Errorf("last page: %d rows", len(rows))
	}
	rows, total = fetchAudit(t, r, adm, "?action=auth.login")
	if total != "2" || len(rows) != 2 {
		t.Errorf("login entries: %d (total %s)", len(rows), total)
	}
	if rows, _ := fetchAudit(t, r, adm, "?action=design.&from=2999-01-01"); len(rows) != 0 {
		t.Errorf("from in the far future: %d rows", len(rows))
	}
	if rows, _ := fetchAudit(t, r, adm, "?action=design.&to=2999-01-01"); len(rows) != 5 {
		t.Errorf("to in the far future: %d rows", len(rows))
	}
	for _, bad := range []string{"?user_id=abc", "?limit=x", "?from=yesterday", "?to=2026-13-45"} {
		if w := do(r, "GET", "/api/v1/audit"+bad, adm, "", nil); w.Code != 400 {
			t.Errorf("GET /audit%s = %d, want 400", bad, w.Code)
		}
	}
}

func TestAuditFailureNeverBreaksARequest(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	adm := bearer(t, r, "admin@example.com")
	if _, err := svc.Pool.Exec(context.Background(), "DROP TABLE audit_log"); err != nil {
		t.Fatal(err)
	}
	// The write is audited, the audit insert fails, and the user still gets their 201.
	w := do(r, "POST", "/api/v1/masters/tags", adm, "application/json", jsonBody(map[string]string{"name": "Floral", "tag_group": "Motif"}))
	if w.Code != 201 {
		t.Errorf("a failing audit insert changed the response: %d %s", w.Code, w.Body)
	}
	// Login too.
	if code, _ := login(t, r, "admin@example.com", pw); code != 200 {
		t.Errorf("login with a broken audit table: %d", code)
	}
}

func TestUnlistedMutatingRoutesAreStillAudited(t *testing.T) {
	_, svc := newAuthEnv(t)
	gin.SetMode(gin.TestMode)
	h := &H{S: svc, JWTSecret: testSecret}
	e := gin.New()
	g := e.Group("/api/v1", h.audit())
	g.POST("/brand-new-endpoint/:id", func(c *gin.Context) { c.Status(204) })
	g.GET("/brand-new-read", func(c *gin.Context) { c.Status(200) })

	do(e, "POST", "/api/v1/brand-new-endpoint/7", "", "", nil)
	do(e, "GET", "/api/v1/brand-new-read", "", "", nil)

	rows, total, err := svc.ListAudit(context.Background(), service.AuditFilter{})
	if err != nil || total != 1 {
		t.Fatalf("entries=%d err=%v", total, err)
	}
	if rows[0].Action != "post /api/v1/brand-new-endpoint/:id" || rows[0].Status != 204 {
		t.Errorf("fallback entry = %+v", rows[0])
	}
}
