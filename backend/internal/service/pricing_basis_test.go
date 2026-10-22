package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

func TestEffectiveBasis(t *testing.T) {
	for name, c := range map[string]struct {
		row  MatrixRowInput
		want string
	}{
		"nothing given: per piece, the default":    {MatrixRowInput{}, "pc"},
		"only a per-carat price keeps its meaning": {MatrixRowInput{RatePerCt: nd("50000")}, "ct"},
		"only a per-piece price":                   {MatrixRowInput{RatePerPc: nd("100")}, "pc"},
		"both prices, no basis: per piece":         {MatrixRowInput{RatePerPc: nd("100"), RatePerCt: nd("50000")}, "pc"},
		"explicit carat":                           {MatrixRowInput{RateBasis: "ct", RatePerPc: nd("100")}, "ct"},
		"explicit piece beats a per-carat price":   {MatrixRowInput{RateBasis: "pc", RatePerCt: nd("50000")}, "pc"},
	} {
		if got := EffectiveBasis(c.row); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}

func TestMatrixRowPriceValidation(t *testing.T) {
	base := MatrixRowInput{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070")}
	bad := map[string]MatrixRowInput{
		"unknown basis":       {SizeMM: base.SizeMM, StoneCode: "2HD", CaratsPerStone: base.CaratsPerStone, RateBasis: "gram"},
		"negative per piece":  {SizeMM: base.SizeMM, StoneCode: "2HD", CaratsPerStone: base.CaratsPerStone, RatePerPc: nd("-1")},
		"three decimal price": {SizeMM: base.SizeMM, StoneCode: "2HD", CaratsPerStone: base.CaratsPerStone, RatePerPc: nd("1.005")},
	}
	for name, r := range bad {
		if err := ValidateMatrixRows([]MatrixRowInput{r}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	for _, b := range []string{"", "pc", "ct"} {
		r := base
		r.RateBasis, r.RatePerPc = b, nd("12.50")
		if err := ValidateMatrixRows([]MatrixRowInput{r}); err != nil {
			t.Errorf("basis %q refused: %v", b, err)
		}
	}
}

func TestStonesCanBePricedPerPieceOrPerCaratAndMixed(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR060")
	setWastage(t, s, "18KR", "10")
	// 24 x 1.10 mm and 12 x 1.20 mm priced per piece, the locked 1 x 2.50 mm per carat.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RatePerPc: nd("100")},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerPc: nd("150"), RatePerCt: nd("1"), RateBasis: "pc"},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RatePerCt: nd("50000")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := s.ListStoneMatrix(ctx)
	bases := map[string]string{}
	for _, r := range m {
		bases[r.SizeMm.StringFixed(2)] = r.RateBasis
	}
	if bases["1.10"] != "pc" || bases["1.20"] != "pc" || bases["2.50"] != "ct" {
		t.Fatalf("bases = %v (a row with only a per-carat price must stay per carat)", bases)
	}
	rows, err := s.CostingRows(ctx, CostingRequest{
		Items:        []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
		GoldRatePerG: d("6000"), SettingRatePerPc: d("25"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 24 x 100 + 12 x 150 = 4200.00 per piece, and 0.0650 ct x 50000 = 3250.00 per carat.
	if got := rows[0].StoneCost.StringFixed(2); got != "7450.00" {
		t.Errorf("stone cost = %s, want 7450.00", got)
	}
	if got := rows[0].Total.StringFixed(2); got != "28505.00" { // 20130.00 + 7450.00 + 37 x 25 = 925.00
		t.Errorf("total = %s, want 28505.00", got)
	}
}

func TestOnlyThePriceOfTheChosenBasisIsUsedAndAMissingOneIsNamed(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR061")
	setWastage(t, s, "18KR", "10")
	// Per piece, but only a per-carat price is entered for 1.10 mm; 2.50 mm is per carat with nothing at all.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RatePerCt: nd("50000"), RateBasis: "pc"},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerPc: nd("150")},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RateBasis: "ct"},
	}, ""); err != nil {
		t.Fatal(err)
	}
	_, err := s.CostingRows(ctx, CostingRequest{
		Items:        []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
		GoldRatePerG: d("6000"), SettingRatePerPc: d("25"),
	})
	var mci *MissingCostInputsError
	if !errors.As(err, &mci) {
		t.Fatalf("want MissingCostInputsError, got %v", err)
	}
	want := []calc.MissingPair{{SizeMM: "1.10", Code: "2HD", Basis: "pc"}, {SizeMM: "2.50", Code: "2HD", Basis: "ct"}}
	if len(mci.StoneRates) != len(want) {
		t.Fatalf("missing = %+v", mci.StoneRates)
	}
	for _, w := range want {
		found := false
		for _, g := range mci.StoneRates {
			found = found || g == w
		}
		if !found {
			t.Errorf("%+v not reported in %+v", w, mci.StoneRates)
		}
	}
	msg := mci.Error()
	if !strings.Contains(msg, "price per piece not set for: (1.10 mm, 2HD)") || !strings.Contains(msg, "rate per carat not set for: (2.50 mm, 2HD)") {
		t.Errorf("message = %q", msg)
	}
}

func TestSetupStatusLooksAtTheActivePrice(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "1HD", CaratsPerStone: d("0.0070"), RatePerPc: nd("100")},
		{SizeMM: d("1.20"), StoneCode: "1HD", CaratsPerStone: d("0.0095"), RatePerCt: nd("50000")},
		{SizeMM: d("1.30"), StoneCode: "1HD", CaratsPerStone: d("0.0100"), RatePerCt: nd("50000"), RateBasis: "pc"}, // wrong price filled in
	}, ""); err != nil {
		t.Fatal(err)
	}
	it, ok := setupKeys(t, s)["matrix_rates"]
	if !ok || !strings.Contains(it.Detail, "1 of 3") {
		t.Errorf("rates item = %+v %v", it, ok)
	}
}
