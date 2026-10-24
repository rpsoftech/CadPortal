package export

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// Order summary PDF and order CSV exports. Every figure comes from the order's own snapshots: an order
// prints the same after its designs change, and no price or wastage figure is on it.

// OrderStatusLabel words a status the way the screens do.
func OrderStatusLabel(s string) string {
	return strings.ReplaceAll(s, "_", " ")
}

// criteriaNote turns the optional "how the selection was found" object into one readable line.
func criteriaNote(raw json.RawMessage) string {
	var obj map[string]any
	if len(raw) == 0 || json.Unmarshal(raw, &obj) != nil || len(obj) == 0 {
		return ""
	}
	keys := make([]string, 0, len(obj))
	for k := range obj {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		var v string
		switch x := obj[k].(type) {
		case []any:
			items := make([]string, len(x))
			for i, e := range x {
				items[i] = fmt.Sprint(e)
			}
			v = strings.Join(items, ", ")
		case nil:
			continue
		default:
			v = fmt.Sprint(x)
		}
		if v != "" {
			parts = append(parts, k+": "+v)
		}
	}
	note := strings.Join(parts, "; ")
	if r := []rune(note); len(r) > 400 {
		note = string(r[:400]) + "..."
	}
	return note
}

type orderCol struct {
	h     string
	w     float64
	align string
}

var orderCols = []orderCol{
	{"Variation", 34, "L"}, {"Metal", 14, "L"}, {"Stone", 14, "L"}, {"Qty", 12, "R"},
	{"Net g", 22, "R"}, {"Stone g", 20, "R"}, {"Gross g", 22, "R"}, {"Stone %", 16, "R"}, {"Line gross g", 26, "R"},
}

const orderRowH = 6.0

