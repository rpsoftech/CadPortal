package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// MaxSheetVariations caps how many variations one costing run may cover.
const MaxSheetVariations = 500

// SheetRequest is what a user asks for when costing several designs at once: whole designs (every
// non-deleted variation) and/or single variations, in one metal and one stone code.
type SheetRequest struct {
	VariationIDs     []int64         `json:"variation_ids,omitempty"`
	DesignIDs        []int64         `json:"design_ids,omitempty"`
	MetalCode        string          `json:"metal_code"`
	StoneCode        string          `json:"stone_code"`
	GoldRatePerG     decimal.Decimal `json:"gold_rate_per_g"`
	SettingRatePerPc decimal.Decimal `json:"setting_rate_per_pc"`
	// SkipIncomplete costs the variations that can be costed and lists the others; without it any
	// incomplete variation fails the whole run and nothing is costed.
	SkipIncomplete bool `json:"skip_incomplete"`
	// Quantities multiplies a variation's pieces: key is the variation id, value the quantity (at least
	// 1). A variation with an entry is costed for that many pieces; its row then carries Quantity and
	// every weight and amount on it is for the whole line. Variations without an entry cost one piece.
	Quantities map[int64]int `json:"quantities,omitempty"`
}

// SheetRow is one costed variation. Money is rounded as in calc.Compute.
type SheetRow struct {
	DesignID      int64  `json:"design_id"`
	DesignCode    string `json:"design_code"`
	DesignName    string `json:"design_name"`
	VariationID   int64  `json:"variation_id"`
	VariationCode string `json:"variation_code"`
	MetalCode     string `json:"metal_code"`
	StoneCode     string `json:"stone_code"`
	// Quantity is the number of pieces the row stands for; 0 means a plain single-piece row (no
	// quantity was given). With a quantity, the weights and amounts below are the line's, not one piece's.
	Quantity         int             `json:"quantity,omitempty"`
	NetG             decimal.Decimal `json:"net_g"`
	GrossG           decimal.Decimal `json:"gross_g"`
	WastagePct       decimal.Decimal `json:"wastage_pct"`
	GoldG            decimal.Decimal `json:"gold_g"`
	GoldRatePerG     decimal.Decimal `json:"gold_rate_per_g"`
	GoldCost         decimal.Decimal `json:"gold_cost"`
	StoneCt          decimal.Decimal `json:"stone_ct"`
	StoneG           decimal.Decimal `json:"stone_g"`
	StoneCost        decimal.Decimal `json:"stone_cost"`
	SettingPcs       int             `json:"setting_pcs"`
	SettingRatePerPc decimal.Decimal `json:"setting_rate_per_pc"`
	SettingCost      decimal.Decimal `json:"setting_cost"`
	Total            decimal.Decimal `json:"total"`
}

// SheetTotals adds up the printed row values, so the totals row always agrees with the rows above it.
type SheetTotals struct {
	Designs     int             `json:"designs"`
	Variations  int             `json:"variations"`
	NetG        decimal.Decimal `json:"net_g"`
	GrossG      decimal.Decimal `json:"gross_g"`
	GoldG       decimal.Decimal `json:"gold_g"`
	GoldCost    decimal.Decimal `json:"gold_cost"`
	StoneCt     decimal.Decimal `json:"stone_ct"`
	StoneG      decimal.Decimal `json:"stone_g"`
	StoneCost   decimal.Decimal `json:"stone_cost"`
	SettingPcs  int             `json:"setting_pcs"`
	SettingCost decimal.Decimal `json:"setting_cost"`
	Total       decimal.Decimal `json:"total"`
}

// VariationProblem says what stops one variation being costed.
type VariationProblem struct {
	VariationID   int64              `json:"variation_id"`
	VariationCode string             `json:"variation_code"`
	Message       string             `json:"message"`
	MetalsNoWaste []string           `json:"metals_missing_wastage,omitempty"`
	StoneRates    []calc.MissingPair `json:"stone_rates_missing,omitempty"`
	MatrixMissing []calc.MissingPair `json:"matrix_missing,omitempty"`
}

