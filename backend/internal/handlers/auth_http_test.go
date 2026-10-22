package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

const pw = "correct horse battery"

// newAuthEnv is the production wiring: real users and the per-request token check.
func newAuthEnv(t *testing.T) (*gin.Engine, *service.Service) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := httpTestDB(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatal(err)
	}
	svc := service.New(pool)
	r := gin.New()
	(&H{S: svc, JWTSecret: testSecret, Validate: svc.ValidateToken}).Register(r)
	return r, svc
}

func mkUser(t *testing.T, svc *service.Service, email, role string) service.UserView {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), service.CreateUserInput{Email: email, Name: email, Password: pw, Role: role})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func jsonBody(v any) []byte { b, _ := json.Marshal(v); return b }

func login(t *testing.T, r http.Handler, email, password string) (int, tokenResponse) {
	t.Helper()
	w := do(r, "POST", "/api/v1/auth/login", "", "application/json", jsonBody(map[string]string{"email": email, "password": password}))
	var resp tokenResponse
	json.Unmarshal(w.Body.Bytes(), &resp) //nolint:errcheck
	return w.Code, resp
}

func bearer(t *testing.T, r http.Handler, email string) string {
	t.Helper()
	code, resp := login(t, r, email, pw)
	if code != 200 {
		t.Fatalf("login %s: %d", email, code)
	}
	return "Bearer " + resp.Token
}

func TestLoginAndMe(t *testing.T) {
	r, svc := newAuthEnv(t)
	u := mkUser(t, svc, "dana@example.com", "designer")

	code, resp := login(t, r, "Dana@Example.com", pw)
	if code != 200 || resp.Token == "" || resp.User.ID != u.ID || resp.User.Role != "designer" {
		t.Fatalf("login: %d %+v", code, resp)
	}
	if d := time.Until(resp.ExpiresAt); d < 7*time.Hour || d > 8*time.Hour+time.Minute {
		t.Errorf("expiry in %v, want about 8h", d)
	}
	w := do(r, "POST", "/api/v1/auth/login", "", "application/json", jsonBody(map[string]string{"email": "dana@example.com", "password": pw}))
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Error("login response must not be cached")
	}
	if strings.Contains(w.Body.String(), "password") || strings.Contains(w.Body.String(), "argon2") {
		t.Errorf("login response leaks credentials: %s", w.Body)
	}

	me := do(r, "GET", "/api/v1/auth/me", "Bearer "+resp.Token, "", nil)
	if me.Code != 200 || !strings.Contains(me.Body.String(), `"email":"dana@example.com"`) {
		t.Errorf("me: %d %s", me.Code, me.Body)
	}
	if w := do(r, "GET", "/api/v1/auth/me", "", "", nil); w.Code != 401 {
		t.Errorf("me without token: %d", w.Code)
	}
}

func TestLoginFailuresAreIdentical(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")

	c1, _ := login(t, r, "nobody@example.com", pw)
	c2, _ := login(t, r, "dana@example.com", "wrong password!!")
	w1 := do(r, "POST", "/api/v1/auth/login", "", "application/json", jsonBody(map[string]string{"email": "nobody@example.com", "password": pw}))
	w2 := do(r, "POST", "/api/v1/auth/login", "", "application/json", jsonBody(map[string]string{"email": "dana@example.com", "password": "wrong password!!"}))
	if c1 != 401 || c2 != 401 || w1.Body.String() != w2.Body.String() {
		t.Fatalf("unknown vs wrong password differ: %d/%d %s | %s", c1, c2, w1.Body, w2.Body)
	}

	// Bad bodies.
	for name, body := range map[string][]byte{
		"not json":     []byte("nope"),
		"empty fields": jsonBody(map[string]string{"email": "", "password": ""}),
		"missing":      []byte("{}"),
	} {
		if w := do(r, "POST", "/api/v1/auth/login", "", "application/json", body); w.Code != 400 {
			t.Errorf("%s: %d", name, w.Code)
		}
	}
	huge := jsonBody(map[string]string{"email": "a@b.co", "password": strings.Repeat("x", 40<<10)})
	if w := do(r, "POST", "/api/v1/auth/login", "", "application/json", huge); w.Code != 413 {
		t.Errorf("oversized body: %d", w.Code)
	}
}

func TestLockoutOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	for i := 0; i < 5; i++ {
		login(t, r, "dana@example.com", "wrong password!!")
	}
	if code, _ := login(t, r, "dana@example.com", pw); code != 401 {
		t.Errorf("locked account must not log in, got %d", code)
	}
}

