package handlers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func (h *H) createMetal(c *gin.Context) {
	var in struct {
		Code            string              `json:"code"`
		SpecificGravity decimal.Decimal     `json:"specific_gravity"`
		WastagePct      decimal.NullDecimal `json:"wastage_pct"`
		IsDefaultInput  bool                `json:"is_default_input"`
		Active          *bool               `json:"active"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	active := in.Active == nil || *in.Active // new alloys are active unless said otherwise
	m, err := h.S.CreateMetal(c, service.MetalCreate{
		Code: in.Code, SpecificGravity: in.SpecificGravity, WastagePct: in.WastagePct,
		IsDefaultInput: in.IsDefaultInput, Active: active,
	})
	if err == nil {
		auditNote(c, "metal", idStr(m.ID), m.Code)
	}
	respond(c, http.StatusCreated, m, err)
}

func (h *H) updateCategory(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Name       string `json:"name"`
		CodePrefix string `json:"code_prefix"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	out, err := h.S.UpdateCategory(c, id, in.Name, in.CodePrefix)
	if err == nil {
		auditNote(c, "", "", fmt.Sprintf("%s prefix=%q", out.Name, in.CodePrefix))
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) updateTag(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Name     string `json:"name"`
		TagGroup string `json:"tag_group"`
		Active   *bool  `json:"active"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if in.Active == nil {
		badRequest(c, "active is required")
		return
	}
	out, err := h.S.UpdateTag(c, id, in.Name, in.TagGroup, *in.Active)
	if err == nil {
		auditNote(c, "", "", fmt.Sprintf("%s: %s active=%t", in.TagGroup, in.Name, *in.Active))
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) createStoneCode(c *gin.Context) {
	var in struct {
		Code string `json:"code"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	out, err := h.S.CreateStoneCode(c, in.Code)
	if err == nil {
		auditNote(c, "stone_code", idStr(out.ID), out.Code)
	}
	respond(c, http.StatusCreated, out, err)
}

func (h *H) setStoneCodeActive(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Active *bool `json:"active"`
	}
	if !bindSmallJSON(c, &in) {
		return
	}
	if in.Active == nil {
		badRequest(c, "active is required")
		return
	}
	out, err := h.S.SetStoneCodeActive(c, id, *in.Active)
	if err == nil {
		auditNote(c, "", "", fmt.Sprintf("%s active=%t", out.Code, *in.Active))
	}
	respond(c, http.StatusOK, out, err)
}

func (h *H) deleteMatrixRow(c *gin.Context) {
	size, err := decimal.NewFromString(c.Query("size_mm"))
	if err != nil || c.Query("stone_code") == "" {
		badRequest(c, "size_mm and stone_code are required")
		return
	}
	if err := h.S.DeleteMatrixRow(c, size, c.Query("stone_code")); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "", "", size.StringFixed(2)+" mm "+c.Query("stone_code"))
	c.Status(http.StatusNoContent)
}

func (h *H) matrixGaps(c *gin.Context) {
	out, err := h.S.MatrixGaps(c)
	respond(c, http.StatusOK, out, err)
}

func (h *H) recalcStatus(c *gin.Context) {
	out, err := h.S.RecalcStatus(c)
	respond(c, http.StatusOK, out, err)
}
