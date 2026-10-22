package handlers

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

// listBackups shows the run history and the stored dumps.
func (h *H) listBackups(c *gin.Context) {
	out, err := h.S.Backups(c)
	respond(c, http.StatusOK, out, err)
}

// requestBackup asks the backup service for a backup now; the service picks it up within a minute.
func (h *H) requestBackup(c *gin.Context) {
	uid, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
	if err := h.S.RequestBackup(c, uid); err != nil {
		fail(c, err)
		return
	}
	c.Status(http.StatusAccepted)
}

// downloadBackup returns a short-lived link to one dump. Audited like every other sensitive read.
func (h *H) downloadBackup(c *gin.Context) {
	key := c.Query("key")
	link, err := h.S.BackupDownload(c, key)
	if err == nil {
		auditNote(c, "backup", "", key)
	}
	respond(c, http.StatusOK, link, err)
}
