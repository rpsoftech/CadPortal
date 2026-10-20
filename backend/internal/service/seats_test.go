package service

import (
	"context"
	"errors"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

// seedFixtureMatrix loads the spec's test-only 2HD pointers (baseline = 2HD)
// and a partial 1HD set so the planner can switch codes.
func seedFixtureMatrix(t *testing.T, s *Service) {
	t.Helper()
	err := s.UpdateStoneMatrix(context.Background(), []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0070")},
		{SizeMM: d("1.20"), StoneCode: "2HD", CaratsPerStone: d("0.0095")},
		{SizeMM: d("2.50"), StoneCode: "2HD", CaratsPerStone: d("0.0650")},
		{SizeMM: d("1.10"), StoneCode: "1HD", CaratsPerStone: d("0.0050")},
		{SizeMM: d("1.20"), StoneCode: "1HD", CaratsPerStone: d("0.0060")},
	}, "2HD")
	if err != nil {
		t.Fatal(err)
	}
}

func fixtureSeats() []SeatInput {
	return []SeatInput{
		{SizeMM: d("1.10"), StoneCount: 24},
		{SizeMM: d("1.20"), StoneCount: 12},
		{SizeMM: d("2.50"), StoneCount: 1, LockedStoneCode: "2HD"},
	}
}

func TestSetSeatsRecalcsSynchronously(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR020")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	out, err := s.SetSeats(ctx, v.ID, fixtureSeats())
	if err != nil {
		t.Fatal(err)
	}
	if out.BaselineStoneCarats.StringFixed(4) != "0.3470" {
		t.Errorf("baseline carats = %s", out.BaselineStoneCarats.StringFixed(4))
	}
	if g := weightsByMetal(out)["18KR"].GrossWeightG.Decimal.StringFixed(3); g != "3.119" {
		t.Errorf("18KR gross = %s want 3.119", g)
	}

	// Replacing with fewer seats rebuilds again.
	out, err = s.SetSeats(ctx, v.ID, fixtureSeats()[:1])
	if err != nil {
		t.Fatal(err)
	}
	if out.BaselineStoneCarats.StringFixed(4) != "0.1680" {
		t.Errorf("baseline carats = %s", out.BaselineStoneCarats.StringFixed(4))
	}
	seats, _ := s.ListSeats(ctx, v.ID)
	if len(seats) != 1 {
		t.Errorf("seats = %d", len(seats))
	}
}

func TestSetSeatsValidation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR021")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	lockNoRow := []SeatInput{
		{SizeMM: d("1.10"), StoneCount: 1, LockedStoneCode: "2HD"},
		{SizeMM: d("3.00"), StoneCount: 1, LockedStoneCode: "2HD"},
	}
	cases := map[string][]SeatInput{
		"zero count":        {{SizeMM: d("1.10"), StoneCount: 0}},
		"zero size":         {{SizeMM: d("0"), StoneCount: 1}},
		"unknown lock code": {{SizeMM: d("1.10"), StoneCount: 1, LockedStoneCode: "9ZZ"}},
		"lock without row":  lockNoRow,
	}
	for name, seats := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.SetSeats(ctx, v.ID, seats); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
	// A failed replace must leave previous seats intact.
	if _, err := s.SetSeats(ctx, v.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	_, _ = s.SetSeats(ctx, v.ID, lockNoRow)
	if seats, _ := s.ListSeats(ctx, v.ID); len(seats) != 3 {
		t.Errorf("seats after failed replace = %d, want 3", len(seats))
	}
	if _, err := s.SetSeats(ctx, 99999, nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing variation: %v", err)
	}
}

func TestSimulateBOM(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR022")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if _, err := s.SetSeats(ctx, v.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}

	res, err := s.SimulateBOM(ctx, v.ID, "2HD")
	if err != nil {
		t.Fatal(err)
	}
	if res.TotalPcs != 37 || res.TotalCarats.StringFixed(4) != "0.3470" || res.StoneWeightG.StringFixed(3) != "0.069" {
		t.Errorf("totals: %d pcs %s ct %s g", res.TotalPcs, res.TotalCarats, res.StoneWeightG)
	}
	for i, w := range []string{"0.1680", "0.1140", "0.0650"} {
		if got := res.Lines[i].LineCarats.StringFixed(4); got != w {
			t.Errorf("line %d = %s want %s", i, got, w)
		}
	}
	for _, m := range res.Metals {
		if m.MetalCode == "18KR" && m.GrossG.StringFixed(3) != "3.119" {
			t.Errorf("18KR gross = %s", m.GrossG)
		}
	}

	// 1HD for flexible seats; the locked 2.50 seat keeps 2HD.
	res, err = s.SimulateBOM(ctx, v.ID, "1HD")
	if err != nil {
		t.Fatal(err)
	}
	// 24*0.0050 + 12*0.0060 + 1*0.0650 = 0.2570
	if res.TotalCarats.StringFixed(4) != "0.2570" {
		t.Errorf("1HD total = %s", res.TotalCarats)
	}
	if !res.Lines[2].Locked || res.Lines[2].StoneCode != "2HD" {
		t.Errorf("locked seat changed: %+v", res.Lines[2])
	}
}

func TestSimulateBOMMissingRows(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR023")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	seats := append(fixtureSeats(), SeatInput{SizeMM: d("1.50"), StoneCount: 2})
	// 1.50 mm has no row for the baseline 2HD: the seat saves but gross stays NULL.
	out, err := s.SetSeats(ctx, v.ID, seats)
	if err != nil {
		t.Fatal(err)
	}
	if out.Weights[0].GrossWeightG.Valid {
		t.Error("gross must be NULL while the matrix has a gap")
	}

	_, err = s.SimulateBOM(ctx, v.ID, "1HD")
	var mme *calc.MissingMatrixError
	if !errors.As(err, &mme) {
		t.Fatalf("want MissingMatrixError, got %v", err)
	}
	if len(mme.Pairs) != 1 || mme.Pairs[0] != (calc.MissingPair{SizeMM: "1.50", Code: "1HD"}) {
		t.Errorf("pairs = %+v", mme.Pairs)
	}
	if _, err := s.SimulateBOM(ctx, v.ID, "9ZZ"); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown code: %v", err)
	}
	if _, err := s.SimulateBOM(ctx, v.ID, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty code: %v", err)
	}
}

func TestCloneSeats(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR024")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	if _, err := s.SetSeats(ctx, v1.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}

	out, err := s.CloneSeats(ctx, v2.ID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	seats, _ := s.ListSeats(ctx, v2.ID)
	if len(seats) != 3 || !seats[2].LockedCode.Valid || seats[2].LockedCode.String != "2HD" {
		t.Errorf("cloned seats = %+v", seats)
	}
	if out.BaselineStoneCarats.StringFixed(4) != "0.3470" {
		t.Errorf("clone carats = %s", out.BaselineStoneCarats)
	}
	// 300 mm3 in 18KR: net 4.575 + 0.0694 stone = 4.6444 -> 4.644
	if g := weightsByMetal(out)["18KR"].GrossWeightG.Decimal.StringFixed(3); g != "4.644" {
		t.Errorf("clone 18KR gross = %s", g)
	}
	if _, err := s.CloneSeats(ctx, v2.ID, v2.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("self clone: %v", err)
	}
	if _, err := s.CloneSeats(ctx, v2.ID, 99999); !errors.Is(err, ErrInvalid) {
		t.Errorf("missing source: %v", err)
	}
}
