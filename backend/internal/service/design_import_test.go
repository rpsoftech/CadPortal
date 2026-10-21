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

// setPrefixes gives the categories the tests use their code prefixes (a fresh database has none).
func setPrefixes(t *testing.T, s *Service) {
	t.Helper()
	ctx := context.Background()
	cats, err := s.ListCategories(ctx)
	if err != nil {
		t.Fatal(err)
	}
	prefix := map[string]string{"Rings": "LR", "Earrings": "LE", "Pendants": "LP"}
	for _, c := range cats {
		if p, ok := prefix[c.Name]; ok {
			if _, err := s.UpdateCategory(ctx, c.ID, c.Name, p); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestImportDesignsCreatesDesignsTagsAndWeights(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	if _, err := s.CreateTag(ctx, "Floral", "Motif"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, "Bridal", "Audience"); err != nil {
		t.Fatal(err)
	}

	rows := []ImportRow{
		{Name: "Lotus ring", Category: "rings", Tags: "floral; Audience:Bridal", VolumeMM3: "200"},
		{VolumeMM3: "300"}, // no code, no category: a second variation of the design above
		{Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Name: "No variation yet", Category: "Pendants"},
	}
	res, err := s.ImportDesigns(ctx, rows, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 || res.Designs != 3 || res.Variations != 3 {
		t.Fatalf("result = %+v", res)
	}
	if got := strings.Join(res.Codes, ","); got != "LR0001,LE0001,LP0001" {
		t.Fatalf("codes = %s", got)
	}

	d, err := s.GetDesignByCode(ctx, "LR0001")
	if err != nil {
		t.Fatal(err)
	}
	if d.Name != "Lotus ring" || len(d.Tags) != 2 || len(d.Variations) != 2 {
		t.Fatalf("LR0001 = %+v", d)
	}
	if d.Variations[0].Code != "LR0001-V1" || d.Variations[1].Code != "LR0001-V2" {
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
	e, _ := s.GetDesignByCode(ctx, "LE0001")
	for _, w := range e.Variations[0].Weights {
		if w.MetalCode == "18KR" && w.NetWeightG.String() != "3.05" {
			t.Errorf("18KR from reference = %s", w.NetWeightG)
		}
	}
	if p, _ := s.GetDesignByCode(ctx, "LP0001"); len(p.Variations) != 0 {
		t.Errorf("design without a base input got variations: %+v", p.Variations)
	}

	// Numbering carries on from where the import stopped.
	more, err := s.ImportDesigns(ctx, []ImportRow{{Category: "Rings", VolumeMM3: "100"}}, false)
	if err != nil || strings.Join(more.Codes, ",") != "LR0002" {
		t.Fatalf("second import = %+v %v", more, err)
	}
}

func TestImportDesignsExplicitCodesPushTheCounter(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)

	// A design that brings its own number (an earlier export, or an old system) keeps it, and
	// automatic numbers handed out in the same import skip past it.
	res, err := s.ImportDesigns(ctx, []ImportRow{
		{Category: "Rings", Name: "auto first in file", VolumeMM3: "100"},
		{Code: "LR0001", Category: "Rings", Name: "explicit", VolumeMM3: "100"},
		{Code: "LR0002", Category: "Rings", Name: "explicit two"},
		{Code: "LR0001", VolumeMM3: "250"}, // another variation of LR0001
	}, false)
	if err != nil || len(res.Errors) != 0 {
		t.Fatalf("import: %+v %v", res, err)
	}
	if got := strings.Join(res.Codes, ","); got != "LR0003,LR0001,LR0002" {
		t.Errorf("codes = %s (the automatic design must not take a number the file supplies)", got)
	}
	if d, _ := s.GetDesignByCode(ctx, "LR0001"); len(d.Variations) != 2 {
		t.Errorf("LR0001 variations = %d", len(d.Variations))
	}
	next, err := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if err != nil || next.Code != "LR0004" {
		t.Errorf("next code = %q %v", next.Code, err)
	}
}

func TestImportDesignsDryRunWritesNothing(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	res, err := s.ImportDesigns(context.Background(), []ImportRow{
		{Category: "Rings", VolumeMM3: "100"},
	}, true)
	if err != nil || len(res.Errors) != 0 || !res.DryRun || res.Designs != 1 || res.Variations != 1 {
		t.Fatalf("dry run = %+v, %v", res, err)
	}
	if n := countDesigns(t, s); n != 0 {
		t.Errorf("dry run created %d designs", n)
	}
	// And it must not have used up a number.
	d, err := s.CreateDesign(context.Background(), DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if err != nil || d.Code != "LR0001" {
		t.Errorf("first real code = %q %v", d.Code, err)
	}
}

func TestImportDesignsReportsEveryProblemAndWritesNothing(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	newDesign(t, s, "LR0200") // already exists
	del := newDesign(t, s, "LR0201")
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
		{Line: 2, Code: "LR0901", Category: "Rings", VolumeMM3: "100"},
		{Line: 3, Code: "bad code!", Category: "Rings"},
		{Line: 4, Category: "Spaceships"},
		{Line: 5, Category: "Rings", Tags: "nope"},
		{Line: 6, Category: "Rings", Tags: "classic"},
		{Line: 7, Category: "Rings", VolumeMM3: "abc"},
		{Line: 8, Category: "Rings", VolumeMM3: "1.2345"},
		{Line: 9, Category: "Rings", VolumeMM3: "0"},
		{Line: 10, Category: "Rings", VolumeMM3: "5", RefWeightG: "1", RefMetal: "18KY"},
		{Line: 11, Category: "Rings", RefWeightG: "1"},
		{Line: 12, Category: "Rings", RefWeightG: "1", RefMetal: "24KZ"},
		{Line: 13, Code: "LR0200", Category: "Rings"},
		{Line: 14, Code: "LR0201", Category: "Rings"},
		{Line: 15, Code: "LR0901", Name: "x", Category: "Rings"},
		{Line: 16, Code: "LE0001", Category: "Rings"},
		{Line: 17, Category: "Bangles"},
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
		3: "must be LR followed by digits", 4: "unknown category", 5: `unknown tag "nope"`, 6: "several groups",
		7: "not a number", 8: "more than 3 decimal", 9: "greater than 0", 10: "not both",
		11: "both required", 12: "unknown ref_metal", 13: "already exists", 14: "already exists",
		15: "differs from line 2", 16: "must be LR followed by digits", 17: "no code prefix",
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

func TestImportDesignsFirstRowNeedsACategory(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	res, err := s.ImportDesigns(context.Background(), []ImportRow{{Line: 2, VolumeMM3: "100"}}, true)
	if err != nil || len(res.Errors) != 1 || !strings.Contains(res.Errors[0].Message, "first row needs a category") {
		t.Fatalf("result = %+v %v", res, err)
	}
}

func TestImportDesignsIsAtomicOnLateFailure(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	// Passes validation but fails inside the transaction: a 14-digit volume overflows numeric(14,3).
	rows := []ImportRow{
		{Category: "Rings", VolumeMM3: "100"},
		{Category: "Rings", VolumeMM3: "99999999999999"},
	}
	if _, err := s.ImportDesigns(ctx, rows, false); err == nil {
		t.Fatal("expected the oversized volume to fail")
	}
	if n := countDesigns(t, s); n != 0 {
		t.Errorf("partial import left %d designs behind", n)
	}
	// The failed import gave its numbers back.
	d, err := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if err != nil || d.Code != "LR0001" {
		t.Errorf("code after a failed import = %q %v", d.Code, err)
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
	setPrefixes(t, s)
	if _, err := s.CreateTag(ctx, "Floral", "Motif"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTag(ctx, "Bridal", "Audience"); err != nil {
		t.Fatal(err)
	}
	in := []ImportRow{
		{Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Name: "Lotus ring", Category: "Rings", Tags: "Floral;Bridal", VolumeMM3: "200"},
		{VolumeMM3: "300.5"},
		{Name: "Bare", Category: "Pendants"},
		{Name: "Gone", Category: "Rings", VolumeMM3: "10"},
	}
	if res, err := s.ImportDesigns(ctx, in, false); err != nil || len(res.Errors) != 0 {
		t.Fatalf("setup: %+v %v", res, err)
	}
	gone, _ := s.GetDesignByCode(ctx, "LR0002")
	if err := s.DeleteDesign(ctx, gone.ID); err != nil {
		t.Fatal(err)
	}

	rows, err := s.ExportDesigns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []ImportRow{
		{Code: "LE0001", Name: "Stud", Category: "Earrings", RefWeightG: "3.116", RefMetal: "18KY"},
		{Code: "LP0001", Name: "Bare", Category: "Pendants"},
		{Code: "LR0001", Name: "Lotus ring", Category: "Rings", Tags: "Audience:Bridal;Motif:Floral", VolumeMM3: "200"},
		{Code: "LR0001", VolumeMM3: "300.5"},
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
	before, _ := s.GetDesignByCode(ctx, "LR0001")
	if _, err := s.Pool.Exec(ctx, "TRUNCATE master_designs CASCADE"); err != nil {
		t.Fatal(err)
	}
	res, err := s.ImportDesigns(ctx, rows, false)
	if err != nil || len(res.Errors) != 0 || res.Designs != 3 || res.Variations != 3 {
		t.Fatalf("re-import: %+v %v", res, err)
	}
	after, _ := s.GetDesignByCode(ctx, "LR0001")
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
