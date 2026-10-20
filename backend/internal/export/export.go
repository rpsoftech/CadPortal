// Package export renders the job-bag PDF and the costing CSV.
package export

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// compress is switched off by tests so page text can be inspected.
var compress = true

// safeCell neutralises spreadsheet formula injection: a cell starting with
// = + - @ (or tab/CR) would be executed by Excel when the CSV is opened.
func safeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

var CostingHeader = []string{
	"variation", "metal", "stone_code", "net_g", "wastage_pct", "gold_g_with_wastage",
	"gold_rate_per_g", "gold_cost", "stone_ct", "stone_cost",
	"setting_pcs", "setting_rate_per_pc", "setting_cost", "total",
}

// WriteCostingCSV writes one row per costed variation/metal.
func WriteCostingCSV(w io.Writer, rows []service.CostRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(CostingHeader); err != nil {
		return err
	}
	for _, r := range rows {
		rec := []string{
			safeCell(r.VariationCode), safeCell(r.MetalCode), safeCell(r.StoneCode),
			r.NetG.StringFixed(3), r.WastagePct.String(), r.GoldG.StringFixed(3),
			r.GoldRatePerG.String(), r.GoldCost.StringFixed(2),
			r.StoneCarats.StringFixed(4), r.StoneCost.StringFixed(2),
			strconv.Itoa(r.SettingPcs), r.SettingRatePerPc.String(), r.SettingCost.StringFixed(2),
			r.Total.StringFixed(2),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// RenderJobBagPDF draws a one-page A4 job bag.
func RenderJobBagPDF(jb service.JobBag) ([]byte, error) {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(compress)
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetTitle("Job Bag "+jb.VariationCode, false)
	tr := pdf.UnicodeTranslatorFromDescriptor("") // UTF-8 -> cp1252 for core fonts
	pdf.AddPage()

	pdf.SetFont("Helvetica", "B", 20)
	pdf.CellFormat(0, 10, "JOB BAG", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 9, tr(jb.VariationCode), "", 1, "L", false, 0, "")
	pdf.Ln(2)

	kv := func(k, v string) {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(38, 6, k, "", 0, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 10)
		pdf.MultiCell(0, 6, tr(v), "", "L", false)
	}
	kv("Design", strings.TrimSpace(jb.DesignCode+" "+jb.DesignName))
	kv("Category", jb.Category)
	if len(jb.Tags) > 0 {
		kv("Tags", strings.Join(jb.Tags, ", "))
	}
	kv("Base input", jb.BaseInput)
	kv("Metal", jb.MetalCode)
	kv("Stone code", jb.BOM.StoneCode)
	pdf.Ln(2)

	// Weights
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "Weights", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.CellFormat(60, 6, "Net weight", "B", 0, "L", false, 0, "")
	pdf.CellFormat(40, 6, jb.NetG.StringFixed(3)+" g", "B", 1, "R", false, 0, "")
	pdf.CellFormat(60, 6, "Stone weight", "B", 0, "L", false, 0, "")
	pdf.CellFormat(40, 6, jb.BOM.StoneWeightG.StringFixed(3)+" g", "B", 1, "R", false, 0, "")
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(60, 6, "Gross weight", "B", 0, "L", false, 0, "")
	pdf.CellFormat(40, 6, jb.GrossG.StringFixed(3)+" g", "B", 1, "R", false, 0, "")
	pdf.Ln(4)

	// Stone BOM
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "Stone BOM", "", 1, "L", false, 0, "")
	cols := []struct {
		h     string
		w     float64
		align string
	}{{"Size (mm)", 28, "L"}, {"Pcs", 20, "R"}, {"Code", 24, "L"}, {"Seat", 24, "L"}, {"ct / stone", 32, "R"}, {"Line ct", 32, "R"}}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.SetFillColor(235, 235, 235)
	for _, c := range cols {
		pdf.CellFormat(c.w, 7, c.h, "1", 0, c.align, true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 10)
	for _, l := range jb.BOM.Lines {
		seat := "flexible"
		if l.Locked {
			seat = "locked"
		}
		vals := []string{l.SizeMM.StringFixed(2), strconv.Itoa(l.StoneCount), l.StoneCode, seat,
			l.CaratsPerStone.StringFixed(4), l.LineCarats.StringFixed(4)}
		for i, c := range cols {
			pdf.CellFormat(c.w, 6, vals[i], "1", 0, c.align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(cols[0].w, 7, "Total", "1", 0, "L", true, 0, "")
	pdf.CellFormat(cols[1].w, 7, strconv.Itoa(jb.BOM.TotalPcs), "1", 0, "R", true, 0, "")
	pdf.CellFormat(cols[2].w+cols[3].w+cols[4].w, 7, "", "1", 0, "L", true, 0, "")
	pdf.CellFormat(cols[5].w, 7, jb.BOM.TotalCarats.StringFixed(4), "1", 1, "R", true, 0, "")
	if len(jb.BOM.Lines) == 0 {
		pdf.SetFont("Helvetica", "I", 10)
		pdf.CellFormat(0, 7, "No stone seats.", "", 1, "L", false, 0, "")
	}
	pdf.Ln(4)

	// CAD files
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 7, "CAD files (SHA-256)", "", 1, "L", false, 0, "")
	file := func(label, name, sha string) {
		if sha == "" {
			kv(label, "not uploaded")
			return
		}
		kv(label, name+"\n"+sha)
	}
	file("3dm", jb.File3dmName, jb.File3dmSHA256)
	file("stl", jb.FileStlName, jb.FileStlSHA256)

	pdf.SetY(-20)
	pdf.SetFont("Helvetica", "I", 8)
	pdf.CellFormat(0, 5, fmt.Sprintf("Generated %s UTC", jb.GeneratedAt.Format("2006-01-02 15:04")), "", 0, "L", false, 0, "")

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
