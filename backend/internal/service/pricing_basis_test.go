package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/migrations"
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

func TestWeightPerStoneKeepsEightDecimalsOfCarats(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	// 1.81 g per 1000 stones is 0.00181 g a stone, which is 0.00905 ct: five decimals of carats.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.00"), StoneCode: "2HD", CaratsPerStone: d("0.00905")},
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.00000005")}, // eight decimals, the limit
	}, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := s.ListStoneMatrix(ctx)
	got := map[string]string{}
	for _, r := range m {
		got[r.SizeMm.StringFixed(2)] = r.CaratsPerStone.String()
	}
	if got["1.00"] != "0.00905" || got["1.10"] != "0.00000005" {
		t.Errorf("stored weights = %v (nothing may be rounded)", got)
	}
	// One decimal more (nine) is refused, never rounded.
	err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.000000005")}}, "")
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "at most 8 decimal places") {
		t.Errorf("nine decimals: %v", err)
	}

	// The line total is still rounded to 4 decimals of carats: 1000 x 0.00905 = 9.05 ct = 1.810 g.
	des := newDesign(t, s, "LR0800")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	res, err := s.SetSeats(ctx, v.ID, []SeatInput{{SizeMM: d("1.00"), StoneCount: 1000}})
	if err != nil {
		t.Fatal(err)
	}
	bom, err := s.SimulateBOM(ctx, res.ID, "2HD")
	if err != nil {
		t.Fatal(err)
	}
	if bom.TotalCarats.StringFixed(4) != "9.0500" || bom.StoneWeightG.StringFixed(3) != "1.810" {
		t.Errorf("1000 stones: %s ct, %s g", bom.TotalCarats, bom.StoneWeightG)
	}
}

func TestSevenDecimalCaratWeightsFlowIntoTotalsRoundedToFour(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.00"), StoneCode: "2HD", CaratsPerStone: d("0.0123457")}, // seven decimals
	}, ""); err != nil {
		t.Fatal(err)
	}
	des := newDesign(t, s, "LR0801")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if _, err := s.SetSeats(ctx, v.ID, []SeatInput{{SizeMM: d("1.00"), StoneCount: 100}}); err != nil {
		t.Fatal(err)
	}
	bom, err := s.SimulateBOM(ctx, v.ID, "2HD")
	if err != nil {
		t.Fatal(err)
	}
	// 100 x 0.0123457 = 1.23457 ct, shown to four decimals (1.2346) and 0.2469 g (1.2346 x 0.2, 3 dp: 0.247).
	if bom.TotalCarats.StringFixed(4) != "1.2346" || bom.StoneWeightG.StringFixed(3) != "0.247" {
		t.Errorf("total = %s ct, %s g", bom.TotalCarats, bom.StoneWeightG)
	}
	if bom.Lines[0].CaratsPerStone.String() != "0.0123457" {
		t.Errorf("per stone = %s (it must not be rounded on the way out)", bom.Lines[0].CaratsPerStone)
	}
}

