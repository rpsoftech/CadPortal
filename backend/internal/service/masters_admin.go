package service

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

const maxMatrixRows = 5000

var (
	metalCodeRe = regexp.MustCompile(`^[A-Z0-9]{2,12}$`)
	stoneCodeRe = regexp.MustCompile(`^[A-Z0-9]{1,12}$`)
	hundred     = decimal.NewFromInt(100)
)

// checkDP rejects a value with more decimal places than the column stores, which
// Postgres would otherwise round silently.
func checkDP(name string, d decimal.Decimal, dp int32) error {
	if !d.Equal(d.Round(dp)) {
		return invalid("%s allows at most %d decimal places", name, dp)
	}
	return nil
}

func checkWastage(w decimal.NullDecimal) error {
	if !w.Valid {
		return nil
	}
	if w.Decimal.IsNegative() || w.Decimal.GreaterThan(hundred) {
		return invalid("wastage_pct must be between 0 and 100")
	}
	return checkDP("wastage_pct", w.Decimal, 3)
}

func checkSG(sg decimal.Decimal) error {
	if !sg.IsPositive() {
		return invalid("specific_gravity must be positive")
	}
	if sg.GreaterThan(decimal.NewFromInt(100)) {
		return invalid("specific_gravity looks wrong (over 100)")
	}
	return checkDP("specific_gravity", sg, 4)
}

// ---------- Categories and tags ----------

func (s *Service) UpdateCategory(ctx context.Context, id int64, name, prefix string) (db.Category, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.Category{}, invalid("name is required")
	}
	c, err := s.Q.UpdateCategory(ctx, db.UpdateCategoryParams{ID: id, Name: name, CodePrefix: pgText(strings.TrimSpace(prefix))})
	return c, friendly(err, "another category already uses that name or code prefix")
}

// ListAllTags includes deactivated tags (for the admin screen).
func (s *Service) ListAllTags(ctx context.Context) ([]db.Tag, error) { return s.Q.ListAllTags(ctx) }

func (s *Service) UpdateTag(ctx context.Context, id int64, name, group string, active bool) (db.Tag, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return db.Tag{}, invalid("name is required")
	}
	if !tagGroups[group] {
		return db.Tag{}, invalid("tag_group must be Audience, Sub-Style, Motif or Attribute")
	}
	t, err := s.Q.UpdateTag(ctx, db.UpdateTagParams{ID: id, Name: name, TagGroup: group, Active: active})
	return t, friendly(err, "that group already has a tag with this name")
}

// ---------- Metals and stone codes ----------

type MetalCreate struct {
	Code            string
	SpecificGravity decimal.Decimal
	WastagePct      decimal.NullDecimal
	IsDefaultInput  bool
	Active          bool
}

// CreateMetal adds an alloy. Every variation gets a weight row for it through a
// recalc job, so the catalog fills in without manual work.
func (s *Service) CreateMetal(ctx context.Context, in MetalCreate) (db.Metal, error) {
	code := strings.ToUpper(strings.TrimSpace(in.Code))
	if !metalCodeRe.MatchString(code) {
		return db.Metal{}, invalid("code must be 2 to 12 letters or digits, e.g. 18KW")
	}
	if err := checkSG(in.SpecificGravity); err != nil {
		return db.Metal{}, err
	}
	if err := checkWastage(in.WastagePct); err != nil {
		return db.Metal{}, err
	}
	var out db.Metal
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		if out, err = q.CreateMetal(ctx, db.CreateMetalParams{
			Code: code, SpecificGravity: in.SpecificGravity, WastagePct: in.WastagePct,
			IsDefaultInput: false, Active: in.Active,
		}); err != nil {
			return friendly(err, "an alloy with that code already exists")
		}
		if in.IsDefaultInput {
			if err := q.ClearDefaultInputMetal(ctx, out.ID); err != nil {
				return err
			}
			if out, err = q.UpdateMetal(ctx, db.UpdateMetalParams{
				ID: out.ID, SpecificGravity: out.SpecificGravity, WastagePct: out.WastagePct,
				IsDefaultInput: true, Active: out.Active,
			}); err != nil {
				return err
			}
		}
		_, err = q.EnqueueRecalcJob(ctx, "metal_sg")
		return err
	})
	return out, err
}

func (s *Service) CreateStoneCode(ctx context.Context, code string) (db.StoneCode, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if !stoneCodeRe.MatchString(code) {
		return db.StoneCode{}, invalid("code must be 1 to 12 letters or digits, e.g. 3HD")
	}
	c, err := s.Q.CreateStoneCode(ctx, code)
	return c, friendly(err, "that stone code already exists")
}

