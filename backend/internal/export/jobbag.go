package export

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"

	"github.com/boombuler/barcode"
	"github.com/boombuler/barcode/qr"
	"github.com/go-pdf/fpdf"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// The QR code sits top right: a box of qrBoxMM that includes the quiet zone (4 modules on every side),
// with the human-readable code printed under it. Error correction is H, so a scuffed print still reads.
const (
	qrBoxMM = 30.0
	qrX     = 210 - 15 - qrBoxMM
	qrY     = 15.0
	qrQuiet = 4 // modules
)

// qrMatrix encodes text at the highest error correction level.
func qrMatrix(text string) (barcode.Barcode, error) {
	return qr.Encode(text, qr.H, qr.Auto)
}

// placeQR draws the code as vector squares (horizontal runs merged), then its text label under it.
func placeQR(pdf *fpdf.Fpdf, text, label string, tr func(string) string) error {
	bc, err := qrMatrix(text)
	if err != nil {
		return fmt.Errorf("qr code: %w", err)
	}
	n := bc.Bounds().Dx()
	mod := qrBoxMM / float64(n+2*qrQuiet)
	ox, oy := qrX+qrQuiet*mod, qrY+qrQuiet*mod
	pdf.SetFillColor(0, 0, 0)
	dark := func(x, y int) bool { r, _, _, _ := bc.At(x, y).RGBA(); return r == 0 }
	for y := 0; y < n; y++ {
		for x := 0; x < n; {
			if !dark(x, y) {
				x++
				continue
			}
			start := x
			for x < n && dark(x, y) {
				x++
			}
			pdf.Rect(ox+float64(start)*mod, oy+float64(y)*mod, float64(x-start)*mod, mod, "F")
		}
	}
	// The label shrinks to fit the box, so a long code never runs into the text beside it.
	size := 8.0
	pdf.SetFont("Helvetica", "B", size)
	for pdf.GetStringWidth(tr(label)) > qrBoxMM && size > 4 {
		size -= 0.5
		pdf.SetFontSize(size)
	}
	pdf.SetXY(qrX, qrY+qrBoxMM)
	pdf.CellFormat(qrBoxMM, 4, tr(label), "", 0, "C", false, 0, "")
	return nil
}

// JobBagsPDF builds one PDF out of any number of job bags, a page (or more) per bag, in the order added.
type JobBagsPDF struct {
	pdf *fpdf.Fpdf
	tr  func(string) string
	cur service.JobBag // the bag being drawn; the footer reads it
	err error
}

func NewJobBagsPDF(title string) *JobBagsPDF {
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.SetCompression(compress)
	pdf.SetMargins(15, 15, 15)
	pdf.SetAutoPageBreak(true, 15)
	pdf.SetTitle(title, false)
	pdf.AliasNbPages("{nb}")
	b := &JobBagsPDF{pdf: pdf, tr: pdf.UnicodeTranslatorFromDescriptor("")} // UTF-8 -> cp1252 for core fonts
	pdf.SetFooterFunc(func() {
		pdf.SetY(-20)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.CellFormat(120, 5, fmt.Sprintf("Generated %s UTC", b.cur.GeneratedAt.Format("2006-01-02 15:04")), "", 0, "L", false, 0, "")
		pdf.CellFormat(0, 5, fmt.Sprintf("Page %d of {nb}", pdf.PageNo()), "", 0, "R", false, 0, "")
	})
	return b
}

// RenderJobBagPDF draws a single job bag.
func RenderJobBagPDF(jb service.JobBag) ([]byte, error) {
	b := NewJobBagsPDF("Job Bag " + jb.VariationCode)
	if err := b.AddBag(jb); err != nil {
		return nil, err
	}
	return b.Bytes()
}

// AddBag draws one job bag. Its photo is not kept by the caller afterwards: only a distinct photo
// is stored in the PDF, once.
func (b *JobBagsPDF) AddBag(jb service.JobBag) error {
	if b.err != nil {
		return b.err
	}
	pdf, tr := b.pdf, b.tr
	pdf.AddPage()
	b.cur = jb
	pdf.SetRightMargin(15)

	text := jb.QR
	if text == "" {
		text = jb.VariationCode
	}
	if err := placeQR(pdf, text, jb.VariationCode, tr); err != nil {
		b.err = err
		return err
	}
	// The photo sits under the QR code; the text beside them stops short, then the page is full width again.
	photoY := qrY + qrBoxMM + 4
	hasPhoto := placeJobBagPhoto(pdf, jb.Photo, photoY)
	colBottom := qrY + qrBoxMM + 4 // QR box and its label
	rightMargin := 15 + qrBoxMM + 3
	if hasPhoto {
		colBottom = photoY + photoBoxH
		rightMargin = 15 + photoBoxW + 5
	}
	pdf.SetXY(15, 15)
	pdf.SetRightMargin(15 + qrBoxMM + 3)
	placeJobBagBrand(pdf)
	pdf.SetRightMargin(rightMargin)
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
	pdf.SetRightMargin(15)
	if pdf.GetY() < colBottom+3 {
		pdf.SetY(colBottom + 3) // never print a table over the photo or the QR code
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
	return nil
}

// AddSkippedPage appends a final page naming the bags left out and why.
func (b *JobBagsPDF) AddSkippedPage(skipped []service.JobBagFailure) {
	if b.err != nil || len(skipped) == 0 {
		return
	}
	pdf, tr := b.pdf, b.tr
	pdf.AddPage()
	pdf.SetRightMargin(15)
	pdf.SetFont("Helvetica", "B", 16)
	pdf.CellFormat(0, 10, "NOT PRINTED", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.MultiCell(0, 6, tr(fmt.Sprintf("%d variation(s) were left out because stone-weight data is missing. "+
		"Add the rows under Admin, Stone matrix, then print them again.", len(skipped))), "", "L", false)
	pdf.Ln(2)
	for _, f := range skipped {
		pdf.SetFont("Helvetica", "B", 10)
		pdf.MultiCell(0, 6, tr(f.VariationCode+" (design "+f.DesignCode+", stone code "+f.StoneCode+")"), "", "L", false)
		pdf.SetFont("Helvetica", "", 10)
		for _, p := range f.Missing {
			pdf.MultiCell(0, 5, tr(fmt.Sprintf("   no row for size %s mm, code %s", p.SizeMM, p.Code)), "", "L", false)
		}
	}
}

// Bytes finishes the document.
func (b *JobBagsPDF) Bytes() ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	var buf bytes.Buffer
	if err := b.pdf.Output(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
