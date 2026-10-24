package handlers

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

var pdfTj = regexp.MustCompile(`\((.*)\)\s?Tj`)

// pdfPageTexts returns the text shown on each page of a PDF made by fpdf (test only): every content
// stream is inflated and the strings of its Tj operators are joined by newlines.
func pdfPageTexts(t *testing.T, pdf []byte) []string {
	t.Helper()
	var pages []string
	rest := pdf
	for {
		i := bytes.Index(rest, []byte("\nstream\n"))
		if i < 0 {
			return pages
		}
		rest = rest[i+len("\nstream\n"):]
		j := bytes.Index(rest, []byte("endstream"))
		if j < 0 {
			t.Fatal("unterminated stream")
		}
		raw := rest[:j]
		rest = rest[j:]
		zr, err := zlib.NewReader(bytes.NewReader(raw))
		if err != nil {
			continue // not a deflate stream (an image, say)
		}
		b, err := io.ReadAll(zr)
		if err != nil || !bytes.HasPrefix(b, []byte("0 J")) {
			continue
		}
		var lines []string
		for _, m := range pdfTj.FindAllStringSubmatch(string(b), -1) {
			lines = append(lines, strings.NewReplacer(`\(`, "(", `\)`, ")", `\\`, `\`).Replace(m[1]))
		}
		pages = append(pages, strings.Join(lines, "\n"))
	}
}

func pdfText(t *testing.T, pdf []byte) string { return strings.Join(pdfPageTexts(t, pdf), "\n") }

func (e *costEnv) newOrder(t *testing.T, qty1, qty2 int) service.OrderDetail {
	t.Helper()
	js, _ := json.Marshal(map[string]any{"name": "Wedding", "metal_code": "18KR", "stone_code": "2HD",
		"items": []map[string]any{{"variation_id": e.v1, "quantity": qty1}, {"variation_id": e.v2, "quantity": qty2}}})
	w := do(e.r, "POST", "/api/v1/orders", e.designer, "application/json", js)
	var o service.OrderDetail
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("create order: %d %s", w.Code, w.Body)
	}
	return o
}

func (e *costEnv) changeVolume(t *testing.T, variationID int64, vol string) {
	t.Helper()
	v := mustDec(vol)
	if _, err := e.svc.UpdateVariationBase(context.Background(), variationID, service.BaseInput{VolumeMM3: &v}); err != nil {
		t.Fatal(err)
	}
}

func TestOrderSummaryPDFUsesTheSnapshotAndMultipliesByQuantity(t *testing.T) {
	e := newCostEnv(t)
	o := e.newOrder(t, 3, 2)
	path := fmt.Sprintf("/api/v1/orders/%d/summary.pdf", o.ID)

	if w := do(e.r, "GET", path, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	w := do(e.r, "GET", path, e.prod, "", nil) // production reads it
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("summary: %d %s", w.Code, w.Header())
	}
	text := pdfText(t, w.Body.Bytes())
	// LR080-V1 (vol 200, 18KR): net 3.050, gross 3.119. LR081-V1 (vol 100): net 1.525.
	// 3 pieces of the first and 2 of the second: net = 3 x 3.050 + 2 x 1.525 = 12.200.
	for _, want := range []string{"ORDER SUMMARY", o.Number, "LR080-V1", "LR081-V1", "Pieces", "12.200 g", "Total gross weight", "18KR", "2HD"} {
		if !strings.Contains(text, want) {
			t.Errorf("summary text lacks %q:\n%s", want, text)
		}
	}
	for i, l := range o.Lines {
		if g := l.Snapshot.GrossG.StringFixed(3); !strings.Contains(text, g) {
			t.Errorf("line %d gross %s missing", i, g)
		}
	}
	totalGross := o.Lines[0].Snapshot.GrossG.Mul(mustDec("3")).Add(o.Lines[1].Snapshot.GrossG.Mul(mustDec("2"))).StringFixed(3)
	if !strings.Contains(text, totalGross+" g") {
		t.Errorf("total gross %s missing", totalGross)
	}
	if low := strings.ToLower(text); strings.Contains(low, "wastage") || strings.Contains(low, "price") {
		t.Errorf("the summary must show no wastage or price:\n%s", text)
	}

	// The design changes after the order: the summary still shows the snapshot.
	e.changeVolume(t, e.v1, "999")
	w = do(e.r, "GET", path, e.prod, "", nil)
	again := pdfText(t, w.Body.Bytes())
	if !strings.Contains(again, "12.200 g") || !strings.Contains(again, totalGross+" g") {
		t.Errorf("summary changed after the design changed:\n%s", again)
	}
	if w := do(e.r, "GET", "/api/v1/orders/9999/summary.pdf", e.prod, "", nil); w.Code != 404 {
		t.Errorf("missing order: %d", w.Code)
	}
}

func TestOrderJobBagsFromTheSnapshotOnePerLineOrPerPiece(t *testing.T) {
	e := newCostEnv(t)
	o := e.newOrder(t, 3, 2)
	base := fmt.Sprintf("/api/v1/orders/%d/job-bags.pdf", o.ID)

	if w := do(e.r, "GET", base, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	w := do(e.r, "GET", base, e.prod, "", nil)
	if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
		t.Fatalf("job bags: %d %s", w.Code, w.Body)
	}
	text := pdfText(t, w.Body.Bytes())
	if n := strings.Count(text, "JOB BAG"); n != 2 {
		t.Errorf("want one bag per line, got %d", n)
	}
	for _, want := range []string{o.Number + ", quantity 3", o.Number + ", quantity 2", "LR080-V1", "3.119 g"} {
		if !strings.Contains(text, want) {
			t.Errorf("bags lack %q", want)
		}
	}

	w = do(e.r, "GET", base+"?per_piece=true", e.prod, "", nil)
	text = pdfText(t, w.Body.Bytes())
	if w.Code != 200 || strings.Count(text, "JOB BAG") != 5 || !strings.Contains(text, o.Number+", piece 3 of 3") {
		t.Errorf("per piece: %d, %d bags", w.Code, strings.Count(text, "JOB BAG"))
	}
	if w := do(e.r, "GET", base+"?per_piece=maybe", e.prod, "", nil); w.Code != 400 {
		t.Errorf("bad flag: %d", w.Code)
	}

	// Bags still print from the snapshot after the design changed.
	e.changeVolume(t, e.v1, "999")
	w = do(e.r, "GET", base, e.prod, "", nil)
	if text := pdfText(t, w.Body.Bytes()); w.Code != 200 || !strings.Contains(text, "3.119 g") || !strings.Contains(text, "Volume 200.000 mm3") {
		t.Errorf("bag changed with the design:\n%s", text)
	}
}

func TestOrderPerPieceBagsAreCapped(t *testing.T) {
	e := newCostEnv(t)
	js, _ := json.Marshal(map[string]any{"metal_code": "18KR", "stone_code": "2HD",
		"items": []map[string]any{{"variation_id": e.v1, "quantity": service.MaxOrderJobBags + 1}}})
	w := do(e.r, "POST", "/api/v1/orders", e.designer, "application/json", js)
	var o service.OrderDetail
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	base := fmt.Sprintf("/api/v1/orders/%d/job-bags.pdf", o.ID)
	if w := do(e.r, "GET", base+"?per_piece=true", e.prod, "", nil); w.Code != 400 || !strings.Contains(w.Body.String(), "per piece") {
		t.Errorf("per piece over the cap: %d %s", w.Code, w.Body)
	}
	if w := do(e.r, "GET", base, e.prod, "", nil); w.Code != 200 {
		t.Errorf("one per line: %d", w.Code)
	}
}

func TestOrderCSVExportsAreSafeAgainstFormulaInjection(t *testing.T) {
	e := newCostEnv(t)
	cats, _ := e.svc.ListCategories(context.Background())
	if _, err := e.svc.UpdateDesign(context.Background(), e.d1, service.DesignInput{Name: `=HYPERLINK("http://evil")`, CategoryID: cats[0].ID}); err != nil {
		t.Fatal(err)
	}
	js, _ := json.Marshal(map[string]any{"name": "@SUM(1)", "metal_code": "18KR", "stone_code": "2HD",
		"items": []map[string]any{{"variation_id": e.v1, "quantity": 3}, {"variation_id": e.v2, "quantity": 2}}})
	w := do(e.r, "POST", "/api/v1/orders", e.designer, "application/json", js)
	var o service.OrderDetail
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &o) != nil {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}

	w = do(e.r, "GET", fmt.Sprintf("/api/v1/orders/%d/csv", o.ID), e.prod, "", nil)
	if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("csv: %d", w.Code)
	}
	body := w.Body.String()
	lines := strings.Split(strings.TrimSpace(body), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "order,design,") || !strings.Contains(lines[3], ",TOTAL,") {
		t.Fatalf("csv = %q", lines)
	}
	if strings.Contains(body, ",=HYPERLINK") || strings.Contains(body, `"=HYPERLINK`) || !strings.Contains(body, `'=HYPERLINK`) {
		t.Errorf("design name not neutralised:\n%s", body)
	}
	// 3 x 3.050 + 2 x 1.525 net, in the TOTAL row.
	if !strings.Contains(lines[3], ",12.200,") {
		t.Errorf("total row = %s", lines[3])
	}

	w = do(e.r, "GET", "/api/v1/exports/orders-csv", e.prod, "", nil)
	if w.Code != 200 || w.Header().Get("X-Total-Count") != "1" {
		t.Fatalf("list csv: %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), `'@SUM(1)`) || strings.Contains(w.Body.String(), ",@SUM(1)") {
		t.Errorf("order name not neutralised:\n%s", w.Body)
	}
	if w := do(e.r, "GET", "/api/v1/exports/orders-csv?status=done", e.prod, "", nil); w.Code != 200 || len(strings.Split(strings.TrimSpace(w.Body.String()), "\n")) != 1 {
		t.Errorf("filtered list csv: %d %s", w.Code, w.Body)
	}
	if w := do(e.r, "GET", "/api/v1/exports/orders-csv", "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
}

func TestCostThisOrder(t *testing.T) {
	e := newCostEnv(t)
	o := e.newOrder(t, 3, 2)
	path := fmt.Sprintf("/api/v1/orders/%d/costing", o.ID)
	sheets := fmt.Sprintf("/api/v1/orders/%d/costing-sheets", o.ID)
	body := []byte(`{"gold_rate_per_g":"6000","setting_rate_per_pc":"25"}`)

	// Production never costs, nor reads the order's sheets.
	for _, c := range []struct{ method, path string }{{"POST", path}, {"GET", sheets}} {
		if w := do(e.r, c.method, c.path, e.prod, "application/json", body); w.Code != 403 {
			t.Errorf("production %s %s: %d", c.method, c.path, w.Code)
		}
		if w := do(e.r, c.method, c.path, "", "application/json", body); w.Code != 401 {
			t.Errorf("anonymous %s %s: %d", c.method, c.path, w.Code)
		}
	}
	if w := do(e.r, "POST", path, e.designer, "application/json", []byte(`{"gold_rate_per_g":"0"}`)); w.Code != 400 {
		t.Errorf("no gold rate: %d", w.Code)
	}

	w := do(e.r, "POST", path, e.designer, "application/json", body)
	var res struct {
		Sheet           service.SheetDetail `json:"sheet"`
		SnapshotDiffers bool                `json:"snapshot_differs"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &res) != nil {
		t.Fatalf("cost: %d %s", w.Code, w.Body)
	}
	// One piece of the first design costs 38405.00 and of the second 13815.00 (see the costing tests);
	// the quantities multiply them: 3 x 38405 + 2 x 13815 = 142845.00.
	if res.Sheet.OrderID == nil || *res.Sheet.OrderID != o.ID || res.Sheet.Totals.Total.StringFixed(2) != "142845.00" || res.SnapshotDiffers {
		t.Fatalf("sheet = %+v differs=%v", res.Sheet.SheetSummary, res.SnapshotDiffers)
	}
	var first service.SheetRow
	for _, r := range res.Sheet.Rows {
		if r.VariationID == e.v1 {
			first = r
		}
	}
	if first.Quantity != 3 || first.Total.StringFixed(2) != "115215.00" || first.NetG.StringFixed(3) != "9.150" || first.SettingPcs != 111 {
		t.Errorf("first row = %+v", first)
	}

	// The saved CSV carries the quantity column and the amounts of the whole line.
	w = do(e.r, "GET", fmt.Sprintf("/api/v1/costing-sheets/%d/csv", res.Sheet.ID), e.designer, "", nil)
	if !strings.HasPrefix(w.Body.String(), "variation,qty,metal,") || !strings.Contains(w.Body.String(), "LR080-V1,3,18KR,2HD,9.150,") {
		t.Errorf("csv = %s", w.Body)
	}

	// The order lists its sheets.
	w = do(e.r, "GET", sheets, e.designer, "", nil)
	var list []service.SheetSummary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || list[0].ID != res.Sheet.ID {
		t.Errorf("order sheets: %d %s", w.Code, w.Body)
	}
	if w := do(e.r, "GET", "/api/v1/orders/999/costing-sheets", e.designer, "", nil); w.Code != 404 {
		t.Errorf("unknown order: %d", w.Code)
	}

	// A re-cost keeps the quantities.
	w = do(e.r, "POST", fmt.Sprintf("/api/v1/costing-sheets/%d/recost", res.Sheet.ID), e.designer, "application/json", []byte(`{}`))
	if w.Code != 201 || !strings.Contains(w.Body.String(), `"quantity":3`) {
		t.Errorf("recost: %d %s", w.Code, w.Body)
	}

	// The design changes: a new costing uses it and warns that the snapshot differs; the old sheet stays.
	e.changeVolume(t, e.v1, "300")
	w = do(e.r, "POST", path, e.designer, "application/json", body)
	var res2 struct {
		Sheet           service.SheetDetail `json:"sheet"`
		SnapshotDiffers bool                `json:"snapshot_differs"`
		Drift           service.OrderDiff   `json:"drift"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &res2) != nil || !res2.SnapshotDiffers || res2.Drift.Changed != 1 {
		t.Fatalf("second cost: %d %s", w.Code, w.Body)
	}
	if res2.Sheet.Totals.Total.Equal(res.Sheet.Totals.Total) {
		t.Error("a new costing must use the live design")
	}
	old, _, err := e.svc.GetCostingSheet(context.Background(), res.Sheet.ID)
	if err != nil || old.Totals.Total.StringFixed(2) != "142845.00" {
		t.Errorf("the saved sheet changed: %v %v", old.Totals.Total, err)
	}
}

func TestCostThisOrderNeverTreatsMissingDataAsZero(t *testing.T) {
	e := newCostEnv(t)
	o := e.newOrder(t, 3, 2)
	path := fmt.Sprintf("/api/v1/orders/%d/costing", o.ID)
	body := []byte(`{"gold_rate_per_g":"6000","setting_rate_per_pc":"25"}`)

	// Both designs use 1.10 mm stones: remove that price.
	if _, err := e.svc.Pool.Exec(context.Background(), `UPDATE stone_size_matrix SET rate_per_ct = NULL WHERE size_mm = 1.10`); err != nil {
		t.Fatal(err)
	}
	w := do(e.r, "POST", path, e.designer, "application/json", body)
	var errBody struct {
		Error    string                  `json:"error"`
		Problems []service.DesignProblem `json:"problems"`
		Rates    []any                   `json:"stone_rates_missing"`
	}
	if w.Code != 422 || json.Unmarshal(w.Body.Bytes(), &errBody) != nil || len(errBody.Problems) != 2 || len(errBody.Rates) == 0 {
		t.Fatalf("missing price: %d %s", w.Code, w.Body)
	}
	rows, err := e.svc.ListOrderCostingSheets(context.Background(), o.ID)
	if err != nil || len(rows) != 0 {
		t.Errorf("a failed costing saved %d sheets (%v)", len(rows), err)
	}

	// skip_incomplete costs what it can (here nothing) and still refuses an empty result.
	skip := []byte(`{"gold_rate_per_g":"6000","setting_rate_per_pc":"25","skip_incomplete":true}`)
	if w := do(e.r, "POST", path, e.designer, "application/json", skip); w.Code != 422 {
		t.Errorf("nothing costable: %d %s", w.Code, w.Body)
	}
}