// DesignProblem groups the problems of one design. A design with no variations at all is a problem of
// its own (Variations empty).
type DesignProblem struct {
	DesignID   int64              `json:"design_id"`
	DesignCode string             `json:"design_code"`
	DesignName string             `json:"design_name"`
	Message    string             `json:"message,omitempty"`
	Variations []VariationProblem `json:"variations"`
}

// SheetRates is the pricing data a result was computed with, kept with a saved sheet.
type SheetRates struct {
	GoldRatePerG     decimal.Decimal  `json:"gold_rate_per_g"`
	SettingRatePerPc decimal.Decimal  `json:"setting_rate_per_pc"`
	Metals           []SheetRateMetal `json:"metals"`
	StoneRates       []SheetRateStone `json:"stone_rates"`
}

type SheetRateMetal struct {
	MetalCode       string          `json:"metal_code"`
	SpecificGravity decimal.Decimal `json:"specific_gravity"`
	WastagePct      decimal.Decimal `json:"wastage_pct"`
}

type SheetRateStone struct {
	SizeMM         decimal.Decimal `json:"size_mm"`
	StoneCode      string          `json:"stone_code"`
	RateBasis      string          `json:"rate_basis"`
	Price          decimal.Decimal `json:"price"`
	CaratsPerStone decimal.Decimal `json:"carats_per_stone"`
}

// CostResult is the outcome of costing a selection. Incomplete data is reported in Problems rather
// than as an error, so the caller decides (all-or-nothing, or skip them).
type CostResult struct {
	Rows     []SheetRow      `json:"rows"`
	Totals   SheetTotals     `json:"totals"`
	Problems []DesignProblem `json:"problems"`
	Rates    SheetRates      `json:"rates"`
	// ResolvedVariationIDs is every variation the request covered, costed or not.
	ResolvedVariationIDs []int64 `json:"resolved_variation_ids"`
}

// IncompleteVariations counts the variations (not designs) that could not be costed.
func (r CostResult) IncompleteVariations() int {
	n := 0
	for _, d := range r.Problems {
		n += len(d.Variations)
	}
	return n
}

// IncompleteCostingError is returned when a run is all-or-nothing and some data is missing. It names
// the designs and variations, grouped, and also carries the flat lists the single-design error has.
type IncompleteCostingError struct {
	Problems   []DesignProblem
	Wastage    []string
	StoneRates []calc.MissingPair
	Matrix     []calc.MissingPair
}

func (e *IncompleteCostingError) Error() string {
	var parts []string
	for _, d := range e.Problems {
		if len(d.Variations) == 0 {
			parts = append(parts, fmt.Sprintf("%s: %s", d.DesignCode, d.Message))
			continue
		}
		var vs []string
		for _, v := range d.Variations {
			vs = append(vs, v.VariationCode)
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", d.DesignCode, strings.Join(vs, ", ")))
	}
	msg := "cannot cost: data is missing for " + strings.Join(parts, "; ")
	if len(e.Wastage) > 0 {
		msg += "; wastage % not set for metals: " + strings.Join(e.Wastage, ", ")
	}
	pairs := func(label string, ps []calc.MissingPair) {
		if len(ps) == 0 {
			return
		}
		s := make([]string, len(ps))
		for i, p := range ps {
			s[i] = fmt.Sprintf("(%s mm, %s)", p.SizeMM, p.Code)
		}
		msg += "; " + label + ": " + strings.Join(s, ", ")
	}
	pairs("stone price not set for", e.StoneRates)
	pairs("no stone matrix row for", e.Matrix)
	return msg
}

func newIncompleteError(r CostResult) *IncompleteCostingError {
	e := &IncompleteCostingError{Problems: r.Problems}
	seenM := map[string]bool{}
	seenS := map[calc.MissingPair]bool{}
	seenX := map[calc.MissingPair]bool{}
	for _, d := range r.Problems {
		for _, v := range d.Variations {
			for _, m := range v.MetalsNoWaste {
				if !seenM[m] {
					seenM[m] = true
					e.Wastage = append(e.Wastage, m)
				}
			}
			for _, p := range v.StoneRates {
				if !seenS[p] {
					seenS[p] = true
					e.StoneRates = append(e.StoneRates, p)
				}
			}
			for _, p := range v.MatrixMissing {
				if !seenX[p] {
					seenX[p] = true
					e.Matrix = append(e.Matrix, p)
				}
			}
		}
	}
	sort.Strings(e.Wastage)
	sortPairs(e.StoneRates)
	sortPairs(e.Matrix)
	return e
}

func sortPairs(ps []calc.MissingPair) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].SizeMM != ps[j].SizeMM {
			return ps[i].SizeMM < ps[j].SizeMM
		}
		return ps[i].Code < ps[j].Code
	})
}

