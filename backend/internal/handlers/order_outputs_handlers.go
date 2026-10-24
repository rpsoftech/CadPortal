package handlers

import (
	"bytes"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/export"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// What an order produces: the summary PDF, job bags and CSV (every role: production works from them;
// nothing in them is a price) and costing (admin/designer only). All weights come from the order's
// snapshot.

func (h *H) orderSummaryPDF(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, err := h.S.GetOrder(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	pdf, err := export.RenderOrderSummaryPDF(d, service.ComputeOrderTotals(d), h.S.OrderQR(d), time.Now().UTC().Format("2006-01-02 15:04"))
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "order", idStr(id), d.Number)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.Number+"-summary.pdf"))
	c.Data(http.StatusOK, "application/pdf", pdf)
}

// orderJobBags prints a bag for every line of the order (or, with per_piece=true, for every piece), from
// the order's snapshot.
func (h *H) orderJobBags(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	perPiece := false
	if v := c.Query("per_piece"); v != "" {
		p, err := strconv.ParseBool(v)
		if err != nil {
			badRequest(c, "per_piece must be true or false")
			return
		}
		perPiece = p
	}
	d, err := h.S.GetOrder(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	bags, err := h.S.OrderJobBags(d, perPiece)
	if err != nil {
		fail(c, err)
		return
	}
	b := export.NewJobBagsPDF(fmt.Sprintf("%s job bags", d.Number))
	photos := map[int64][]byte{} // one read per variation, however many pieces
	for _, jb := range bags {
		if jb.VariationID != 0 {
			p, seen := photos[jb.VariationID]
			if !seen {
				p = h.S.JobBagPhoto(c, jb)
				photos[jb.VariationID] = p
			}
			jb.Photo = p
		}
		if err := b.AddBag(jb); err != nil {
			fail(c, err)
			return
		}
	}
	pdf, err := b.Bytes()
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "order", idStr(id), fmt.Sprintf("%s: %d bags", d.Number, len(bags)))
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.Number+"-jobbags.pdf"))
	c.Data(http.StatusOK, "application/pdf", pdf)
}

func (h *H) orderCSV(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	d, err := h.S.GetOrder(c, id)
	if err != nil {
		fail(c, err)
		return
	}
	var buf bytes.Buffer
	if err := export.WriteOrderCSV(&buf, d, service.ComputeOrderTotals(d)); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "order", idStr(id), d.Number)
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%q", d.Number+".csv"))
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// maxOrdersCSV bounds the list export; the response says when it was cut.
const maxOrdersCSV = 5000

// ordersCSV exports the orders list with the list's own filters (status, q, from, to).
func (h *H) ordersCSV(c *gin.Context) {
	from, err := optTime(c, "from", false)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	to, err := optTime(c, "to", true)
	if err != nil {
		badRequest(c, err.Error())
		return
	}
	var all []service.OrderSummary
	var total int64
	for len(all) < maxOrdersCSV {
		rows, t, err := h.S.ListOrders(c, c.Query("status"), c.Query("q"), from, to, 200, int32(len(all)))
		if err != nil {
			fail(c, err)
			return
		}
		total = t
		all = append(all, rows...)
		if len(rows) == 0 || int64(len(all)) >= total {
			break
		}
	}
	var buf bytes.Buffer
	if err := export.WriteOrdersCSV(&buf, all); err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "order", "", strconv.Itoa(len(all))+" orders")
	c.Header("X-Total-Count", strconv.FormatInt(total, 10))
	c.Header("Content-Disposition", `attachment; filename="orders.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

// costOrder costs the order from the current masters and designs for its quantities and saves the result
// as an immutable costing sheet linked to the order. Missing data fails the run with the grouped list
// (nothing is costed as zero), unless skip_incomplete is set. The response says how far the live designs
// have moved from the order's snapshots.
func (h *H) costOrder(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in service.OrderCostParams
	if c.ShouldBindJSON(&in) != nil {
		badRequest(c, "invalid JSON body")
		return
	}
	res, req, d, err := h.S.CostOrder(c, id, in)
	if err == nil {
		err = res.Costable(req.SkipIncomplete)
	}
	if err != nil {
		fail(c, err)
		return
	}
	var buf bytes.Buffer
	if err := export.WriteCostingSheetCSV(&buf, res.Rows, res.Totals, res.Problems); err != nil {
		fail(c, err)
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = d.Number + " costing"
	}
	sheet, err := h.S.SaveCostingSheet(c, service.SaveSheetParams{Name: name, UserID: currentUserID(c), OrderID: id,
		Request: req, Result: res, CSV: buf.String()})
	if err != nil {
		fail(c, err)
		return
	}
	drift, err := h.S.OrderDrift(c, d)
	if err != nil {
		fail(c, err)
		return
	}
	auditNote(c, "order", idStr(id), fmt.Sprintf("%s: sheet %d, %d variations", d.Number, sheet.ID, sheet.VariationCount))
	c.JSON(http.StatusCreated, gin.H{"sheet": sheet, "drift": drift, "snapshot_differs": drift.Changed+drift.Skipped > 0})
}

func (h *H) listOrderCostingSheets(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	rows, err := h.S.ListOrderCostingSheets(c, id)
	respond(c, http.StatusOK, rows, err)
}
