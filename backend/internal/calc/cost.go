package calc

import "github.com/shopspring/decimal"

var hundred = decimal.NewFromInt(100)

func RoundMoney(d decimal.Decimal) decimal.Decimal { return d.Round(2) }

// StoneCostLine is one resolved BOM line with the price that applies to it: per piece (Basis "pc",
// the default way stones are priced), per gram of stone weight (Basis "g") or per carat (Basis "ct";
// an empty Basis also means per carat, as it did before pieces were an option).
type StoneCostLine struct {
	LineCarats decimal.Decimal
	// ExactCarats is the line's carats before any rounding (stone_count x carats_per_stone). A per-gram
	// line is priced from it, so rounding the shown carats to 4 dp never moves money. Zero means "use
	// LineCarats".
	ExactCarats decimal.Decimal
	RatePerCt   decimal.Decimal
	Pcs         int
	RatePerPc   decimal.Decimal
	RatePerG    decimal.Decimal
	Basis       string
}

// PricedLine records how one stone line was priced: the basis, the quantity priced (pieces, carats or
// grams, by basis), the price and the line's cost (unrounded; only the Cost totals are rounded).
type PricedLine struct {
	Basis string
	Qty   decimal.Decimal
	Rate  decimal.Decimal
	Cost  decimal.Decimal
}

// Unit is the label of the quantity priced: "pcs", "ct" or "g".
func (p PricedLine) Unit() string {
	switch p.Basis {
	case "pc":
		return "pcs"
	case "g":
		return "g"
	}
	return "ct"
}

// CostInput holds everything the costing CSV needs for one variation/metal.
type CostInput struct {
	NetG             decimal.Decimal
	WastagePct       decimal.Decimal
	GoldRatePerG     decimal.Decimal
	StoneLines       []StoneCostLine
	SettingPcs       int
	SettingRatePerPc decimal.Decimal
}

// Cost is the costing breakdown. Money is rounded to 2 dp per component and
// Total is the sum of the rounded components, so a printed sheet always adds up.
type Cost struct {
	GoldG       decimal.Decimal // net + wastage, grams (3 dp)
	GoldCost    decimal.Decimal
	StoneCarats decimal.Decimal // 4 dp
	StoneCost   decimal.Decimal
	SettingPcs  int
	SettingCost decimal.Decimal
	Total       decimal.Decimal
	StonePriced []PricedLine // one per stone line, in BOM order
}

// Compute: gold = net x (1 + wastage%/100) x rate; stone = sum(line pcs x price per pc, line carats x
// price per ct, or line grams x price per g, by each line's basis; line grams = line carats x 0.200);
// setting = pcs x rate per pc.
func Compute(in CostInput) Cost {
	goldG := in.NetG.Mul(decimal.NewFromInt(1).Add(in.WastagePct.Div(hundred)))
	carats, stone := decimal.Zero, decimal.Zero
	var priced []PricedLine
	for _, l := range in.StoneLines {
		carats = carats.Add(l.LineCarats)
		var p PricedLine
		switch l.Basis {
		case "pc":
			p = PricedLine{Basis: "pc", Qty: decimal.NewFromInt(int64(l.Pcs)), Rate: l.RatePerPc}
		case "g":
			ct := l.ExactCarats
			if ct.IsZero() {
				ct = l.LineCarats
			}
			p = PricedLine{Basis: "g", Qty: StoneWeight(ct), Rate: l.RatePerG}
		default:
			p = PricedLine{Basis: "ct", Qty: l.LineCarats, Rate: l.RatePerCt}
		}
		p.Cost = p.Qty.Mul(p.Rate)
		priced = append(priced, p)
		stone = stone.Add(p.Cost)
	}
	c := Cost{
		GoldG:       RoundGrams(goldG),
		GoldCost:    RoundMoney(goldG.Mul(in.GoldRatePerG)),
		StoneCarats: RoundCarats(carats),
		StoneCost:   RoundMoney(stone),
		SettingPcs:  in.SettingPcs,
		SettingCost: RoundMoney(decimal.NewFromInt(int64(in.SettingPcs)).Mul(in.SettingRatePerPc)),
		StonePriced: priced,
	}
	c.Total = c.GoldCost.Add(c.StoneCost).Add(c.SettingCost)
	return c
}
