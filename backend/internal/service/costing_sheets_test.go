package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/shopspring/decimal"
)

// same compares values by their printed form: decimals with equal value can differ in representation.
func same(a, b any) bool { return fmt.Sprintf("%+v", a) == fmt.Sprintf("%+v", b) }

// sheetEnv builds two designs priced with the CLAUDE.md fixture: LR070-V1 is the 200 mm3 / 37 pcs
// fixture (18KR total 38405.00 at gold 6000, labour 25, 2HD at 50000/ct, wastage 10 %); LR071-V1 is a
// smaller piece with one flexible seat. The wastage and prices are test-only numbers.
type sheetEnv struct {
	s          *Service
	d1, d2     int64
	v1, v2     int64
	baseReq    SheetRequest
	ratePerCt  decimal.NullDecimal
	updateRows func(t *testing.T, rate string)
}

func newSheetEnv(t *testing.T) *sheetEnv {
	t.Helper()
	s := newTestService(t)
	ctx := context.Background()
	v1 := fixtureVariation(t, s, "LR070")
	setWastage(t, s, "18KR", "10")
	des2 := newDesign(t, s, "LR071")
	v2, err := s.CreateVariation(ctx, des2.ID, BaseInput{VolumeMM3: dp("100")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSeats(ctx, v2.ID, []SeatInput{{SizeMM: d("1.10"), StoneCount: 10}}); err != nil {
		t.Fatal(err)
	}
	v1row, _ := s.Q.GetVariation(ctx, v1)
	e := &sheetEnv{s: s, d1: v1row.DesignID, d2: des2.ID, v1: v1, v2: v2.ID}
	e.updateRows = func(t *testing.T, rate string) {
		t.Helper()
		r := decimal.NullDecimal{Decimal: d(rate), Valid: true}
		if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
			{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070"), RatePerCt: r},
			{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RatePerCt: r},
			{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650"), RatePerCt: r},
		}, ""); err != nil {
			t.Fatal(err)
		}
	}
	e.updateRows(t, "50000")
	e.baseReq = SheetRequest{MetalCode: "18KR", StoneCode: "2HD", GoldRatePerG: d("6000"), SettingRatePerPc: d("25")}
	return e
}

func (e *sheetEnv) req(f func(*SheetRequest)) SheetRequest {
	r := e.baseReq
	f(&r)
	return r
}

func TestCostSelectionTwoDesignsMatchEachAlone(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()

	both, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d2, e.d1} }))
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Rows) != 2 || len(both.Problems) != 0 {
		t.Fatalf("rows=%d problems=%v", len(both.Rows), both.Problems)
	}
	// Sorted by design code, regardless of the order of the request.
	if both.Rows[0].VariationCode != "LR070-V1" || both.Rows[1].VariationCode != "LR071-V1" {
		t.Fatalf("order: %s, %s", both.Rows[0].VariationCode, both.Rows[1].VariationCode)
	}

	// The spec fixture: net 3.050, gold 3.355 g, 20130.00 + 17350.00 + 925.00 = 38405.00.
	r := both.Rows[0]
	for k, p := range map[string][2]string{
		"net": {r.NetG.StringFixed(3), "3.050"}, "gross": {r.GrossG.StringFixed(3), "3.119"},
		"gold g": {r.GoldG.StringFixed(3), "3.355"}, "gold": {r.GoldCost.StringFixed(2), "20130.00"},
		"stone ct": {r.StoneCt.StringFixed(4), "0.3470"}, "stone g": {r.StoneG.StringFixed(4), "0.0694"},
		"stone": {r.StoneCost.StringFixed(2), "17350.00"}, "setting": {r.SettingCost.StringFixed(2), "925.00"},
		"total": {r.Total.StringFixed(2), "38405.00"},
	} {
		if p[0] != p[1] {
			t.Errorf("%s = %s want %s", k, p[0], p[1])
		}
	}
	if r.SettingPcs != 37 {
		t.Errorf("pcs = %d", r.SettingPcs)
	}

	// Each row equals costing that design alone, and equals the single-design costing code path.
	for i, vid := range []int64{e.v1, e.v2} {
		alone, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.VariationIDs = []int64{vid} }))
		if err != nil {
			t.Fatal(err)
		}
		if !same(alone.Rows[0], both.Rows[i]) {
			t.Errorf("row %d differs alone:\n%+v\n%+v", i, alone.Rows[0], both.Rows[i])
		}
		old, err := e.s.CostingRows(ctx, CostingRequest{Items: []CostingItem{{VariationID: vid, MetalCode: "18KR", StoneCode: "2HD"}},
			GoldRatePerG: d("6000"), SettingRatePerPc: d("25")})
		if err != nil {
			t.Fatal(err)
		}
		if !old[0].Total.Equal(both.Rows[i].Total) || !old[0].StoneCost.Equal(both.Rows[i].StoneCost) || !old[0].GoldCost.Equal(both.Rows[i].GoldCost) {
			t.Errorf("row %d differs from CostingRows: %+v vs %+v", i, old[0].Cost, both.Rows[i])
		}
	}

	// Totals are the sum of the rows as printed.
	sum := both.Rows[0].Total.Add(both.Rows[1].Total)
	if !both.Totals.Total.Equal(sum) || both.Totals.Variations != 2 || both.Totals.Designs != 2 {
		t.Errorf("totals = %+v want total %s", both.Totals, sum)
	}
	if both.Totals.SettingPcs != 47 {
		t.Errorf("total pcs = %d", both.Totals.SettingPcs)
	}

	// A design plus one of its own variations is one row, not two.
	dup, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1}; r.VariationIDs = []int64{e.v1} }))
	if err != nil || len(dup.Rows) != 1 {
		t.Fatalf("dup: %v rows=%d", err, len(dup.Rows))
	}
}

