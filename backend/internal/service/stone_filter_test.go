package service

import (
	"context"
	"errors"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

func listCodes(t *testing.T, s *Service, f DesignFilter) string {
	t.Helper()
	if f.Limit == 0 {
		f.Limit = 200
	}
	items, err := s.ListDesigns(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	return codes(items)
}

// stoneFixture: LR100 is the CLAUDE.md fixture (volume 200, seats 24 x 1.10, 12 x 1.20, 1 x 2.50 locked
// 2HD); LR101 has a flexible 3.00 mm seat that no code has a matrix row for.
func stoneFixture(t *testing.T, s *Service) (fixture, gap VariationDetail) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.CreateStoneCode(ctx, "3HD"); err != nil {
		t.Fatal(err)
	}
	seedFixtureMatrix(t, s)
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: d("1.10"), StoneCode: "3HD", CaratsPerStone: d("0.0090")},
		{SizeMM: d("1.20"), StoneCode: "3HD", CaratsPerStone: d("0.0110")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	var err error
	des := newDesign(t, s, "LR100")
	if fixture, err = s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetSeats(ctx, fixture.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	des2 := newDesign(t, s, "LR101")
	if gap, err = s.CreateVariation(ctx, des2.ID, BaseInput{VolumeMM3: dp("200")}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SetSeats(ctx, gap.ID, []SeatInput{{SizeMM: d("3.00"), StoneCount: 2}}); err != nil {
		t.Fatal(err)
	}
	return fixture, gap
}

func TestStonePercentFixtures(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	stoneFixture(t, s)

	// hand-computed: carats, stone g, gross, stone % (of gross) for volume 200 in 18KR (net 3.050)
	tests := []struct{ code, carats, stoneG, gross, pct string }{
		{"2HD", "0.3470", "0.069", "3.119", "2.23"},
		{"1HD", "0.2570", "0.051", "3.101", "1.66"},
		{"3HD", "0.4130", "0.083", "3.133", "2.64"},
	}
	for _, tc := range tests {
		t.Run(tc.code, func(t *testing.T) {
			items, err := s.ListDesigns(ctx, DesignFilter{Query: "LR100", MetalCode: "18KR", StoneCode: tc.code})
			if err != nil || len(items) != 1 {
				t.Fatalf("items=%d err=%v", len(items), err)
			}
			it := items[0]
			got := []string{it.StoneCarats.StringFixed(4), it.StoneG.StringFixed(3), it.GrossG.StringFixed(3), it.StonePct.StringFixed(2)}
			want := []string{tc.carats, tc.stoneG, tc.gross, tc.pct}
			for i := range got {
				if got[i] != want[i] {
					t.Errorf("figure %d: got %v want %v", i, got, want)
					break
				}
			}
			if it.StoneStatus != "ok" || it.NetG.StringFixed(3) != "3.050" || it.StoneCode != tc.code {
				t.Errorf("status=%q net=%s code=%s", it.StoneStatus, it.NetG, it.StoneCode)
			}
		})
	}

	t.Run("filters and boundaries", func(t *testing.T) {
		cases := []struct {
			name string
			f    DesignFilter
			want string
		}{
			{"inclusive min", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MinStonePct: dp("2.23")}, "LR100"},
			{"inclusive max", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MaxStonePct: dp("2.23")}, "LR100"},
			{"just above", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MinStonePct: dp("2.24")}, ""},
			{"just below", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MaxStonePct: dp("2.22")}, ""},
			{"the code changes the answer", DesignFilter{MetalCode: "18KR", StoneCode: "3HD", MinStonePct: dp("2.5"), MaxStonePct: dp("3")}, "LR100"},
			{"1HD is below 2 percent", DesignFilter{MetalCode: "18KR", StoneCode: "1HD", MaxStonePct: dp("2")}, "LR100"},
			{"baseline code by default", DesignFilter{MetalCode: "18KR", MinStonePct: dp("2.23"), MaxStonePct: dp("2.23")}, "LR100"},
			{"gross range uses the chosen code", DesignFilter{MetalCode: "18KR", StoneCode: "3HD", MinGrossWt: dp("3.133"), MaxGrossWt: dp("3.133")}, "LR100"},
			{"gross range, other code misses", DesignFilter{MetalCode: "18KR", StoneCode: "1HD", MinGrossWt: dp("3.133")}, ""},
			{"net range unaffected by stone", DesignFilter{MetalCode: "18KR", MinNetWt: dp("3.05"), MaxNetWt: dp("3.05")}, "LR100,LR101"},
			{"22KY net", DesignFilter{MetalCode: "22KY", MinNetWt: dp("3.55")}, "LR100,LR101"},
			// a missing matrix row is never 0: the gap design is excluded by any stone-% filter
			{"gap excluded from max 100", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MaxStonePct: dp("100")}, "LR100"},
			{"gap excluded from min 0", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MinStonePct: dp("0")}, "LR100"},
			{"gap kept without a stone filter", DesignFilter{MetalCode: "18KR", StoneCode: "2HD"}, "LR100,LR101"},
		}
		for _, tc := range cases {
			if got := listCodes(t, s, tc.f); got != tc.want {
				t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
			}
		}
	})

	t.Run("gap is flagged, not zero", func(t *testing.T) {
		items, err := s.ListDesigns(ctx, DesignFilter{Query: "LR101", MetalCode: "18KR", StoneCode: "2HD"})
		if err != nil || len(items) != 1 {
			t.Fatalf("items=%d err=%v", len(items), err)
		}
		it := items[0]
		if it.StoneStatus != "no_data" || it.StoneNote != "no data for 2HD" || it.StonePct != nil || it.StoneG != nil || it.GrossG != nil {
			t.Errorf("%+v", it)
		}
		if it.NetG == nil || it.NetG.StringFixed(3) != "3.050" {
			t.Errorf("net should still show: %v", it.NetG)
		}
		if it.GrossMin != nil {
			t.Errorf("gross range must be absent when stone data is missing, got %v", it.GrossMin)
		}
	})

	t.Run("sorting", func(t *testing.T) {
		// 2HD: LR100 is 2.23 %, LR101 has no figure and goes last in either direction
		for _, sort := range []string{"stone_pct_asc", "stone_pct_desc"} {
			if got := listCodes(t, s, DesignFilter{MetalCode: "18KR", StoneCode: "2HD", Sort: sort}); got != "LR100,LR101" {
				t.Errorf("%s: %q", sort, got)
			}
		}
		if got := listCodes(t, s, DesignFilter{MetalCode: "22KY", Sort: "net_desc"}); got != "LR100,LR101" {
			t.Errorf("net_desc tie falls back to code: %q", got)
		}
	})

	t.Run("validation", func(t *testing.T) {
		bad := []DesignFilter{
			{MetalCode: "18KR", StoneCode: "9ZZ"},
			{MetalCode: "18KR", MinStonePct: dp("-1")},
			{MetalCode: "18KR", MaxStonePct: dp("100.01")},
			{MetalCode: "18KR", MinStonePct: dp("1.234")},
			{MinStonePct: dp("1")},
			{Sort: "stone_pct_asc"},
			{MetalCode: "18KR", Sort: "price"},
		}
		for i, f := range bad {
			if _, err := s.ListDesigns(ctx, f); !errors.Is(err, ErrInvalid) {
				t.Errorf("case %d: want ErrInvalid, got %v", i, err)
			}
		}
	})
}

