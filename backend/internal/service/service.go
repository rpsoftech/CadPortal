// Package service holds business logic between the HTTP handlers and sqlc queries.
package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/mailer"
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

// friendly replaces the database's raw conflict detail with a message an admin can act on.
func friendly(err error, msg string) error {
	if err = mapErr(err); errors.Is(err, ErrConflict) {
		return fmt.Errorf("%w: %s", ErrConflict, msg)
	}
	return err
}

// nonNil turns a nil slice into an empty one. Go marshals a nil slice as JSON null, which
// clients then trip over; list endpoints must always return [].
func nonNil[T any](s []T, err error) ([]T, error) {
	if s == nil {
		s = []T{}
	}
	return s, err
}

func pgText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

func newTicker() *time.Ticker { return time.NewTicker(5 * time.Second) }

type Service struct {
	Pool       *pgxpool.Pool
	Q          *db.Queries
	Store      storage.Store // nil disables CAD upload/download
	PresignTTL time.Duration
	// Retention is how long a removed or replaced file is kept before it is deleted for good; 0 or less
	// deletes it at once. See retire and PurgeTrash.
	Retention time.Duration
	// BackupMaxAge, when positive, makes the setup checklist warn if the backup service has not finished a
	// good run within this time. Zero leaves backups out of the checklist (local development).
	BackupMaxAge time.Duration
	// BackupBucket is a read-only view of the backup bucket; nil hides the dump list and downloads.
	BackupBucket BackupBucket
	// Mail sends password reset emails; nil (with AppBaseURL empty) turns self-service reset off.
	Mail mailer.Sender
	// AppBaseURL is the public address of the portal (https://cad.example.com), used in emailed links.
	AppBaseURL string

	mailWG sync.WaitGroup
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
	return nonNil(s.Q.ListCategories(ctx))
}

func (s *Service) CreateCategory(ctx context.Context, name, prefix string) (db.Category, error) {
	if name == "" {
		return db.Category{}, invalid("name is required")
	}
	prefix, err := normalizePrefix(prefix)
	if err != nil {
		return db.Category{}, err
	}
	c, err := s.Q.CreateCategory(ctx, db.CreateCategoryParams{Name: name, CodePrefix: pgText(prefix)})
	return c, friendly(err, "a category with that name or code prefix already exists")
}

var tagGroups = map[string]bool{"Audience": true, "Sub-Style": true, "Motif": true, "Attribute": true}

func (s *Service) ListTags(ctx context.Context) ([]db.Tag, error) { return nonNil(s.Q.ListTags(ctx)) }

func (s *Service) CreateTag(ctx context.Context, name, group string) (db.Tag, error) {
	if name == "" {
		return db.Tag{}, invalid("name is required")
	}
	if !tagGroups[group] {
		return db.Tag{}, invalid("tag_group must be Audience, Sub-Style, Motif or Attribute")
	}
	t, err := s.Q.CreateTag(ctx, db.CreateTagParams{Name: name, TagGroup: group})
	return t, friendly(err, "that group already has a tag with this name")
}

func (s *Service) ListStoneCodes(ctx context.Context) ([]db.StoneCode, error) {
	return nonNil(s.Q.ListStoneCodes(ctx))
}

func (s *Service) ListMetals(ctx context.Context) ([]db.Metal, error) {
	return nonNil(s.Q.ListMetals(ctx))
}

type MetalUpdate struct {
	SpecificGravity decimal.Decimal
	WastagePct      decimal.NullDecimal
	IsDefaultInput  bool
	Active          bool
}

// UpdateMetal edits a metal. An SG or active change enqueues a weight recalc.
func (s *Service) UpdateMetal(ctx context.Context, id int64, u MetalUpdate) (db.Metal, error) {
	if err := checkSG(u.SpecificGravity); err != nil {
		return db.Metal{}, err
	}
	if err := checkWastage(u.WastagePct); err != nil {
		return db.Metal{}, err
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
	return nonNil(s.Q.ListStoneMatrix(ctx))
}

type MatrixRowInput struct {
	SizeMM         decimal.Decimal
	StoneCode      string
	Sieve          string
	CaratsPerStone decimal.Decimal
	RatePerCt      decimal.NullDecimal
	RatePerPc      decimal.NullDecimal
	RatePerG       decimal.NullDecimal // price per gram of stone weight (carats x 0.200)
	// RateBasis is "pc" (price per piece, the default), "ct" (per carat) or "g" (per gram): which price costing uses.
	// Empty means "pc", except that a row giving only a per-carat price keeps meaning per carat, so
	// older clients and files behave as they always did.
	RateBasis string
}

// UpdateStoneMatrix upserts rows and optionally moves the baseline code.
// Changes to baseline-code carats or the baseline itself enqueue a recalc.
func (s *Service) UpdateStoneMatrix(ctx context.Context, rows []MatrixRowInput, baselineCode string) error {
	if len(rows) > 0 {
		if err := ValidateMatrixRows(rows); err != nil {
			return err
		}
	}
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
				RatePerPc: r.RatePerPc, RatePerG: r.RatePerG, RateBasis: EffectiveBasis(r),
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

func pgInt8(v int64) pgtype.Int8 { return pgtype.Int8{Int64: v, Valid: true} }

// EffectiveBasis is the pricing basis a matrix row ends up with (see MatrixRowInput.RateBasis).
func EffectiveBasis(r MatrixRowInput) string {
	switch r.RateBasis {
	case "pc", "ct", "g":
		return r.RateBasis
	}
	if r.RatePerCt.Valid && !r.RatePerPc.Valid {
		return "ct"
	}
	return "pc"
}
