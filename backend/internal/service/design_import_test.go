package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func countDesigns(t *testing.T, s *Service) int {
	t.Helper()
	var n int
	if err := s.Pool.QueryRow(context.Background(), "SELECT count(*) FROM master_designs").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestImportDesignsCreatesDesignsTagsAndWeights(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.CreateTag(ctx, "Floral", "Motif"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, "Bridal", "Audience"); err != nil {
		t.Fatal(err)
	}

	rows := []ImportRow{
		{Code: "LR100", Name: "Lotus ring", Category: "rings", Tags: "floral; Audience:Bridal", VolumeMM3: "200"},
		{Code: "LR100", VolumeMM3: "300"}, // second variation of the same design
		{Code: "LE100", Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Code: "LP100", Name: "No variation yet", Category: "Pendants"},
	}
	res, err := s.ImportDesigns(ctx, rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Designs != 3 || res.Variations != 3 {
		t.Fatalf("result = %+v", res)
	}

	d, err := s.GetDesignByCode(ctx, "LR100")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Lotus ring" || len(d.Tags) != 2 || len(d.Variations) != 2 {
		t.Fatalf("LR100 = %+v", d)
	}
	if d.Variations[0].Code != "LR100-V1" || d.Variations[1].Code != "LR100-V2" {
		t.Errorf("codes = %s, %s", d.Variations[0].Code, d.Variations[1].Code)
	}
	// Volume 200 mm3 is the spec fixture: 22KY 3.550 g, 18KY 3.116 g, 18KR 3.050 g.
	want := map[string]string{"22KY": "3.55", "18KY": "3.116", "18KR": "3.05"}
	got := map[string]string{}
	for _, w := range d.Variations[0].Weights {
		got[w.MetalCode] = w.NetWeightG.String()
	}
	for m, g := range want {
		if got[m] != g {
			t.Errorf("%s net = %s, want %s", m, got[m], g)
		}
	}
	// A reference weight in 18KY scales to 18KR: 3.116 * 15.25 / 15.58 = 3.050.
	e, _ := s.GetDesignByCode(ctx, "LE100")
	for _, w := range e.Variations[0].Weights {
		if w.MetalCode == "18KR" && w.NetWeightG.String() != "3.05" {
			t.Errorf("18KR from reference = %s", w.NetWeightG)
		}
	}
	if p, _ := s.GetDesignByCode(ctx, "LP100"); len(p.Variations) != 0 {
		t.Errorf("design without a base input got variations: %+v", p.Variations)
	}
}

func TestImportDesignsDryRunWritesNothing(t *testing.T) {
	s := newTestService(t)
	res, err := s.ImportDesigns(context.Background(), []ImportRow{
		{Code: "LR101", Category: "Rings", VolumeMM3: "100"},
	}, true)
	if err != nil || len(res.Errors) != 0 || !res.DryRun || res.Designs != 1 || res.Variations != 1 {
		t.Fatalf("dry run = %+v, %v", res, err)
	}
	if n := countDesigns(t, s); n != 0 {
		t.Errorf("dry run created %d designs", n)
	}
}

func TestImportDesignsReportsEveryProblemAndWritesNothing(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	newDesign(t, s, "LR200") // already exists
	del := newDesign(t, s, "LR201")
	if err := s.DeleteDesign(ctx, del.ID); err != nil {
		t.Fatal(err)
	}
	before := countDesigns(t, s)
	if _, err := s.CreateTag(ctx, "Classic", "Sub-Style"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, "Classic", "Motif"); err != nil {
		t.Fatal(err)
	}

	rows := []ImportRow{
		{Line: 2, Code: "OK1", Category: "Rings", VolumeMM3: "100"},
		{Line: 3, Code: "bad code!", Category: "Rings"},
		{Line: 4, Code: "OK2", Category: "Spaceships"},
		{Line: 5, Code: "OK3", Category: "Rings", Tags: "nope"},
		{Line: 6, Code: "OK4", Category: "Rings", Tags: "classic"},
		{Line: 7, Code: "OK5", Category: "Rings", VolumeMM3: "abc"},
		{Line: 8, Code: "OK6", Category: "Rings", VolumeMM3: "1.2345"},
		{Line: 9, Code: "OK7", Category: "Rings", VolumeMM3: "0"},
		{Line: 10, Code: "OK8", Category: "Rings", VolumeMM3: "5", RefWeightG: "1", RefMetal: "18KY"},
		{Line: 11, Code: "OK9", Category: "Rings", RefWeightG: "1"},
		{Line: 12, Code: "OK10", Category: "Rings", RefWeightG: "1", RefMetal: "24KZ"},
		{Line: 13, Code: "LR200", Category: "Rings"},
		{Line: 14, Code: "LR201", Category: "Rings"},
		{Line: 15, Code: "OK1", Name: "x", Category: "Rings"},
	}
	res, err := s.ImportDesigns(ctx, rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.Designs != 0 || res.Variations != 0 {
		t.Errorf("counts must be zero when rejected: %+v", res)
	}
	byLine := map[int]string{}
	for _, e := range res.Errors {
		byLine[e.Line] += e.Message + "|"
	}
	for line, frag := range map[int]string{
		3: "must be 1 to 32", 4: "unknown category", 5: `unknown tag "nope"`, 6: "several groups",
		7: "not a number", 8: "more than 3 decimal", 9: "greater than 0", 10: "not both",
		11: "both required", 12: "unknown ref_metal", 13: "already exists", 14: "already exists",
		15: "differs from line 2",
	} {
		if !strings.Contains(byLine[line], frag) {
			t.Errorf("line %d: want %q, got %q", line, frag, byLine[line])
		}
	}
	if _, bad := byLine[2]; bad {
		t.Errorf("line 2 is valid but was reported: %q", byLine[2])
	}
	if n := countDesigns(t, s); n != before {
		t.Errorf("rejected import changed the database: %d -> %d designs", before, n)
	}
}

func TestImportDesignsIsAtomicOnLateFailure(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	// Passes validation but fails inside the transaction: a 15-digit volume overflows numeric(14,3).
	rows := []ImportRow{
		{Code: "AT1", Category: "Rings", VolumeMM3: "100"},
		{Code: "AT2", Category: "Rings", VolumeMM3: "99999999999999"},
	}
	if _, err := s.ImportDesigns(ctx, rows, false); err == nil {
		t.Fatal("expected the oversized volume to fail")
	}
	if n := countDesigns(t, s); n != 0 {
		t.Errorf("partial import left %d designs behind", n)
	}
}

func TestImportDesignsLimits(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.ImportDesigns(ctx, nil, false); err == nil {
		t.Error("empty import accepted")
	}
	many := make([]ImportRow, MaxImportRows+1)
	for i := range many {
		many[i] = ImportRow{Code: fmt.Sprintf("X%d", i), Category: "Rings"}
	}
	if _, err := s.ImportDesigns(ctx, many, false); err == nil {
		t.Error("oversized import accepted")
	}
}

func TestExportDesignsRoundTripsThroughImport(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.CreateTag(ctx, "Floral", "Motif"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, "Bridal", "Audience"); err != nil {
		t.Fatal(err)
	}
	in := []ImportRow{
		{Code: "RT2", Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Code: "RT1", Name: "Lotus ring", Category: "Rings", Tags: "Floral;Bridal", VolumeMM3: "200"},
		{Code: "RT1", VolumeMM3: "300.5"},
		{Code: "RT3", Name: "Bare", Category: "Pendants"},
		{Code: "RT4", Name: "Gone", Category: "Rings", VolumeMM3: "10"},
	}
	if res, err := s.ImportDesigns(ctx, in, false); err != nil || len(res.Errors) != 0 {
		t.Fatalf("setup: %+v %v", res, err)
	}
	gone, _ := s.GetDesignByCode(ctx, "RT4")
	if err := s.DeleteDesign(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ExportDesigns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ImportRow{
		{Code: "RT1", Name: "Lotus ring", Category: "Rings", Tags: "Audience:Bridal;Motif:Floral", VolumeMM3: "200"},
		{Code: "RT1", VolumeMM3: "300.5"},
		{Code: "RT2", Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Code: "RT3", Name: "Bare", Category: "Pendants"},
	}
	if len(rows) != len(want) {
		t.Fatalf("rows = %+v", rows)
	}
	for i := range want {
		if rows[i] != want[i] {
			t.Errorf("row %d = %+v, want %+v", i, rows[i], want[i])
		}
	}

	// The export imports cleanly into an empty catalog and rebuilds the same weights and tags.
	before, _ := s.GetDesignByCode(ctx, "RT1")
	if _, err := s.Pool.Exec(ctx, "TRUNCATE master_designs CASCADE"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ImportDesigns(ctx, rows, false)
	if err != nil || len(res.Errors) != 0 || res.Designs != 3 || res.Variations != 3 {
		t.Fatalf("re-import: %+v %v", res, err)
	}
	after, _ := s.GetDesignByCode(ctx, "RT1")
	if len(after.Tags) != 2 || len(after.Variations) != 2 {
		t.Fatalf("after = %+v", after)
	}
	for i, v := range after.Variations {
		for j, w := range v.Weights {
			if !w.NetWeightG.Equal(before.Variations[i].Weights[j].NetWeightG) {
				t.Errorf("%s %s net %s != %s", v.Code, w.MetalCode, w.NetWeightG, before.Variations[i].Weights[j].NetWeightG)
			}
		}
	}
}