func TestCostSelectionMissingDataIsGroupedAndNeverZero(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()

	// 22KY has no wastage % set: every variation is incomplete, and the metal is named.
	res, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.MetalCode = "22KY"; r.DesignIDs = []int64{e.d1, e.d2} }))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || len(res.Problems) != 2 {
		t.Fatalf("rows=%d problems=%+v", len(res.Rows), res.Problems)
	}
	if res.Problems[0].DesignCode != "LR070" || res.Problems[0].Variations[0].MetalsNoWaste[0] != "22KY" {
		t.Errorf("problems = %+v", res.Problems)
	}
	err = res.Costable(false)
	var ice *IncompleteCostingError
	if !errors.As(err, &ice) || len(ice.Wastage) != 1 || ice.Wastage[0] != "22KY" {
		t.Fatalf("want IncompleteCostingError naming 22KY, got %v", err)
	}
	if err := res.Costable(true); err == nil {
		t.Error("nothing costed: skip_incomplete must still be an error")
	}

	// A price missing for (1.20, 2HD) only affects LR070, which uses that size; LR071 is complete.
	if err := e.s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095")},
	}, ""); err != nil {
		t.Fatal(err)
	}
	res, err = e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1, e.d2} }))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0].VariationCode != "LR071-V1" {
		t.Fatalf("rows = %+v", res.Rows)
	}
	if len(res.Problems) != 1 || res.Problems[0].DesignCode != "LR070" {
		t.Fatalf("problems = %+v", res.Problems)
	}
	p := res.Problems[0].Variations[0]
	if len(p.StoneRates) != 1 || p.StoneRates[0].SizeMM != "1.20" || p.StoneRates[0].Code != "2HD" {
		t.Errorf("stone rates missing = %+v", p.StoneRates)
	}
	// All-or-nothing: the error costs nothing and lists the pair; skip_incomplete costs the other.
	err = res.Costable(false)
	if !errors.As(err, &ice) || len(ice.StoneRates) != 1 || len(ice.Problems) != 1 {
		t.Fatalf("all-or-nothing: %v", err)
	}
	if err := res.Costable(true); err != nil {
		t.Errorf("skip_incomplete: %v", err)
	}
	if res.IncompleteVariations() != 1 || len(res.ResolvedVariationIDs) != 2 {
		t.Errorf("incomplete=%d resolved=%v", res.IncompleteVariations(), res.ResolvedVariationIDs)
	}
	// The costed total only holds the complete row.
	if !res.Totals.Total.Equal(res.Rows[0].Total) || res.Totals.Variations != 1 {
		t.Errorf("totals include more than the costed row: %+v", res.Totals)
	}
}