// Costable applies the all-or-nothing rule: unless skip is set, any problem is an error that costs
// nothing; with skip, there must still be at least one costed row.
func (r CostResult) Costable(skip bool) error {
	if len(r.Problems) > 0 && !skip {
		return newIncompleteError(r)
	}
	if len(r.Rows) == 0 {
		if len(r.Problems) > 0 {
			return newIncompleteError(r)
		}
		return invalid("nothing to cost")
	}
	return nil
}

type resolvedVariation struct {
	v db.CadVariation
	d db.MasterDesign
}

// resolveSelection turns design and variation ids into variations, in design code / sequence order.
// Ids that no longer exist are returned as problems (a saved sheet can be re-costed after deletions).
func (s *Service) resolveSelection(ctx context.Context, req SheetRequest) ([]resolvedVariation, []DesignProblem, error) {
	if len(req.VariationIDs) == 0 && len(req.DesignIDs) == 0 {
		return nil, nil, invalid("select at least one design or variation")
	}
	if len(req.VariationIDs) > MaxSheetVariations || len(req.DesignIDs) > MaxSheetVariations {
		return nil, nil, invalid("too many selected: at most %d variations can be costed at once", MaxSheetVariations)
	}
	designs := map[int64]db.MasterDesign{}
	design := func(id int64) (db.MasterDesign, error) {
		if d, ok := designs[id]; ok {
			return d, nil
		}
		d, err := s.Q.GetDesignByID(ctx, id)
		if err != nil {
			return d, err
		}
		designs[id] = d
		return d, nil
	}
	var out []resolvedVariation
	var gone []DesignProblem
	seen := map[int64]bool{}
	var empty []DesignProblem

	for _, id := range req.DesignIDs {
		d, err := design(id)
		if err != nil {
			if mapErr(err) == ErrNotFound {
				return nil, nil, invalid("design %d not found", id)
			}
			return nil, nil, err
		}
		vs, err := s.Q.ListVariationsByDesign(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		if len(vs) == 0 {
			empty = append(empty, DesignProblem{DesignID: d.ID, DesignCode: d.Code, DesignName: d.Name,
				Message: "has no variations", Variations: []VariationProblem{}})
		}
		for _, v := range vs {
			if !seen[v.ID] {
				seen[v.ID] = true
				out = append(out, resolvedVariation{v, d})
			}
			if len(out) > MaxSheetVariations {
				return nil, nil, invalid("too many variations: at most %d can be costed at once", MaxSheetVariations)
			}
		}
	}
	for _, id := range req.VariationIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		v, err := s.Q.GetVariation(ctx, id)
		if err != nil {
			if mapErr(err) != ErrNotFound {
				return nil, nil, err
			}
			gone = append(gone, DesignProblem{Variations: []VariationProblem{{VariationID: id,
				VariationCode: fmt.Sprintf("#%d", id), Message: "variation no longer exists"}}})
			continue
		}
		d, err := design(v.DesignID)
		if err != nil {
			if mapErr(err) != ErrNotFound {
				return nil, nil, err
			}
			gone = append(gone, DesignProblem{DesignID: v.DesignID, Variations: []VariationProblem{{VariationID: id,
				VariationCode: v.Code, Message: "its design has been deleted"}}})
			continue
		}
		out = append(out, resolvedVariation{v, d})
		if len(out) > MaxSheetVariations {
			return nil, nil, invalid("too many variations: at most %d can be costed at once", MaxSheetVariations)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].d.Code != out[j].d.Code {
			return out[i].d.Code < out[j].d.Code
		}
		return out[i].v.Seq < out[j].v.Seq
	})
	return out, append(empty, gone...), nil
}

