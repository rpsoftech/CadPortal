// Package export renders the job-bag PDF and the costing and designs CSVs.
package export

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"image/jpeg"
	"io"
	"strconv"
	"strings"

	"github.com/go-pdf/fpdf"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
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
	"gold_rate_per_g", "gold_cost", "stone_ct", "stone_g", "stone_cost",
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
			r.StoneCarats.StringFixed(4), calc.RoundLineGrams(calc.StoneWeight(r.StoneCarats)).StringFixed(4), r.StoneCost.StringFixed(2),
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
	// The photo sits top right; the text beside it stops short of it, then the page is full width again.
	hasPhoto := placeJobBagPhoto(pdf, jb.Photo)
	if hasPhoto {
		pdf.SetRightMargin(15 + photoBoxW + 5)
	}

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
	if hasPhoto {
		pdf.SetRightMargin(15)
		if pdf.GetY() < 15+photoBoxH+3 {
			pdf.SetY(15 + photoBoxH + 3) // never print a table over the photo
		}
	}
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
	}{{"Size (mm)", 28, "L"}, {"Pcs", 20, "R"}, {"Code", 24, "L"}, {"Seat", 24, "L"}, {"ct / stone", 28, "R"}, {"Line ct", 28, "R"}, {"Line g", 28, "R"}}
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
			l.CaratsPerStone.StringFixed(4), l.LineCarats.StringFixed(4), l.LineWeightG.StringFixed(4)}
		for i, c := range cols {
			pdf.CellFormat(c.w, 6, vals[i], "1", 0, c.align, false, 0, "")
		}
		pdf.Ln(-1)
	}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(cols[0].w, 7, "Total", "1", 0, "L", true, 0, "")
	pdf.CellFormat(cols[1].w, 7, strconv.Itoa(jb.BOM.TotalPcs), "1", 0, "R", true, 0, "")
	pdf.CellFormat(cols[2].w+cols[3].w+cols[4].w, 7, "", "1", 0, "L", true, 0, "")
	pdf.CellFormat(cols[5].w, 7, jb.BOM.TotalCarats.StringFixed(4), "1", 0, "R", true, 0, "")
	pdf.CellFormat(cols[6].w, 7, calc.RoundLineGrams(calc.StoneWeight(jb.BOM.TotalCarats)).StringFixed(4), "1", 1, "R", true, 0, "")
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

// DesignsHeader is the column order of the designs export, which the design import reads.
var DesignsHeader = []string{"code", "name", "category", "tags", "volume_mm3", "ref_weight_g", "ref_metal"}

// WriteDesignsCSV writes the catalog in the design import format.
func WriteDesignsCSV(w io.Writer, rows []service.ImportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(DesignsHeader); err != nil {
		return err
	}
	for _, r := range rows {
		rec := []string{
			safeCell(r.Code), safeCell(r.Name), safeCell(r.Category), safeCell(r.Tags),
			r.VolumeMM3, r.RefWeightG, r.RefMetal,
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// The photo box on a job bag: top right, as wide as 50 mm and as tall as 45 mm.
const (
	photoBoxW = 50.0
	photoBoxH = 45.0
)

// placeJobBagPhoto draws a JPEG thumbnail in the top-right corner, scaled to fit its box. It reports
// whether a photo was drawn; unreadable data is ignored, because the photo is decoration.
func placeJobBagPhoto(pdf *fpdf.Fpdf, jpg []byte) bool {
	if len(jpg) == 0 {
		return false
	}
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return false
	}
	w, h := photoBoxW, photoBoxW*float64(cfg.Height)/float64(cfg.Width)
	if h > photoBoxH {
		h, w = photoBoxH, photoBoxH*float64(cfg.Width)/float64(cfg.Height)
	}
	opts := fpdf.ImageOptions{ImageType: "JPG"}
	pdf.RegisterImageOptionsReader("jobbag-photo", opts, bytes.NewReader(jpg))
	if pdf.Err() {
		pdf.ClearError() // a photo that fpdf cannot use must not spoil the sheet
		return false
	}
	x := 210 - 15 - w // right-aligned to the margin
	pdf.ImageOptions("jobbag-photo", x, 15, w, h, false, opts, 0, "")
	return !pdf.Err()
}