// SetStoneCodeActive hides or shows a stone code. The baseline code cannot be
// deactivated: weights are computed from it.
func (s *Service) SetStoneCodeActive(ctx context.Context, id int64, active bool) (db.StoneCode, error) {
	var out db.StoneCode
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetStoneCodeForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur.IsDefaultBaseline && !active {
			return invalid("the baseline stone code cannot be deactivated; choose another baseline first")
		}
		out, err = q.SetStoneCodeActive(ctx, db.SetStoneCodeActiveParams{ID: id, Active: active})
		return err
	})
	return out, err
}

// ---------- Stone matrix ----------

// ValidateMatrixRows checks everything the database would otherwise round or reject.
func ValidateMatrixRows(rows []MatrixRowInput) error {
	if len(rows) == 0 {
		return invalid("rows must not be empty")
	}
	if len(rows) > maxMatrixRows {
		return invalid("at most %d rows per request", maxMatrixRows)
	}
	seen := map[string]bool{}
	for i, r := range rows {
		n := i + 1
		if strings.TrimSpace(r.StoneCode) == "" {
			return invalid("row %d: stone_code is required", n)
		}
		if !r.SizeMM.IsPositive() || r.SizeMM.GreaterThanOrEqual(decimal.NewFromInt(1000)) {
			return invalid("row %d: size_mm must be positive and under 1000", n)
		}
		if err := checkDP(fmt.Sprintf("row %d: size_mm", n), r.SizeMM, 2); err != nil {
			return err
		}
		if r.CaratsPerStone.IsNegative() || r.CaratsPerStone.GreaterThanOrEqual(decimal.NewFromInt(10000)) {
			return invalid("row %d: carats_per_stone must be zero or more and under 10000", n)
		}
		if err := checkDP(fmt.Sprintf("row %d: carats_per_stone", n), r.CaratsPerStone, 4); err != nil {
			return err
		}
		if r.RatePerCt.Valid {
			if r.RatePerCt.Decimal.IsNegative() {
				return invalid("row %d: rate_per_ct must not be negative", n)
			}
			if err := checkDP(fmt.Sprintf("row %d: rate_per_ct", n), r.RatePerCt.Decimal, 2); err != nil {
				return err
			}
		}
		key := r.SizeMM.StringFixed(2) + "|" + strings.ToUpper(strings.TrimSpace(r.StoneCode))
		if seen[key] {
			return invalid("row %d: duplicate (size_mm, stone_code) in the same request", n)
		}
		seen[key] = true
	}
	return nil
}

// DeleteMatrixRow removes one matrix row. It refuses while a seat is locked to that
// (size, code), because deleting would silently unlock it and change its BOM.
func (s *Service) DeleteMatrixRow(ctx context.Context, size decimal.Decimal, stoneCode string) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		code, err := q.GetStoneCodeByCode(ctx, strings.ToUpper(strings.TrimSpace(stoneCode)))
		if err != nil {
			return ErrNotFound
		}
		if n, err := q.CountSeatsLockedTo(ctx, db.CountSeatsLockedToParams{SizeMm: size, LockedStoneCodeID: pgInt8(code.ID)}); err != nil {
			return err
		} else if n > 0 {
			return fmt.Errorf("%w: %d seat(s) are locked to %s at %s mm; unlock them first", ErrConflict, n, code.Code, size.StringFixed(2))
		}
		n, err := q.DeleteStoneMatrixRow(ctx, db.DeleteStoneMatrixRowParams{SizeMm: size, StoneCodeID: code.ID})
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
		if code.IsDefaultBaseline {
			_, err = q.EnqueueRecalcJob(ctx, "matrix_baseline")
		}
		return err
	})
}

func (s *Service) MatrixGaps(ctx context.Context) ([]db.ListMatrixGapsRow, error) {
	rows, err := s.Q.ListMatrixGaps(ctx)
	if rows == nil {
		rows = []db.ListMatrixGapsRow{}
	}
	return rows, err
}

// RecalcStatus tells the admin screen whether weights are still being rebuilt.
type RecalcStatus struct {
	Pending   int32  `json:"pending"`
	Running   int32  `json:"running"`
	Failed    int32  `json:"failed"`
	LastError string `json:"last_error"`
}

func (s *Service) RecalcStatus(ctx context.Context) (RecalcStatus, error) {
	st, err := s.Q.RecalcQueueStatus(ctx)
	if err != nil {
		return RecalcStatus{}, err
	}
	out := RecalcStatus{Pending: st.Pending, Running: st.Running, Failed: st.Failed}
	if st.Failed > 0 {
		out.LastError, _ = s.Q.LastFailedRecalcError(ctx)
	}
	return out, nil
}