// RenderOrderSummaryPDF draws the order summary: a first block with the number, QR, status, dates, notes
// and totals, then the lines in a table that continues over as many pages as it needs, repeating its
// header. qr is the text the QR code holds (see Service.OrderQR).
func RenderOrderSummaryPDF(d service.OrderDetail, totals service.OrderTotals, qr string, generated string) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(compress)
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(false, 0)
	pdf.SetTitle("Order "+d.Number, false)
	pdf.AliasNbPages("{nb}")
	tr := pdf.UnicodeTranslatorFromDescriptor("")
	pdf.SetFooterFunc(func() {
		pdf.SetY(-20)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.CellFormat(120, 5, tr(fmt.Sprintf("%s - generated %s UTC", d.Number, generated)), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 5, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	pdf.AddPage()

	if err := placeQR(pdf, qr, d.Number, tr); err != nil {
		return nil, err
	}
	pdf.SetXY(15, 15)
	pdf.SetRightMargin(15 + qrBoxMM + 3)
	placeJobBagBrand(pdf)
	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, "ORDER SUMMARY", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 9, tr(d.Number), "", 1, "L", false, 0, "")
	pdf.Ln(2)
	kv := func(k, v string) {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(32, 6, k, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(0, 6, tr(v), "", "L", false)
	}
	if d.Name != "" {
		kv("Name", d.Name)
	}
	kv("Status", OrderStatusLabel(d.Status))
	if d.DueDate != nil {
		kv("Due date", *d.DueDate)
	}
	kv("Metal", d.MetalCode)
	kv("Stone code", d.StoneCode)
	kv("Created", d.CreatedAt.UTC().Format("2006-01-02")+" by "+d.CreatedByName)
	if d.Notes != "" {
		kv("Notes", d.Notes)
	}
	if c := criteriaNote(d.Criteria); c != "" {
		kv("Selection", c)
	}
	pdf.SetRightMargin(15)
	if y := qrY + qrBoxMM + 6; pdf.GetY() < y {
		pdf.SetY(y)
	}
	pdf.Ln(2)

	// Totals: sum of line weight x quantity.
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "Totals", "", 1, "L", false, 0, "")
	row := func(label, value string, bold bool) {
		style := ""
		if bold {
			style = "B"
		}
		pdf.SetFont("Helvetica", style, 10)
		pdf.CellFormat(70, 6, label, "B", 0, "L", false, 0, "")
		pdf.CellFormat(50, 6, value, "B", 1, "R", false, 0, "")
	}
	row("Lines", strconv.Itoa(totals.Lines), false)
	row("Pieces", strconv.Itoa(totals.Pieces), false)
	row("Stones to set", strconv.Itoa(totals.StonePcs), false)
	row("Total net weight", totals.NetG.StringFixed(3)+" g", false)
	row("Total stone weight", totals.StoneG.StringFixed(3)+" g", false)
	row("Total gross weight", totals.GrossG.StringFixed(3)+" g", true)
	row("Average stone %", totals.StonePct.StringFixed(2)+" %", false)
	pdf.Ln(5)

	_, pageH := pdf.GetPageSize()
	limit := pageH - 25 // above the footer
	header := func() {
		pdf.SetFont("Helvetica", "B", 9)
		pdf.SetFillColor(235, 235, 235)
		for _, c := range orderCols {
			pdf.CellFormat(c.w, 7, c.h, "1", 0, c.align, true, 0, "")
		}
		pdf.Ln(-1)
	}
	if pdf.GetY()+7+orderRowH > limit {
		pdf.AddPage()
	}
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "Lines (weights per piece, from the order's snapshot)", "", 1, "L", false, 0, "")
	header()
	pdf.SetFont("Helvetica", "", 9)
	for _, l := range d.Lines {
		if pdf.GetY()+orderRowH > limit {
			pdf.AddPage()
			header()
			pdf.SetFont("Helvetica", "", 9)
		}
		sn := l.Snapshot
		lineGross := sn.GrossG.Mul(decimal.NewFromInt(int64(l.Quantity)))
		vals := []string{sn.VariationCode, sn.MetalCode, sn.StoneCode, strconv.Itoa(l.Quantity),
			sn.NetG.StringFixed(3), sn.StoneG.StringFixed(3), sn.GrossG.StringFixed(3), sn.StonePct.StringFixed(2), lineGross.StringFixed(3)}
		for i, c := range orderCols {
			text := tr(vals[i])
			if i == 0 { // a long code is cut, not allowed to run into the next column
				for pdf.GetStringWidth(text) > c.w-2 && len(text) > 1 {
					text = text[:len(text)-1]
				}
			}
			pdf.CellFormat(c.w, orderRowH, text, "1", 0, c.align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	if len(d.Lines) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.CellFormat(0, 7, "No lines.", "", 1, "L", false, 0, "")
	}
	// A last row repeats the totals under the table so a printed copy reads without turning back.
	if len(d.Lines) > 0 {
		if pdf.GetY()+orderRowH > limit {
			pdf.AddPage()
			header()
		}
		pdf.SetFont("Helvetica", "B", 9)
		w := orderCols[0].w + orderCols[1].w + orderCols[2].w
		pdf.CellFormat(w, orderRowH, "Total", "1", 0, "L", true, 0, "")
		pdf.CellFormat(orderCols[3].w, orderRowH, strconv.Itoa(totals.Pieces), "1", 0, "R", true, 0, "")
		pdf.CellFormat(orderCols[4].w+orderCols[5].w+orderCols[6].w+orderCols[7].w, orderRowH, "", "1", 0, "R", true, 0, "")
		pdf.CellFormat(orderCols[8].w, orderRowH, totals.GrossG.StringFixed(3), "1", 1, "R", true, 0, "")
	}
	if err := pdf.Error(); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// OrderLinesHeader is the column order of one order's CSV.
var OrderLinesHeader = []string{
	"order", "design", "design_name", "variation", "category", "metal", "stone_code", "quantity",
	"net_g", "stone_g", "stone_ct", "gross_g", "stone_pct", "stone_pcs",
	"line_net_g", "line_stone_g", "line_gross_g",
}

// WriteOrderCSV writes one order: a row per line (weights per piece and for the whole line) and a
// TOTAL row. Text that came from people is neutralised against spreadsheet formula injection.
func WriteOrderCSV(w io.Writer, d service.OrderDetail, totals service.OrderTotals) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(OrderLinesHeader); err != nil {
		return err
	}
	for _, l := range d.Lines {
		sn := l.Snapshot
		q := decimal.NewFromInt(int64(l.Quantity))
		rec := []string{
			safeCell(d.Number), safeCell(sn.DesignCode), safeCell(sn.DesignName), safeCell(sn.VariationCode), safeCell(sn.CategoryName),
			safeCell(sn.MetalCode), safeCell(sn.StoneCode), strconv.Itoa(l.Quantity),
			sn.NetG.StringFixed(3), sn.StoneG.StringFixed(3), sn.StoneCt.StringFixed(4), sn.GrossG.StringFixed(3),
			sn.StonePct.StringFixed(2), strconv.Itoa(sn.TotalPcs),
			sn.NetG.Mul(q).StringFixed(3), sn.StoneG.Mul(q).StringFixed(3), sn.GrossG.Mul(q).StringFixed(3),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	total := []string{
		safeCell(d.Number), "TOTAL", "", "", "", "", "", strconv.Itoa(totals.Pieces),
		"", "", "", "", totals.StonePct.StringFixed(2), strconv.Itoa(totals.StonePcs),
		totals.NetG.StringFixed(3), totals.StoneG.StringFixed(3), totals.GrossG.StringFixed(3),
	}
	if err := cw.Write(total); err != nil {
		return err
	}
	cw.Flush()
	return cw.Error()
}

// OrdersListHeader is the column order of the orders list CSV.
var OrdersListHeader = []string{
	"number", "name", "status", "due_date", "metal", "stone_code", "lines", "pieces", "created_by", "created_at",
}

// WriteOrdersCSV writes a list of orders, one row each.
func WriteOrdersCSV(w io.Writer, rows []service.OrderSummary) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(OrdersListHeader); err != nil {
		return err
	}
	for _, o := range rows {
		due := ""
		if o.DueDate != nil {
			due = *o.DueDate
		}
		rec := []string{
			safeCell(o.Number), safeCell(o.Name), o.Status, due, safeCell(o.MetalCode), safeCell(o.StoneCode),
			strconv.Itoa(o.LineCount), strconv.FormatInt(o.TotalQuantity, 10), safeCell(o.CreatedByName),
			o.CreatedAt.UTC().Format("2006-01-02 15:04"),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}