func TestPerGramValidation(t *testing.T) {
	base := MatrixRowInput{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070")}
	with := func(f func(r *MatrixRowInput)) MatrixRowInput { r := base; f(&r); return r }
	bad := map[string]MatrixRowInput{
		"g without its price":                with(func(r *MatrixRowInput) { r.RateBasis = "g" }),
		"g with only another price":          with(func(r *MatrixRowInput) { r.RateBasis, r.RatePerPc = "g", nd("100") }),
		"negative per gram":                  with(func(r *MatrixRowInput) { r.RatePerG = nd("-1") }),
		"three decimal per gram":             with(func(r *MatrixRowInput) { r.RatePerG = nd("1.005") }),
		"per gram price of the wrong length": with(func(r *MatrixRowInput) { r.RateBasis, r.RatePerG = "g", nd("1.005") }),
	}
	for name, r := range bad {
		if err := ValidateMatrixRows([]MatrixRowInput{r}); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
	if err := ValidateMatrixRows([]MatrixRowInput{with(func(r *MatrixRowInput) { r.RateBasis, r.RatePerG = "g", nd("5000.50") })}); err != nil {
		t.Errorf("a per-gram row refused: %v", err)
	}
	// A per-gram price kept beside another basis is fine; only the named basis is used.
	if err := ValidateMatrixRows([]MatrixRowInput{with(func(r *MatrixRowInput) { r.RateBasis, r.RatePerPc, r.RatePerG = "pc", nd("100"), nd("5000") })}); err != nil {
		t.Errorf("spare per-gram price refused: %v", err)
	}
	// Legacy rows are unchanged: a per-gram price alone does not switch the basis.
	if got := EffectiveBasis(MatrixRowInput{RatePerG: nd("5000")}); got != "pc" {
		t.Errorf("legacy basis = %q, want pc", got)
	}
	if got := EffectiveBasis(MatrixRowInput{RateBasis: "g", RatePerG: nd("5000")}); got != "g" {
		t.Errorf("explicit g = %q", got)
	}
}

func TestStonesPricedPerGramAlongsideOtherBases(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR062")
	setWastage(t, s, "18KR", "10")
	// 24 x 1.10 mm per gram (0.0336 g), 12 x 1.20 mm per piece, the locked 1 x 2.50 mm per carat.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RateBasis: "g", RatePerG: nd("5000")},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerPc: nd("150")},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RateBasis: "ct", RatePerCt: nd("50000")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	m, _ := s.ListStoneMatrix(ctx)
	for _, r := range m {
		if r.StoneCode == "2HD" && r.SizeMm.StringFixed(2) == "1.10" && (r.RateBasis != "g" || !r.RatePerG.Valid || r.RatePerG.Decimal.StringFixed(2) != "5000.00") {
			t.Errorf("1.10 row = %+v", r)
		}
	}
	rows, err := s.CostingRows(ctx, CostingRequest{
		Items:        []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
		GoldRatePerG: d("6000"), SettingRatePerPc: d("25"),
	})
	if err != nil {
		t.Fatal(err)
	}
	// 0.0336 g x 5000 = 168.00, 12 x 150 = 1800.00, 0.0650 ct x 50000 = 3250.00.
	if got := rows[0].StoneCost.StringFixed(2); got != "5218.00" {
		t.Errorf("stone cost = %s, want 5218.00", got)
	}
	if got := rows[0].Total.StringFixed(2); got != "26273.00" { // 20130.00 + 5218.00 + 925.00
		t.Errorf("total = %s, want 26273.00", got)
	}
	priced := rows[0].StonePriced
	if len(priced) != 3 || priced[0].Basis != "g" || priced[0].Qty.StringFixed(4) != "0.0336" ||
		priced[1].Basis != "pc" || priced[1].Qty.String() != "12" || priced[2].Basis != "ct" {
		t.Errorf("priced lines = %+v", priced)
	}
}

func TestMissingPerGramPriceIsNamedAndCounted(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	vid := fixtureVariation(t, s, "LR063")
	setWastage(t, s, "18KR", "10")
	// Rows stored as per gram without a price cannot come through the API, so write one the way an
	// older client could have left it: per gram chosen, the price cleared underneath.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RateBasis: "g", RatePerG: nd("5000")},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerPc: nd("150")},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RatePerPc: nd("100")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE stone_size_matrix SET rate_per_g = NULL WHERE size_mm = 1.10"); err != nil {
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
	if msg := mci.Error(); !strings.Contains(msg, "price per gram not set for: (1.10 mm, 2HD)") {
		t.Errorf("message = %q", msg)
	}
	// The seeded matrix has two other rows with no price, so 3 of 5 have none: the cleared 1.10 mm per-gram
	// row and the two seeded ones.
	it, ok := setupKeys(t, s)["matrix_rates"]
	if !ok || !strings.Contains(it.Detail, "3 of 5") {
		t.Errorf("rates item = %+v %v", it, ok)
	}
}

func TestPerGramMigrationDownRefusesWhileInUse(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	down, err := migrations.FS.ReadFile("000014_stone_price_per_gram.down.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RateBasis: "g", RatePerG: nd("5000")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(down)); err == nil || !strings.Contains(err.Error(), "000014") {
		t.Fatalf("down ran over per-gram rows: %v", err)
	}
	// With no per-gram data the rollback works, and the constraint is back to two bases.
	if _, err := s.Pool.Exec(ctx, "UPDATE stone_size_matrix SET rate_basis = 'pc', rate_per_g = NULL"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Pool.Exec(ctx, string(down)); err != nil {
		t.Fatalf("down: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE stone_size_matrix SET rate_basis = 'g'"); err == nil {
		t.Error("basis g accepted after rollback")
	}
}
