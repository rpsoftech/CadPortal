package calc

import (
	"errors"
	"testing"

	"github.com/shopspring/decimal"
)

func d(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func TestNetFromVolume(t *testing.T) {
	tests := []struct {
		metal, sg, want string
	}{
		{"22KY", "17.75", "3.550"},
		{"18KY", "15.58", "3.116"},
		{"18KR", "15.25", "3.050"},
	}
	for _, tc := range tests {
		t.Run(tc.metal, func(t *testing.T) {
			got := RoundGrams(NetFromVolume(d("200"), d(tc.sg)))
			if got.StringFixed(3) != tc.want {
				t.Fatalf("got %s want %s", got.StringFixed(3), tc.want)
			}
		})
	}
}

func TestNetFromReference(t *testing.T) {
	// 22KY reference of 3.550 g converts to the other alloys of the 200 mm3 fixture.
	tests := []struct{ sgTarget, want string }{
		{"15.58", "3.116"},
		{"15.25", "3.050"},
		{"17.75", "3.550"},
	}
	for _, tc := range tests {
		got, err := NetFromReference(d("3.550"), d(tc.sgTarget), d("17.75"))
		if err != nil {
			t.Fatal(err)
		}
		if RoundGrams(got).StringFixed(3) != tc.want {
			t.Fatalf("sg %s: got %s want %s", tc.sgTarget, RoundGrams(got).StringFixed(3), tc.want)
		}
	}
	if _, err := NetFromReference(d("3"), d("15"), decimal.Zero); err == nil {
		t.Fatal("expected error for zero reference SG")
	}
}

func fixtureMatrix() Matrix {
	m := Matrix{}
	m.Set(d("1.10"), "2HD", d("0.0070"))
	m.Set(d("1.20"), "2HD", d("0.0095"))
	m.Set(d("2.50"), "2HD", d("0.0650"))
	return m
}

func fixtureSeats() []Seat {
	return []Seat{
		{SizeMM: d("1.10"), Count: 24},
		{SizeMM: d("1.20"), Count: 12},
		{SizeMM: d("2.50"), Count: 1, LockedCode: "2HD"},
	}
}

func TestResolveBOMFixture(t *testing.T) {
	bom, err := ResolveBOM(fixtureSeats(), "2HD", fixtureMatrix())
	if err != nil {
		t.Fatal(err)
	}
	wantLines := []string{"0.1680", "0.1140", "0.0650"}
	for i, w := range wantLines {
		if got := RoundCarats(bom.Lines[i].LineCarats).StringFixed(4); got != w {
			t.Errorf("line %d: got %s want %s", i, got, w)
		}
	}
	if !bom.Lines[2].Locked || bom.Lines[0].Locked {
		t.Error("locked flag wrong")
	}
	if bom.TotalPcs != 37 {
		t.Errorf("pcs: got %d want 37", bom.TotalPcs)
	}
	if got := RoundCarats(bom.TotalCarats).StringFixed(4); got != "0.3470" {
		t.Errorf("carats: got %s want 0.3470", got)
	}
	if got := RoundGrams(StoneWeight(bom.TotalCarats)).StringFixed(3); got != "0.069" {
		t.Errorf("stone g: got %s want 0.069", got)
	}
}

func TestGross(t *testing.T) {
	bom, _ := ResolveBOM(fixtureSeats(), "2HD", fixtureMatrix())
	tests := []struct{ metal, sg, want string }{
		{"18KR", "15.25", "3.119"},
	}
	for _, tc := range tests {
		net := NetFromVolume(d("200"), d(tc.sg))
		if got := RoundGrams(Gross(net, bom.TotalCarats)).StringFixed(3); got != tc.want {
			t.Errorf("%s gross: got %s want %s", tc.metal, got, tc.want)
		}
	}
}

func TestResolveBOMMissingRows(t *testing.T) {
	m := fixtureMatrix()
	seats := append(fixtureSeats(),
		Seat{SizeMM: d("1.50"), Count: 3},
		Seat{SizeMM: d("1.50"), Count: 2}, // duplicate gap reported once
	)
	_, err := ResolveBOM(seats, "2HD", m)
	var mme *MissingMatrixError
	if !errors.As(err, &mme) {
		t.Fatalf("want MissingMatrixError, got %v", err)
	}
	if len(mme.Pairs) != 1 || mme.Pairs[0] != (MissingPair{SizeMM: "1.50", Code: "2HD"}) {
		t.Fatalf("pairs: %+v", mme.Pairs)
	}

	// Chosen code with no rows at all: every flexible size is a gap; locked seat still resolves.
	_, err = ResolveBOM(fixtureSeats(), "1HD", fixtureMatrix())
	if !errors.As(err, &mme) {
		t.Fatalf("want MissingMatrixError, got %v", err)
	}
	if len(mme.Pairs) != 2 || mme.Pairs[0].Code != "1HD" {
		t.Fatalf("pairs: %+v", mme.Pairs)
	}
}

func TestStoneCostPerPieceOrPerCarat(t *testing.T) {
	dec := decimal.RequireFromString
	for name, c := range map[string]struct {
		lines []StoneCostLine
		want  string
	}{
		"per piece":           {[]StoneCostLine{{Pcs: 24, LineCarats: dec("0.1680"), RatePerPc: dec("100"), Basis: "pc"}}, "2400.00"},
		"per carat":           {[]StoneCostLine{{Pcs: 24, LineCarats: dec("0.1680"), RatePerCt: dec("50000"), Basis: "ct"}}, "8400.00"},
		"legacy: empty basis": {[]StoneCostLine{{LineCarats: dec("0.3470"), RatePerCt: dec("50000")}}, "17350.00"},
		"mixed":               {[]StoneCostLine{{Pcs: 24, RatePerPc: dec("100"), Basis: "pc"}, {Pcs: 1, LineCarats: dec("0.0650"), RatePerCt: dec("50000"), Basis: "ct"}}, "5650.00"},
		"a piece price is not multiplied by carats": {[]StoneCostLine{{Pcs: 3, LineCarats: dec("9.9999"), RatePerPc: dec("0.05"), Basis: "pc"}}, "0.15"},
	} {
		got := Compute(CostInput{NetG: dec("1"), StoneLines: c.lines}).StoneCost.StringFixed(2)
		if got != c.want {
			t.Errorf("%s: stone cost %s, want %s", name, got, c.want)
		}
	}
}
