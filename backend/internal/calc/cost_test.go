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
