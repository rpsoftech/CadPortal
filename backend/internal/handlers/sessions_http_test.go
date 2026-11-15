package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// sessionOf reads the session id out of a "Bearer <token>" value without checking the signature.
func sessionOf(t *testing.T, bearerValue string) string {
	t.Helper()
	var cl auth.Claims
	if _, _, err := jwt.NewParser().ParseUnverified(strings.TrimPrefix(bearerValue, "Bearer "), &cl); err != nil {
		t.Fatal(err)
	}
	return cl.SessionID
}

func makeIdle(t *testing.T, svc *service.Service, sid string) {
	t.Helper()
	if _, err := svc.Pool.Exec(context.Background(),
		`UPDATE sessions SET last_seen_at = now() - interval '33 minutes' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
}

func TestLoginOpensASessionAndTheTokenNamesIt(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	tok := bearer(t, r, "dana@example.com")

	sid := sessionOf(t, tok)
	if len(sid) != 32 {
		t.Fatalf("session id %q", sid)
	}
	var n int
	if err := svc.Pool.QueryRow(context.Background(), `SELECT count(*) FROM sessions WHERE id = $1 AND revoked_at IS NULL`, sid).Scan(&n); err != nil || n != 1 {
		t.Fatalf("the token's session is not an open row: %d %v", n, err)
	}
}

func TestTokenWithoutASessionIsRefused(t *testing.T) {
	r, svc := newAuthEnv(t)
	u := mkUser(t, svc, "dana@example.com", "designer")
	// A token from before sessions existed: valid signature, no "sid".
	old, err := auth.Issue(testSecret, "1", auth.Designer, time.Hour)
	if err != nil || u.ID != 1 {
		t.Fatalf("setup: %v (user id %d)", err, u.ID)
	}
	if w := do(r, "GET", "/api/v1/auth/me", "Bearer "+old, "", nil); w.Code != 401 {
		t.Errorf("a token with no session: %d, want 401 (the user signs in again once)", w.Code)
	}
}

func TestIdleLoginIsEndedWithAReason(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	tok := bearer(t, r, "dana@example.com")

	if w := do(r, "GET", "/api/v1/auth/me", tok, "", nil); w.Code != 200 {
		t.Fatalf("a fresh login: %d", w.Code)
	}
	makeIdle(t, svc, sessionOf(t, tok)) // 33 minutes without a request: past 30 plus the 2 minute grace

	w := do(r, "GET", "/api/v1/auth/me", tok, "", nil)
	if w.Code != 401 {
		t.Fatalf("an idle login: %d, want 401", w.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body["code"] != auth.CodeIdleTimeout {
		t.Errorf("the answer must say why so the web app can too: %s", w.Body)
	}
	// Asking again does not bring it back, and signing in again does.
	if w := do(r, "GET", "/api/v1/auth/me", tok, "", nil); w.Code != 401 {
		t.Errorf("an idle login came back: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/auth/me", bearer(t, r, "dana@example.com"), "", nil); w.Code != 200 {
		t.Errorf("a new login after being idle: %d", w.Code)
	}
}

func TestAnActiveLoginIsNotEnded(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	tok := bearer(t, r, "dana@example.com")
	sid := sessionOf(t, tok)

	// 31 minutes ago, one request (the web app's activity ping) renews it, so 31 more minutes later it is
	// still within 32 minutes of the last request.
	if _, err := svc.Pool.Exec(context.Background(), `UPDATE sessions SET last_seen_at = now() - interval '31 minutes' WHERE id = $1`, sid); err != nil {
		t.Fatal(err)
	}
	if w := do(r, "GET", "/api/v1/auth/me", tok, "", nil); w.Code != 200 {
		t.Fatalf("31 minutes is inside the 32 minute limit: %d", w.Code)
	}
	var secs float64
	if err := svc.Pool.QueryRow(context.Background(), `SELECT extract(epoch FROM now() - last_seen_at)::float8 FROM sessions WHERE id = $1`, sid).Scan(&secs); err != nil || secs > 10 {
		t.Errorf("the request did not renew the login: last seen %.0f s ago (%v)", secs, err)
	}
}

func TestLogoutEndsOnlyThisLogin(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	mkUser(t, svc, "omar@example.com", "admin")
	first := bearer(t, r, "dana@example.com")
	second := bearer(t, r, "dana@example.com") // the same person on another device
	omar := bearer(t, r, "omar@example.com")

	if w := do(r, "POST", "/api/v1/auth/logout", first, "", nil); w.Code != 204 {
		t.Fatalf("logout: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/auth/me", first, "", nil); w.Code != 401 {
		t.Errorf("the token still works after sign-out: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/auth/logout", first, "", nil); w.Code != 401 {
		t.Errorf("signing out twice with the same token: %d, want 401", w.Code)
	}
	for name, tok := range map[string]string{"the same user's other login": second, "another user": omar} {
		if w := do(r, "GET", "/api/v1/auth/me", tok, "", nil); w.Code != 200 {
			t.Errorf("%s was signed out too: %d", name, w.Code)
		}
	}
	if w := do(r, "POST", "/api/v1/auth/logout", "", "", nil); w.Code != 401 {
		t.Errorf("logout without a token: %d", w.Code)
	}
}

func TestChangingThePasswordGivesANewLogin(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "dana@example.com", "designer")
	_, resp := login(t, r, "dana@example.com", pw)
	old := "Bearer " + resp.Token

	time.Sleep(1100 * time.Millisecond) // the password-changed time has one-second precision
	w := do(r, "POST", "/api/v1/auth/change-password", old, "application/json",
		jsonBody(map[string]string{"current_password": pw, "new_password": "another long passphrase"}))
	if w.Code != 200 {
		t.Fatalf("change password: %d %s", w.Code, w.Body)
	}
	var fresh tokenResponse
	if err := json.Unmarshal(w.Body.Bytes(), &fresh); err != nil {
		t.Fatal(err)
	}
	if sid := sessionOf(t, "Bearer "+fresh.Token); sid == "" || sid == sessionOf(t, old) {
		t.Errorf("the new token must belong to a new login, got %q", sid)
	}
	if w := do(r, "GET", "/api/v1/auth/me", "Bearer "+fresh.Token, "", nil); w.Code != 200 {
		t.Errorf("the new token: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/auth/me", old, "", nil); w.Code != 401 {
		t.Errorf("the old token after a password change: %d", w.Code)
	}
}
