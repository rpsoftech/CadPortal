package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func nd(s string) decimal.NullDecimal { return decimal.NullDecimal{Decimal: d(s), Valid: true} }

func TestCreateMetal(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR080")
	if _, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")}); err != nil {
		t.Fatal(err)
	}

	m, err := s.CreateMetal(ctx, MetalCreate{Code: " 18kw ", SpecificGravity: d("15.6"), WastagePct: nd("9.5"), IsDefaultInput: true, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.Code != "18KW" || !m.IsDefaultInput {
		t.Errorf("metal = %+v", m)
	}
	// Becoming the default input clears the flag elsewhere.
	all, _ := s.ListMetals(ctx)
	defaults := 0
	for _, x := range all {
		if x.IsDefaultInput {
			defaults++
		}
	}
	if defaults != 1 {
		t.Errorf("default input metals = %d, want exactly 1", defaults)
	}

	// The recalc job fills in a weight row for the new alloy on existing variations.
	if did, err := ProcessNextRecalcJob(ctx, s.Pool); err != nil || !did {
		t.Fatalf("job: %v %v", did, err)
	}
	got, _ := s.GetDesignByCode(ctx, "LR080")
	w := weightsByMetal(got.Variations[0])["18KW"]
	if w.NetWeightG.StringFixed(3) != "3.120" { // 200 mm3 x 15.6 / 1000
		t.Errorf("18KW net = %v", w.NetWeightG)
	}

	bad := map[string]MetalCreate{
		"lowercase code ok but too short": {Code: "x", SpecificGravity: d("10"), Active: true},
		"symbols":                         {Code: "18K-W", SpecificGravity: d("10"), Active: true},
		"zero SG":                         {Code: "9KY", SpecificGravity: d("0"), Active: true},
		"huge SG":                         {Code: "9KY", SpecificGravity: d("1000"), Active: true},
		"SG 5 places":                     {Code: "9KY", SpecificGravity: d("10.12345"), Active: true},
		"wastage over 100":                {Code: "9KY", SpecificGravity: d("10"), WastagePct: nd("100.5"), Active: true},
		"negative wastage":                {Code: "9KY", SpecificGravity: d("10"), WastagePct: nd("-1"), Active: true},
		"wastage 4 places":                {Code: "9KY", SpecificGravity: d("10"), WastagePct: nd("1.2345"), Active: true},
	}
	for name, in := range bad {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateMetal(ctx, in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	if _, err := s.CreateMetal(ctx, MetalCreate{Code: "18kw", SpecificGravity: d("15"), Active: true}); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate code: %v", err)
	}
	// The same bounds apply when editing.
	id, sg := metalID(t, s, "22KY")
	if _, err := s.UpdateMetal(ctx, id, MetalUpdate{SpecificGravity: sg, WastagePct: nd("150"), Active: true}); !errors.Is(err, ErrInvalid) {
		t.Errorf("edit wastage over 100: %v", err)
	}
}

func TestStoneCodes(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	c, err := s.CreateStoneCode(ctx, " 3hd ")
	if err != nil || c.Code != "3HD" || !c.Active {
		t.Fatalf("create: %+v %v", c, err)
	}
	for _, bad := range []string{"", "bad code", "WAYTOOLONGCODE1"} {
		if _, err := s.CreateStoneCode(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("code %q: %v", bad, err)
		}
	}
	if _, err := s.CreateStoneCode(ctx, "3HD"); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate: %v", err)
	}
	off, err := s.SetStoneCodeActive(ctx, c.ID, false)
	if err != nil || off.Active {
		t.Fatalf("deactivate: %+v %v", off, err)
	}
	// The baseline cannot be switched off.
	base, _ := s.Q.GetBaselineStoneCode(ctx)
	if _, err := s.SetStoneCodeActive(ctx, base.ID, false); !errors.Is(err, ErrInvalid) {
		t.Errorf("deactivate baseline: %v", err)
	}
	if _, err := s.SetStoneCodeActive(ctx, 99999, true); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing code: %v", err)
	}
}

