package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// pdfPages counts page objects: "/Type /Page" but not the page tree "/Type /Pages".
func pdfPages(b []byte) int {
	return bytes.Count(b, []byte("/Type /Page\n")) + bytes.Count(b, []byte("/Type /Page>>"))
}

type bagFixture struct {
	designA, designB int64
	a1, a2, b1, b2   int64 // b1 and b2 use a seat size with no 2HD row
}

func makeBagFixture(t *testing.T, svc *service.Service) bagFixture {
	t.Helper()
	ctx := context.Background()
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070")},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	cats, _ := svc.ListCategories(ctx)
	mk := func(code string) int64 {
		d, err := svc.CreateDesign(ctx, service.DesignInput{Code: code, CategoryID: cats[0].ID})
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	variation := func(design int64, size string) int64 {
		vol := mustDec("200")
		v, err := svc.CreateVariation(ctx, design, service.BaseInput{VolumeMM3: &vol})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.SetSeats(ctx, v.ID, []service.SeatInput{{SizeMM: mustDec(size), StoneCount: 12}}); err != nil {
			t.Fatal(err)
		}
		return v.ID
	}
	var f bagFixture
	f.designA, f.designB = mk("LR070"), mk("LR071")
	f.a1, f.a2 = variation(f.designA, "1.10"), variation(f.designA, "1.20")
	f.b1, f.b2 = variation(f.designB, "3.00"), variation(f.designB, "3.10")
	return f
}

func TestJobBagsPDFOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	mkUser(t, svc, "prod@example.com", "production")
	adm, prod := bearer(t, r, "admin@example.com"), bearer(t, r, "prod@example.com")
	f := makeBagFixture(t, svc)
	post := func(tok string, body any) (int, []byte, map[string][]string) {
		w := do(r, "POST", "/api/v1/exports/job-bags-pdf", tok, "application/json", jsonBody(body))
		return w.Code, w.Body.Bytes(), w.Header()
	}
	item := func(id int64) map[string]any { return map[string]any{"variation_id": id} }

	if code, _, _ := post("", map[string]any{}); code != 401 {
		t.Errorf("no token: %d", code)
	}

	// All variations of a design, with a shared metal and stone code; production may print them.
	code, pdf, h := post(prod, map[string]any{"design_ids": []int64{f.designA}, "metal_code": "18KR", "stone_code": "2HD"})
	if code != 200 || !bytes.HasPrefix(pdf, []byte("%PDF-")) || pdfPages(pdf) != 2 {
		t.Fatalf("design bags: %d pages=%d", code, pdfPages(pdf))
	}
	if h["Content-Type"][0] != "application/pdf" || len(h["X-Skipped"]) != 0 {
		t.Errorf("headers: %v", h)
	}

	// Items in the given order, with a per-item metal overriding the shared one; one item twice is two bags.
	code, pdf, _ = post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD", "items": []map[string]any{
		item(f.a2), {"variation_id": f.a1, "metal_code": "22KY"}, item(f.a2),
	}})
	if code != 200 || pdfPages(pdf) != 3 {
		t.Fatalf("items: %d pages=%d", code, pdfPages(pdf))
	}

	// One variation with missing rows fails everything and names every bad variation.
	code, body, _ := post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD",
		"items": []map[string]any{item(f.a1), item(f.b1), item(f.b2)}})
	var e struct {
		Error    string `json:"error"`
		Failures []struct {
			DesignCode    string `json:"design_code"`
			VariationCode string `json:"variation_code"`
			Missing       []struct{ SizeMM, Code string }
		}
	}
	json.Unmarshal(body, &e) //nolint:errcheck
	if code != 422 || len(e.Failures) != 2 || e.Failures[0].VariationCode != "LR071-V1" || e.Failures[0].DesignCode != "LR071" ||
		e.Failures[1].VariationCode != "LR071-V2" || len(e.Failures[1].Missing) != 1 || e.Failures[1].Missing[0].SizeMM != "3.10" ||
		!strings.Contains(e.Error, "LR071-V1") || !strings.Contains(e.Error, "LR071-V2") || bytes.HasPrefix(body, []byte("%PDF")) {
		t.Fatalf("incomplete: %d %s", code, body)
	}

	// skip_incomplete prints the complete bags, names the skipped ones, and adds a final page.
	code, pdf, h = post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD", "skip_incomplete": true,
		"items": []map[string]any{item(f.a1), item(f.b1), item(f.a2), item(f.b2)}})
	if code != 200 || pdfPages(pdf) != 3 || h["X-Skipped"][0] != "LR071-V1,LR071-V2" {
		t.Fatalf("skip: %d pages=%d %v", code, pdfPages(pdf), h["X-Skipped"])
	}
	// Nothing complete to print is still an error.
	if code, _, _ := post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD", "skip_incomplete": true,
		"items": []map[string]any{item(f.b1)}}); code != 422 {
		t.Errorf("all skipped: %d", code)
	}

	// Bad requests.
	var many []map[string]any
	for i := 0; i < service.MaxJobBags+1; i++ {
		many = append(many, item(f.a1))
	}
	for name, body := range map[string]map[string]any{
		"empty":           {},
		"no metal":        {"stone_code": "2HD", "items": []map[string]any{item(f.a1)}},
		"no stone":        {"metal_code": "18KR", "items": []map[string]any{item(f.a1)}},
		"design no metal": {"design_ids": []int64{f.designA}},
		"over the cap":    {"metal_code": "18KR", "stone_code": "2HD", "items": many},
		"unknown metal":   {"metal_code": "99ZZ", "stone_code": "2HD", "items": []map[string]any{item(f.a1)}},
		"unknown stone":   {"metal_code": "18KR", "stone_code": "ZZZ", "items": []map[string]any{item(f.a1)}},
		"cap via designs": {"metal_code": "18KR", "stone_code": "2HD", "items": many[:service.MaxJobBags], "design_ids": []int64{f.designA}},
		"zero variation":  {"metal_code": "18KR", "stone_code": "2HD", "items": []map[string]any{item(0)}},
	} {
		if code, _, _ := post(adm, body); code != 400 {
			t.Errorf("%s: %d, want 400", name, code)
		}
	}
	if code, _, _ := post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD", "items": []map[string]any{item(999999)}}); code != 404 {
		t.Errorf("unknown variation: %d", code)
	}
	if code, _, _ := post(adm, map[string]any{"metal_code": "18KR", "stone_code": "2HD", "design_ids": []int64{999999}}); code != 404 {
		t.Errorf("unknown design: %d", code)
	}

	// The old single endpoint still works.
	w := do(r, "POST", "/api/v1/exports/job-bag-pdf", prod, "application/json",
		jsonBody(map[string]any{"variation_id": f.a1, "metal_code": "18KR", "stone_code": "2HD"}))
	if w.Code != 200 || pdfPages(w.Body.Bytes()) != 1 || !strings.Contains(w.Header().Get("Content-Disposition"), "jobbag-LR070-V1.pdf") {
		t.Errorf("single: %d", w.Code)
	}

	// Audited with the count.
	rows, _ := fetchAudit(t, r, adm, "?action=export.job_bags")
	if findWhere(rows, func(a auditRow) bool { return a.Detail == "3" && a.Status == 200 }) == nil || findWhere(rows, func(a auditRow) bool { return a.Detail == "2" && a.UserEmail == "prod@example.com" }) == nil {
		t.Errorf("audit: the bag count is missing from %d entries", len(rows))
	}
}

// 200 bags of the same variation: the largest allowed request finishes quickly and the file stays modest.
func TestJobBagsPDFAtTheCap(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	adm := bearer(t, r, "admin@example.com")
	f := makeBagFixture(t, svc)
	items := make([]map[string]any, service.MaxJobBags)
	for i := range items {
		items[i] = map[string]any{"variation_id": f.a1}
	}
	w := do(r, "POST", "/api/v1/exports/job-bags-pdf", adm, "application/json",
		jsonBody(map[string]any{"metal_code": "18KR", "stone_code": "2HD", "items": items}))
	if w.Code != 200 || pdfPages(w.Body.Bytes()) != service.MaxJobBags {
		t.Fatalf("cap: %d pages=%d", w.Code, pdfPages(w.Body.Bytes()))
	}
	if kb := w.Body.Len() / 1024; kb > 1500 {
		t.Errorf("200 bags are %d KB", kb)
	}
	fmt.Println("200 job bags:", w.Body.Len()/1024, "KB")
}
