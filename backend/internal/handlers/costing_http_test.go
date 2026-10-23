package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

type costEnv struct {
	r        *gin.Engine
	svc      *service.Service
	d1, d2   int64
	v1, v2   int64
	admin    string
	designer string
	other    string // a second designer
	prod     string
	adminID  int64
	desID    int64
}

func userToken(t *testing.T, svc *service.Service, email string, role auth.Role) (string, int64) {
	t.Helper()
	u, err := svc.CreateUser(context.Background(), service.CreateUserInput{Email: email, Name: email, Password: "a-long-test-password-1", Role: string(role)})
	if err != nil {
		t.Fatal(err)
	}
	tok, err := auth.Issue(testSecret, fmt.Sprint(u.ID), role, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + tok, u.ID
}

func newCostEnv(t *testing.T) *costEnv {
	t.Helper()
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	nd := func(s string) decimal.NullDecimal { return decimal.NullDecimal{Decimal: mustDec(s), Valid: true} }
	rate := nd("50000")
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070"), RatePerCt: rate},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095"), RatePerCt: rate},
		{SizeMM: mustDec("2.50"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0650"), RatePerCt: rate},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	m, _ := svc.Q.GetMetalByCode(ctx, "18KR")
	if _, err := svc.UpdateMetal(ctx, m.ID, service.MetalUpdate{SpecificGravity: m.SpecificGravity, WastagePct: nd("10"), Active: true}); err != nil {
		t.Fatal(err)
	}
	cats, _ := svc.ListCategories(ctx)
	mk := func(code, vol string, seats []service.SeatInput) (int64, int64) {
		des, err := svc.CreateDesign(ctx, service.DesignInput{Code: code, CategoryID: cats[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		v := mustDec(vol)
		va, err := svc.CreateVariation(ctx, des.ID, service.BaseInput{VolumeMM3: &v})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetSeats(ctx, va.ID, seats); err != nil {
			t.Fatal(err)
		}
		return des.ID, va.ID
	}
	e := &costEnv{r: r, svc: svc}
	e.d1, e.v1 = mk("LR080", "200", []service.SeatInput{
		{SizeMM: mustDec("1.10"), StoneCount: 24}, {SizeMM: mustDec("1.20"), StoneCount: 12},
		{SizeMM: mustDec("2.50"), StoneCount: 1, LockedStoneCode: "2HD"},
	})
	e.d2, e.v2 = mk("LR081", "100", []service.SeatInput{{SizeMM: mustDec("1.10"), StoneCount: 10}})
	e.admin, e.adminID = userToken(t, svc, "admin@example.com", auth.Admin)
	e.designer, e.desID = userToken(t, svc, "designer@example.com", auth.Designer)
	e.other, _ = userToken(t, svc, "designer2@example.com", auth.Designer)
	e.prod, _ = userToken(t, svc, "prod@example.com", auth.Production)
	return e
}

func (e *costEnv) body(extra string) []byte {
	return []byte(fmt.Sprintf(`{"design_ids":[%d,%d],"metal_code":"18KR","stone_code":"2HD","gold_rate_per_g":"6000","setting_rate_per_pc":"25"%s}`, e.d1, e.d2, extra))
}

func TestCostingSheetsRolesAndEndpoints(t *testing.T) {
	e := newCostEnv(t)
	body := e.body(`,"name":"Autumn"`)

	// The production role (and no token) is refused on every costing route.
	routes := []struct{ method, path string }{
		{"POST", "/api/v1/exports/costing-csv"}, {"POST", "/api/v1/exports/costing-preview"},
		{"POST", "/api/v1/costing-sheets"}, {"GET", "/api/v1/costing-sheets"},
		{"GET", "/api/v1/costing-sheets/1"}, {"GET", "/api/v1/costing-sheets/1/csv"},
		{"DELETE", "/api/v1/costing-sheets/1"}, {"POST", "/api/v1/costing-sheets/1/recost"},
	}
	for _, rt := range routes {
		if w := do(e.r, rt.method, rt.path, e.prod, "application/json", body); w.Code != 403 {
			t.Errorf("production %s %s: %d", rt.method, rt.path, w.Code)
		}
		if w := do(e.r, rt.method, rt.path, "", "application/json", body); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", rt.method, rt.path, w.Code)
		}
	}

	// Multi-design CSV: header, one row per variation, a TOTAL row.
	w := do(e.r, "POST", "/api/v1/exports/costing-csv", e.designer, "application/json", e.body(""))
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") || w.Header().Get("X-Costing-Incomplete") != "0" {
		t.Fatalf("csv: %d %s", w.Code, w.Body)
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "LR080-V1,18KR,2HD,3.050,10,3.355,6000,20130.00,0.3470,0.0694,17350.00,37,25,925.00,38405.00") ||
		!strings.HasPrefix(lines[2], "LR081-V1,") || !strings.HasPrefix(lines[3], "TOTAL,") || !strings.HasSuffix(lines[3], ",52220.00") {
		t.Errorf("csv = %q", lines)
	}
	wantCSV := w.Body.String()

	// Preview: JSON with rows and totals.
	w = do(e.r, "POST", "/api/v1/exports/costing-preview", e.designer, "application/json", e.body(""))
	var pv struct {
		Rows     []map[string]any `json:"rows"`
		Totals   map[string]any   `json:"totals"`
		Problems []any            `json:"problems"`
		Complete bool             `json:"complete"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil || w.Code != 200 || len(pv.Rows) != 2 || !pv.Complete || pv.Totals["total"] != "52220" {
		t.Fatalf("preview: %d %s", w.Code, w.Body)
	}
	if pv.Rows[0]["design_code"] != "LR080" || pv.Rows[0]["gross_g"] != "3.119" {
		t.Errorf("preview row = %v", pv.Rows[0])
	}

	// Create a sheet; the response is the detail.
	w = do(e.r, "POST", "/api/v1/costing-sheets", e.designer, "application/json", body)
	var sheet service.SheetDetail
	if err := json.Unmarshal(w.Body.Bytes(), &sheet); err != nil || w.Code != 201 || sheet.ID == 0 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if sheet.Name != "Autumn" || sheet.VariationCount != 2 || sheet.DesignCount != 2 || !sheet.Total.Equal(mustDec("52220")) ||
		sheet.CreatedBy == nil || *sheet.CreatedBy != e.desID || sheet.CreatedByName != "designer@example.com" {
		t.Errorf("sheet = %+v", sheet.SheetSummary)
	}
	if w := do(e.r, "POST", "/api/v1/costing-sheets", e.designer, "application/json", e.body("")); w.Code != 400 {
		t.Errorf("no name: %d %s", w.Code, w.Body)
	}

	// Everything it depends on changes; the stored sheet and its CSV do not.
	m, _ := e.svc.Q.GetMetalByCode(context.Background(), "18KR")
	if _, err := e.svc.UpdateMetal(context.Background(), m.ID, service.MetalUpdate{SpecificGravity: m.SpecificGravity,
		WastagePct: decimal.NullDecimal{Decimal: mustDec("30"), Valid: true}, Active: true}); err != nil {
		t.Fatal(err)
	}
	rate := decimal.NullDecimal{Decimal: mustDec("1"), Valid: true}
	if err := e.svc.UpdateStoneMatrix(context.Background(), []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070"), RatePerCt: rate}}, ""); err != nil {
		t.Fatal(err)
	}
	w = do(e.r, "GET", fmt.Sprintf("/api/v1/costing-sheets/%d/csv", sheet.ID), e.admin, "", nil)
	if w.Code != 200 || w.Body.String() != wantCSV || !strings.Contains(w.Header().Get("Content-Disposition"), "costing-sheet-") {
		t.Errorf("stored csv differs after master change: %d\n%s\nwant\n%s", w.Code, w.Body, wantCSV)
	}
	w = do(e.r, "GET", fmt.Sprintf("/api/v1/costing-sheets/%d", sheet.ID), e.admin, "", nil)
	var got service.SheetDetail
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck
	if w.Code != 200 || !got.Total.Equal(mustDec("52220")) || len(got.Rows) != 2 || !got.Rates.Metals[0].WastagePct.Equal(mustDec("10")) {
		t.Errorf("detail after master change: %d %s", w.Code, w.Body)
	}

	// List: newest first, paging header.
	w = do(e.r, "POST", "/api/v1/costing-sheets", e.admin, "application/json", e.body(`,"name":"Second"`))
	if w.Code != 201 {
		t.Fatalf("second create: %d %s", w.Code, w.Body)
	}
	w = do(e.r, "GET", "/api/v1/costing-sheets?limit=1", e.designer, "", nil)
	var list []service.SheetSummary
	json.Unmarshal(w.Body.Bytes(), &list) //nolint:errcheck
	if w.Code != 200 || w.Header().Get("X-Total-Count") != "2" || len(list) != 1 || list[0].Name != "Second" {
		t.Errorf("list: %d %s %s", w.Code, w.Header().Get("X-Total-Count"), w.Body)
	}

	// Re-cost with a new gold rate: a NEW sheet, a comparison, the old sheet untouched.
	w = do(e.r, "POST", fmt.Sprintf("/api/v1/costing-sheets/%d/recost", sheet.ID), e.designer, "application/json", []byte(`{"gold_rate_per_g":"7000"}`))
	var rc struct {
		Sheet      service.SheetDetail     `json:"sheet"`
		Comparison service.SheetComparison `json:"comparison"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rc); err != nil || w.Code != 201 {
		t.Fatalf("recost: %d %s", w.Code, w.Body)
	}
	if rc.Sheet.ID == sheet.ID || rc.Sheet.RecostOf == nil || *rc.Sheet.RecostOf != sheet.ID || rc.Sheet.Name != "Autumn (re-cost)" ||
		!rc.Sheet.GoldRatePerG.Equal(mustDec("7000")) || rc.Comparison.PreviousID != sheet.ID || rc.Comparison.Difference.IsZero() || len(rc.Comparison.Rows) != 2 {
		t.Errorf("recost = %+v / %+v", rc.Sheet.SheetSummary, rc.Comparison)
	}
	// The new sheet used today's wastage (30 %) and prices, not the old snapshot.
	if !rc.Sheet.Rates.Metals[0].WastagePct.Equal(mustDec("30")) {
		t.Errorf("recost wastage = %v", rc.Sheet.Rates.Metals)
	}
	if w := do(e.r, "POST", "/api/v1/costing-sheets/99999/recost", e.designer, "application/json", []byte(`{}`)); w.Code != 404 {
		t.Errorf("recost unknown: %d", w.Code)
	}

	// Delete: a designer may not delete someone else's sheet; the creator and an admin may.
	path := fmt.Sprintf("/api/v1/costing-sheets/%d", sheet.ID)
	if w := do(e.r, "DELETE", path, e.other, "", nil); w.Code != 403 {
		t.Errorf("other designer delete: %d", w.Code)
	}
	if w := do(e.r, "DELETE", path, e.designer, "", nil); w.Code != 204 {
		t.Errorf("creator delete: %d", w.Code)
	}
	if w := do(e.r, "GET", path, e.designer, "", nil); w.Code != 404 {
		t.Errorf("after delete: %d", w.Code)
	}
	if w := do(e.r, "DELETE", fmt.Sprintf("/api/v1/costing-sheets/%d", rc.Sheet.ID), e.admin, "", nil); w.Code != 204 {
		t.Errorf("admin delete: %d", w.Code)
	}
	if w := do(e.r, "GET", "/api/v1/costing-sheets/abc", e.admin, "", nil); w.Code != 400 {
		t.Errorf("bad id: %d", w.Code)
	}

	// Audit: create, download, recost and delete are recorded, with no request body.
	rows, _, err := e.svc.ListAudit(context.Background(), service.AuditFilter{ActionPrefix: "costing_sheet."})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for _, r := range rows {
		seen[r.Action]++
	}
	for _, a := range []string{"costing_sheet.create", "costing_sheet.download", "costing_sheet.recost", "costing_sheet.delete"} {
		if seen[a] == 0 {
			t.Errorf("audit missing %s: %v", a, seen)
		}
	}
	exp, _, _ := e.svc.ListAudit(context.Background(), service.AuditFilter{ActionPrefix: "export.costing"})
	found := false
	for _, x := range exp {
		found = found || (x.Status == 200 && strings.Contains(x.Detail, "2 variations in 2 designs"))
	}
	if !found {
		t.Errorf("export audit = %+v", exp)
	}
}

func TestCostingIncompleteOverHTTP(t *testing.T) {
	e := newCostEnv(t)
	// Remove the price of (1.20, 2HD): LR080 becomes incomplete, LR081 stays complete.
	if err := e.svc.UpdateStoneMatrix(context.Background(), []service.MatrixRowInput{
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095")}}, ""); err != nil {
		t.Fatal(err)
	}

	// All-or-nothing: 422 naming the design, variation and pair, nothing saved.
	w := do(e.r, "POST", "/api/v1/exports/costing-csv", e.admin, "application/json", e.body(""))
	var errBody struct {
		Error    string `json:"error"`
		Problems []struct {
			DesignCode string `json:"design_code"`
			Variations []struct {
				VariationCode string `json:"variation_code"`
				StoneRates    []struct {
					SizeMM string `json:"SizeMM"`
					Code   string `json:"Code"`
				} `json:"stone_rates_missing"`
			} `json:"variations"`
		} `json:"problems"`
		Rates []struct{ SizeMM, Code string } `json:"stone_rates_missing"`
	}
	json.Unmarshal(w.Body.Bytes(), &errBody) //nolint:errcheck
	if w.Code != 422 || len(errBody.Problems) != 1 || errBody.Problems[0].DesignCode != "LR080" ||
		errBody.Problems[0].Variations[0].VariationCode != "LR080-V1" || errBody.Problems[0].Variations[0].StoneRates[0].SizeMM != "1.20" ||
		len(errBody.Rates) != 1 || !strings.Contains(errBody.Error, "LR080") {
		t.Fatalf("all-or-nothing: %d %s", w.Code, w.Body)
	}
	if w := do(e.r, "POST", "/api/v1/costing-sheets", e.admin, "application/json", e.body(`,"name":"x"`)); w.Code != 422 {
		t.Errorf("sheet of incomplete data: %d", w.Code)
	}
	if rows, _, _ := e.svc.ListCostingSheets(context.Background(), 10, 0); len(rows) != 0 {
		t.Errorf("a sheet was saved: %+v", rows)
	}

	// The preview still answers, listing the problem and the complete row.
	w = do(e.r, "POST", "/api/v1/exports/costing-preview", e.admin, "application/json", e.body(""))
	var pv struct {
		Rows     []any `json:"rows"`
		Problems []any `json:"problems"`
		Complete bool  `json:"complete"`
	}
	json.Unmarshal(w.Body.Bytes(), &pv) //nolint:errcheck
	if w.Code != 200 || len(pv.Rows) != 1 || len(pv.Problems) != 1 || pv.Complete {
		t.Errorf("preview: %d %s", w.Code, w.Body)
	}

	// skip_incomplete: the CSV costs the complete row and lists the other in a separate section.
	w = do(e.r, "POST", "/api/v1/exports/costing-csv", e.admin, "application/json", e.body(`,"skip_incomplete":true`))
	out := w.Body.String()
	if w.Code != 200 || w.Header().Get("X-Costing-Incomplete") != "1" || !strings.Contains(out, "\nNOT COSTED") ||
		!strings.Contains(out, "LR080,LR080-V1,") || strings.Contains(strings.Split(out, "NOT COSTED")[0], "LR080-V1") {
		t.Fatalf("skip csv: %d %s", w.Code, out)
	}
	w = do(e.r, "POST", "/api/v1/costing-sheets", e.admin, "application/json", e.body(`,"name":"Partial","skip_incomplete":true`))
	var s service.SheetDetail
	json.Unmarshal(w.Body.Bytes(), &s) //nolint:errcheck
	if w.Code != 201 || s.IncompleteCount != 1 || s.VariationCount != 1 || len(s.Problems) != 1 {
		t.Fatalf("partial sheet: %d %s", w.Code, w.Body)
	}
	w = do(e.r, "GET", fmt.Sprintf("/api/v1/costing-sheets/%d/csv", s.ID), e.admin, "", nil)
	if w.Code != 200 || w.Header().Get("X-Costing-Incomplete") != "1" || !strings.Contains(w.Body.String(), "NOT COSTED") {
		t.Errorf("partial sheet csv: %d %s", w.Code, w.Body)
	}

	// Size cap: a clear 400 beyond 500 variations.
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprint(i + 1)
	}
	big := []byte(`{"variation_ids":[` + strings.Join(ids, ",") + `],"metal_code":"18KR","stone_code":"2HD","gold_rate_per_g":"1"}`)
	if w := do(e.r, "POST", "/api/v1/exports/costing-csv", e.admin, "application/json", big); w.Code != 400 || !strings.Contains(w.Body.String(), "500") {
		t.Errorf("over the cap: %d %s", w.Code, w.Body)
	}
	// The older single-design request shape still works.
	legacy := []byte(fmt.Sprintf(`{"items":[{"variation_id":%d,"metal_code":"18KR","stone_code":"2HD"}],"gold_rate_per_g":"6000","setting_rate_per_pc":"25"}`, e.v2))
	if w := do(e.r, "POST", "/api/v1/exports/costing-csv", e.admin, "application/json", legacy); w.Code != 200 || strings.Count(strings.TrimSpace(w.Body.String()), "\n") != 1 {
		t.Errorf("legacy shape: %d %s", w.Code, w.Body)
	}
}
