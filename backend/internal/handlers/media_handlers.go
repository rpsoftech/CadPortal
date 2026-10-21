package handlers

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// mediaBodyLimit is the request cap for one media upload: the largest video plus room for the
// multipart framing.
const mediaBodyLimit = service.MaxVideoBytes + 1<<20

// uploadMedia attaches one photo or video to a design, or to one of its variations when the form
// has a "variation_id" field. The field must come before the "file" part, because the file is
// streamed to storage as it arrives and cannot be held back until the rest of the form is read.
func (h *H) uploadMedia(c *gin.Context) {
	designID, ok := idParam(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, mediaBodyLimit)
	mr, err := c.Request.MultipartReader()
	if err != nil {
		badRequest(c, "expected multipart/form-data with a \"file\" part")
		return
	}
	var variationID *int64
	for {
		part, err := mr.NextPart()
		if err != nil {
			if errors.Is(err, io.EOF) {
				badRequest(c, "missing \"file\" part")
			} else {
				fail(c, err)
			}
			return
		}
		switch part.FormName() {
		case "variation_id":
			raw, _ := io.ReadAll(io.LimitReader(part, 32))
			if s := string(raw); s != "" {
				v, err := strconv.ParseInt(s, 10, 64)
				if err != nil || v <= 0 {
					badRequest(c, "variation_id must be a positive number")
					return
				}
				variationID = &v
			}
		case "file":
			uid, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
			out, err := h.S.AddMedia(c, service.AddMediaInput{
				DesignID: designID, VariationID: variationID, Filename: part.FileName(), Body: part, UserID: uid,
			})
			if err == nil {
				target := "design"
				if variationID != nil {
					target = "variation " + strconv.FormatInt(*variationID, 10)
				}
				auditNote(c, "design", idStr(designID), fmt.Sprintf("%s %s on %s (%d bytes)", out.Kind, out.Filename, target, out.SizeBytes))
			}
			respond(c, http.StatusCreated, out, err)
			return
		}
	}
}

func (h *H) deleteMedia(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	row, err := h.S.DeleteMedia(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "design", idStr(row.DesignID), fmt.Sprintf("%s %s", row.Kind, row.Filename))
	c.Status(http.StatusNoContent)
}

func (h *H) setCover(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	row, err := h.S.SetCover(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "design", idStr(row.DesignID), row.Filename)
	c.Status(http.StatusNoContent)
}
