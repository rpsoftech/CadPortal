package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

const maxCostingItems = 200

// MissingCostInputsError lists everything that blocks a costing export: metals
// with no wastage % set and (size, code) pairs with no price (per piece, per carat or per gram, as each is set). Missing
// values are never treated as zero.
type MissingCostInputsError struct {
	Wastage    []string           `json:"metals_missing_wastage"`
	StoneRates []calc.MissingPair `json:"stone_rates_missing"`
}

func (e *MissingCostInputsError) Error() string {
	var parts []string
	if len(e.Wastage) > 0 {
		parts = append(parts, "wastage % not set for metals: "+strings.Join(e.Wastage, ", "))
	}
	for _, basis := range []struct{ code, what string }{{"pc", "price per piece"}, {"ct", "rate per carat"}, {"g", "price per gram"}} {
		var ps []string
		for _, p := range e.StoneRates {
			if p.Basis == basis.code || (p.Basis == "" && basis.code == "ct") {
				ps = append(ps, fmt.Sprintf("(%s mm, %s)", p.SizeMM, p.Code))
			}
		}
		if len(ps) > 0 {
			parts = append(parts, basis.what+" not set for: "+strings.Join(ps, ", "))
		}
	}
	return "cannot cost: " + strings.Join(parts, "; ")
}

type CostingItem struct {
	VariationID int64
	MetalCode   string
	StoneCode   string
}

type CostingRequest struct {
	Items            []CostingItem
	GoldRatePerG     decimal.Decimal
	SettingRatePerPc decimal.Decimal
}

type CostRow struct {
	VariationCode    string
	MetalCode        string
	StoneCode        string
	NetG             decimal.Decimal
	WastagePct       decimal.Decimal
	GoldRatePerG     decimal.Decimal
	SettingRatePerPc decimal.Decimal
	calc.Cost
}

// CostingRows prices each requested (variation, metal, stone code). Gold rate
// and setting rate come from the request (no live pricing); wastage % comes
// from the metal master and stone rates from stone_size_matrix.
func (s *Service) CostingRows(ctx context.Context, req CostingRequest) ([]CostRow, error) {
	if len(req.Items) == 0 || len(req.Items) > maxCostingItems {
		return nil, invalid("items must contain between 1 and %d entries", maxCostingItems)
	}
	if !req.GoldRatePerG.IsPositive() {
		return nil, invalid("gold_rate_per_g must be positive")
	}
	if req.SettingRatePerPc.IsNegative() {
		return nil, invalid("setting_rate_per_pc must not be negative")
	}

	matrix, err := s.Q.ListStoneMatrix(ctx)
	if err != nil {
		return nil, err
	}
	type rateKey struct{ size, code string }
	type stoneRate struct {
		basis string
		price decimal.NullDecimal // the one that applies: per piece, per carat or per gram
	}
	rates := map[rateKey]stoneRate{}
	for _, m := range matrix {
		sr := stoneRate{basis: m.RateBasis, price: m.RatePerPc}
		switch m.RateBasis {
		case "ct":
			sr.price = m.RatePerCt
		case "g":
			sr.price = m.RatePerG
		}
		rates[rateKey{m.SizeMm.StringFixed(2), m.StoneCode}] = sr
	}

	missingMetals := map[string]bool{}
	missingRates := map[calc.MissingPair]bool{}
	rows := make([]CostRow, 0, len(req.Items))

	for i, it := range req.Items {
		if it.MetalCode == "" || it.StoneCode == "" {
			return nil, invalid("item %d: metal_code and stone_code are required", i+1)
		}
		metal, err := s.Q.GetMetalByCode(ctx, it.MetalCode)
		if err != nil {
			if mapErr(err) == ErrNotFound {
				return nil, invalid("item %d: unknown metal_code %q", i+1, it.MetalCode)
			}
			return nil, err
		}
		bom, err := s.SimulateBOM(ctx, it.VariationID, it.StoneCode)
		if err != nil {
			return nil, err
		}
		var net decimal.Decimal
		found := false
		for _, m := range bom.Metals {
			if m.MetalCode == it.MetalCode {
				net, found = m.NetG, true
			}
		}
		if !found {
			return nil, invalid("item %d: metal %q is not active", i+1, it.MetalCode)
		}

		lines := make([]calc.StoneCostLine, 0, len(bom.Lines))
		for _, l := range bom.Lines {
			r := rates[rateKey{l.SizeMM.StringFixed(2), l.StoneCode}]
			if !r.price.Valid {
				missingRates[calc.MissingPair{SizeMM: l.SizeMM.StringFixed(2), Code: l.StoneCode, Basis: r.basis}] = true
				continue
			}
			line := calc.StoneCostLine{LineCarats: l.LineCarats, ExactCarats: l.ExactCarats, Pcs: l.StoneCount, Basis: r.basis}
			switch r.basis {
			case "ct":
				line.RatePerCt = r.price.Decimal
			case "g":
				line.RatePerG = r.price.Decimal
			default:
				line.RatePerPc = r.price.Decimal
			}
			lines = append(lines, line)
		}
		if !metal.WastagePct.Valid {
			missingMetals[metal.Code] = true
			continue
		}
		rows = append(rows, CostRow{
			VariationCode: bom.VariationCode, MetalCode: metal.Code, StoneCode: it.StoneCode,
			NetG: net, WastagePct: metal.WastagePct.Decimal,
			GoldRatePerG: req.GoldRatePerG, SettingRatePerPc: req.SettingRatePerPc,
			Cost: calc.Compute(calc.CostInput{
				NetG: net, WastagePct: metal.WastagePct.Decimal, GoldRatePerG: req.GoldRatePerG,
				StoneLines: lines, SettingPcs: bom.TotalPcs, SettingRatePerPc: req.SettingRatePerPc,
			}),
		})
	}

	if len(missingMetals) > 0 || len(missingRates) > 0 {
		e := &MissingCostInputsError{}
		for m := range missingMetals {
			e.Wastage = append(e.Wastage, m)
		}
		for p := range missingRates {
			e.StoneRates = append(e.StoneRates, p)
		}
		sort.Strings(e.Wastage)
		sort.Slice(e.StoneRates, func(i, j int) bool {
			a, b := e.StoneRates[i], e.StoneRates[j]
			if a.SizeMM != b.SizeMM {
				return a.SizeMM < b.SizeMM
			}
			return a.Code < b.Code
		})
		return nil, e
	}
	return rows, nil
}

