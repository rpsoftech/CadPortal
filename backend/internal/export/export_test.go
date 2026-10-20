package export

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestWriteCostingCSV(t *testing.T) {
	cost := calc.Compute(calc.CostInput{
		NetG: d("3.050"), WastagePct: d("10"), GoldRatePerG: d("6000"),
		StoneLines: []calc.StoneCostLine{{LineCarats: d("0.3470"), RatePerCt: d("50000")}},
		SettingPcs: 37, SettingRatePerPc: d("25"),
	})
	rows := []service.CostRow{
		{VariationCode: "LR001-V1", MetalCode: "18KR", StoneCode: "2HD", NetG: d("3.050"),
			WastagePct: d("10"), GoldRatePerG: d("6000"), SettingRatePerPc: d("25"), Cost: cost},
		// Hostile design code must not become a formula.
		{VariationCode: "=HYPERLINK(\"x\")-V1", MetalCode: "18KR", StoneCode: "2HD", Cost: cost},
	}
	var buf bytes.Buffer
	if err := WriteCostingCSV(&buf, rows); err != nil {
		t.Fatal(err)
	}
	recs, err := csv.NewReader(&buf).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 || strings.Join(recs[0], ",") != strings.Join(CostingHeader, ",") {
		t.Fatalf("records = %v", recs)
	}
	want := []string{"LR001-V1", "18KR", "2HD", "3.050", "10", "3.355", "6000", "20130.00",
		"0.3470", "17350.00", "37", "25", "925.00", "38405.00"}
	for i, w := range want {
		if recs[1][i] != w {
			t.Errorf("col %s = %q want %q", CostingHeader[i], recs[1][i], w)
		}
	}
	if !strings.HasPrefix(recs[2][0], "'=") {
		t.Errorf("formula not neutralised: %q", recs[2][0])
	}
}

func TestRenderJobBagPDF(t *testing.T) {
	compress = false
	defer func() { compress = true }()

	bom := service.BOMResult{
		StoneCode: "2HD", TotalPcs: 37, TotalCarats: d("0.3470"), StoneWeightG: d("0.069"),
		Lines: []service.BOMLineOut{
			{SizeMM: d("1.10"), StoneCount: 24, StoneCode: "2HD", CaratsPerStone: d("0.0070"), LineCarats: d("0.1680")},
			{SizeMM: d("2.50"), StoneCount: 1, StoneCode: "2HD", Locked: true, CaratsPerStone: d("0.0650"), LineCarats: d("0.0650")},
		},
	}
	jb := service.JobBag{
		DesignCode: "LR001", DesignName: "Rose Halo – Ring", Category: "Rings", Tags: []string{"Women", "Floral"},
		VariationCode: "LR001-V1", BaseInput: "Volume 200.000 mm3", MetalCode: "18KR",
		NetG: d("3.050"), GrossG: d("3.119"), BOM: bom,
		File3dmName: "rose.3dm", File3dmSHA256: strings.Repeat("ab", 32),
		GeneratedAt: time.Date(2026, 10, 20, 9, 30, 0, 0, time.UTC),
	}
	pdf, err := RenderJobBagPDF(jb)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) || !bytes.HasSuffix(bytes.TrimSpace(pdf), []byte("%%EOF")) {
		t.Fatal("not a well-formed PDF")
	}
	for _, s := range []string{"JOB BAG", "LR001-V1", "3.119 g", "0.3470", "locked", "not uploaded", strings.Repeat("ab", 32)} {
		if !bytes.Contains(pdf, []byte(s)) {
			t.Errorf("PDF missing %q", s)
		}
	}

	// No seats still renders.
	jb.BOM.Lines, jb.BOM.TotalPcs = nil, 0
	if _, err := RenderJobBagPDF(jb); err != nil {
		t.Errorf("no seats: %v", err)
	}
}
