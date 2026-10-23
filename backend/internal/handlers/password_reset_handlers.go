package handlers

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

// authConfig tells the web app whether "forgot password" can send an email and how long it may sit idle
// before signing the user out. It is public and reveals nothing about any account.
func (h *H) authConfig(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"password_reset_email": h.S.ResetEmailEnabled(),
		"idle_timeout_minutes": h.IdleTimeoutMinutes,
	})
}

// forgotPassword is the public "forgot password" form. It answers the same way whatever the address is
// (known, unknown, disabled, throttled), so it cannot be used to find out who has an account.
func (h *H) forgotPassword(c *gin.Context) {
	var in struct {
		Email string `json:"email"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	h.S.RequestPasswordReset(c, in.Email)
	auditNote(c, "user", "", "reset requested for "+clip(strings.ToLower(strings.TrimSpace(in.Email)), 120))
	c.JSON(http.StatusAccepted, gin.H{"message": "If that address belongs to an account and email is set up, a reset link has been sent."})
}

// resetWithToken sets a new password with the token from a reset link.
func (h *H) resetWithToken(c *gin.Context) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	u, err := h.S.ResetPassword(c, strings.TrimSpace(in.Token), in.Password)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "user", idStr(u.ID), "password reset with a link")
	c.Status(http.StatusNoContent)
}

// adminResetLink makes a one-hour link for a user. The token is shown once, to the admin, who passes it
// on; the user then chooses their own password.
func (h *H) adminResetLink(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	actor, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64) // 0 if unknown; the audit log has the caller anyway
	token, expires, err := h.S.AdminResetLink(c, actor, id)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"path": "/reset-password?token=" + token, "expires_at": expires})
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
