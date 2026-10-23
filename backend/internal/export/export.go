// Package export renders the job-bag PDF and the costing and designs CSVs.
package export

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
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
	"setting_pcs", "setting_rate_per_pc", "setting_cost", "total", "stone_pricing",
}

// stonePricing says how each stone line was priced and the quantity priced, e.g.
// "24 pcs x 2.00/pc; 0.0336 g x 5.00/g; 0.0650 ct x 900.00/ct".
func stonePricing(lines []calc.PricedLine) string {
	parts := make([]string, len(lines))
	for i, l := range lines {
		qty := l.Qty.StringFixed(4)
		if l.Basis == "pc" {
			qty = l.Qty.StringFixed(0)
		}
		unit := strings.TrimSuffix(l.Unit(), "s") // "pcs" prices per "pc"
		parts[i] = fmt.Sprintf("%s %s x %s/%s", qty, l.Unit(), l.Rate.StringFixed(2), unit)
	}
	return strings.Join(parts, "; ")
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
			r.Total.StringFixed(2), stonePricing(r.StonePriced),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
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

// The photo box on a job bag: top right below the QR code, as wide as 50 mm and as tall as 45 mm.
const (
	photoBoxW = 50.0
	photoBoxH = 45.0
)

// placeJobBagPhoto draws a JPEG thumbnail in the top-right corner, scaled to fit its box. It reports
// whether a photo was drawn; unreadable data is ignored, because the photo is decoration.
func placeJobBagPhoto(pdf *fpdf.Fpdf, jpg []byte, y float64) bool {
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
	name := fmt.Sprintf("jobbag-photo-%x", sha256.Sum256(jpg)) // one copy per distinct photo, however many pages show it
	if pdf.GetImageInfo(name) == nil {
		pdf.RegisterImageOptionsReader(name, opts, bytes.NewReader(jpg))
	}
	if pdf.Err() {
		pdf.ClearError() // a photo that fpdf cannot use must not spoil the sheet
		return false
	}
	x := 210 - 15 - w // right-aligned to the margin
	pdf.ImageOptions(name, x, y, w, h, false, opts, 0, "")
	return !pdf.Err()
}

// CompanyName is printed on every job bag next to the logo.
const CompanyName = "GOLDEN OAK JEWELS LLP"

//go:embed assets/logo.png
var brandLogoPNG []byte

// placeJobBagBrand draws the logo and company name at the top left and moves the cursor below them.
// The logo is decoration: if fpdf cannot use it the name is still printed.
func placeJobBagBrand(pdf *fpdf.Fpdf) {
	const logoH = 14.0
	x, y := pdf.GetX(), pdf.GetY()
	opts := fpdf.ImageOptions{ImageType: "PNG"}
	pdf.RegisterImageOptionsReader("brand-logo", opts, bytes.NewReader(brandLogoPNG))
	textX := x
	if pdf.Err() {
		pdf.ClearError()
	} else if info := pdf.GetImageInfo("brand-logo"); info != nil {
		w := logoH * info.Width() / info.Height()
		pdf.ImageOptions("brand-logo", x, y, w, logoH, false, opts, 0, "")
		textX = x + w + 3
	}
	pdf.SetXY(textX, y+logoH/2-3)
	pdf.SetFont("Helvetica", "B", 11)
	pdf.CellFormat(0, 6, CompanyName, "", 1, "L", false, 0, "")
	pdf.SetXY(x, y+logoH+4)
}
