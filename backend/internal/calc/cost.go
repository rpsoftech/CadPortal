package calc

import "github.com/shopspring/decimal"

var hundred = decimal.NewFromInt(100)

func RoundMoney(d decimal.Decimal) decimal.Decimal { return d.Round(2) }

// StoneCostLine is one resolved BOM line with its rate per carat.
type StoneCostLine struct {
	LineCarats decimal.Decimal
	RatePerCt  decimal.Decimal
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
}

// Compute: gold = net x (1 + wastage%/100) x rate; stone = sum(line carats x
// rate per ct); setting = pcs x rate per pc.
func Compute(in CostInput) Cost {
	goldG := in.NetG.Mul(decimal.NewFromInt(1).Add(in.WastagePct.Div(hundred)))
	carats, stone := decimal.Zero, decimal.Zero
	for _, l := range in.StoneLines {
		carats = carats.Add(l.LineCarats)
		stone = stone.Add(l.LineCarats.Mul(l.RatePerCt))
	}
	c := Cost{
		GoldG:       RoundGrams(goldG),
		GoldCost:    RoundMoney(goldG.Mul(in.GoldRatePerG)),
		StoneCarats: RoundCarats(carats),
		StoneCost:   RoundMoney(stone),
		SettingPcs:  in.SettingPcs,
		SettingCost: RoundMoney(decimal.NewFromInt(int64(in.SettingPcs)).Mul(in.SettingRatePerPc)),
	}
	c.Total = c.GoldCost.Add(c.StoneCost).Add(c.SettingCost)
	return c
}
