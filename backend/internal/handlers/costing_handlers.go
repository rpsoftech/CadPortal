package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/export"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// Costing of several designs at once, and saved costing sheets. All of it is admin/designer only:
// it carries wastage-derived weights and stone prices.

// costRun costs a selection and applies the all-or-nothing rule (unless skip_incomplete is set).
func (h *H) costRun(c *gin.Context, req service.SheetRequest) (service.CostResult, []byte, bool) {
	res, err := h.S.CostSelection(c, req)
	if err == nil {
		err = res.Costable(req.SkipIncomplete)
	}
	if err != nil {
		fail(c, err)
		return res, nil, false
	}
	var buf bytes.Buffer
	if err := export.WriteCostingSheetCSV(&buf, res.Rows, res.Totals, res.Problems); err != nil {
		fail(c, err)
		return res, nil, false
	}
	return res, buf.Bytes(), true
}

func sendCostingCSV(c *gin.Context, res service.CostResult, csv []byte, filename string) {
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	c.Header("X-Costing-Incomplete", strconv.Itoa(res.IncompleteVariations()))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", csv)
}

// costingPreview returns the costed rows and the problems as JSON without failing on incomplete
// data, so the screen can show both before anything is downloaded or saved.
func (h *H) costingPreview(c *gin.Context) {
	var in service.SheetRequest
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	res, err := h.S.CostSelection(c, in)
	if err != nil {
		fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"rows": res.Rows, "totals": res.Totals, "problems": res.Problems,
		"complete": len(res.Problems) == 0 && len(res.Rows) > 0,
	})
}

func (h *H) createCostingSheet(c *gin.Context) {
	var in struct {
		service.SheetRequest
		Name string `json:"name"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	res, csv, ok := h.costRun(c, in.SheetRequest)
	if !ok {
		return
	}
	uid, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
	d, err := h.S.SaveCostingSheet(c, service.SaveSheetParams{Name: in.Name, UserID: uid,
		Request: in.SheetRequest, Result: res, CSV: string(csv)})
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "costing_sheet", idStr(d.ID), fmt.Sprintf("%s: %d variations in %d designs", d.Name, d.VariationCount, d.DesignCount))
	c.JSON(http.StatusCreated, d)
}

func (h *H) listCostingSheets(c *gin.Context) {
	var limit, offset int32
	for name, dst := range map[string]*int32{"limit": &limit, "offset": &offset} {
		v, err := optInt(c, name)
		if err != nil {
			badRequest(c, err.Error())
			return
		}
		if v != nil {
			*dst = int32(*v)
		}
	}
	rows, total, err := h.S.ListCostingSheets(c, limit, offset)
	if err != nil {
		fail(c, err)
		return
	}
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.JSON(http.StatusOK, rows)
}

func (h *H) getCostingSheet(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, _, err := h.S.GetCostingSheet(c, id)
	respond(c, http.StatusOK, d, err)
}

func (h *H) costingSheetCSV(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, csv, err := h.S.GetCostingSheet(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "costing_sheet", idStr(id), d.Name)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fmt.Sprintf("costing-sheet-%d.csv", id)))
	c.Header("X-Costing-Incomplete", strconv.Itoa(d.IncompleteCount))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", []byte(csv))
}

// deleteCostingSheet: an admin may delete any sheet, a designer only their own.
func (h *H) deleteCostingSheet(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, _, err := h.S.GetCostingSheet(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	if auth.RoleFrom(c) != auth.Admin && (d.CreatedBy == nil || idStr(*d.CreatedBy) != auth.UserIDFrom(c)) {
		c.JSON(http.StatusForbidden, gin.H{"error": "only an admin or the person who saved a sheet can delete it"})
		return
	}
	if err := h.S.DeleteCostingSheet(c, id); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "", "", d.Name)
	c.Status(http.StatusNoContent)
}

// recostCostingSheet makes a NEW sheet from a saved one's variations, metal and stone code with the
// current masters and the gold / setting rate given, and shows the difference from the old one.
func (h *H) recostCostingSheet(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		Name             string           `json:"name"`
		GoldRatePerG     *decimal.Decimal `json:"gold_rate_per_g"`
		SettingRatePerPc *decimal.Decimal `json:"setting_rate_per_pc"`
		SkipIncomplete   *bool            `json:"skip_incomplete"`
	}
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	old, _, err := h.S.GetCostingSheet(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	req := service.RecostRequest(old, service.RecostOverrides{GoldRatePerG: in.GoldRatePerG,
		SettingRatePerPc: in.SettingRatePerPc, SkipIncomplete: in.SkipIncomplete})
	res, csv, ok := h.costRun(c, req)
	if !ok {
		return
	}
	name := in.Name
	if name == "" {
		name = old.Name
		if r := []rune(name); len(r) > 100 {
			name = string(r[:100])
		}
		name += " (re-cost)"
	}
	uid, _ := strconv.ParseInt(auth.UserIDFrom(c), 10, 64)
	p := service.SaveSheetParams{Name: name, UserID: uid, RecostOf: id, Request: req, Result: res, CSV: string(csv)}
	if old.OrderID != nil { // a re-cost of an order's sheet stays on that order
		p.OrderID = *old.OrderID
	}
	d, err := h.S.SaveCostingSheet(c, p)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "costing_sheet", idStr(d.ID), fmt.Sprintf("re-cost of %d", id))
	c.JSON(http.StatusCreated, gin.H{"sheet": d, "comparison": service.CompareSheets(old, d)})
}
