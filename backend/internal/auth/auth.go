// Package auth issues and verifies HS256 bearer tokens carrying a role claim and
// gates routes by role.
package auth

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

type Role string

const (
	Admin      Role = "admin"
	Designer   Role = "designer"
	Production Role = "production"
)

// AllRoles is every signed-in role, for routes any authenticated user may call.
var AllRoles = []Role{Admin, Designer, Production}

// Editors may change designs, variations and seats.
var Editors = []Role{Admin, Designer}

func (r Role) Valid() bool { return r == Admin || r == Designer || r == Production }

type Claims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

// Validator is an optional per-request check on a token that already passed
// signature and expiry (for example: the user still exists, is active, still has
// this role, and has not changed their password since the token was issued).
//
// It returns ok=false for a token that is genuinely no longer valid (401). It returns
// an error only when it could not decide, such as a database outage; that is a 503, so a
// brief outage does not sign every user out.
type Validator func(ctx context.Context, c Claims) (ok bool, err error)

// Authenticator verifies tokens. A nil Validate means signature and expiry only.
type Authenticator struct {
	Secret   []byte
	Validate Validator
}

const (
	roleKey = "auth.role"
	userKey = "auth.user"
)

// Issue signs a token for subject with the given role.
func Issue(secret []byte, subject string, role Role, ttl time.Duration) (string, error) {
	tok, _, err := IssueAt(secret, subject, role, ttl, time.Now())
	return tok, err
}

// IssueAt is Issue with an explicit issue time; it also returns the expiry.
func IssueAt(secret []byte, subject string, role Role, ttl time.Duration, now time.Time) (string, time.Time, error) {
	if !role.Valid() {
		return "", time.Time{}, errors.New("auth: unknown role")
	}
	exp := now.Add(ttl)
	tok, err := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(exp),
		},
	}).SignedString(secret)
	return tok, exp, err
}

// Require returns middleware that accepts only the listed roles. A missing or
// invalid token is 401; a valid token with the wrong role is 403.
func (a *Authenticator) Require(roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		var cl Claims
		tok, err := jwt.ParseWithClaims(raw, &cl, func(*jwt.Token) (any, error) { return a.Secret, nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
		if err != nil || !tok.Valid || !cl.Role.Valid() || cl.Subject == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if a.Validate != nil {
			ok, err := a.Validate(c.Request.Context(), cl)
			if err != nil {
				_ = c.Error(err) // detail stays in server logs
				c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "service temporarily unavailable"})
				return
			}
			if !ok {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
				return
			}
		}
		// Identify the caller before the role check, so a refused request can still be
		// attributed to them (the audit log records who tried).
		c.Set(roleKey, cl.Role)
		c.Set(userKey, cl.Subject)
		if !slices.Contains(roles, cl.Role) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "role not permitted"})
			return
		}
		c.Next()
	}
}

// SetCaller marks the request as made by subject with role, for a route that is authorised by something
// other than a bearer token (a signed download link), so the audit log still names who acted.
func SetCaller(c *gin.Context, subject string, role Role) {
	c.Set(roleKey, role)
	c.Set(userKey, subject)
}

// Require is shorthand for a stateless Authenticator (signature and expiry only).
func Require(secret []byte, roles ...Role) gin.HandlerFunc {
	return (&Authenticator{Secret: secret}).Require(roles...)
}

// RoleFrom returns the authenticated caller's role ("" outside Require).
func RoleFrom(c *gin.Context) Role {
	r, _ := c.Get(roleKey)
	role, _ := r.(Role)
	return role
}

// UserIDFrom returns the authenticated caller's user id ("" outside Require).
func UserIDFrom(c *gin.Context) string {
	u, _ := c.Get(userKey)
	id, _ := u.(string)
	return id
}