func TestRoutesByRole(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "designer@example.com", "designer")
	mkUser(t, svc, "production@example.com", "production")
	tok := map[string]string{
		"admin":      bearer(t, r, "admin@example.com"),
		"designer":   bearer(t, r, "designer@example.com"),
		"production": bearer(t, r, "production@example.com"),
	}
	const (
		A = "admin"
		D = "designer"
		P = "production"
	)
	type route struct {
		method, path string
		allowed      []string
	}
	all := []string{A, D, P}
	edit := []string{A, D}
	adm := []string{A}
	routes := []route{
		{"GET", "/api/v1/masters/categories", all},
		{"POST", "/api/v1/masters/categories", adm},
		{"GET", "/api/v1/masters/tags", all},
		{"POST", "/api/v1/masters/tags", adm},
		{"GET", "/api/v1/masters/metals", all},
		{"PUT", "/api/v1/masters/metals/1", adm},
		{"GET", "/api/v1/masters/stone-codes", all},
		{"GET", "/api/v1/masters/stone-matrix", all},
		{"POST", "/api/v1/masters/metals", adm},
		{"PUT", "/api/v1/masters/categories/1", adm},
		{"PUT", "/api/v1/masters/tags/1", adm},
		{"POST", "/api/v1/masters/stone-codes", adm},
		{"PUT", "/api/v1/masters/stone-codes/1", adm},
		{"DELETE", "/api/v1/masters/stone-matrix", adm},
		{"GET", "/api/v1/masters/matrix-gaps", adm},
		{"GET", "/api/v1/masters/recalc-status", adm},
		{"PUT", "/api/v1/masters/stone-matrix", adm},
		{"GET", "/api/v1/designs", all},
		{"GET", "/api/v1/designs/LR001", all},
		{"POST", "/api/v1/designs", edit},
		{"PUT", "/api/v1/designs/1", edit},
		{"DELETE", "/api/v1/designs/1", edit},
		{"POST", "/api/v1/designs/1/variations", edit},
		{"PUT", "/api/v1/variations/1/base-input", edit},
		{"DELETE", "/api/v1/variations/1", edit},
		{"GET", "/api/v1/variations/1/seats", all},
		{"PUT", "/api/v1/variations/1/seats", edit},
		{"POST", "/api/v1/variations/1/clone-seats", edit},
		{"GET", "/api/v1/variations/1/simulate-bom?code=2HD", all},
		{"POST", "/api/v1/variations/1/upload-cad", edit},
		{"GET", "/api/v1/variations/1/download", all},
		{"POST", "/api/v1/exports/job-bag-pdf", all},
		{"POST", "/api/v1/exports/costing-csv", edit},
		{"GET", "/api/v1/users", adm},
		{"GET", "/api/v1/audit", adm},
		{"POST", "/api/v1/users", adm},
		{"PUT", "/api/v1/users/1", adm},
		{"POST", "/api/v1/users/1/password", adm},
		{"GET", "/api/v1/auth/me", all},
		{"POST", "/api/v1/auth/change-password", all},
	}
	contains := func(l []string, s string) bool {
		for _, x := range l {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, rt := range routes {
		if w := do(r, rt.method, rt.path, "", "application/json", []byte("{}")); w.Code != 401 {
			t.Errorf("%s %s without token = %d, want 401", rt.method, rt.path, w.Code)
		}
		for _, role := range all {
			w := do(r, rt.method, rt.path, tok[role], "application/json", []byte("{}"))
			if contains(rt.allowed, role) {
				if w.Code == 401 || w.Code == 403 {
					t.Errorf("%s %s as %s = %d, want it allowed", rt.method, rt.path, role, w.Code)
				}
			} else if w.Code != 403 {
				t.Errorf("%s %s as %s = %d, want 403", rt.method, rt.path, role, w.Code)
			}
		}
	}
}

func TestProductionNeverSeesWastageOrRates(t *testing.T) {
	r, svc := newAuthEnv(t)
	ctx := context.Background()
	mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "production@example.com", "production")
	m, _ := svc.Q.GetMetalByCode(ctx, "18KR")
	wastage := decimal.NullDecimal{Decimal: mustDec("10"), Valid: true}
	if _, err := svc.UpdateMetal(ctx, m.ID, service.MetalUpdate{SpecificGravity: m.SpecificGravity, WastagePct: wastage, Active: true}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070"), RatePerCt: decimal.NullDecimal{Decimal: mustDec("50000"), Valid: true}},
	}, ""); err != nil {
		t.Fatal(err)
	}

	get := func(who, path string) string {
		w := do(r, "GET", path, bearer(t, r, who), "", nil)
		if w.Code != 200 {
			t.Fatalf("%s %s: %d", who, path, w.Code)
		}
		return w.Body.String()
	}
	if body := get("admin@example.com", "/api/v1/masters/metals"); !strings.Contains(body, `"wastage_pct":"10"`) {
		t.Errorf("admin should see wastage: %s", body)
	}
	if body := get("production@example.com", "/api/v1/masters/metals"); strings.Contains(body, `"wastage_pct":"10"`) {
		t.Errorf("production must not see wastage: %s", body)
	}
	if body := get("admin@example.com", "/api/v1/masters/stone-matrix"); !strings.Contains(body, "50000") {
		t.Errorf("admin should see rates: %s", body)
	}
	if body := get("production@example.com", "/api/v1/masters/stone-matrix"); strings.Contains(body, "50000") {
		t.Errorf("production must not see rates: %s", body)
	}
}

func TestUserManagementOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	admin := mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "designer@example.com", "designer")
	adm := bearer(t, r, "admin@example.com")
	des := bearer(t, r, "designer@example.com")

	w := do(r, "POST", "/api/v1/users", adm, "application/json",
		jsonBody(map[string]string{"email": "New@Example.com", "name": "New", "password": pw, "role": "production"}))
	if w.Code != 201 || strings.Contains(w.Body.String(), "password") || !strings.Contains(w.Body.String(), "new@example.com") {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	var created service.UserView
	json.Unmarshal(w.Body.Bytes(), &created) //nolint:errcheck

	if w := do(r, "POST", "/api/v1/users", des, "application/json", jsonBody(map[string]string{"email": "x@example.com", "password": pw, "role": "admin"})); w.Code != 403 {
		t.Errorf("designer created a user: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/users", adm, "application/json", jsonBody(map[string]string{"email": "new@example.com", "password": pw, "role": "admin"})); w.Code != 409 {
		t.Errorf("duplicate email: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/users", adm, "application/json", jsonBody(map[string]string{"email": "weak@example.com", "password": "short", "role": "admin"})); w.Code != 400 {
		t.Errorf("weak password: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/users", adm, "", nil); w.Code != 200 || strings.Contains(w.Body.String(), "argon2") {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}

	// 'active' must be present; omitting it must not disable the user.
	path := "/api/v1/users/" + itoa(created.ID)
	if w := do(r, "PUT", path, adm, "application/json", jsonBody(map[string]string{"name": "N", "role": "production"})); w.Code != 400 {
		t.Errorf("missing active: %d", w.Code)
	}
	// Admin cannot disable themself.
	if w := do(r, "PUT", "/api/v1/users/"+itoa(admin.ID), adm, "application/json", jsonBody(map[string]any{"name": "A", "role": "admin", "active": false})); w.Code != 400 {
		t.Errorf("self-disable: %d", w.Code)
	}

	// Disabling a user locks them out immediately, even with a valid unexpired token.
	newTok := bearer(t, r, "new@example.com")
	if w := do(r, "GET", "/api/v1/designs", newTok, "", nil); w.Code != 200 {
		t.Fatalf("new user: %d", w.Code)
	}
	if w := do(r, "PUT", path, adm, "application/json", jsonBody(map[string]any{"name": "N", "role": "production", "active": false})); w.Code != 200 {
		t.Fatalf("disable: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/designs", newTok, "", nil); w.Code != 401 {
		t.Errorf("disabled user's token still works: %d", w.Code)
	}
	if code, _ := login(t, r, "new@example.com", pw); code != 401 {
		t.Errorf("disabled user can log in: %d", code)
	}
}

func TestPasswordChangeInvalidatesOldTokens(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	admin := mkUser(t, svc, "admin@example.com", "admin")
	old := bearer(t, r, "dana@example.com")
	time.Sleep(1100 * time.Millisecond) // tokens are compared at one-second precision

	w := do(r, "POST", "/api/v1/auth/change-password", old, "application/json",
		jsonBody(map[string]string{"current_password": "not my password!", "new_password": "another long password"}))
	if w.Code != 400 { // not 401: the session itself is fine
		t.Errorf("wrong current password: %d", w.Code)
	}
	w = do(r, "POST", "/api/v1/auth/change-password", old, "application/json",
		jsonBody(map[string]string{"current_password": pw, "new_password": "short"}))
	if w.Code != 400 {
		t.Errorf("weak new password: %d", w.Code)
	}
	w = do(r, "POST", "/api/v1/auth/change-password", old, "application/json",
		jsonBody(map[string]string{"current_password": pw, "new_password": "another long password"}))
	if w.Code != 200 {
		t.Fatalf("change: %d %s", w.Code, w.Body)
	}
	var fresh tokenResponse
	json.Unmarshal(w.Body.Bytes(), &fresh) //nolint:errcheck

	if w := do(r, "GET", "/api/v1/designs", old, "", nil); w.Code != 401 {
		t.Errorf("token from before the change still works: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/designs", "Bearer "+fresh.Token, "", nil); w.Code != 200 {
		t.Errorf("fresh token rejected: %d", w.Code)
	}
	if code, _ := login(t, r, "dana@example.com", pw); code != 401 {
		t.Error("old password still works")
	}

	// Admin reset also invalidates.
	adm := bearer(t, r, "admin@example.com")
	time.Sleep(1100 * time.Millisecond)
	danaID := fresh.User.ID
	if w := do(r, "POST", "/api/v1/users/"+itoa(danaID)+"/password", adm, "application/json", jsonBody(map[string]string{"new_password": "admin set password"})); w.Code != 204 {
		t.Fatalf("reset: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/designs", "Bearer "+fresh.Token, "", nil); w.Code != 401 {
		t.Errorf("token survived an admin reset: %d", w.Code)
	}
	_ = admin
}

func TestTokenForDeletedOrForgedUserRejected(t *testing.T) {
	r, svc := newAuthEnv(t)
	u := mkUser(t, svc, "dana@example.com", "designer")
	tok := bearer(t, r, "dana@example.com")
	if _, err := svc.Pool.Exec(context.Background(), "DELETE FROM users WHERE id=$1", u.ID); err != nil {
		t.Fatal(err)
	}
	if w := do(r, "GET", "/api/v1/designs", tok, "", nil); w.Code != 401 {
		t.Errorf("token of a deleted user: %d", w.Code)
	}
	// A correctly signed token for a user that never existed is rejected too.
	forged, _ := forgedToken(t)
	if w := do(r, "GET", "/api/v1/users", forged, "", nil); w.Code != 401 {
		t.Errorf("forged-subject token: %d", w.Code)
	}
}

// forgedToken is correctly signed (same secret) for a subject that is not a user.
func forgedToken(t *testing.T) (string, error) {
	t.Helper()
	tok, err := auth.Issue(testSecret, "99999", auth.Admin, time.Hour)
	return "Bearer " + tok, err
}

func TestInactiveTagsOnlyForAdmins(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "designer@example.com", "designer")
	tag, _ := svc.CreateTag(context.Background(), "Retired", "Motif")
	if _, err := svc.UpdateTag(context.Background(), tag.ID, "Retired", "Motif", false); err != nil {
		t.Fatal(err)
	}
	list := func(who string) string {
		w := do(r, "GET", "/api/v1/masters/tags?include_inactive=true", bearer(t, r, who), "", nil)
		if w.Code != 200 {
			t.Fatalf("%s: %d", who, w.Code)
		}
		return w.Body.String()
	}
	if !strings.Contains(list("admin@example.com"), "Retired") {
		t.Error("admin should see deactivated tags")
	}
	if strings.Contains(list("designer@example.com"), "Retired") {
		t.Error("include_inactive must be ignored for non-admins")
	}
}

func TestMatrixRowDeleteAndGapsOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	adm := bearer(t, r, "admin@example.com")
	if err := svc.UpdateStoneMatrix(context.Background(), []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	if w := do(r, "DELETE", "/api/v1/masters/stone-matrix?size_mm=abc&stone_code=2HD", adm, "", nil); w.Code != 400 {
		t.Errorf("bad size: %d", w.Code)
	}
	if w := do(r, "DELETE", "/api/v1/masters/stone-matrix?size_mm=1.10", adm, "", nil); w.Code != 400 {
		t.Errorf("missing code: %d", w.Code)
	}
	if w := do(r, "DELETE", "/api/v1/masters/stone-matrix?size_mm=1.10&stone_code=2HD", adm, "", nil); w.Code != 204 {
		t.Errorf("delete: %d %s", w.Code, w.Body)
	}
	if w := do(r, "DELETE", "/api/v1/masters/stone-matrix?size_mm=1.10&stone_code=2HD", adm, "", nil); w.Code != 404 {
		t.Errorf("delete again: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/masters/matrix-gaps", adm, "", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("gaps: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/masters/recalc-status", adm, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"pending":`) {
		t.Errorf("status: %d %s", w.Code, w.Body)
	}
	// Over-precise input is rejected instead of silently rounded.
	body := jsonBody(map[string]any{"rows": []map[string]string{{"size_mm": "1.10", "stone_code": "2HD", "carats_per_stone": "0.0070001"}}})
	if w := do(r, "PUT", "/api/v1/masters/stone-matrix", adm, "application/json", body); w.Code != 400 {
		t.Errorf("over-precise carats: %d %s", w.Code, w.Body)
	}
}
