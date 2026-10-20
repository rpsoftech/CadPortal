// Package auth verifies HS256 bearer tokens carrying a role claim and gates
// routes by role. Tokens are issued elsewhere (see cmd/devtoken for dev use).
package auth

import (
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

func (r Role) valid() bool { return r == Admin || r == Designer || r == Production }

type Claims struct {
	Role Role `json:"role"`
	jwt.RegisteredClaims
}

const roleKey = "auth.role"

// Issue signs a token for subject with the given role.
func Issue(secret []byte, subject string, role Role, ttl time.Duration) (string, error) {
	if !role.valid() {
		return "", errors.New("auth: unknown role")
	}
	now := time.Now()
	return jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{
		Role: role,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject: subject, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}).SignedString(secret)
}

// Require returns middleware that accepts only the listed roles. A missing or
// invalid token is 401; a valid token with the wrong role is 403.
func Require(secret []byte, roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		raw, ok := strings.CutPrefix(h, "Bearer ")
		if !ok || raw == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing bearer token"})
			return
		}
		var cl Claims
		tok, err := jwt.ParseWithClaims(raw, &cl, func(*jwt.Token) (any, error) { return secret, nil },
			jwt.WithValidMethods([]string{"HS256"}), jwt.WithExpirationRequired())
		if err != nil || !tok.Valid || !cl.Role.valid() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
			return
		}
		if !slices.Contains(roles, cl.Role) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "role not permitted"})
			return
		}
		c.Set(roleKey, cl.Role)
		c.Next()
	}
}
