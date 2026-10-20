package service

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

type DesignInput struct {
	Code       string // required on create; ignored on update
	Name       string
	CategoryID int64
	TagIDs     []int64
}

func (s *Service) CreateDesign(ctx context.Context, in DesignInput) (db.MasterDesign, error) {
	if in.Code == "" {
		return db.MasterDesign{}, invalid("code is required")
	}
	if in.CategoryID == 0 {
		return db.MasterDesign{}, invalid("category_id is required")
	}
	var d db.MasterDesign
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		if d, err = q.CreateDesign(ctx, db.CreateDesignParams{Code: in.Code, Name: in.Name, CategoryID: in.CategoryID}); err != nil {
			return friendly(err, "a design with that code already exists (codes of deleted designs stay reserved)")
		}
		return setTags(ctx, q, d.ID, in.TagIDs)
	})
	return d, err
}

func setTags(ctx context.Context, q *db.Queries, designID int64, tagIDs []int64) error {
	if err := q.ClearDesignTags(ctx, designID); err != nil {
		return err
	}
	for _, t := range tagIDs {
		if err := q.AddDesignTag(ctx, db.AddDesignTagParams{DesignID: designID, TagID: t}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) UpdateDesign(ctx context.Context, id int64, in DesignInput) (db.MasterDesign, error) {
	if in.CategoryID == 0 {
		return db.MasterDesign{}, invalid("category_id is required")
	}
	var d db.MasterDesign
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		if d, err = q.UpdateDesign(ctx, db.UpdateDesignParams{ID: id, Name: in.Name, CategoryID: in.CategoryID}); err != nil {
			return err
		}
		return setTags(ctx, q, id, in.TagIDs)
	})
	return d, err
}