// CostSelection costs every variation of the selected designs (and the selected single variations) in
// one metal and stone code. It uses the same maths as the single-design costing (calc.Compute).
// Missing data (no wastage %, no stone price, no matrix row) is never treated as zero: the variation
// is listed in Problems and not costed.
func (s *Service) CostSelection(ctx context.Context, req SheetRequest) (CostResult, error) {
	if req.MetalCode == "" || req.StoneCode == "" {
		return CostResult{}, invalid("metal_code and stone_code are required")
	}
	if !req.GoldRatePerG.IsPositive() {
		return CostResult{}, invalid("gold_rate_per_g must be positive")
	}
	if req.SettingRatePerPc.IsNegative() {
		return CostResult{}, invalid("setting_rate_per_pc must not be negative")
	}
	for id, q := range req.Quantities {
		if q < 1 || q > MaxLineQuantity {
			return CostResult{}, invalid("quantity of variation %d must be between 1 and %d", id, MaxLineQuantity)
		}
	}
	metal, err := s.Q.GetMetalByCode(ctx, req.MetalCode)
	if err != nil {
		if mapErr(err) == ErrNotFound {
			return CostResult{}, invalid("unknown metal_code %q", req.MetalCode)
		}
		return CostResult{}, err
	}
	if !metal.Active {
		return CostResult{}, invalid("metal %q is not active", req.MetalCode)
	}
	if _, err := s.Q.GetStoneCodeByCode(ctx, req.StoneCode); err != nil {
		if mapErr(err) == ErrNotFound {
			return CostResult{}, invalid("unknown stone code %q", req.StoneCode)
		}
		return CostResult{}, err
	}
	vars, preProblems, err := s.resolveSelection(ctx, req)
	if err != nil {
		return CostResult{}, err
	}
	rateTable, err := s.stoneRates(ctx)
	if err != nil {
		return CostResult{}, err
	}

	res := CostResult{Rows: []SheetRow{}, Problems: []DesignProblem{}, ResolvedVariationIDs: []int64{}}
	usedStone := map[rateKey]bool{}
	problemAt := map[int64]int{} // design id -> index in res.Problems
	addProblem := func(d db.MasterDesign, p VariationProblem) {
		i, ok := problemAt[d.ID]
		if !ok {
			i = len(res.Problems)
			problemAt[d.ID] = i
			res.Problems = append(res.Problems, DesignProblem{DesignID: d.ID, DesignCode: d.Code, DesignName: d.Name, Variations: []VariationProblem{}})
		}
		res.Problems[i].Variations = append(res.Problems[i].Variations, p)
	}
	designsCosted := map[int64]bool{}

	for _, rv := range vars {
		v, d := rv.v, rv.d
		res.ResolvedVariationIDs = append(res.ResolvedVariationIDs, v.ID)
		bom, err := s.SimulateBOM(ctx, v.ID, req.StoneCode)
		if err != nil {
			var mme *calc.MissingMatrixError
			if errors.As(err, &mme) {
				addProblem(d, VariationProblem{VariationID: v.ID, VariationCode: v.Code,
					Message: mme.Error(), MatrixMissing: mme.Pairs})
				continue
			}
			return CostResult{}, err
		}
		var net, gross decimal.Decimal
		found := false
		for _, m := range bom.Metals {
			if m.MetalCode == req.MetalCode {
				net, gross, found = m.NetG, m.GrossG, true
			}
		}
		if !found {
			return CostResult{}, invalid("metal %q is not active", req.MetalCode)
		}
		lines, missing := stoneCostLines(bom, rateTable)
		p := VariationProblem{VariationID: v.ID, VariationCode: v.Code, StoneRates: missing}
		if !metal.WastagePct.Valid {
			p.MetalsNoWaste = []string{metal.Code}
		}
		if len(p.StoneRates) > 0 || len(p.MetalsNoWaste) > 0 {
			var what []string
			if len(p.MetalsNoWaste) > 0 {
				what = append(what, "wastage % not set for "+metal.Code)
			}
			for _, m := range missing {
				what = append(what, fmt.Sprintf("price not set for (%s mm, %s)", m.SizeMM, m.Code))
			}
			p.Message = strings.Join(what, "; ")
			addProblem(d, p)
			continue
		}
		cost := calc.Compute(calc.CostInput{
			NetG: net, WastagePct: metal.WastagePct.Decimal, GoldRatePerG: req.GoldRatePerG,
			StoneLines: lines, SettingPcs: bom.TotalPcs, SettingRatePerPc: req.SettingRatePerPc,
		})
		for _, l := range bom.Lines {
			usedStone[rateKey{l.SizeMM.StringFixed(2), l.StoneCode}] = true
		}
		designsCosted[d.ID] = true
		row := SheetRow{
			DesignID: d.ID, DesignCode: d.Code, DesignName: d.Name,
			VariationID: v.ID, VariationCode: v.Code, MetalCode: metal.Code, StoneCode: req.StoneCode,
			NetG: net, GrossG: gross, WastagePct: metal.WastagePct.Decimal,
			GoldG: cost.GoldG, GoldRatePerG: req.GoldRatePerG, GoldCost: cost.GoldCost,
			StoneCt: cost.StoneCarats, StoneG: calc.RoundLineGrams(calc.StoneWeight(cost.StoneCarats)), StoneCost: cost.StoneCost,
			SettingPcs: cost.SettingPcs, SettingRatePerPc: req.SettingRatePerPc, SettingCost: cost.SettingCost,
			Total: cost.Total,
		}
		if q, ok := req.Quantities[v.ID]; ok {
			row = row.times(q)
		}
		res.Rows = append(res.Rows, row)
	}
	res.Problems = append(res.Problems, preProblems...)
	for _, p := range preProblems {
		for _, v := range p.Variations {
			res.ResolvedVariationIDs = append(res.ResolvedVariationIDs, v.VariationID)
		}
	}

	res.Totals = sumRows(res.Rows)
	res.Totals.Designs = len(designsCosted)

	res.Rates = SheetRates{GoldRatePerG: req.GoldRatePerG, SettingRatePerPc: req.SettingRatePerPc, StoneRates: []SheetRateStone{}, Metals: []SheetRateMetal{}}
	if metal.WastagePct.Valid {
		res.Rates.Metals = append(res.Rates.Metals, SheetRateMetal{MetalCode: metal.Code, SpecificGravity: metal.SpecificGravity, WastagePct: metal.WastagePct.Decimal})
	}
	if len(res.Rows) > 0 {
		for k := range usedStone {
			r := rateTable[k]
			if r.price.Valid {
				res.Rates.StoneRates = append(res.Rates.StoneRates, SheetRateStone{SizeMM: r.sizeMM, StoneCode: r.stoneCode,
					RateBasis: r.basis, Price: r.price.Decimal, CaratsPerStone: r.carats})
			}
		}
		sort.Slice(res.Rates.StoneRates, func(i, j int) bool {
			a, b := res.Rates.StoneRates[i], res.Rates.StoneRates[j]
			if a.StoneCode != b.StoneCode {
				return a.StoneCode < b.StoneCode
			}
			return a.SizeMM.LessThan(b.SizeMM)
		})
	}
	return res, nil
}

