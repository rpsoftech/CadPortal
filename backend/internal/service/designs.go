package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

type DesignInput struct {
	Code       string // leave empty: the code is assigned from the category. Ignored on update
	Name       string
	CategoryID int64
	TagIDs     []int64
}

func (s *Service) CreateDesign(ctx context.Context, in DesignInput) (db.MasterDesign, error) {
	if in.CategoryID == 0 {
		return db.MasterDesign{}, invalid("category_id is required")
	}
	var d db.MasterDesign
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		code := in.Code
		if code == "" { // the normal case: the number is assigned
			if code, err = allocateCode(ctx, q, in.CategoryID); err != nil {
				return err
			}
		}
		if d, err = q.CreateDesign(ctx, db.CreateDesignParams{Code: code, Name: in.Name, CategoryID: in.CategoryID}); err != nil {
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

// DeleteVariation deletes a variation so that it can be brought back: the row stays, hidden
// everywhere, with its seats, base input, CAD files and photos, until the trash purge removes
// it for good after the retention period (at once when Retention is 0 or less). Its code stays
// taken, and so do its CAD checksums, so a restore can never collide with a later upload.
func (s *Service) DeleteVariation(ctx context.Context, id int64) error {
	var keys []string
	err := s.inTx(ctx, func(q *db.Queries) error {
		var purgeAfter pgtype.Timestamptz
		if s.Retention > 0 {
			purgeAfter = pgtype.Timestamptz{Time: time.Now().Add(s.Retention), Valid: true}
		}
		v, err := q.SoftDeleteVariation(ctx, db.SoftDeleteVariationParams{ID: id, PurgeAfter: purgeAfter})
		if err != nil {
			return err
		}
		// A hidden variation must not feed any weight query; a restore recomputes them.
		if err := q.DeleteVariationWeights(ctx, id); err != nil {
			return err
		}
		if s.Retention > 0 {
			return nil
		}
		keys, err = purgeVariationRow(ctx, q, v)
		return err
	})
	if err != nil {
		return err
	}
	s.deleteObjects(ctx, keys...)
	return nil
}

// purgeVariationRow removes a deleted variation's row (seats, weights and photo rows cascade)
// and returns the stored objects that belonged to it.
func purgeVariationRow(ctx context.Context, q *db.Queries, v db.CadVariation) ([]string, error) {
	var keys []string
	for _, k := range []pgtype.Text{v.File3dmKey, v.FileStlKey} {
		if !k.Valid {
			continue
		}
		// A file that an order which is not cancelled recorded stays in storage, listed as removed with its
		// time already up; the purge deletes it once no such order holds it any more.
		held, err := q.ObjectKeyInOpenOrder(ctx, k.String)
		if err != nil {
			return nil, err
		}
		if !held {
			keys = append(keys, k.String)
			continue
		}
		if _, err := q.InsertTrashedFile(ctx, db.InsertTrashedFileParams{
			Kind: "cad", ObjectKey: k.String, Label: v.Code + " (kept for an order)", Reason: "variation_purged",
			PurgeAfter: pgtype.Timestamptz{Time: time.Now(), Valid: true},
		}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	photos, err := q.ListMediaForPurgedVariation(ctx, pgtype.Int8{Int64: v.ID, Valid: true})
	if err != nil {
		return nil, err
	}
	for _, p := range photos {
		keys = append(keys, p.ObjectKey)
		if p.ThumbKey.Valid {
			keys = append(keys, p.ThumbKey.String)
		}
	}
	return keys, q.HardDeleteVariation(ctx, v.ID)
}

// deleteObjects removes stored objects on a best-effort basis (an orphaned object is harmless,
// a row pointing at a missing object is not, so the row always goes first).
func (s *Service) deleteObjects(ctx context.Context, keys ...string) {
	if s.Store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, k := range keys {
		_ = s.Store.Delete(ctx, k)
	}
}

// RestoreVariation brings a deleted variation back with its seats, base input, CAD files and
// photos, and recomputes its weights in the same transaction. Its design must be live.
func (s *Service) RestoreVariation(ctx context.Context, id int64) (VariationDetail, error) {
	var out VariationDetail
	err := s.inTx(ctx, func(q *db.Queries) error {
		v, err := q.GetDeletedVariationForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if _, err := q.GetDesignByID(ctx, v.DesignID); errors.Is(err, pgx.ErrNoRows) {
			return invalid("%s belongs to a deleted design: restore the design first", v.Code)
		} else if err != nil {
			return err
		}
		if _, err := q.RestoreVariation(ctx, id); err != nil {
			return err
		}
		return s.reload(ctx, q, id, &out)
	})
	return out, err
}

const maxDesignNameRunes = 200

// DuplicateInput says what to copy. VariationIDs nil means every live variation.
type DuplicateInput struct {
	Name         string
	VariationIDs []int64
	CopyTags     bool
}

// DuplicateDesign creates a new design in the same category with the next automatic code, the
// same tags (optionally) and, for each chosen variation, a new variation with the same base
// input and stone seats, its weights computed in the same transaction. CAD files and photos are
// not copied: checksums are unique, and a copy of a file is the file. All or nothing.
func (s *Service) DuplicateDesign(ctx context.Context, sourceID int64, in DuplicateInput) (DesignDetail, error) {
	var newCode string
	err := s.inTx(ctx, func(q *db.Queries) error {
		src, err := q.GetDesignByID(ctx, sourceID) // a deleted source is not found
		if err != nil {
			return err
		}
		live, err := q.ListVariationsByDesign(ctx, src.ID)
		if err != nil {
			return err
		}
		chosen := live
		if in.VariationIDs != nil {
			byID := make(map[int64]bool, len(live))
			for _, v := range live {
				byID[v.ID] = true
			}
			picked := map[int64]bool{}
			for _, vid := range in.VariationIDs {
				if !byID[vid] {
					return invalid("variation %d is not a variation of %s", vid, src.Code)
				}
				picked[vid] = true
			}
			chosen = nil
			for _, v := range live { // keep the source order so V1, V2 stay in sequence
				if picked[v.ID] {
					chosen = append(chosen, v)
				}
			}
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = copyName(src.Name)
		}
		if r := []rune(name); len(r) > maxDesignNameRunes {
			name = string(r[:maxDesignNameRunes])
		}
		code, err := allocateCode(ctx, q, src.CategoryID)
		if err != nil {
			return err
		}
		d, err := q.CreateDesign(ctx, db.CreateDesignParams{Code: code, Name: name, CategoryID: src.CategoryID})
		if err != nil {
			return err
		}
		newCode = d.Code
		if in.CopyTags {
			tags, err := q.ListDesignTags(ctx, src.ID)
			if err != nil {
				return err
			}
			ids := make([]int64, len(tags))
			for i, t := range tags {
				ids[i] = t.ID
			}
			if err := setTags(ctx, q, d.ID, ids); err != nil {
				return err
			}
		}
		for _, sv := range chosen {
			seq, err := q.NextVariationSeq(ctx, d.ID)
			if err != nil {
				return err
			}
			nv, err := q.CreateVariation(ctx, db.CreateVariationParams{
				DesignID: d.ID, Seq: seq, Code: fmt.Sprintf("%s-V%d", d.Code, seq),
				BaseVolumeMm3: sv.BaseVolumeMm3, BaseRefWeightG: sv.BaseRefWeightG, BaseRefMetalID: sv.BaseRefMetalID,
			})
			if err != nil {
				return err
			}
			if err := q.CloneSeats(ctx, db.CloneSeatsParams{TargetID: nv.ID, SourceID: sv.ID}); err != nil {
				return err
			}
			if err := RecalcVariation(ctx, q, nv.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return DesignDetail{}, err
	}
	return s.GetDesignByCode(ctx, newCode)
}

// copyName is "<name> (copy)", cut so the suffix always survives.
func copyName(name string) string {
	const suffix = " (copy)"
	name = strings.TrimSpace(name)
	if name == "" {
		return strings.TrimSpace(suffix)
	}
	if r := []rune(name); len(r) > maxDesignNameRunes-len(suffix) {
		name = string(r[:maxDesignNameRunes-len(suffix)])
	}
	return name + suffix
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
	Media      []MediaItem       `json:"media"` // photos and videos; empty when file storage is off
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
	if out.Media, err = s.ListMedia(ctx, d.ID); err != nil {
		return out, err
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
	// StoneCode picks the stone code the stone figures are computed for (flexible seats use it, locked
	// seats keep their own); empty means the baseline code. MinStonePct/MaxStonePct are percentages of the
	// gross weight and need a MetalCode.
	StoneCode   string
	MinStonePct *decimal.Decimal
	MaxStonePct *decimal.Decimal
	// Sort is "" (design code), or net/gross/stone_pct with _asc or _desc; those need a MetalCode.
	Sort   string
	Limit  int32
	Offset int32
}

var validSorts = map[string]bool{
	"": true, "code": true, "net_asc": true, "net_desc": true, "gross_asc": true, "gross_desc": true,
	"stone_pct_asc": true, "stone_pct_desc": true,
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
	// CoverURL is a signed link to the cover photo's thumbnail (empty when the design has no photo).
	CoverURL string           `json:"cover_url"`
	NetMin   *decimal.Decimal `json:"net_min"`
	NetMax   *decimal.Decimal `json:"net_max"`
	GrossMin *decimal.Decimal `json:"gross_min"`
	GrossMax *decimal.Decimal `json:"gross_max"`

	// Shown hides the raw JSON column of the row (the fields below carry its content).
	Shown *struct{} `json:"shown,omitempty"`
	// With a metal selected: the figures of the shown variation for that metal and StoneCode. The stone
	// figures are null with StoneStatus "no_data" when a matrix row is missing (never 0).
	StoneCode          string           `json:"stone_code"`
	ShownVariationID   *int64           `json:"shown_variation_id"`
	ShownVariationCode string           `json:"shown_variation_code"`
	NetG               *decimal.Decimal `json:"net_g"`
	StoneCarats        *decimal.Decimal `json:"stone_carats"`
	StoneG             *decimal.Decimal `json:"stone_g"`
	GrossG             *decimal.Decimal `json:"gross_g"`
	StonePct           *decimal.Decimal `json:"stone_pct"`
	StoneStatus        string           `json:"stone_status"` // "", "ok" or "no_data"
	StoneNote          string           `json:"stone_note"`   // e.g. "no data for 2HD"
}

// shownFigures is the JSON the list query builds for the shown variation.
type shownFigures struct {
	VariationID   int64            `json:"variation_id"`
	VariationCode string           `json:"variation_code"`
	NetG          decimal.Decimal  `json:"net_g"`
	Carats        *decimal.Decimal `json:"carats"`
	StoneG        *decimal.Decimal `json:"stone_g"`
	GrossG        *decimal.Decimal `json:"gross_g"`
	StonePct      *decimal.Decimal `json:"stone_pct"`
}

func checkStonePct(name string, d *decimal.Decimal) error {
	if d == nil {
		return nil
	}
	if d.IsNegative() || d.GreaterThan(hundred) || !d.Equal(d.Round(2)) {
		return invalid("%s must be between 0 and 100 with at most 2 decimals", name)
	}
	return nil
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
	if !validSorts[f.Sort] {
		return nil, invalid("sort must be one of net_asc, net_desc, gross_asc, gross_desc, stone_pct_asc, stone_pct_desc")
	}
	if f.Sort == "code" {
		f.Sort = ""
	}
	if err := checkStonePct("min_stone_pct", f.MinStonePct); err != nil {
		return nil, err
	}
	if err := checkStonePct("max_stone_pct", f.MaxStonePct); err != nil {
		return nil, err
	}
	if f.MetalCode == "" && (f.MinStonePct != nil || f.MaxStonePct != nil || f.Sort != "") {
		return nil, invalid("metal_code is required with min_stone_pct, max_stone_pct and sort")
	}
	// the stone code every figure is computed for: the chosen one, else the baseline
	var stoneID pgtype.Int8
	stoneCode := f.StoneCode
	if stoneCode != "" {
		sc, err := s.Q.GetStoneCodeByCode(ctx, stoneCode)
		if err != nil {
			if mapErr(err) == ErrNotFound {
				return nil, invalid("unknown stone code %q", stoneCode)
			}
			return nil, err
		}
		stoneID = pgtype.Int8{Int64: sc.ID, Valid: true}
	} else if bc, err := s.Q.GetBaselineStoneCode(ctx); err == nil {
		stoneID, stoneCode = pgtype.Int8{Int64: bc.ID, Valid: true}, bc.Code
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	p := db.ListDesignsParams{
		Q:      pgText(likePattern(f.Query)),
		TagIds: f.TagIDs, MetalCode: pgText(f.MetalCode),
		MinNetWt: nullDec(f.MinNetWt), MaxNetWt: nullDec(f.MaxNetWt),
		MinGrossWt: nullDec(f.MinGrossWt), MaxGrossWt: nullDec(f.MaxGrossWt),
		MinStonePct: nullDec(f.MinStonePct), MaxStonePct: nullDec(f.MaxStonePct),
		MaxStoneCts: nullDec(f.MaxStoneCts), StoneCodeID: stoneID, Sort: f.Sort,
		Lim: f.Limit, Off: f.Offset,
	}
	// the stone sums are only needed for every variation when they decide the filter or the order
	p.StoneNeeded = p.MinGrossWt.Valid || p.MaxGrossWt.Valid || p.MinStonePct.Valid || p.MaxStonePct.Valid ||
		strings.HasPrefix(f.Sort, "gross") || strings.HasPrefix(f.Sort, "stone_pct")
	p.CalcNeeded = f.MetalCode != "" || p.MinNetWt.Valid || p.MaxNetWt.Valid || p.MinGrossWt.Valid ||
		p.MaxGrossWt.Valid || p.MaxStoneCts.Valid
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
		if r.CoverThumbKey.Valid && s.Store != nil {
			// A failed signature only costs the card its picture, never the whole list.
			if u, err := s.Store.PresignView(ctx, r.CoverThumbKey.String, "image/jpeg", MediaLinkTTL); err == nil {
				items[i].CoverURL = u
			}
		}
		items[i].StoneCode = stoneCode
		ids[i] = r.ID
	}
	if f.MetalCode == "" || len(items) == 0 {
		return items, nil
	}
	ws, err := s.Q.ListDesignVariationFigures(ctx, db.ListDesignVariationFiguresParams{
		Code: f.MetalCode, DesignIds: ids, StoneCodeID: stoneID})
	if err != nil {
		return nil, err
	}
	idx := make(map[int64]*DesignListItem, len(items))
	for i := range items {
		idx[items[i].ID] = &items[i]
	}
	byDesign := make(map[int64][]shownFigures, len(items))
	for _, w := range ws {
		var sf shownFigures
		if err := json.Unmarshal(w.Figures, &sf); err != nil {
			return nil, fmt.Errorf("decode variation figures: %w", err)
		}
		byDesign[w.DesignID] = append(byDesign[w.DesignID], sf)
		it := idx[w.DesignID]
		it.NetMin, it.NetMax = widen(it.NetMin, it.NetMax, sf.NetG)
		if sf.GrossG != nil {
			it.GrossMin, it.GrossMax = widen(it.GrossMin, it.GrossMax, *sf.GrossG)
		}
	}
	for i := range items {
		if pick := pickShown(byDesign[items[i].ID], rows[i].Shown, p.StoneNeeded, f); pick != nil {
			applyShown(&items[i], pick, stoneCode)
		}
	}
	return items, nil
}

// pickShown chooses the variation a card shows. When the stone sums decided the filter or the order, the
// query already chose it (shown); otherwise it is the first variation passing the net filters, preferring
// one that has stone data.
func pickShown(vs []shownFigures, shown []byte, stoneDecided bool, f DesignFilter) *shownFigures {
	if stoneDecided {
		var sf shownFigures
		if json.Unmarshal(shown, &sf) != nil {
			return nil
		}
		for i := range vs {
			if vs[i].VariationID == sf.VariationID {
				return &vs[i]
			}
		}
		return nil
	}
	var first *shownFigures
	for i := range vs {
		v := &vs[i]
		if f.MinNetWt != nil && v.NetG.LessThan(*f.MinNetWt) || f.MaxNetWt != nil && v.NetG.GreaterThan(*f.MaxNetWt) {
			continue
		}
		if v.Carats != nil {
			return v
		}
		if first == nil {
			first = v
		}
	}
	return first
}

// applyShown copies a variation's figures onto the card and flags missing stone data.
func applyShown(it *DesignListItem, sf *shownFigures, stoneCode string) {
	it.ShownVariationID, it.ShownVariationCode = &sf.VariationID, sf.VariationCode
	it.NetG, it.StoneCarats, it.StoneG, it.GrossG, it.StonePct = &sf.NetG, sf.Carats, sf.StoneG, sf.GrossG, sf.StonePct
	if sf.Carats != nil {
		it.StoneStatus = "ok"
		return
	}
	it.StoneStatus = "no_data"
	it.StoneNote = "no data for " + stoneCode
	if stoneCode == "" {
		it.StoneNote = "no stone code configured"
	}
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
