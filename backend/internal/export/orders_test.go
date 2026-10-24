package export

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func bigOrder(lines int) service.OrderDetail {
	o := service.OrderDetail{Notes: "Handle with care"}
	o.Number, o.Status, o.MetalCode, o.StoneCode = "ORD-0042", "in_production", "18KR", "2HD"
	o.CreatedAt = time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC)
	for i := 1; i <= lines; i++ {
		o.Lines = append(o.Lines, service.OrderLine{ID: int64(i), Position: i, Quantity: 2, Snapshot: service.LineSnapshot{
			DesignCode: "LR0001", VariationCode: fmt.Sprintf("LR0001-V%d", i), MetalCode: "18KR", StoneCode: "2HD",
			NetG: d("3.050"), StoneG: d("0.069"), StoneCt: d("0.3470"), GrossG: d("3.119"), StonePct: d("2.21"), TotalPcs: 37,
		}})
	}
	return o
}

func TestOrderTotalsAreWeightTimesQuantity(t *testing.T) {
	o := bigOrder(2)
	o.Lines[1].Quantity = 5
	tot := service.ComputeOrderTotals(o)
	// (2 + 5) pieces of 3.050 net, 0.069 stone, 3.119 gross; 37 stones each.
	if tot.Pieces != 7 || tot.Lines != 2 || tot.StonePcs != 259 || tot.NetG.StringFixed(3) != "21.350" ||
		tot.StoneG.StringFixed(3) != "0.483" || tot.GrossG.StringFixed(3) != "21.833" || tot.StoneCt.StringFixed(4) != "2.4290" || tot.StonePct.StringFixed(2) != "2.23" {
		t.Errorf("totals = %+v", tot)
	}
	if got := service.ComputeOrderTotals(service.OrderDetail{}); got.Pieces != 0 || got.StonePct.Sign() != 0 {
		t.Errorf("empty order = %+v", got)
	}
}

func TestOrderSummaryPDFPaginatesAndStaysModestForFiveHundredLines(t *testing.T) {
	compress = false
	defer func() { compress = true }()

	o := bigOrder(service.MaxOrderLines)
	tot := service.ComputeOrderTotals(o)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	pdf, err := RenderOrderSummaryPDF(o, tot, "ORD-0042", "2026-10-24 10:00")
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("500 lines: PDF %d KB, live heap grew %d KB", len(pdf)/1024, (int64(after.HeapAlloc)-int64(before.HeapAlloc))/1024)
	if len(pdf) > 2<<20 {
		t.Errorf("PDF is %d KB for 500 lines", len(pdf)/1024)
	}
	if grew := int64(after.HeapAlloc) - int64(before.HeapAlloc); grew > 30<<20 {
		t.Errorf("live heap grew %d MB", grew>>20)
	}

	pages := pageStreams(t, pdf)
	if len(pages) < 10 || len(pages) > 25 {
		t.Fatalf("%d pages", len(pages))
	}
	text := string(pdf)
	// Every line is there once, the table header repeats on each page, and the footer counts the pages.
	for _, i := range []int{1, 250, 500} {
		if n := strings.Count(text, fmt.Sprintf("(LR0001-V%d)", i)); n != 1 {
			t.Errorf("line %d appears %d times", i, n)
		}
	}
	if n := strings.Count(text, "(Variation)"); n != len(pages) {
		t.Errorf("table header on %d of %d pages", n, len(pages))
	}
	// 500 lines x 2 pieces: 1000 pieces, 3050.000 g net, 3119.000 g gross.
	for _, want := range []string{"(1000)", "3050.000 g", "3119.000 g", "ORDER SUMMARY", fmt.Sprintf("of %d", len(pages)), "in production"} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF lacks %q", want)
		}
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Error("not a PDF")
	}

	// The QR code on the first page decodes to the text it was given.
	if got := scanQR(t, pages[0]); got != "ORD-0042" {
		t.Errorf("QR = %q", got)
	}
}

func TestOrderSummaryPDFWithoutLinesStillRenders(t *testing.T) {
	o := bigOrder(0)
	if _, err := RenderOrderSummaryPDF(o, service.ComputeOrderTotals(o), "ORD-0042", "now"); err != nil {
		t.Fatal(err)
	}
}

func TestOrderCSVNeutralisesFormulasEverywhere(t *testing.T) {
	o := bigOrder(1)
	o.Number = "=ORD"
	o.Lines[0].Snapshot.DesignName = "+cmd"
	o.Lines[0].Snapshot.CategoryName = "@x"
	var buf bytes.Buffer
	if err := WriteOrderCSV(&buf, o, service.ComputeOrderTotals(o)); err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(buf.String(), "\n") {
		for _, cell := range strings.Split(line, ",") {
			if cell != "" && strings.ContainsRune("=+@\t\r", rune(cell[0])) {
				t.Errorf("unsafe cell %q in %q", cell, line)
			}
		}
	}
	buf.Reset()
	rows := []service.OrderSummary{{Number: "ORD-1", Name: "-1+1", CreatedByName: "@me", Status: "draft"}}
	if err := WriteOrdersCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), ",-1+1") || strings.Contains(buf.String(), ",@me") {
		t.Errorf("list csv = %s", buf.String())
	}
}