// DeleteVariation removes a variation with its seats, weights and stored CAD files. The
// database row goes first; objects are deleted afterwards on a best-effort basis (an
// orphaned object is harmless, a row pointing at a missing object is not).
func (s *Service) DeleteVariation(ctx context.Context, id int64) error {
	var keys []string
	err := s.inTx(ctx, func(q *db.Queries) error {
		v, err := q.DeleteVariation(ctx, id)
		if err != nil {
			return err
		}
		for _, k := range []pgtype.Text{v.File3dmKey, v.FileStlKey} {
			if k.Valid {
				keys = append(keys, k.String)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.Store != nil {
		for _, k := range keys {
			_ = s.Store.Delete(context.WithoutCancel(ctx), k)
		}
	}
	return nil
}

func (s *Service) DeleteDesign(ctx context.Context, id int64) error {
	n, err := s.Q.SoftDeleteDesign(ctx, id)
	if err != nil {
		return mapErr(err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type VariationDetail struct {
	db.CadVariation
	Weights []db.ListVariationWeightsRow `json:"weights"`
}

type DesignDetail struct {
	db.MasterDesign
	Tags       []db.Tag          `json:"tags"`
	Variations []VariationDetail `json:"variations"`
}

func (s *Service) GetDesignByCode(ctx context.Context, code string) (DesignDetail, error) {
	d, err := s.Q.GetDesignByCode(ctx, code)
	if err != nil {
		return DesignDetail{}, mapErr(err)
	}
	out := DesignDetail{MasterDesign: d}
	if out.Tags, err = nonNil(s.Q.ListDesignTags(ctx, d.ID)); err != nil {
		return out, err
	}
	vs, err := s.Q.ListVariationsByDesign(ctx, d.ID)
	if err != nil {
		return out, err
	}
	out.Variations = make([]VariationDetail, 0, len(vs))
	for _, v := range vs {
		w, err := nonNil(s.Q.ListVariationWeights(ctx, v.ID))
		if err != nil {
			return out, err
		}
		out.Variations = append(out.Variations, VariationDetail{CadVariation: v, Weights: w})
	}
	return out, nil
}

type DesignFilter struct {
	// Query matches the design code or name, case-insensitively, as a substring.
	Query       string
	CategoryID  *int64
	TagIDs      []int64
	MetalCode   string
	MinNetWt    *decimal.Decimal
	MaxNetWt    *decimal.Decimal
	MinGrossWt  *decimal.Decimal
	MaxGrossWt  *decimal.Decimal
	MaxStoneCts *decimal.Decimal
	Limit       int32
	Offset      int32
}

func nullDec(d *decimal.Decimal) decimal.NullDecimal {
	if d == nil {
		return decimal.NullDecimal{}
	}
	return decimal.NullDecimal{Decimal: *d, Valid: true}
}

// DesignListItem is a catalog card: the design plus its tags and, when a metal
// is selected, the net/gross range across its variations for that metal.
type DesignListItem struct {
	db.ListDesignsRow
	NetMin   *decimal.Decimal `json:"net_min"`
	NetMax   *decimal.Decimal `json:"net_max"`
	GrossMin *decimal.Decimal `json:"gross_min"`
	GrossMax *decimal.Decimal `json:"gross_max"`
}

// likeEscaper escapes LIKE wildcards with '!' (the ESCAPE character used in the query),
// so a search for "50%" or "a_b" matches literally.
var likeEscaper = strings.NewReplacer("!", "!!", "%", "!%", "_", "!_")

const maxQueryLen = 100

func likePattern(q string) string {
	q = strings.TrimSpace(q)
	if r := []rune(q); len(r) > maxQueryLen {
		q = string(r[:maxQueryLen])
	}
	return likeEscaper.Replace(q)
}

func (s *Service) ListDesigns(ctx context.Context, f DesignFilter) ([]DesignListItem, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	p := db.ListDesignsParams{
		Q:      pgText(likePattern(f.Query)),
		TagIds: f.TagIDs, MetalCode: pgText(f.MetalCode),
		MinNetWt: nullDec(f.MinNetWt), MaxNetWt: nullDec(f.MaxNetWt),
		MinGrossWt: nullDec(f.MinGrossWt), MaxGrossWt: nullDec(f.MaxGrossWt),
		MaxStoneCts: nullDec(f.MaxStoneCts), Lim: f.Limit, Off: f.Offset,
	}
	if p.TagIds == nil {
		p.TagIds = []int64{}
	}
	if f.CategoryID != nil {
		p.CategoryID = pgtype.Int8{Int64: *f.CategoryID, Valid: true}
	}
	rows, err := s.Q.ListDesigns(ctx, p)
	if err != nil {
		return nil, mapErr(err)
	}
	items := make([]DesignListItem, len(rows))
	ids := make([]int64, len(rows))
	for i, r := range rows {
		if r.TagNames == nil {
			r.TagNames = []string{}
		}
		items[i] = DesignListItem{ListDesignsRow: r}
		ids[i] = r.ID
	}
	if f.MetalCode == "" || len(items) == 0 {
		return items, nil
	}
	ws, err := s.Q.ListDesignWeightsForMetal(ctx, db.ListDesignWeightsForMetalParams{Code: f.MetalCode, Column2: ids})
	if err != nil {
		return nil, err
	}
	idx := make(map[int64]*DesignListItem, len(items))
	for i := range items {
		idx[items[i].ID] = &items[i]
	}
	for _, w := range ws {
		it := idx[w.DesignID]
		it.NetMin, it.NetMax = widen(it.NetMin, it.NetMax, w.NetWeightG)
		if w.GrossWeightG.Valid {
			it.GrossMin, it.GrossMax = widen(it.GrossMin, it.GrossMax, w.GrossWeightG.Decimal)
		}
	}
	return items, nil
}

func widen(lo, hi *decimal.Decimal, v decimal.Decimal) (*decimal.Decimal, *decimal.Decimal) {
	if lo == nil || v.LessThan(*lo) {
		lo = &v
	}
	if hi == nil || v.GreaterThan(*hi) {
		hi = &v
	}
	return lo, hi
}

// BaseInput is either VolumeMM3, or RefWeightG + RefMetalCode.
type BaseInput struct {
	VolumeMM3    *decimal.Decimal
	RefWeightG   *decimal.Decimal
	RefMetalCode string
}

func (s *Service) resolveBase(ctx context.Context, q *db.Queries, b BaseInput) (vol, w decimal.NullDecimal, metal pgtype.Int8, err error) {
	hasVol := b.VolumeMM3 != nil
	hasRef := b.RefWeightG != nil || b.RefMetalCode != ""
	switch {
	case hasVol == hasRef:
		err = invalid("provide either volume_mm3, or ref_weight_g with ref_metal_code")
	case hasVol:
		if !b.VolumeMM3.IsPositive() {
			err = invalid("volume_mm3 must be positive")
			return
		}
		vol = nullDec(b.VolumeMM3)
	default:
		if b.RefWeightG == nil || b.RefMetalCode == "" || !b.RefWeightG.IsPositive() {
			err = invalid("ref_weight_g (positive) and ref_metal_code are both required")
			return
		}
		m, e := q.GetMetalByCode(ctx, b.RefMetalCode)
		if e != nil {
			err = invalid("unknown ref_metal_code %q", b.RefMetalCode)
			return
		}
		w = nullDec(b.RefWeightG)
		metal = pgtype.Int8{Int64: m.ID, Valid: true}
	}
	return
}

// CreateVariation adds the next -V{n} variation and computes its weights in
// the same transaction.
func (s *Service) CreateVariation(ctx context.Context, designID int64, b BaseInput) (VariationDetail, error) {
	var out VariationDetail
	err := s.inTx(ctx, func(q *db.Queries) error {
		d, err := q.GetDesignByID(ctx, designID)
		if err != nil {
			return err
		}
		vol, w, metal, err := s.resolveBase(ctx, q, b)
		if err != nil {
			return err
		}
		seq, err := q.NextVariationSeq(ctx, designID)
		if err != nil {
			return err
		}
		v, err := q.CreateVariation(ctx, db.CreateVariationParams{
			DesignID: designID, Seq: seq, Code: fmt.Sprintf("%s-V%d", d.Code, seq),
			BaseVolumeMm3: vol, BaseRefWeightG: w, BaseRefMetalID: metal,
		})
		if err != nil {
			return err
		}
		return s.reload(ctx, q, v.ID, &out)
	})
	return out, err
}

// UpdateVariationBase changes the base input and recomputes weights synchronously.
func (s *Service) UpdateVariationBase(ctx context.Context, id int64, b BaseInput) (VariationDetail, error) {
	var out VariationDetail
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetVariationForUpdate(ctx, id); err != nil {
			return err
		}
		vol, w, metal, err := s.resolveBase(ctx, q, b)
		if err != nil {
			return err
		}
		if _, err := q.UpdateVariationBaseInput(ctx, db.UpdateVariationBaseInputParams{
			ID: id, BaseVolumeMm3: vol, BaseRefWeightG: w, BaseRefMetalID: metal,
		}); err != nil {
			return err
		}
		return s.reload(ctx, q, id, &out)
	})
	return out, err
}

func (s *Service) reload(ctx context.Context, q *db.Queries, id int64, out *VariationDetail) error {
	if err := RecalcVariation(ctx, q, id); err != nil {
		return err
	}
	v, err := q.GetVariation(ctx, id)
	if err != nil {
		return err
	}
	w, err := nonNil(q.ListVariationWeights(ctx, id))
	if err != nil {
		return err
	}
	*out = VariationDetail{CadVariation: v, Weights: w}
	return nil
}