func TestStonePercentIgnoresDeletedVariations(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	fix, _ := stoneFixture(t, s)
	f := DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MinStonePct: dp("2")}
	if got := listCodes(t, s, f); got != "LR100" {
		t.Fatalf("before delete: %q", got)
	}
	if err := s.DeleteVariation(ctx, fix.ID); err != nil {
		t.Fatal(err)
	}
	if got := listCodes(t, s, f); got != "" {
		t.Fatalf("a soft-deleted variation must not match, got %q", got)
	}
}

// TestStoneFiguresMatchSimulateBOM proves the on-the-fly SQL gives exactly what simulate-bom gives, on
// generated variations (random volumes and reference weights, 0 to 5 seats, locked seats, matrix gaps in
// 1HD) for every stone code and metal.
func TestStoneFiguresMatchSimulateBOM(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	if _, err := s.CreateStoneCode(ctx, "3HD"); err != nil {
		t.Fatal(err)
	}
	seedScale(t, s, 100, 0.23)
	// make 1HD gappy for most sizes (locked seats keep their 2HD rows, so the delete cannot unlock them)
	if _, err := s.Pool.Exec(ctx, `DELETE FROM stone_size_matrix WHERE size_mm BETWEEN 1.5 AND 3.5
		AND stone_code_id = (SELECT id FROM stone_codes WHERE code = '1HD')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Q.EnqueueRecalcJob(ctx, "metal_sg"); err != nil {
		t.Fatal(err)
	}
	if did, err := ProcessNextRecalcJob(ctx, s.Pool); err != nil || !did {
		t.Fatalf("recalc did=%v err=%v", did, err)
	}
	checked, noData := 0, 0
	for _, metal := range []string{"18KR", "22KY"} {
		for _, code := range []string{"1HD", "2HD", "3HD"} {
			for off := int32(0); ; off += 200 {
				items, err := s.ListDesigns(ctx, DesignFilter{MetalCode: metal, StoneCode: code, Limit: 200, Offset: off})
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range items {
					if it.ShownVariationID == nil {
						continue
					}
					bom, err := s.SimulateBOM(ctx, *it.ShownVariationID, code)
					var mme *calc.MissingMatrixError
					if errors.As(err, &mme) {
						noData++
						if it.StoneStatus != "no_data" || it.StonePct != nil || it.GrossG != nil {
							t.Fatalf("%s %s %s: simulate-bom has gaps, list says %+v", metal, code, it.ShownVariationCode, it)
						}
						// a design is only flagged when none of its variations has data
						vs, _ := s.Q.ListVariationsByDesign(ctx, it.ID)
						for _, v := range vs {
							if _, err := s.SimulateBOM(ctx, v.ID, code); err == nil {
								t.Fatalf("%s %s: design %s flagged no data but %s resolves", metal, code, it.Code, v.Code)
							}
						}
						continue
					}
					if err != nil {
						t.Fatal(err)
					}
					var net, gross decimal.Decimal
					for _, m := range bom.Metals {
						if m.MetalCode == metal {
							net, gross = m.NetG, m.GrossG
						}
					}
					var pct decimal.Decimal
					if gross.IsPositive() {
						pct = calc.StoneWeight(bom.TotalCarats).Div(gross).Mul(decimal.NewFromInt(100)).Round(2)
					}
					if it.StoneStatus != "ok" || !it.NetG.Equal(net) || !it.StoneCarats.Equal(bom.TotalCarats) ||
						!it.StoneG.Equal(bom.StoneWeightG) || !it.GrossG.Equal(gross) || !it.StonePct.Equal(pct) {
						t.Fatalf("%s %s %s: list net=%v ct=%v g=%v gross=%v pct=%v; simulate-bom net=%s ct=%s g=%s gross=%s pct=%s",
							metal, code, it.ShownVariationCode, it.NetG, it.StoneCarats, it.StoneG, it.GrossG, it.StonePct,
							net, bom.TotalCarats, bom.StoneWeightG, gross, pct)
					}
					checked++
				}
				if len(items) < 200 {
					break
				}
			}
		}
	}
	if checked < 300 || noData == 0 {
		t.Fatalf("seed too thin to prove anything: checked=%d noData=%d", checked, noData)
	}
	t.Logf("checked %d card figures against simulate-bom, %d flagged no data", checked, noData)
}