// times makes the row stand for q pieces: every weight and amount is multiplied by q, so the totals row
// of a sheet still adds up the printed values. Per-piece values are exact at their printed precision,
// so the product needs no further rounding.
func (r SheetRow) times(q int) SheetRow {
	m := decimal.NewFromInt(int64(q))
	r.Quantity = q
	r.NetG, r.GrossG, r.GoldG = r.NetG.Mul(m), r.GrossG.Mul(m), r.GoldG.Mul(m)
	r.GoldCost = r.GoldCost.Mul(m)
	r.StoneCt, r.StoneG, r.StoneCost = r.StoneCt.Mul(m), r.StoneG.Mul(m), r.StoneCost.Mul(m)
	r.SettingPcs *= q
	r.SettingCost, r.Total = r.SettingCost.Mul(m), r.Total.Mul(m)
	return r
}

func sumRows(rows []SheetRow) SheetTotals {
	var t SheetTotals
	t.Variations = len(rows)
	for _, r := range rows {
		t.NetG = t.NetG.Add(r.NetG)
		t.GrossG = t.GrossG.Add(r.GrossG)
		t.GoldG = t.GoldG.Add(r.GoldG)
		t.GoldCost = t.GoldCost.Add(r.GoldCost)
		t.StoneCt = t.StoneCt.Add(r.StoneCt)
		t.StoneG = t.StoneG.Add(r.StoneG)
		t.StoneCost = t.StoneCost.Add(r.StoneCost)
		t.SettingPcs += r.SettingPcs
		t.SettingCost = t.SettingCost.Add(r.SettingCost)
		t.Total = t.Total.Add(r.Total)
	}
	return t
}