func TestCostSelectionMatrixGapAndOddSelections(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()

	// Flexible seat at 2.50 mm: 2HD has a row (baseline), 1HD does not.
	des := newDesign(t, e.s, "LR072")
	v, err := e.s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("50")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SetSeats(ctx, v.ID, []SeatInput{{SizeMM: d("2.50"), StoneCount: 1}}); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.StoneCode = "1HD"; r.VariationIDs = []int64{v.ID} }))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 || len(res.Problems) != 1 || len(res.Problems[0].Variations[0].MatrixMissing) != 1 {
		t.Fatalf("matrix gap: %+v", res)
	}
	if m := res.Problems[0].Variations[0].MatrixMissing[0]; m.SizeMM != "2.50" || m.Code != "1HD" {
		t.Errorf("pair = %+v", m)
	}

	// A design without variations and a variation that does not exist are problems, not silent drops.
	empty := newDesign(t, e.s, "LR073")
	res, err = e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1, empty.ID}; r.VariationIDs = []int64{999999} }))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || len(res.Problems) != 2 {
		t.Fatalf("rows=%d problems=%+v", len(res.Rows), res.Problems)
	}

	// Unknown design is a bad request; so are bad parameters.
	bad := map[string]SheetRequest{
		"unknown design":  e.req(func(r *SheetRequest) { r.DesignIDs = []int64{999999} }),
		"nothing chosen":  e.req(func(r *SheetRequest) {}),
		"no metal":        e.req(func(r *SheetRequest) { r.MetalCode = ""; r.DesignIDs = []int64{e.d1} }),
		"unknown metal":   e.req(func(r *SheetRequest) { r.MetalCode = "99K"; r.DesignIDs = []int64{e.d1} }),
		"unknown stone":   e.req(func(r *SheetRequest) { r.StoneCode = "9ZZ"; r.DesignIDs = []int64{e.d1} }),
		"zero gold":       e.req(func(r *SheetRequest) { r.GoldRatePerG = d("0"); r.DesignIDs = []int64{e.d1} }),
		"negative labour": e.req(func(r *SheetRequest) { r.SettingRatePerPc = d("-1"); r.DesignIDs = []int64{e.d1} }),
	}
	for name, req := range bad {
		if _, err := e.s.CostSelection(ctx, req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want ErrInvalid, got %v", name, err)
		}
	}
}

func TestCostSelectionSizeCap(t *testing.T) {
	e := newSheetEnv(t)
	ids := make([]int64, MaxSheetVariations+1)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	_, err := e.s.CostSelection(context.Background(), e.req(func(r *SheetRequest) { r.VariationIDs = ids }))
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("want ErrInvalid over the cap, got %v", err)
	}
	// Exactly at the cap is accepted (ids that do not exist are problems, not a size error).
	res, err := e.s.CostSelection(context.Background(), e.req(func(r *SheetRequest) { r.VariationIDs = ids[:MaxSheetVariations] }))
	if err != nil {
		t.Fatalf("at the cap: %v", err)
	}
	if res.IncompleteVariations() < MaxSheetVariations-2 {
		t.Errorf("incomplete = %d", res.IncompleteVariations())
	}
}

