package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/testdb"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }
func dp(s string) *decimal.Decimal {
	v := d(s)
	return &v
}

// newTestService resets this package's own test database to freshly migrated state.
func newTestService(t *testing.T) *Service {
	t.Helper()
	pool, _ := testdb.Reset(t)
	return New(pool)
}

func weightsByMetal(v VariationDetail) map[string]db.ListVariationWeightsRow {
	m := map[string]db.ListVariationWeightsRow{}
	for _, w := range v.Weights {
		m[w.MetalCode] = w
	}
	return m
}

func newDesign(t *testing.T, s *Service, code string) db.MasterDesign {
	t.Helper()
	cats, err := s.ListCategories(context.Background())
	if err != nil || len(cats) == 0 {
		t.Fatalf("categories: %v", err)
	}
	des, err := s.CreateDesign(context.Background(), DesignInput{Code: code, CategoryID: cats[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	return des
}

func TestVariationNetWeightsFromVolume(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR001")

	v, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if err != nil {
		t.Fatal(err)
	}
	if v.Code != "LR001-V1" {
		t.Errorf("code = %s", v.Code)
	}
	w := weightsByMetal(v)
	for metal, want := range map[string]string{"22KY": "3.550", "18KY": "3.116", "18KR": "3.050"} {
		if got := w[metal].NetWeightG.StringFixed(3); got != want {
			t.Errorf("%s net = %s want %s", metal, got, want)
		}
		// No seats yet: gross equals net.
		if !w[metal].GrossWeightG.Valid || !w[metal].GrossWeightG.Decimal.Equal(w[metal].NetWeightG) {
			t.Errorf("%s gross = %v want net", metal, w[metal].GrossWeightG)
		}
	}

	v2, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("100")})
	if err != nil || v2.Code != "LR001-V2" {
		t.Fatalf("v2: %v %s", err, v2.Code)
	}
}

func TestVariationFromReferenceWeight(t *testing.T) {
	s := newTestService(t)
	des := newDesign(t, s, "LR002")
	v, err := s.CreateVariation(context.Background(), des.ID,
		BaseInput{RefWeightG: dp("3.550"), RefMetalCode: "22KY"})
	if err != nil {
		t.Fatal(err)
	}
	w := weightsByMetal(v)
	for metal, want := range map[string]string{"22KY": "3.550", "18KY": "3.116", "18KR": "3.050"} {
		if got := w[metal].NetWeightG.StringFixed(3); got != want {
			t.Errorf("%s net = %s want %s", metal, got, want)
		}
	}
}

func TestBaseInputValidation(t *testing.T) {
	s := newTestService(t)
	des := newDesign(t, s, "LR003")
	cases := map[string]BaseInput{
		"none":         {},
		"both":         {VolumeMM3: dp("10"), RefWeightG: dp("1"), RefMetalCode: "22KY"},
		"weight only":  {RefWeightG: dp("1")},
		"unknown":      {RefWeightG: dp("1"), RefMetalCode: "99KX"},
		"negative vol": {VolumeMM3: dp("-5")},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CreateVariation(context.Background(), des.ID, in); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestDuplicateDesignCodeConflicts(t *testing.T) {
	s := newTestService(t)
	des := newDesign(t, s, "LR004")
	_, err := s.CreateDesign(context.Background(), DesignInput{Code: "LR004", CategoryID: des.CategoryID})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
}

// Seats arrive in stage 2; here they are inserted directly to prove the
// recalc job rebuilds gross weights from the baseline stone code.
func TestRecalcJobBaselineGross(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR005")
	v, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if err != nil {
		t.Fatal(err)
	}

	// Test-only 2HD pointers from the spec fixtures; 2HD becomes the baseline.
	err = s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070")},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095")},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650")},
	}, "2HD")
	if err != nil {
		t.Fatal(err)
	}
	two, _ := s.Q.GetStoneCodeByCode(ctx, "2HD")
	for i, seat := range []struct {
		size  string
		count int
		lock  bool
	}{{"1.10", 24, false}, {"1.20", 12, false}, {"2.50", 1, true}} {
		var lock any
		if seat.lock {
			lock = two.ID
		}
		if _, err := s.Pool.Exec(ctx,
			`INSERT INTO variation_stone_seats (variation_id, position, size_mm, stone_count, locked_stone_code_id)
			 VALUES ($1,$2,$3,$4,$5)`, v.ID, i+1, seat.size, seat.count, lock); err != nil {
			t.Fatal(err)
		}
	}

	did, err := ProcessNextRecalcJob(ctx, s.Pool)
	if err != nil || !did {
		t.Fatalf("job: did=%v err=%v", did, err)
	}
	if did, _ := ProcessNextRecalcJob(ctx, s.Pool); did {
		t.Fatal("queue should be drained")
	}

	got, err := s.GetDesignByCode(ctx, "LR005")
	if err != nil {
		t.Fatal(err)
	}
	gv := got.Variations[0]
	if gv.BaselineStoneCarats.StringFixed(4) != "0.3470" {
		t.Errorf("baseline carats = %s", gv.BaselineStoneCarats.StringFixed(4))
	}
	w := weightsByMetal(gv)
	if g := w["18KR"].GrossWeightG.Decimal.StringFixed(3); g != "3.119" {
		t.Errorf("18KR gross = %s want 3.119", g)
	}

	// A new seat size with no matrix row leaves gross NULL (never 0).
	if _, err := s.Pool.Exec(ctx,
		`INSERT INTO variation_stone_seats (variation_id, position, size_mm, stone_count) VALUES ($1,4,1.50,2)`, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.inTx(ctx, func(q *db.Queries) error { return RecalcVariation(ctx, q, v.ID) }); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetDesignByCode(ctx, "LR005")
	if gw := weightsByMetal(got.Variations[0])["18KR"].GrossWeightG; gw.Valid {
		t.Errorf("gross should be NULL with a matrix gap, got %v", gw)
	}
	var gaps int
	if err := s.Pool.QueryRow(ctx, "SELECT count(*) FROM v_seat_matrix_gaps WHERE variation_id=$1", v.ID).Scan(&gaps); err != nil || gaps != 1 {
		t.Errorf("gaps view = %d err=%v", gaps, err)
	}
}

