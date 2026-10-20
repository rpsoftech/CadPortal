package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

func metalID(t *testing.T, s *Service, code string) (id int64, sg decimal.Decimal) {
	t.Helper()
	m, err := s.Q.GetMetalByCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	return m.ID, m.SpecificGravity
}

func setWastage(t *testing.T, s *Service, code, pct string) {
	t.Helper()
	id, sg := metalID(t, s, code)
	if _, err := s.UpdateMetal(context.Background(), id, MetalUpdate{
		SpecificGravity: sg, WastagePct: decimal.NullDecimal{Decimal: d(pct), Valid: true}, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
}

// fixtureVariation builds the spec fixture: 200 mm3 with the 37-pc 2HD seats.
func fixtureVariation(t *testing.T, s *Service, code string) int64 {
	t.Helper()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, code)
	v, err := s.CreateVariation(context.Background(), des.ID, BaseInput{VolumeMM3: dp("200")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSeats(context.Background(), v.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	return v.ID
}

func TestCostingRowsReportsEveryMissingInput(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR050")

	// Neither wastage nor stone rates are set (open decisions): nothing may be guessed.
	_, err := s.CostingRows(ctx, CostingRequest{
		Items:        []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
		GoldRatePerG: d("6000"), SettingRatePerPc: d("25"),
	})
	var mci *MissingCostInputsError
	if !errors.As(err, &mci) {
		t.Fatalf("want MissingCostInputsError, got %v", err)
	}
	if len(mci.Wastage) != 1 || mci.Wastage[0] != "18KR" {
		t.Errorf("wastage = %v", mci.Wastage)
	}
	want := []calc.MissingPair{{SizeMM: "1.10", Code: "2HD"}, {SizeMM: "1.20", Code: "2HD"}, {SizeMM: "2.50", Code: "2HD"}}
	if len(mci.StoneRates) != 3 {
		t.Fatalf("rates = %v", mci.StoneRates)
	}
	for i, w := range want {
		if mci.StoneRates[i] != w {
			t.Errorf("rate %d = %v want %v", i, mci.StoneRates[i], w)
		}
	}
}

func TestCostingRowsFixture(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR051")
	setWastage(t, s, "18KR", "10") // test-only numbers
	rate := decimal.NullDecimal{Decimal: d("50000"), Valid: true}
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RatePerCt: rate},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerCt: rate},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RatePerCt: rate},
	}, ""); err != nil {
		t.Fatal(err)
	}

	rows, err := s.CostingRows(ctx, CostingRequest{
		Items:        []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
		GoldRatePerG: d("6000"), SettingRatePerPc: d("25"),
	})
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	// net 3.050 x 1.10 = 3.355 g -> 20130.00; 0.3470 ct x 50000 = 17350.00; 37 pcs x 25 = 925.00
	checks := map[string][2]string{
		"variation": {r.VariationCode, "LR051-V1"},
		"net":       {r.NetG.StringFixed(3), "3.050"},
		"gold g":    {r.GoldG.StringFixed(3), "3.355"},
		"gold":      {r.GoldCost.StringFixed(2), "20130.00"},
		"stone ct":  {r.StoneCarats.StringFixed(4), "0.3470"},
		"stone":     {r.StoneCost.StringFixed(2), "17350.00"},
		"setting":   {r.SettingCost.StringFixed(2), "925.00"},
		"total":     {r.Total.StringFixed(2), "38405.00"},
	}
	for k, p := range checks {
		if p[0] != p[1] {
			t.Errorf("%s = %s want %s", k, p[0], p[1])
		}
	}
	if r.SettingPcs != 37 {
		t.Errorf("pcs = %d", r.SettingPcs)
	}
}

func TestCostingRowsValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR052")
	item := CostingItem{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}
	cases := map[string]CostingRequest{
		"no items":        {GoldRatePerG: d("1")},
		"zero gold rate":  {Items: []CostingItem{item}, GoldRatePerG: d("0")},
		"negative labour": {Items: []CostingItem{item}, GoldRatePerG: d("1"), SettingRatePerPc: d("-1")},
		"unknown metal":   {Items: []CostingItem{{VariationID: vid, MetalCode: "99K", StoneCode: "2HD"}}, GoldRatePerG: d("1")},
		"no stone code":   {Items: []CostingItem{{VariationID: vid, MetalCode: "18KR"}}, GoldRatePerG: d("1")},
		"unknown stone":   {Items: []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "9ZZ"}}, GoldRatePerG: d("1")},
	}
	for name, req := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.CostingRows(ctx, req); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	req := CostingRequest{Items: []CostingItem{{VariationID: 99999, MetalCode: "18KR", StoneCode: "2HD"}}, GoldRatePerG: d("1")}
	if _, err := s.CostingRows(ctx, req); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing variation: %v", err)
	}
}

func TestJobBag(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR053")

	jb, err := s.JobBag(ctx, vid, "18KR", "2HD")
	if err != nil {
		t.Fatal(err)
	}
	if jb.VariationCode != "LR053-V1" || jb.DesignCode != "LR053" || jb.Category != "Bangles" {
		t.Errorf("header = %+v", jb)
	}
	if jb.NetG.StringFixed(3) != "3.050" || jb.GrossG.StringFixed(3) != "3.119" || jb.BOM.TotalPcs != 37 {
		t.Errorf("weights: net %s gross %s pcs %d", jb.NetG, jb.GrossG, jb.BOM.TotalPcs)
	}
	if jb.BaseInput != "Volume 200.000 mm3" {
		t.Errorf("base = %q", jb.BaseInput)
	}
	if _, err := s.JobBag(ctx, vid, "99K", "2HD"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown metal: %v", err)
	}
	if _, err := s.JobBag(ctx, vid, "", "2HD"); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty metal: %v", err)
	}
	// A matrix gap for the chosen code is an error, not a zero.
	if _, err := s.SetSeats(ctx, vid, append(fixtureSeats(), SeatInput{SizeMM: d("1.50"), StoneCount: 2})); err != nil {
		t.Fatal(err)
	}
	var mme *calc.MissingMatrixError
	if _, err := s.JobBag(ctx, vid, "18KR", "2HD"); !errors.As(err, &mme) {
		t.Errorf("gap: %v", err)
	}
}