func TestSavedSheetIsASnapshot(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	req := e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1, e.d2} })
	res, err := e.s.CostSelection(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	const csv = "variation,total\nLR070-V1,38405.00\n"
	saved, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: "  Autumn range ", Request: req, Result: res, CSV: csv})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Name != "Autumn range" || saved.VariationCount != 2 || saved.DesignCount != 2 || !saved.Total.Equal(res.Totals.Total) {
		t.Fatalf("saved = %+v", saved.SheetSummary)
	}

	// Everything the sheet depends on changes afterwards.
	setWastage(t, e.s, "18KR", "25")
	e.updateRows(t, "90000")
	id, sg := metalID(t, e.s, "18KR")
	if _, err := e.s.UpdateMetal(ctx, id, MetalUpdate{SpecificGravity: sg.Add(d("1")), WastagePct: decimal.NullDecimal{Decimal: d("25"), Valid: true}, Active: true}); err != nil {
		t.Fatal(err)
	}
	fresh, err := e.s.CostSelection(ctx, e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1, e.d2}; r.GoldRatePerG = d("9000") }))
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Totals.Total.Equal(res.Totals.Total) {
		t.Fatal("test is not changing anything")
	}

	got, gotCSV, err := e.s.GetCostingSheet(ctx, saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotCSV != csv {
		t.Errorf("csv changed: %q", gotCSV)
	}
	if len(got.Rows) != 2 || !same(got.Rows, res.Rows) {
		t.Errorf("rows changed:\n%+v\n%+v", got.Rows, res.Rows)
	}
	if !same(got.Totals, res.Totals) || !got.Total.Equal(res.Totals.Total) {
		t.Errorf("totals changed: %+v", got.Totals)
	}
	if len(got.Rates.Metals) != 1 || !got.Rates.Metals[0].WastagePct.Equal(d("10")) {
		t.Errorf("wastage snapshot = %+v", got.Rates.Metals)
	}
	if len(got.Rates.StoneRates) != 3 || !got.Rates.StoneRates[0].Price.Equal(d("50000")) || got.Rates.StoneRates[0].RateBasis != "ct" {
		t.Errorf("stone rate snapshot = %+v", got.Rates.StoneRates)
	}
	if !got.GoldRatePerG.Equal(d("6000")) || len(got.Inputs.ResolvedVariationIDs) != 2 {
		t.Errorf("inputs = %+v", got.Inputs)
	}

	// Re-cost uses the same variations with today's masters and the gold rate given.
	nr := RecostRequest(got, RecostOverrides{GoldRatePerG: dp("9000")})
	if nr.MetalCode != "18KR" || nr.StoneCode != "2HD" || !nr.GoldRatePerG.Equal(d("9000")) || !nr.SettingRatePerPc.Equal(d("25")) || len(nr.VariationIDs) != 2 {
		t.Fatalf("recost request = %+v", nr)
	}
	nres, err := e.s.CostSelection(ctx, nr)
	if err != nil {
		t.Fatal(err)
	}
	nsaved, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: "Autumn range (re-cost)", RecostOf: saved.ID, Request: nr, Result: nres, CSV: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if nsaved.RecostOf == nil || *nsaved.RecostOf != saved.ID {
		t.Errorf("recost_of = %v", nsaved.RecostOf)
	}
	cmp := CompareSheets(got, nsaved)
	if len(cmp.Rows) != 2 || !cmp.Difference.Equal(nsaved.Totals.Total.Sub(got.Totals.Total)) || cmp.Difference.IsZero() {
		t.Errorf("comparison = %+v", cmp)
	}
	if cmp.Rows[0].Difference == nil || !cmp.Rows[0].Difference.Equal(nsaved.Rows[0].Total.Sub(got.Rows[0].Total)) {
		t.Errorf("row difference = %+v", cmp.Rows[0])
	}
	// And the first sheet is still untouched.
	again, _, _ := e.s.GetCostingSheet(ctx, saved.ID)
	if !same(again.Totals, res.Totals) {
		t.Error("re-cost changed the original sheet")
	}
}

func TestSaveSheetRulesAndListing(t *testing.T) {
	e := newSheetEnv(t)
	ctx := context.Background()
	req := e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1} })
	res, _ := e.s.CostSelection(ctx, req)

	for _, name := range []string{"", "   ", string(make([]rune, 121))} {
		if _, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: name, Request: req, Result: res, CSV: "x"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("name %q: want ErrInvalid, got %v", name, err)
		}
	}
	// An incomplete result cannot be saved unless skip_incomplete was asked for.
	e.updateRows(t, "50000")
	if err := e.s.UpdateStoneMatrix(ctx, []MatrixRowInput{{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095")}}, ""); err != nil {
		t.Fatal(err)
	}
	both := e.req(func(r *SheetRequest) { r.DesignIDs = []int64{e.d1, e.d2} })
	mixed, _ := e.s.CostSelection(ctx, both)
	if _, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: "a", Request: both, Result: mixed, CSV: "x"}); err == nil {
		t.Error("incomplete result saved without skip_incomplete")
	}
	both.SkipIncomplete = true
	sk, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: "a", Request: both, Result: mixed, CSV: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if sk.IncompleteCount != 1 || sk.VariationCount != 1 || len(sk.Problems) != 1 {
		t.Errorf("skip sheet = %+v", sk.SheetSummary)
	}

	for _, n := range []string{"b", "c"} {
		if _, err := e.s.SaveCostingSheet(ctx, SaveSheetParams{Name: n, Request: both, Result: mixed, CSV: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := e.s.ListCostingSheets(ctx, 2, 0)
	if err != nil || total != 3 || len(list) != 2 || list[0].Name != "c" {
		t.Fatalf("list = %+v total=%d err=%v", list, total, err)
	}
	list, _, _ = e.s.ListCostingSheets(ctx, 2, 2)
	if len(list) != 1 || list[0].Name != "a" {
		t.Errorf("page 2 = %+v", list)
	}

	if err := e.s.DeleteCostingSheet(ctx, sk.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.s.GetCostingSheet(ctx, sk.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("after delete: %v", err)
	}
	if err := e.s.DeleteCostingSheet(ctx, sk.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
}
