package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestExportsOverHTTP(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()

	// Fixture variation: 200 mm3, 2HD pointers (test-only), 37 pcs.
	nd := func(s string) decimal.NullDecimal { return decimal.NullDecimal{Decimal: mustDec(s), Valid: true} }
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070")},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095")},
		{SizeMM: mustDec("2.50"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0650")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	cats, _ := svc.ListCategories(ctx)
	des, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR060", CategoryID: cats[0].ID})
	vol := mustDec("200")
	v, _ := svc.CreateVariation(ctx, des.ID, service.BaseInput{VolumeMM3: &vol})
	if _, err := svc.SetSeats(ctx, v.ID, []service.SeatInput{
		{SizeMM: mustDec("1.10"), StoneCount: 24},
		{SizeMM: mustDec("1.20"), StoneCount: 12},
		{SizeMM: mustDec("2.50"), StoneCount: 1, LockedStoneCode: "2HD"},
	}); err != nil {
		t.Fatal(err)
	}

	pdfBody := []byte(`{"variation_id":` + itoa(v.ID) + `,"metal_code":"18KR","stone_code":"2HD"}`)
	csvBody := []byte(`{"items":[{"variation_id":` + itoa(v.ID) + `,"metal_code":"18KR","stone_code":"2HD"}],
		"gold_rate_per_g":"6000","setting_rate_per_pc":"25"}`)

	// Job-bag PDF: every role; no token is 401.
	if w := do(r, "POST", "/api/v1/exports/job-bag-pdf", "", "application/json", pdfBody); w.Code != 401 {
		t.Fatalf("pdf no token: %d", w.Code)
	}
	w := do(r, "POST", "/api/v1/exports/job-bag-pdf", token(t, auth.Production), "application/json", pdfBody)
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" ||
		!strings.HasPrefix(w.Body.String(), "%PDF-") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "jobbag-LR060-V1.pdf") {
		t.Fatalf("pdf: %d %q %q", w.Code, w.Header(), w.Body.String()[:min(20, w.Body.Len())])
	}
	if w := do(r, "POST", "/api/v1/exports/job-bag-pdf", token(t, auth.Admin), "application/json", []byte(`{}`)); w.Code != 400 {
		t.Errorf("pdf empty body: %d", w.Code)
	}

	// Costing: production is forbidden; with wastage/rates unset it is 422 listing them.
	if w := do(r, "POST", "/api/v1/exports/costing-csv", token(t, auth.Production), "application/json", csvBody); w.Code != 403 {
		t.Fatalf("csv production: %d", w.Code)
	}
	w = do(r, "POST", "/api/v1/exports/costing-csv", token(t, auth.Designer), "application/json", csvBody)
	var miss struct {
		Wastage []string                        `json:"metals_missing_wastage"`
		Rates   []struct{ SizeMM, Code string } `json:"stone_rates_missing"`
	}
	json.Unmarshal(w.Body.Bytes(), &miss) //nolint:errcheck
	if w.Code != 422 || len(miss.Wastage) != 1 || miss.Wastage[0] != "18KR" || len(miss.Rates) != 3 {
		t.Fatalf("missing inputs: %d %s", w.Code, w.Body)
	}

	// Fill in test-only wastage and rates, then the CSV is produced.
	m, _ := svc.Q.GetMetalByCode(ctx, "18KR")
	if _, err := svc.UpdateMetal(ctx, m.ID, service.MetalUpdate{SpecificGravity: m.SpecificGravity, WastagePct: nd("10"), Active: true}); err != nil {
		t.Fatal(err)
	}
	rate := nd("50000")
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070"), RatePerCt: rate},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095"), RatePerCt: rate},
		{SizeMM: mustDec("2.50"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0650"), RatePerCt: rate},
	}, ""); err != nil {
		t.Fatal(err)
	}
	w = do(r, "POST", "/api/v1/exports/costing-csv", token(t, auth.Admin), "application/json", csvBody)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d %s", w.Code, w.Body)
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 2 || !strings.HasSuffix(lines[1], ",38405.00") || !strings.HasPrefix(lines[1], "LR060-V1,18KR,2HD,3.050,10,3.355,") {
		t.Errorf("csv = %q", lines)
	}
	if w := do(r, "POST", "/api/v1/exports/costing-csv", token(t, auth.Admin), "application/json",
		[]byte(`{"items":[],"gold_rate_per_g":"1"}`)); w.Code != 400 {
		t.Errorf("empty items: %d", w.Code)
	}
}

func itoa(i int64) string { return decimal.NewFromInt(i).String() }