// ---------- Saved costing sheets ----------

// SheetInputs is what a saved sheet was asked to do. ResolvedVariationIDs lets a re-cost run on exactly
// the same variations even if designs gained variations since.
type SheetInputs struct {
	SheetRequest
	ResolvedVariationIDs []int64 `json:"resolved_variation_ids"`
}

// SheetSummary is a row of the list screen.
type SheetSummary struct {
	ID               int64           `json:"id"`
	Name             string          `json:"name"`
	OrderID          *int64          `json:"order_id"`
	CreatedAt        time.Time       `json:"created_at"`
	CreatedBy        *int64          `json:"created_by"`
	CreatedByName    string          `json:"created_by_name"`
	RecostOf         *int64          `json:"recost_of"`
	MetalCode        string          `json:"metal_code"`
	StoneCode        string          `json:"stone_code"`
	GoldRatePerG     decimal.Decimal `json:"gold_rate_per_g"`
	SettingRatePerPc decimal.Decimal `json:"setting_rate_per_pc"`
	DesignCount      int             `json:"design_count"`
	VariationCount   int             `json:"variation_count"`
	IncompleteCount  int             `json:"incomplete_count"`
	Total            decimal.Decimal `json:"total"`
}

// SheetDetail is a saved sheet in full. Everything in it is a snapshot from the moment it was saved.
type SheetDetail struct {
	SheetSummary
	Inputs   SheetInputs     `json:"inputs"`
	Rows     []SheetRow      `json:"rows"`
	Totals   SheetTotals     `json:"totals"`
	Problems []DesignProblem `json:"problems"`
	Rates    SheetRates      `json:"rates"`
}

// SaveSheetParams: the CSV is stored as given, so a later download is byte-identical to the first.
type SaveSheetParams struct {
	Name     string
	UserID   int64
	RecostOf int64
	OrderID  int64
	Request  SheetRequest
	Result   CostResult
	CSV      string
}