func TestValidateMatrixRows(t *testing.T) {
	row := func(size, code, cts string) MatrixRowInput {
		return MatrixRowInput{SizeMM: d(size), StoneCode: code, CaratsPerStone: d(cts)}
	}
	if err := ValidateMatrixRows([]MatrixRowInput{row("1.10", "2HD", "0.0070"), row("1.10", "1HD", "0.005")}); err != nil {
		t.Fatalf("valid rows rejected: %v", err)
	}
	tooMany := make([]MatrixRowInput, maxMatrixRows+1)
	for i := range tooMany {
		tooMany[i] = row(decimal.NewFromInt(int64(i+1)).Div(decimal.NewFromInt(100)).String(), "2HD", "0.001")
	}
	withRate := row("1.10", "2HD", "0.007")
	withRate.RatePerCt = nd("50000.123")
	negRate := row("1.10", "2HD", "0.007")
	negRate.RatePerCt = nd("-5")

	bad := map[string][]MatrixRowInput{
		"empty":           {},
		"too many":        tooMany,
		"no code":         {row("1.10", " ", "0.007")},
		"zero size":       {row("0", "2HD", "0.007")},
		"huge size":       {row("1000", "2HD", "0.007")},
		"size 3 places":   {row("1.105", "2HD", "0.007")},
		"negative carats": {row("1.10", "2HD", "-0.001")},
		"carats 7 places": {row("1.10", "2HD", "0.0070001")},
		"rate 3 places":   {withRate},
		"negative rate":   {negRate},
		"duplicate":       {row("1.10", "2HD", "0.007"), row("1.1", "2hd", "0.008")},
	}
	for name, rows := range bad {
		t.Run(name, func(t *testing.T) {
			if err := ValidateMatrixRows(rows); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	// Row numbers in messages help locate a bad line in a large import.
	err := ValidateMatrixRows([]MatrixRowInput{row("1.10", "2HD", "0.007"), row("1.20", "2HD", "0.0070001")})
	if err == nil || !strings.Contains(err.Error(), "row 2") {
		t.Errorf("message should name the row: %v", err)
	}
}

func TestDeleteMatrixRow(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR081") // seats: 24x1.10, 12x1.20 flexible, 1x2.50 locked to 2HD; baseline 2HD
	_ = vid

	// 2.50/2HD has a locked seat: refused.
	err := s.DeleteMatrixRow(ctx, d("2.50"), "2HD")
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "1 seat") {
		t.Fatalf("locked row: %v", err)
	}
	// A row nobody locks can go; deleting a baseline row queues a recalc and leaves a gap.
	if err := s.DeleteMatrixRow(ctx, d("1.20"), "2HD"); err != nil {
		t.Fatal(err)
	}
	st, _ := s.RecalcStatus(ctx)
	if st.Pending == 0 {
		t.Error("deleting a baseline row should queue a recalc")
	}
	if did, err := ProcessNextRecalcJob(ctx, s.Pool); err != nil || !did {
		t.Fatalf("job: %v %v", did, err)
	}
	gaps, err := s.MatrixGaps(ctx)
	if err != nil || len(gaps) != 1 || gaps[0].SizeMm.StringFixed(2) != "1.20" || gaps[0].StoneCode != "2HD" || gaps[0].VariationCount != 1 {
		t.Fatalf("gaps = %+v %v", gaps, err)
	}
	got, _ := s.GetDesignByCode(ctx, "LR081")
	if got.Variations[0].BaselineGrossWeightG.Valid {
		t.Error("gross must be unknown after the row was removed")
	}

	if err := s.DeleteMatrixRow(ctx, d("1.20"), "2HD"); !errors.Is(err, ErrNotFound) {
		t.Errorf("already deleted: %v", err)
	}
	if err := s.DeleteMatrixRow(ctx, d("1.20"), "9ZZ"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown code: %v", err)
	}
}

func TestRecalcStatusAndGapsEmpty(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	st, err := s.RecalcStatus(ctx)
	if err != nil || st.Pending != 0 || st.Running != 0 || st.Failed != 0 || st.LastError != "" {
		t.Fatalf("fresh status = %+v %v", st, err)
	}
	gaps, err := s.MatrixGaps(ctx)
	if err != nil || gaps == nil || len(gaps) != 0 {
		t.Fatalf("gaps = %#v %v", gaps, err)
	}
	if _, err := s.Pool.Exec(ctx, "INSERT INTO weight_recalc_jobs (reason, status, error) VALUES ('metal_sg','failed','boom')"); err != nil {
		t.Fatal(err)
	}
	if st, _ := s.RecalcStatus(ctx); st.Failed != 1 || st.LastError != "boom" {
		t.Errorf("failed status = %+v", st)
	}
}

func TestCategoryAndTagAdmin(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	cats, _ := s.ListCategories(ctx)

	c, err := s.UpdateCategory(ctx, cats[0].ID, " Bangles ", " BG ")
	if err != nil || c.Name != "Bangles" || c.CodePrefix.String != "BG" {
		t.Fatalf("update: %+v %v", c, err)
	}
	if _, err := s.UpdateCategory(ctx, cats[1].ID, "Other", "BG"); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate prefix: %v", err)
	} else if strings.Contains(err.Error(), "Key (") || !strings.Contains(err.Error(), "code prefix") {
		t.Errorf("conflict message should be friendly, got %q", err)
	}
	if _, err := s.UpdateCategory(ctx, cats[1].ID, "  ", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("blank name: %v", err)
	}
	if c, err := s.UpdateCategory(ctx, cats[1].ID, cats[1].Name, ""); err != nil || c.CodePrefix.Valid {
		t.Errorf("clearing the prefix: %+v %v", c, err)
	}
	if _, err := s.UpdateCategory(ctx, 99999, "X", ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing category: %v", err)
	}

	tag, _ := s.CreateTag(ctx, "Floral", "Motif")
	upd, err := s.UpdateTag(ctx, tag.ID, "Flowers", "Sub-Style", false)
	if err != nil || upd.Name != "Flowers" || upd.TagGroup != "Sub-Style" || upd.Active {
		t.Fatalf("update tag: %+v %v", upd, err)
	}
	active, _ := s.ListTags(ctx)
	all, _ := s.ListAllTags(ctx)
	if len(active) != 0 || len(all) != 1 {
		t.Errorf("active=%d all=%d: deactivated tags must be hidden from the public list but visible to admins", len(active), len(all))
	}
	if _, err := s.UpdateTag(ctx, tag.ID, "X", "Bad", true); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad group: %v", err)
	}
	other, _ := s.CreateTag(ctx, "Halo", "Sub-Style")
	if _, err := s.UpdateTag(ctx, other.ID, "Flowers", "Sub-Style", true); !errors.Is(err, ErrConflict) {
		t.Errorf("duplicate tag in a group: %v", err)
	}
}
