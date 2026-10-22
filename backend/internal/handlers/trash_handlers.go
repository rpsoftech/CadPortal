package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// listTrash shows what can still be brought back: deleted designs and removed files.
func (h *H) listTrash(c *gin.Context) {
	out, err := h.S.ListTrash(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) restoreFile(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	item, err := h.S.RestoreFile(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "trashed_file", idStr(id), item.Label)
	c.Status(http.StatusNoContent)
}

func (h *H) restoreDesign(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, err := h.S.RestoreDesign(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "design", idStr(id), fmt.Sprintf("restored %s", d.Code))
	c.Status(http.StatusNoContent)
}
