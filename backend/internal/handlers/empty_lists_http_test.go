package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// A fresh install has no tags, matrix rows, designs or seats. Go marshals a nil slice as
// JSON null, which crashed the Angular app (null.filter / null.length), so every list
// endpoint must answer [] instead.
func TestEmptyCollectionsAreArraysNotNull(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "admin@example.com", "admin")
	adm := bearer(t, r, "admin@example.com")
	ctx := context.Background()

	for _, path := range []string{
		"/api/v1/masters/tags",
		"/api/v1/masters/tags?include_inactive=true",
		"/api/v1/masters/stone-matrix",
		"/api/v1/masters/matrix-gaps",
		"/api/v1/designs",
	} {
		w := do(r, "GET", path, adm, "", nil)
		if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
			t.Errorf("GET %s = %d %q, want 200 []", path, w.Code, w.Body)
		}
	}

	// A design without tags, and a variation with no seats, must also serialise arrays.
	cats, _ := svc.ListCategories(ctx)
	des, err := svc.CreateDesign(ctx, service.DesignInput{Code: "LR900", CategoryID: cats[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	vol := mustDec("200")
	v, err := svc.CreateVariation(ctx, des.ID, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}

	w := do(r, "GET", "/api/v1/designs/LR900", adm, "", nil)
	var d map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &d); err != nil || w.Code != 200 {
		t.Fatalf("design: %d %v", w.Code, err)
	}
	if string(d["tags"]) != "[]" {
		t.Errorf("design tags = %s, want []", d["tags"])
	}
	var variations []map[string]json.RawMessage
	if err := json.Unmarshal(d["variations"], &variations); err != nil || len(variations) != 1 {
		t.Fatalf("variations = %s (%v)", d["variations"], err)
	}
	if string(variations[0]["weights"]) == "null" {
		t.Error("variation weights must be an array")
	}

	seats := do(r, "GET", "/api/v1/variations/"+itoa(v.ID)+"/seats", adm, "", nil)
	if seats.Code != 200 || strings.TrimSpace(seats.Body.String()) != "[]" {
		t.Errorf("seats = %d %q", seats.Code, seats.Body)
	}
	list := do(r, "GET", "/api/v1/designs", adm, "", nil)
	if !strings.Contains(list.Body.String(), `"tag_names":[]`) {
		t.Errorf("card tag_names must be []: %s", list.Body)
	}
}
