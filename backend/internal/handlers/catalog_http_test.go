package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestCatalogSearchAndPagingOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "designer@example.com", "designer")
	tok := bearer(t, r, "designer@example.com")
	ctx := context.Background()
	cats, _ := svc.ListCategories(ctx)
	for i := 1; i <= 7; i++ {
		name := "Ring"
		if i == 7 {
			name = "Pendant 50% off"
		}
		if _, err := svc.CreateDesign(ctx, service.DesignInput{Code: fmt.Sprintf("LR%03d", i), Name: name, CategoryID: cats[0].ID}); err != nil {
			t.Fatal(err)
		}
	}

	get := func(query string) (codes []string, total string) {
		w := do(r, "GET", "/api/v1/designs"+query, tok, "", nil)
		if w.Code != 200 {
			t.Fatalf("GET %s: %d %s", query, w.Code, w.Body)
		}
		var rows []struct{ Code string }
		if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			codes = append(codes, r.Code)
		}
		return codes, w.Header().Get("X-Total-Count")
	}

	c, total := get("?limit=3")
	if strings.Join(c, ",") != "LR001,LR002,LR003" || total != "7" {
		t.Errorf("page 1: %v total=%s", c, total)
	}
	c, total = get("?limit=3&offset=6")
	if strings.Join(c, ",") != "LR007" || total != "7" {
		t.Errorf("last page: %v total=%s", c, total)
	}
	c, total = get("?limit=3&offset=60")
	if len(c) != 0 || total != "0" {
		t.Errorf("past the end: %v total=%s", c, total)
	}
	if c, total = get("?q=50%25"); strings.Join(c, ",") != "LR007" || total != "1" {
		t.Errorf("literal percent search: %v total=%s", c, total)
	}
	if c, total = get("?q=lr00&limit=2"); len(c) != 2 || total != "7" {
		t.Errorf("search + paging: %v total=%s", c, total)
	}
	for _, bad := range []string{"?limit=abc", "?offset=x"} {
		if w := do(r, "GET", "/api/v1/designs"+bad, tok, "", nil); w.Code != 400 {
			t.Errorf("GET %s = %d, want 400", bad, w.Code)
		}
	}
}

func TestDesignEditDeleteOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "designer@example.com", "designer")
	mkUser(t, svc, "production@example.com", "production")
	des := bearer(t, r, "designer@example.com")
	prod := bearer(t, r, "production@example.com")
	ctx := context.Background()
	cats, _ := svc.ListCategories(ctx)
	tag, _ := svc.CreateTag(ctx, "Floral", "Motif")

	d, err := svc.CreateDesign(ctx, service.DesignInput{Code: "LR100", Name: "Old", CategoryID: cats[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/designs/" + itoa(d.ID)

	// Rename, move category, assign a tag.
	body := jsonBody(map[string]any{"name": "New name", "category_id": cats[1].ID, "tag_ids": []int64{tag.ID}})
	if w := do(r, "PUT", path, des, "application/json", body); w.Code != 200 {
		t.Fatalf("update: %d %s", w.Code, w.Body)
	}
	got := do(r, "GET", "/api/v1/designs/LR100", des, "", nil)
	if !strings.Contains(got.Body.String(), `"name":"New name"`) || !strings.Contains(got.Body.String(), "Floral") {
		t.Errorf("after update: %s", got.Body)
	}
	if w := do(r, "PUT", path, des, "application/json", jsonBody(map[string]any{"name": "x"})); w.Code != 400 {
		t.Errorf("update without a category: %d", w.Code)
	}
	if w := do(r, "PUT", path, prod, "application/json", body); w.Code != 403 {
		t.Errorf("production update: %d", w.Code)
	}
	if w := do(r, "DELETE", path, prod, "", nil); w.Code != 403 {
		t.Errorf("production delete: %d", w.Code)
	}

	// Codes are assigned automatically: a supplied code is refused outright, and a category
	// without a prefix cannot take designs yet.
	manual := jsonBody(map[string]any{"code": "LR101", "category_id": cats[0].ID})
	w := do(r, "POST", "/api/v1/designs", des, "application/json", manual)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "assigned automatically") {
		t.Errorf("manual code: %d %s", w.Code, w.Body)
	}
	noPrefix := jsonBody(map[string]any{"category_id": cats[0].ID})
	w = do(r, "POST", "/api/v1/designs", des, "application/json", noPrefix)
	if w.Code != 400 || !strings.Contains(w.Body.String(), "no code prefix") {
		t.Errorf("category without a prefix: %d %s", w.Code, w.Body)
	}
	if _, err := svc.UpdateCategory(ctx, cats[0].ID, cats[0].Name, "zz"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ZZ0001", "ZZ0002"} {
		w = do(r, "POST", "/api/v1/designs", des, "application/json", noPrefix)
		if w.Code != 201 || !strings.Contains(w.Body.String(), `"code":"`+want+`"`) {
			t.Errorf("auto code %s: %d %s", want, w.Code, w.Body)
		}
	}

	// Variation delete, then design delete.
	vol := mustDec("200")
	v, _ := svc.CreateVariation(ctx, d.ID, service.BaseInput{VolumeMM3: &vol})
	vpath := "/api/v1/variations/" + itoa(v.ID)
	if w := do(r, "DELETE", vpath, prod, "", nil); w.Code != 403 {
		t.Errorf("production variation delete: %d", w.Code)
	}
	if w := do(r, "DELETE", vpath, des, "", nil); w.Code != 204 {
		t.Errorf("variation delete: %d %s", w.Code, w.Body)
	}
	if w := do(r, "DELETE", vpath, des, "", nil); w.Code != 404 {
		t.Errorf("variation delete again: %d", w.Code)
	}
	if w := do(r, "DELETE", path, des, "", nil); w.Code != 204 {
		t.Errorf("design delete: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/designs/LR100", des, "", nil); w.Code != 404 {
		t.Errorf("deleted design still readable: %d", w.Code)
	}
}
