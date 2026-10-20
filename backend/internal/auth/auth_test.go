package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

var secret = []byte("test-secret-test-secret-test-secret")

func serve(t *testing.T, header string, roles ...Role) int {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", Require(secret, roles...), func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

func TestRequire(t *testing.T) {
	tok := func(role Role, ttl time.Duration) string {
		s, err := Issue(secret, "u1", role, ttl)
		if err != nil {
			t.Fatal(err)
		}
		return "Bearer " + s
	}
	// Token signed with a different key, and an unsigned "none" token.
	other, _ := Issue([]byte("another-secret-another-secret-123"), "u1", Admin, time.Hour)
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{Role: Admin}).SignedString(jwt.UnsafeAllowNoneSignatureType)

	tests := []struct {
		name   string
		header string
		roles  []Role
		want   int
	}{
		{"no header", "", []Role{Admin}, 401},
		{"garbage", "Bearer abc", []Role{Admin}, 401},
		{"wrong key", "Bearer " + other, []Role{Admin}, 401},
		{"alg none", "Bearer " + none, []Role{Admin}, 401},
		{"expired", tok(Admin, -time.Minute), []Role{Admin}, 401},
		{"admin ok", tok(Admin, time.Hour), []Role{Admin, Designer}, 200},
		{"designer ok", tok(Designer, time.Hour), []Role{Admin, Designer}, 200},
		{"production forbidden on upload", tok(Production, time.Hour), []Role{Admin, Designer}, 403},
		{"production ok on download", tok(Production, time.Hour), []Role{Admin, Designer, Production}, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := serve(t, tc.header, tc.roles...); got != tc.want {
				t.Fatalf("got %d want %d", got, tc.want)
			}
		})
	}
	if _, err := Issue(secret, "u", Role("root"), time.Hour); err == nil {
		t.Error("unknown role should not issue")
	}
}

func TestAuthenticatorValidator(t *testing.T) {
	gin.SetMode(gin.TestMode)
	calls := 0
	allow := true
	broken := false
	a := &Authenticator{Secret: secret, Validate: func(_ context.Context, c Claims) (bool, error) {
		calls++
		if broken {
			return false, errors.New("database unreachable")
		}
		return allow && c.Subject == "42" && c.Role == Designer, nil
	}}
	r := gin.New()
	r.GET("/x", a.Require(AllRoles...), func(c *gin.Context) {
		c.JSON(200, gin.H{"role": RoleFrom(c), "user": UserIDFrom(c)})
	})
	get := func(tok string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.String()
	}
	tok, _ := Issue(secret, "42", Designer, time.Hour)

	if code, body := get(tok); code != 200 || !strings.Contains(body, `"user":"42"`) || !strings.Contains(body, `"role":"designer"`) {
		t.Fatalf("valid: %d %s", code, body)
	}
	allow = false // e.g. the user was disabled
	if code, _ := get(tok); code != 401 {
		t.Errorf("rejected by validator should be 401, got %d", code)
	}
	allow = true
	other, _ := Issue(secret, "7", Designer, time.Hour) // validator says no for another user
	if code, _ := get(other); code != 401 {
		t.Errorf("other user: %d", code)
	}
	// A validator that cannot decide (database down) is a 503, never a 401 that ends sessions.
	broken = true
	code, body := get(tok)
	if code != 503 || strings.Contains(body, "database") {
		t.Errorf("validator error: %d %s (want 503 without internals)", code, body)
	}
	broken = false
	if code, _ := get(tok); code != 200 {
		t.Errorf("recovers once the validator can decide again: %d", code)
	}
	// Validator is not consulted for tokens that fail signature or expiry.
	before := calls
	bad, _ := Issue([]byte("another-secret-another-secret-123"), "42", Designer, time.Hour)
	expired, _ := Issue(secret, "42", Designer, -time.Minute)
	get(bad)
	get(expired)
	if calls != before {
		t.Error("validator ran for an invalid token")
	}
	// A token without a subject is invalid.
	noSub, _ := Issue(secret, "", Designer, time.Hour)
	if code, _ := get(noSub); code != 401 {
		t.Errorf("no subject: %d", code)
	}
}

func TestIssueAtReportsExpiry(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	_, exp, err := IssueAt(secret, "1", Admin, 8*time.Hour, now)
	if err != nil || !exp.Equal(now.Add(8*time.Hour)) {
		t.Fatalf("exp=%v err=%v", exp, err)
	}
	if _, _, err := IssueAt(secret, "1", Role("root"), time.Hour, now); err == nil {
		t.Error("unknown role should not issue")
	}
}