func (s *Service) SaveCostingSheet(ctx context.Context, p SaveSheetParams) (SheetDetail, error) {
	name := strings.TrimSpace(p.Name)
	if name == "" || len([]rune(name)) > 120 {
		return SheetDetail{}, invalid("name is required (at most 120 characters)")
	}
	if err := p.Result.Costable(p.Request.SkipIncomplete); err != nil {
		return SheetDetail{}, err
	}
	inputs := SheetInputs{SheetRequest: p.Request, ResolvedVariationIDs: p.Result.ResolvedVariationIDs}
	enc := func(v any) []byte {
		b, _ := json.Marshal(v)
		return b
	}
	arg := db.InsertCostingSheetParams{
		Name: name, MetalCode: p.Request.MetalCode, StoneCode: p.Request.StoneCode,
		GoldRatePerG: p.Request.GoldRatePerG, SettingRatePerPc: p.Request.SettingRatePerPc,
		DesignCount: int32(p.Result.Totals.Designs), VariationCount: int32(len(p.Result.Rows)),
		IncompleteCount: int32(p.Result.IncompleteVariations()), Total: p.Result.Totals.Total,
		Inputs: enc(inputs), ResultRows: enc(p.Result.Rows), Totals: enc(p.Result.Totals),
		Problems: enc(p.Result.Problems), Rates: enc(p.Result.Rates), Csv: p.CSV,
	}
	if p.UserID != 0 {
		arg.CreatedBy = pgtype.Int8{Int64: p.UserID, Valid: true}
	}
	if p.RecostOf != 0 {
		arg.RecostOf = pgtype.Int8{Int64: p.RecostOf, Valid: true}
	}
	if p.OrderID != 0 {
		arg.OrderID = pgtype.Int8{Int64: p.OrderID, Valid: true}
	}
	row, err := s.Q.InsertCostingSheet(ctx, arg)
	if err != nil {
		return SheetDetail{}, mapErr(err)
	}
	d, _, err := s.GetCostingSheet(ctx, row.ID)
	return d, err
}

// ListCostingSheets returns saved sheets, newest first, and the total count.
func (s *Service) ListCostingSheets(ctx context.Context, limit, offset int32) ([]SheetSummary, int64, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.Q.ListCostingSheets(ctx, db.ListCostingSheetsParams{Limit: limit, Offset: offset})
	if err != nil {
		return nil, 0, err
	}
	out := make([]SheetSummary, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.TotalCount
		out = append(out, SheetSummary{
			ID: r.ID, Name: r.Name, OrderID: int8Ptr(r.OrderID), CreatedAt: r.CreatedAt.Time, CreatedBy: int8Ptr(r.CreatedBy),
			CreatedByName: r.CreatedByName, RecostOf: int8Ptr(r.RecostOf), MetalCode: r.MetalCode,
			StoneCode: r.StoneCode, GoldRatePerG: r.GoldRatePerG, SettingRatePerPc: r.SettingRatePerPc,
			DesignCount: int(r.DesignCount), VariationCount: int(r.VariationCount),
			IncompleteCount: int(r.IncompleteCount), Total: r.Total,
		})
	}
	return out, total, nil
}

func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

// GetCostingSheet returns the sheet and the CSV that was produced when it was saved.
func (s *Service) GetCostingSheet(ctx context.Context, id int64) (SheetDetail, string, error) {
	r, err := s.Q.GetCostingSheet(ctx, id)
	if err != nil {
		return SheetDetail{}, "", mapErr(err)
	}
	d := SheetDetail{SheetSummary: SheetSummary{
		ID: r.ID, Name: r.Name, OrderID: int8Ptr(r.OrderID), CreatedAt: r.CreatedAt.Time, CreatedBy: int8Ptr(r.CreatedBy),
		CreatedByName: r.CreatedByName, RecostOf: int8Ptr(r.RecostOf), MetalCode: r.MetalCode,
		StoneCode: r.StoneCode, GoldRatePerG: r.GoldRatePerG, SettingRatePerPc: r.SettingRatePerPc,
		DesignCount: int(r.DesignCount), VariationCount: int(r.VariationCount),
		IncompleteCount: int(r.IncompleteCount), Total: r.Total,
	}}
	for _, f := range []struct {
		raw []byte
		dst any
	}{{r.Inputs, &d.Inputs}, {r.ResultRows, &d.Rows}, {r.Totals, &d.Totals}, {r.Problems, &d.Problems}, {r.Rates, &d.Rates}} {
		if err := json.Unmarshal(f.raw, f.dst); err != nil {
			return SheetDetail{}, "", fmt.Errorf("costing sheet %d: %w", id, err)
		}
	}
	return d, r.Csv, nil
}

