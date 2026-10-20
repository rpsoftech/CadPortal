package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// bindSmallJSON reads a bounded JSON body; anything larger or malformed is a 400/413.
func bindSmallJSON(c *gin.Context, dst any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxJSONBody)
	if err := c.ShouldBindJSON(dst); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request too large"})
		} else {
			badRequest(c, "invalid JSON body")
		}
		return false
	}
	return true
}

func (h *H) ttl() time.Duration {
	if h.TokenTTL > 0 {
		return h.TokenTTL
	}
	return DefaultTokenTTL
}

type tokenResponse struct {
	Token     string           `json:"token"`
	ExpiresAt time.Time        `json:"expires_at"`
	User      service.UserView `json:"user"`
}

func (h *H) issue(u service.UserView) (tokenResponse, error) {
	tok, exp, err := auth.IssueAt(h.JWTSecret, strconv.FormatInt(u.ID, 10), auth.Role(u.Role), h.ttl(), time.Now())
	return tokenResponse{Token: tok, ExpiresAt: exp, User: u}, err
}

// login is the only unauthenticated route. Every failure is the same 401.
func (h *H) login(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if in.Email == "" || in.Password == "" {
		badRequest(c, "email and password are required")
		return
	}
	u, err := h.S.Authenticate(c, in.Email, in.Password)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			h.recordLogin(c, false, in.Email, 0, http.StatusUnauthorized)
		}
		fail(c, err)
		return
	}
	resp, err := h.issue(u)
	if err != nil {
		fail(c, err)
		return
	}
	h.recordLogin(c, true, "", u.ID, http.StatusOK)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

func callerID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid token"})
		return 0, false
	}
	return id, true
}

func (h *H) me(c *gin.Context) {
	id, ok := callerID(c)
	if !ok {
		return
	}
	u, err := h.S.GetUser(c, id)
	respond(c, http.StatusOK, u, err)
}

// changePassword also returns a fresh token, because the change invalidates every
// token issued before it, including the one used for this request.
func (h *H) changePassword(c *gin.Context) {
	id, ok := callerID(c)
	if !ok {
		return
	}
	var in struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if err := h.S.ChangePassword(c, id, in.CurrentPassword, in.NewPassword); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "user", idStr(id), "")
	u, err := h.S.GetUser(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	resp, err := h.issue(u)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, resp)
}

func (h *H) listUsers(c *gin.Context) {
	out, err := h.S.ListUsers(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) createUser(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Name     string `json:"name"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	u, err := h.S.CreateUser(c, service.CreateUserInput(in))
	if err == nil {
		auditNote(c, "user", idStr(u.ID), u.Email+" ("+u.Role+")")
	}
	respond(c, http.StatusCreated, u, err)
}

func (h *H) updateUser(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	actor, ok := callerID(c)
	if !ok {
		return
	}
	var in struct {
		Name   string `json:"name"`
		Role   string `json:"role"`
		Active *bool  `json:"active"` // required: an omitted flag must not silently disable the user
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if in.Active == nil {
		badRequest(c, "active is required")
		return
	}
	u, err := h.S.UpdateUser(c, actor, id, in.Name, in.Role, *in.Active)
	if err == nil {
		auditNote(c, "", "", fmt.Sprintf("%s role=%s active=%t", u.Email, u.Role, *in.Active))
	}
	respond(c, http.StatusOK, u, err)
}

func (h *H) resetPassword(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		NewPassword string `json:"new_password"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if err := h.S.SetPassword(c, id, in.NewPassword); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
