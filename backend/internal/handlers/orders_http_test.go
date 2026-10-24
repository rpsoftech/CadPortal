package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// Fixture: the stone matrix, one design with variation 1 (fixture seats, a 3dm file) and variation 2 with a
// seat size the matrix does not know.
func TestOrdersOverHTTP(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	ctx := context.Background()
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070")},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095")},
		{SizeMM: mustDec("2.50"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0650")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	design, code, v1 := mediaDesignOverHTTP(t, svc)
	if _, err := svc.SetSeats(ctx, v1, []service.SeatInput{
		{SizeMM: mustDec("1.10"), StoneCount: 24}, {SizeMM: mustDec("1.20"), StoneCount: 12},
		{SizeMM: mustDec("2.50"), StoneCount: 1, LockedStoneCode: "2HD"},
	}); err != nil {
		t.Fatal(err)
	}
	vol := mustDec("100")
	vb, err := svc.CreateVariation(ctx, design, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSeats(ctx, vb.ID, []service.SeatInput{{SizeMM: mustDec("3.00"), StoneCount: 1}}); err != nil {
		t.Fatal(err)
	}
	body, ct := multipartBody(t, "file", "ring.3dm", make([]byte, 3000))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", v1), token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	admin, designer, prod := token(t, auth.Admin), token(t, auth.Designer), token(t, auth.Production)
	js := func(v any) []byte { b, _ := json.Marshal(v); return b }

	// Roles: nobody anonymous, production reads but never writes.
	create := map[string]any{"name": "Wedding", "metal_code": "18KR", "stone_code": "2HD", "items": []map[string]any{{"variation_id": v1, "quantity": 3}}}
	if w := do(r, "POST", "/api/v1/orders", "", "application/json", js(create)); w.Code != 401 {
		t.Errorf("anonymous create: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/orders", prod, "application/json", js(create)); w.Code != 403 {
		t.Errorf("production create: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/orders", "", "", nil); w.Code != 401 {
		t.Errorf("anonymous list: %d", w.Code)
	}

	// A selection with one variation lacking stone data: 422 with the listing, nothing created.
	bad := map[string]any{"metal_code": "18KR", "stone_code": "2HD", "items": []map[string]any{{"variation_id": v1, "quantity": 1}, {"variation_id": vb.ID, "quantity": 1}}}
	w := do(r, "POST", "/api/v1/orders", designer, "application/json", js(bad))
	var errBody struct {
		Error    string `json:"error"`
		Problems []struct {
			VariationID   int64  `json:"variation_id"`
			VariationCode string `json:"variation_code"`
		} `json:"problems"`
	}
	if w.Code != 422 || json.Unmarshal(w.Body.Bytes(), &errBody) != nil || len(errBody.Problems) != 1 || errBody.Problems[0].VariationID != vb.ID ||
		!strings.Contains(errBody.Error, code+"-V2") {
		t.Fatalf("incomplete: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", "/api/v1/orders", designer, "application/json", []byte("{")); w.Code != 400 {
		t.Errorf("bad json: %d", w.Code)
	}

	// Create as designer.
	w = do(r, "POST", "/api/v1/orders", designer, "application/json", js(create))
	var o service.OrderDetail
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &o) != nil || o.Number != "ORD-0001" || o.Status != "draft" || len(o.Lines) != 1 {
		t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), "cad/") || strings.Contains(w.Body.String(), "object_key") || strings.Contains(w.Body.String(), "price") {
		t.Errorf("response leaks storage keys or prices: %s", w.Body)
	}
	if f := o.Lines[0].Snapshot.File3dm; f == nil || f.Name != "ring.3dm" || f.SizeBytes != 3000 || len(f.SHA256) != 64 {
		t.Errorf("file snapshot = %+v", f)
	}

	// Production can read the list and the detail.
	path := fmt.Sprintf("/api/v1/orders/%d", o.ID)
	if w := do(r, "GET", path, prod, "", nil); w.Code != 200 {
		t.Errorf("production get: %d", w.Code)
	}
	w = do(r, "GET", "/api/v1/orders?status=draft&q=ord-0001", prod, "", nil)
	var list []service.OrderSummary
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &list) != nil || len(list) != 1 || w.Header().Get("X-Total-Count") != "1" || list[0].TotalQuantity != 3 {
		t.Errorf("list: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/orders?status=bogus", prod, "", nil); w.Code != 400 {
		t.Errorf("bad status filter: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/orders?status=done", prod, "", nil); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("empty list must be []: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/orders/999", prod, "", nil); w.Code != 404 {
		t.Errorf("missing order: %d", w.Code)
	}
	// The live check is readable by production and says "unchanged" for a fresh order.
	if w := do(r, "GET", path+"/live", prod, "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"unchanged"`) {
		t.Errorf("live: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", "/api/v1/orders/999/live", prod, "", nil); w.Code != 404 {
		t.Errorf("live of a missing order: %d", w.Code)
	}

	// Production cannot write anything else either.
	for _, c := range []struct{ method, path string }{
		{"PUT", path}, {"PUT", path + "/lines"}, {"POST", path + "/status"}, {"POST", path + "/clone"},
	} {
		if w := do(r, c.method, c.path, prod, "application/json", []byte("{}")); w.Code != 403 {
			t.Errorf("production %s %s: %d", c.method, c.path, w.Code)
		}
	}

	// Edit, lines while draft, status flow, invalid transition.
	if w := do(r, "PUT", path, designer, "application/json", js(map[string]any{"name": "Mehta", "notes": "rush", "due_date": "2026-12-31"})); w.Code != 200 || !strings.Contains(w.Body.String(), `"2026-12-31"`) {
		t.Errorf("update: %d %s", w.Code, w.Body)
	}
	if w := do(r, "PUT", path+"/lines", designer, "application/json", js(map[string]any{"items": []map[string]any{{"variation_id": v1, "quantity": 9}}})); w.Code != 200 || !strings.Contains(w.Body.String(), `"quantity":9`) {
		t.Errorf("lines: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", path+"/status", designer, "application/json", js(map[string]any{"status": "done"})); w.Code != 409 {
		t.Errorf("draft -> done: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", path+"/status", designer, "application/json", js(map[string]any{"status": "placed", "note": "ok"})); w.Code != 200 {
		t.Errorf("place: %d %s", w.Code, w.Body)
	}
	if w := do(r, "PUT", path+"/lines", designer, "application/json", js(map[string]any{"items": []map[string]any{{"variation_id": v1, "quantity": 1}}})); w.Code != 409 {
		t.Errorf("lines of a placed order: %d", w.Code)
	}

	// Clone: a new draft with a diff.
	w = do(r, "POST", path+"/clone", designer, "application/json", nil)
	var cl struct {
		Order service.OrderDetail `json:"order"`
		Diff  service.OrderDiff   `json:"diff"`
	}
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &cl) != nil || cl.Order.Number != "ORD-0002" || cl.Order.Status != "draft" ||
		cl.Diff.Unchanged != 1 || cl.Diff.SourceID != o.ID || cl.Order.Lines[0].Quantity != 9 {
		t.Fatalf("clone: %d %s", w.Code, w.Body)
	}

	// Cancel, never delete.
	if w := do(r, "POST", path+"/status", admin, "application/json", js(map[string]any{"status": "cancelled"})); w.Code != 200 {
		t.Errorf("cancel: %d", w.Code)
	}
	if w := do(r, "DELETE", path, admin, "", nil); w.Code != 404 && w.Code != 405 {
		t.Errorf("orders must have no delete route: %d", w.Code)
	}

	// Audit trail names every change, with the order number as detail and no request body.
	rows, _ := fetchAudit(t, r, admin, "?action=order.")
	have := map[string]bool{}
	for _, a := range rows {
		have[a.Action] = true
		if a.EntityType != "order" || strings.Contains(a.Detail, "rush") {
			t.Errorf("audit row = %+v", a)
		}
	}
	for _, a := range []string{"order.create", "order.update", "order.set_lines", "order.status", "order.clone"} {
		if !have[a] {
			t.Errorf("no audit entry %s (have %v)", a, have)
		}
	}
}
