package auth

import (
	"net/http"
	"net/http/httptest"
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