func TestMetalUpdateEnqueuesRecalc(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR006")
	if _, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")}); err != nil {
		t.Fatal(err)
	}
	metals, _ := s.ListMetals(ctx)
	var kr db.Metal
	for _, m := range metals {
		if m.Code == "18KR" {
			kr = m
		}
	}
	if _, err := s.UpdateMetal(ctx, kr.ID, MetalUpdate{SpecificGravity: d("15.50"), Active: true}); err != nil {
		t.Fatal(err)
	}
	if did, err := ProcessNextRecalcJob(ctx, s.Pool); err != nil || !did {
		t.Fatalf("job: %v %v", did, err)
	}
	got, _ := s.GetDesignByCode(ctx, "LR006")
	if n := weightsByMetal(got.Variations[0])["18KR"].NetWeightG.StringFixed(3); n != "3.100" {
		t.Errorf("18KR net after SG change = %s want 3.100", n)
	}
}

func TestListDesignsFilters(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := newDesign(t, s, "LR010")
	b := newDesign(t, s, "LR011")
	_, _ = s.CreateVariation(ctx, a.ID, BaseInput{VolumeMM3: dp("200")}) // 18KR 3.050
	_, _ = s.CreateVariation(ctx, b.ID, BaseInput{VolumeMM3: dp("400")}) // 18KR 6.100

	rows, err := s.ListDesigns(ctx, DesignFilter{MetalCode: "18KR", MinNetWt: dp("5"), MaxNetWt: dp("7")})
	if err != nil || len(rows) != 1 || rows[0].Code != "LR011" {
		t.Fatalf("rows=%v err=%v", rows, err)
	}
	rows, _ = s.ListDesigns(ctx, DesignFilter{})
	if len(rows) != 2 {
		t.Fatalf("unfiltered = %d", len(rows))
	}
}

func TestListDesignsCardSummary(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	a := newDesign(t, s, "LR012")
	empty := newDesign(t, s, "LR013")                                                        // no variations
	if _, err := s.CreateVariation(ctx, a.ID, BaseInput{VolumeMM3: dp("200")}); err != nil { // 18KR 3.050
		t.Fatal(err)
	}
	if _, err := s.CreateVariation(ctx, a.ID, BaseInput{VolumeMM3: dp("400")}); err != nil { // 18KR 6.100
		t.Fatal(err)
	}
	tag, err := s.CreateTag(ctx, "Floral", "Motif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateDesign(ctx, a.ID, DesignInput{CategoryID: a.CategoryID, TagIDs: []int64{tag.ID}}); err != nil {
		t.Fatal(err)
	}

	// Without a metal there is no range, but tags and counts are present.
	rows, err := s.ListDesigns(ctx, DesignFilter{})
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if rows[0].NetMin != nil || rows[0].VariationCount != 2 || len(rows[0].TagNames) != 1 || rows[0].TagNames[0] != "Floral" {
		t.Errorf("no-metal card = %+v", rows[0])
	}
	if rows[1].Code != empty.Code || rows[1].TagNames == nil || rows[1].VariationCount != 0 {
		t.Errorf("empty design card = %+v", rows[1])
	}

	// With a metal, the range spans the variations. Gross equals net (no seats).
	rows, _ = s.ListDesigns(ctx, DesignFilter{MetalCode: "18KR", CategoryID: &a.CategoryID, TagIDs: []int64{tag.ID}})
	if len(rows) != 1 {
		t.Fatalf("filtered rows = %d", len(rows))
	}
	r := rows[0]
	if r.NetMin.StringFixed(3) != "3.050" || r.NetMax.StringFixed(3) != "6.100" ||
		r.GrossMin.StringFixed(3) != "3.050" || r.GrossMax.StringFixed(3) != "6.100" {
		t.Errorf("range = %v %v %v %v", r.NetMin, r.NetMax, r.GrossMin, r.GrossMax)
	}
}

func TestListStoneCodes(t *testing.T) {
	s := newTestService(t)
	codes, err := s.ListStoneCodes(context.Background())
	if err != nil || len(codes) != 2 || codes[0].Code != "1HD" || !codes[0].IsDefaultBaseline {
		t.Fatalf("codes=%+v err=%v", codes, err)
	}
}