// DeleteCostingSheet removes a saved sheet.
func (s *Service) DeleteCostingSheet(ctx context.Context, id int64) error {
	n, err := s.Q.DeleteCostingSheet(ctx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecostOverrides are the values a re-cost may change; nil keeps the saved sheet's value.
type RecostOverrides struct {
	GoldRatePerG     *decimal.Decimal
	SettingRatePerPc *decimal.Decimal
	SkipIncomplete   *bool
}

// RecostRequest builds the request for a new sheet from a saved one: the same variations, metal and
// stone code, with today's masters (wastage, stone prices) and the gold / setting rates given.
func RecostRequest(old SheetDetail, o RecostOverrides) SheetRequest {
	req := SheetRequest{
		VariationIDs: old.Inputs.ResolvedVariationIDs, MetalCode: old.MetalCode, StoneCode: old.StoneCode,
		GoldRatePerG: old.GoldRatePerG, SettingRatePerPc: old.SettingRatePerPc, SkipIncomplete: old.Inputs.SkipIncomplete,
		Quantities: old.Inputs.Quantities,
	}
	if o.GoldRatePerG != nil {
		req.GoldRatePerG = *o.GoldRatePerG
	}
	if o.SettingRatePerPc != nil {
		req.SettingRatePerPc = *o.SettingRatePerPc
	}
	if o.SkipIncomplete != nil {
		req.SkipIncomplete = *o.SkipIncomplete
	}
	return req
}

// SheetComparisonRow compares one variation across two sheets; a nil side means it was not costed there.
type SheetComparisonRow struct {
	VariationID   int64            `json:"variation_id"`
	VariationCode string           `json:"variation_code"`
	DesignCode    string           `json:"design_code"`
	Previous      *decimal.Decimal `json:"previous_total"`
	Current       *decimal.Decimal `json:"total"`
	Difference    *decimal.Decimal `json:"difference"`
}

// SheetComparison is the difference between a re-cost and the sheet it came from.
type SheetComparison struct {
	PreviousID    int64                `json:"previous_id"`
	PreviousTotal decimal.Decimal      `json:"previous_total"`
	Total         decimal.Decimal      `json:"total"`
	Difference    decimal.Decimal      `json:"difference"`
	Rows          []SheetComparisonRow `json:"rows"`
}

func CompareSheets(old SheetDetail, now SheetDetail) SheetComparison {
	c := SheetComparison{PreviousID: old.ID, PreviousTotal: old.Totals.Total, Total: now.Totals.Total,
		Difference: now.Totals.Total.Sub(old.Totals.Total), Rows: []SheetComparisonRow{}}
	idx := map[int64]int{}
	for _, r := range old.Rows {
		t := r.Total
		idx[r.VariationID] = len(c.Rows)
		c.Rows = append(c.Rows, SheetComparisonRow{VariationID: r.VariationID, VariationCode: r.VariationCode, DesignCode: r.DesignCode, Previous: &t})
	}
	for _, r := range now.Rows {
		t := r.Total
		i, ok := idx[r.VariationID]
		if !ok {
			i = len(c.Rows)
			c.Rows = append(c.Rows, SheetComparisonRow{VariationID: r.VariationID, VariationCode: r.VariationCode, DesignCode: r.DesignCode})
		}
		c.Rows[i].Current = &t
	}
	for i := range c.Rows {
		r := &c.Rows[i]
		if r.Previous != nil && r.Current != nil {
			d := r.Current.Sub(*r.Previous)
			r.Difference = &d
		}
	}
	return c
}
