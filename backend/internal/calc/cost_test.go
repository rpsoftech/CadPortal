package calc

import "testing"

func TestCompute(t *testing.T) {
	// Fixture BOM (24x1.10, 12x1.20, 1x2.50 locked) on 18KR net 3.050 g.
	// Wastage and every rate below are test-only numbers; real ones come from Admin.
	lines := func(r1, r2, r3 string) []StoneCostLine {
		return []StoneCostLine{
			{LineCarats: d("0.1680"), RatePerCt: d(r1)},
			{LineCarats: d("0.1140"), RatePerCt: d(r2)},
			{LineCarats: d("0.0650"), RatePerCt: d(r3)},
		}
	}
	tests := []struct {
		name string
		in   CostInput
		want Cost
	}{
		{
			name: "flat stone rate",
			in: CostInput{NetG: d("3.050"), WastagePct: d("10"), GoldRatePerG: d("6000"),
				StoneLines: lines("50000", "50000", "50000"), SettingPcs: 37, SettingRatePerPc: d("25")},
			want: Cost{GoldG: d("3.355"), GoldCost: d("20130.00"), StoneCarats: d("0.3470"),
				StoneCost: d("17350.00"), SettingPcs: 37, SettingCost: d("925.00"), Total: d("38405.00")},
		},
		{
			name: "per-line stone rates",
			in: CostInput{NetG: d("3.050"), WastagePct: d("10"), GoldRatePerG: d("6000"),
				StoneLines: lines("40000", "50000", "100000"), SettingPcs: 37, SettingRatePerPc: d("25")},
			// 0.1680x40000 + 0.1140x50000 + 0.0650x100000 = 6720 + 5700 + 6500
			want: Cost{GoldG: d("3.355"), GoldCost: d("20130.00"), StoneCarats: d("0.3470"),
				StoneCost: d("18920.00"), SettingPcs: 37, SettingCost: d("925.00"), Total: d("39975.00")},
		},
		{
			name: "no stones, zero wastage",
			in:   CostInput{NetG: d("3.116"), WastagePct: d("0"), GoldRatePerG: d("5000")},
			want: Cost{GoldG: d("3.116"), GoldCost: d("15580.00"), StoneCarats: d("0"),
				StoneCost: d("0"), SettingPcs: 0, SettingCost: d("0"), Total: d("15580.00")},
		},
		{
			name: "rounding to 2dp per component",
			in: CostInput{NetG: d("3.050"), WastagePct: d("7.5"), GoldRatePerG: d("6123.457"),
				SettingPcs: 3, SettingRatePerPc: d("12.345")},
			// gold 3.050 x 1.075 = 3.27875 g; x 6123.457 = 20077.28 (checked by hand)
			want: Cost{GoldG: d("3.279"), GoldCost: d("20077.28"), StoneCarats: d("0"),
				StoneCost: d("0"), SettingPcs: 3, SettingCost: d("37.04"), Total: d("20114.32")},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(tc.in)
			for field, pair := range map[string][2]string{
				"GoldG":       {got.GoldG.String(), tc.want.GoldG.String()},
				"GoldCost":    {got.GoldCost.StringFixed(2), tc.want.GoldCost.StringFixed(2)},
				"StoneCarats": {got.StoneCarats.StringFixed(4), tc.want.StoneCarats.StringFixed(4)},
				"StoneCost":   {got.StoneCost.StringFixed(2), tc.want.StoneCost.StringFixed(2)},
				"SettingCost": {got.SettingCost.StringFixed(2), tc.want.SettingCost.StringFixed(2)},
				"Total":       {got.Total.StringFixed(2), tc.want.Total.StringFixed(2)},
			} {
				if pair[0] != pair[1] {
					t.Errorf("%s = %s want %s", field, pair[0], pair[1])
				}
			}
			if got.SettingPcs != tc.want.SettingPcs {
				t.Errorf("SettingPcs = %d want %d", got.SettingPcs, tc.want.SettingPcs)
			}
		})
	}
}

// Stone cost on all three bases, with the fixture seats (24 x 1.10, 12 x 1.20, 1 x 2.50 locked, all 2HD):
// 0.1680, 0.1140 and 0.0650 ct, which are 0.0336, 0.0228 and 0.0130 g (carats x 0.200).
func TestComputeStoneBases(t *testing.T) {
	fixture := func(basis string, rate string) []StoneCostLine {
		mk := func(ct string, pcs int) StoneCostLine {
			l := StoneCostLine{LineCarats: d(ct), ExactCarats: d(ct), Pcs: pcs, Basis: basis}
			switch basis {
			case "pc":
				l.RatePerPc = d(rate)
			case "ct":
				l.RatePerCt = d(rate)
			case "g":
				l.RatePerG = d(rate)
			}
			return l
		}
		return []StoneCostLine{mk("0.1680", 24), mk("0.1140", 12), mk("0.0650", 1)}
	}
	tests := []struct {
		name      string
		lines     []StoneCostLine
		wantStone string
		wantQty   []string
		wantUnit  string
	}{
		{"per piece", fixture("pc", "100"), "3700.00", []string{"24", "12", "1"}, "pcs"},                          // 37 x 100
		{"per carat", fixture("ct", "50000"), "17350.00", []string{"0.1680", "0.1140", "0.0650"}, "ct"},           // 0.3470 x 50000
		{"per gram", fixture("g", "5000"), "347.00", []string{"0.03360", "0.02280", "0.01300"}, "g"},              // 0.0694 g x 5000
		{"per gram, odd price", fixture("g", "1234.56"), "85.68", []string{"0.03360", "0.02280", "0.01300"}, "g"}, // 0.0694 x 1234.56 = 85.678464, rounded once
		{"no stones", nil, "0.00", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Compute(CostInput{NetG: d("3.050"), WastagePct: d("10"), GoldRatePerG: d("6000"), StoneLines: tc.lines})
			if got.StoneCost.StringFixed(2) != tc.wantStone {
				t.Errorf("StoneCost = %s, want %s", got.StoneCost.StringFixed(2), tc.wantStone)
			}
			if len(got.StonePriced) != len(tc.wantQty) {
				t.Fatalf("priced lines = %+v", got.StonePriced)
			}
			for i, q := range tc.wantQty {
				if !got.StonePriced[i].Qty.Equal(d(q)) || got.StonePriced[i].Unit() != tc.wantUnit {
					t.Errorf("line %d priced %s %s, want %s %s", i, got.StonePriced[i].Qty, got.StonePriced[i].Unit(), q, tc.wantUnit)
				}
			}
		})
	}
}

// A per-gram line is priced from the unrounded carats, not from the 4 dp figure shown on the BOM.
func TestPerGramUsesUnroundedCarats(t *testing.T) {
	// 3 stones x 0.01234567 ct = 0.03703701 ct = 0.007407402 g; shown as 0.0370 ct (0.0074 g).
	l := StoneCostLine{LineCarats: d("0.0370"), ExactCarats: d("0.03703701"), Pcs: 3, Basis: "g", RatePerG: d("1000")}
	if got := Compute(CostInput{StoneLines: []StoneCostLine{l}}).StoneCost.StringFixed(2); got != "7.41" {
		t.Errorf("stone cost = %s, want 7.41 (from unrounded carats)", got)
	}
	// Without the exact figure it falls back to the line carats.
	l.ExactCarats = d("0")
	if got := Compute(CostInput{StoneLines: []StoneCostLine{l}}).StoneCost.StringFixed(2); got != "7.40" {
		t.Errorf("fallback = %s, want 7.40", got)
	}
}
