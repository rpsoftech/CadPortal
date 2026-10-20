// Package service holds business logic between the HTTP handlers and sqlc queries.
package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")
	ErrInvalid  = errors.New("invalid input")
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// mapErr translates pgx/pg errors into the sentinel errors above.
func mapErr(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case "23505":
			return fmt.Errorf("%w: %s", ErrConflict, pe.Detail)
		case "23503", "23514", "22P02":
			return fmt.Errorf("%w: %s", ErrInvalid, pe.Message)
		}
	}
	return err
}

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func newTicker() *time.Ticker { return time.NewTicker(5 * time.Second) }

type Service struct {
	Pool       *pgxpool.Pool
	Q          *db.Queries
	Store      storage.Store // nil disables CAD upload/download
	PresignTTL time.Duration
}

func New(pool *pgxpool.Pool) *Service { return &Service{Pool: pool, Q: db.New(pool)} }

// inTx runs fn in a transaction.
func (s *Service) inTx(ctx context.Context, fn func(q *db.Queries) error) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if err := fn(db.New(tx)); err != nil {
		return mapErr(err)
	}
	return mapErr(tx.Commit(ctx))
}

// ---------- Masters ----------

func (s *Service) ListCategories(ctx context.Context) ([]db.Category, error) {
	return s.Q.ListCategories(ctx)
}

func (s *Service) CreateCategory(ctx context.Context, name, prefix string) (db.Category, error) {
	if name == "" {
		return db.Category{}, invalid("name is required")
	}
	c, err := s.Q.CreateCategory(ctx, db.CreateCategoryParams{Name: name, CodePrefix: pgText(prefix)})
	return c, mapErr(err)
}

var tagGroups = map[string]bool{"Audience": true, "Sub-Style": true, "Motif": true, "Attribute": true}

func (s *Service) ListTags(ctx context.Context) ([]db.Tag, error) { return s.Q.ListTags(ctx) }

func (s *Service) CreateTag(ctx context.Context, name, group string) (db.Tag, error) {
	if name == "" {
		return db.Tag{}, invalid("name is required")
	}
	if !tagGroups[group] {
		return db.Tag{}, invalid("tag_group must be Audience, Sub-Style, Motif or Attribute")
	}
	t, err := s.Q.CreateTag(ctx, db.CreateTagParams{Name: name, TagGroup: group})
	return t, mapErr(err)
}

func (s *Service) ListStoneCodes(ctx context.Context) ([]db.StoneCode, error) {
	return s.Q.ListStoneCodes(ctx)
}

func (s *Service) ListMetals(ctx context.Context) ([]db.Metal, error) { return s.Q.ListMetals(ctx) }

type MetalUpdate struct {
	SpecificGravity decimal.Decimal
	WastagePct      decimal.NullDecimal
	IsDefaultInput  bool
	Active          bool
}

// UpdateMetal edits a metal. An SG or active change enqueues a weight recalc.
func (s *Service) UpdateMetal(ctx context.Context, id int64, u MetalUpdate) (db.Metal, error) {
	if !u.SpecificGravity.IsPositive() {
		return db.Metal{}, invalid("specific_gravity must be positive")
	}
	var out db.Metal
	err := s.inTx(ctx, func(q *db.Queries) error {
		old, err := q.GetMetalForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if u.IsDefaultInput {
			if err := q.ClearDefaultInputMetal(ctx, id); err != nil {
				return err
			}
		}
		out, err = q.UpdateMetal(ctx, db.UpdateMetalParams{
			ID: id, SpecificGravity: u.SpecificGravity, WastagePct: u.WastagePct,
			IsDefaultInput: u.IsDefaultInput, Active: u.Active,
		})
		if err != nil {
			return err
		}
		// Default-input flag feeds baseline_gross_weight_g, so it also needs a rebuild.
		if !old.SpecificGravity.Equal(u.SpecificGravity) || old.Active != u.Active ||
			old.IsDefaultInput != u.IsDefaultInput {
			_, err = q.EnqueueRecalcJob(ctx, "metal_sg")
		}
		return err
	})
	return out, err
}

func (s *Service) ListStoneMatrix(ctx context.Context) ([]db.ListStoneMatrixRow, error) {
	return s.Q.ListStoneMatrix(ctx)
}

type MatrixRowInput struct {
	SizeMM         decimal.Decimal
	StoneCode      string
	Sieve          string
	CaratsPerStone decimal.Decimal
	RatePerCt      decimal.NullDecimal
}

// UpdateStoneMatrix upserts rows and optionally moves the baseline code.
// Changes to baseline-code carats or the baseline itself enqueue a recalc.
func (s *Service) UpdateStoneMatrix(ctx context.Context, rows []MatrixRowInput, baselineCode string) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		codes, err := q.ListStoneCodes(ctx)
		if err != nil {
			return err
		}
		byCode := map[string]db.StoneCode{}
		baselineID := int64(0)
		for _, c := range codes {
			byCode[c.Code] = c
			if c.IsDefaultBaseline {
				baselineID = c.ID
			}
		}
		recalc := false

		if baselineCode != "" {
			c, ok := byCode[baselineCode]
			if !ok {
				return invalid("unknown stone code %q", baselineCode)
			}
			if c.ID != baselineID {
				if err := q.SetBaselineStoneCode(ctx, c.ID); err != nil {
					return err
				}
				baselineID, recalc = c.ID, true
			}
		}
		for _, r := range rows {
			c, ok := byCode[r.StoneCode]
			if !ok {
				return invalid("unknown stone code %q", r.StoneCode)
			}
			if !r.SizeMM.IsPositive() || r.CaratsPerStone.IsNegative() {
				return invalid("size_mm must be positive and carats_per_stone non-negative")
			}
			if _, err := q.UpsertStoneMatrixRow(ctx, db.UpsertStoneMatrixRowParams{
				SizeMm: r.SizeMM, StoneCodeID: c.ID, Sieve: pgText(r.Sieve),
				CaratsPerStone: r.CaratsPerStone, RatePerCt: r.RatePerCt,
			}); err != nil {
				return err
			}
			if c.ID == baselineID {
				recalc = true
			}
		}
		if recalc {
			_, err = q.EnqueueRecalcJob(ctx, "matrix_baseline")
		}
		return err
	})
}
