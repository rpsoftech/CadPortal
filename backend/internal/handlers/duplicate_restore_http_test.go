package handlers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestDuplicateDesignOverHTTP(t *testing.T) {
	r, svc, mem := newHTTPEnv(t)
	design, code, variation := mediaDesignOverHTTP(t, svc)
	body, ct := multipartBody(t, "file", "ring.3dm", make([]byte, 3000))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", variation), token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	objects := len(mem.Keys())
	url := fmt.Sprintf("/api/v1/designs/%d/duplicate", design)
	empty := jsonBody(map[string]any{})

	if w := do(r, "POST", url, "", "application/json", empty); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Production), "application/json", empty); w.Code != 403 {
		t.Errorf("production: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/designs/999999/duplicate", token(t, auth.Designer), "application/json", empty); w.Code != 404 {
		t.Errorf("unknown design: %d", w.Code)
	}
	// A variation that is not the source's: refused, and nothing is created.
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", jsonBody(map[string]any{"variation_ids": []int64{424242}})); w.Code != 400 {
		t.Errorf("bad variation: %d %s", w.Code, w.Body)
	}

	w := do(r, "POST", url, token(t, auth.Designer), "application/json", jsonBody(map[string]any{"name": "Twin"}))
	var got service.DesignDetail
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &got) != nil {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}
	if got.Code != "LR0002" || got.Name != "Twin" || len(got.Variations) != 1 || got.Variations[0].Code != "LR0002-V1" ||
		len(got.Variations[0].Weights) == 0 || got.Variations[0].File3dmKey.Valid || len(got.Media) != 0 {
		t.Fatalf("copy = %+v", got)
	}
	if len(mem.Keys()) != objects {
		t.Errorf("a file was copied: %v", mem.Keys())
	}
	// The next one takes the next number and the default name.
	w = do(r, "POST", url, token(t, auth.Admin), "application/json", nil)
	if w.Code != 201 || json.Unmarshal(w.Body.Bytes(), &got) != nil || got.Code != "LR0003" {
		t.Fatalf("second copy: %d %s", w.Code, w.Body)
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=design.duplicate&limit=50")
	if a := findWhere(rows, func(a auditRow) bool { return a.Status == 201 && a.Detail == "from "+code }); a == nil {
		t.Errorf("duplicate not audited with its source: %+v", rows)
	}

	// A deleted source is gone.
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/designs/%d", design), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", empty); w.Code != 404 {
		t.Errorf("deleted source: %d", w.Code)
	}
}

func TestDeletedVariationCanBeRestoredOverHTTP(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	svc.Retention = 24 * time.Hour
	design, code, variation := mediaDesignOverHTTP(t, svc)
	body, ct := multipartBody(t, "file", "ring.3dm", make([]byte, 3000))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", variation), token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/variations/%d", variation), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("delete: %d", w.Code)
	}

	g := do(r, "GET", "/api/v1/designs/"+code, token(t, auth.Production), "", nil)
	var detail service.DesignDetail
	if g.Code != 200 || json.Unmarshal(g.Body.Bytes(), &detail) != nil || len(detail.Variations) != 0 {
		t.Fatalf("design after delete: %d %s", g.Code, g.Body)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", variation), token(t, auth.Production), "", nil); w.Code != 404 {
		t.Errorf("download of a deleted variation: %d", w.Code)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/simulate-bom?code=2HD", variation), token(t, auth.Production), "", nil); w.Code != 404 {
		t.Errorf("simulate-bom of a deleted variation: %d", w.Code)
	}

	var tr service.Trash
	w := do(r, "GET", "/api/v1/trash", token(t, auth.Designer), "", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tr) != nil || len(tr.Variations) != 1 || len(tr.Files) != 0 ||
		tr.Variations[0].Code != code+"-V1" || tr.Variations[0].DesignCode != code || tr.Variations[0].PurgeAfter == nil {
		t.Fatalf("trash: %d %s", w.Code, w.Body)
	}

	restore := fmt.Sprintf("/api/v1/variations/%d/restore", variation)
	if w := do(r, "POST", restore, "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := do(r, "POST", restore, token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production: %d", w.Code)
	}
	// Under a deleted design it must wait for the design.
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/designs/%d", design), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("delete design: %d", w.Code)
	}
	if w := do(r, "POST", restore, token(t, auth.Designer), "", nil); w.Code != 400 {
		t.Errorf("deleted design: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/restore", design), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("restore design: %d", w.Code)
	}
	if w := do(r, "POST", restore, token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("restore: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", restore, token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("restore twice: %d", w.Code)
	}

	g = do(r, "GET", "/api/v1/designs/"+code, token(t, auth.Production), "", nil)
	if g.Code != 200 || json.Unmarshal(g.Body.Bytes(), &detail) != nil || len(detail.Variations) != 1 ||
		len(detail.Variations[0].Weights) == 0 || detail.Variations[0].File3dmName.String != "ring.3dm" {
		t.Fatalf("restored: %d %s", g.Code, g.Body)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", variation), token(t, auth.Production), "", nil); w.Code != 200 {
		t.Errorf("download after restore: %d", w.Code)
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=variation.&limit=50")
	if findWhere(rows, func(a auditRow) bool { return a.Action == "variation.restore" && a.Status == 204 }) == nil ||
		findAction(rows, "variation.delete") == nil {
		t.Errorf("delete and restore should both be audited: %+v", rows)
	}
}