// JobBag is the data printed on a job-bag PDF.
type JobBag struct {
	DesignID    int64
	VariationID int64
	// QR is the text the job-bag QR code holds: a link to the design page when the portal has a public
	// address, otherwise the plain variation code. Never names, never prices.
	QR            string
	DesignCode    string
	DesignName    string
	Category      string
	Tags          []string
	VariationCode string
	BaseInput     string
	MetalCode     string
	NetG          decimal.Decimal
	GrossG        decimal.Decimal
	BOM           BOMResult
	File3dmName   string
	File3dmSHA256 string
	FileStlName   string
	FileStlSHA256 string
	GeneratedAt   time.Time
	// Photo is a JPEG thumbnail of the variation (or, failing that, the design) for the top of the
	// sheet; nil when there is none or storage cannot be read. The job bag never fails for want of it.
	Photo []byte
}

// JobBag assembles the spec sheet for one variation in one metal, with the
// planner-chosen stone code resolved through the BOM rules.
func (s *Service) JobBag(ctx context.Context, variationID int64, metalCode, stoneCode string) (JobBag, error) {
	jb, err := s.JobBagData(ctx, variationID, metalCode, stoneCode)
	if err != nil {
		return JobBag{}, err
	}
	jb.Photo = s.jobBagPhoto(ctx, jb.DesignID, variationID)
	return jb, nil
}

// JobBagData is JobBag without the photo, so many bags can be checked before any photo is read.
func (s *Service) JobBagData(ctx context.Context, variationID int64, metalCode, stoneCode string) (JobBag, error) {
	if metalCode == "" {
		return JobBag{}, invalid("metal_code is required")
	}
	bom, err := s.SimulateBOM(ctx, variationID, stoneCode)
	if err != nil {
		return JobBag{}, err
	}
	jb := JobBag{VariationCode: bom.VariationCode, MetalCode: metalCode, BOM: bom, GeneratedAt: time.Now().UTC()}
	found := false
	for _, m := range bom.Metals {
		if m.MetalCode == metalCode {
			jb.NetG, jb.GrossG, found = m.NetG, m.GrossG, true
		}
	}
	if !found {
		return JobBag{}, invalid("metal %q is unknown or not active", metalCode)
	}

	v, err := s.Q.GetVariation(ctx, variationID)
	if err != nil {
		return JobBag{}, mapErr(err)
	}
	d, err := s.Q.GetDesignByID(ctx, v.DesignID)
	if err != nil {
		return JobBag{}, mapErr(err)
	}
	jb.DesignCode, jb.DesignName = d.Code, d.Name
	cats, err := s.Q.ListCategories(ctx)
	if err != nil {
		return JobBag{}, err
	}
	for _, c := range cats {
		if c.ID == d.CategoryID {
			jb.Category = c.Name
		}
	}
	tags, err := s.Q.ListDesignTags(ctx, d.ID)
	if err != nil {
		return JobBag{}, err
	}
	for _, t := range tags {
		jb.Tags = append(jb.Tags, t.Name)
	}

	switch {
	case v.BaseVolumeMm3.Valid:
		jb.BaseInput = fmt.Sprintf("Volume %s mm3", v.BaseVolumeMm3.Decimal.StringFixed(3))
	case v.BaseRefWeightG.Valid:
		ref := ""
		if all, err := s.Q.ListMetals(ctx); err == nil {
			for _, m := range all {
				if m.ID == v.BaseRefMetalID.Int64 {
					ref = m.Code
				}
			}
		}
		jb.BaseInput = fmt.Sprintf("Reference %s g in %s", v.BaseRefWeightG.Decimal.StringFixed(3), ref)
	}
	jb.File3dmName, jb.File3dmSHA256 = v.File3dmName.String, v.File3dmSha256.String
	jb.FileStlName, jb.FileStlSHA256 = v.FileStlName.String, v.FileStlSha256.String
	jb.DesignID, jb.VariationID = d.ID, variationID
	jb.QR = s.jobBagQR(d.Code, bom.VariationCode)
	return jb, nil
}
