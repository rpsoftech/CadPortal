package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// recalcLockKey is the pg_advisory_xact_lock key shared by every recalc so
// two recalcs never interleave.
const recalcLockKey int64 = 0x43414457 // "CADW"

func loadMatrix(ctx context.Context, q *db.Queries) (calc.Matrix, error) {
	rows, err := q.ListMatrixAll(ctx)
	if err != nil {
		return nil, err
	}
	m := calc.Matrix{}
	for _, r := range rows {
		m.Set(r.SizeMm, r.StoneCode, r.CaratsPerStone)
	}
	return m, nil
}

// recalcInputs is the master data a recalc needs; load once, reuse per variation.
type recalcInputs struct {
	metals       []db.Metal
	metalByID    map[int64]db.Metal
	matrix       calc.Matrix
	baselineCode string // "" when no baseline code is configured
}

func loadRecalcInputs(ctx context.Context, q *db.Queries) (*recalcInputs, error) {
	metals, err := q.ListActiveMetals(ctx)
	if err != nil {
		return nil, err
	}
	all, err := q.ListMetals(ctx) // reference metal may be inactive
	if err != nil {
		return nil, err
	}
	in := &recalcInputs{metals: metals, metalByID: map[int64]db.Metal{}}
	for _, m := range all {
		in.metalByID[m.ID] = m
	}
	if in.matrix, err = loadMatrix(ctx, q); err != nil {
		return nil, err
	}
	bc, err := q.GetBaselineStoneCode(ctx)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	if err == nil {
		in.baselineCode = bc.Code
	}
	return in, nil
}

// RecalcVariation rebuilds variation_metal_weights and the baseline rollups
// for one variation. Run inside the caller's transaction.
func RecalcVariation(ctx context.Context, q *db.Queries, variationID int64) error {
	in, err := loadRecalcInputs(ctx, q)
	if err != nil {
		return err
	}
	return recalcVariation(ctx, q, in, variationID)
}

func recalcVariation(ctx context.Context, q *db.Queries, in *recalcInputs, variationID int64) error {
	v, err := q.GetVariation(ctx, variationID)
	if err != nil {
		return err
	}
	dbSeats, err := q.ListVariationSeats(ctx, variationID)
	if err != nil {
		return err
	}
	seats := make([]calc.Seat, len(dbSeats))
	for i, s := range dbSeats {
		seats[i] = calc.Seat{SizeMM: s.SizeMm, Count: int(s.StoneCount), LockedCode: s.LockedCode.String}
	}

	// Baseline stone carats (flexible seats use the baseline code). Gaps leave
	// the gross NULL rather than silently treating missing rows as zero.
	baselineCarats, baselineOK := decimal.Zero, true
	if len(seats) > 0 {
		if in.baselineCode == "" {
			baselineOK = false
		} else if bom, err := calc.ResolveBOM(seats, in.baselineCode, in.matrix); err != nil {
			var mme *calc.MissingMatrixError
			if !errors.As(err, &mme) {
				return err
			}
			baselineOK = false
		} else {
			baselineCarats = bom.TotalCarats
		}
	}

	if err := q.DeleteVariationWeights(ctx, variationID); err != nil {
		return err
	}
	var defaultGross decimal.NullDecimal
	for _, m := range in.metals {
		net, err := netFor(v, in, m)
		if err != nil {
			return err
		}
		net = calc.RoundGrams(net)
		var gross decimal.NullDecimal
		if baselineOK {
			// Gross is derived from the rounded stored carats so the cache is self-consistent.
			gross = decimal.NullDecimal{Valid: true,
				Decimal: calc.RoundGrams(calc.Gross(net, calc.RoundCarats(baselineCarats)))}
		}
		if err := q.UpsertVariationWeight(ctx, db.UpsertVariationWeightParams{
			VariationID: variationID, MetalID: m.ID, NetWeightG: net, GrossWeightG: gross,
		}); err != nil {
			return err
		}
		if m.IsDefaultInput {
			defaultGross = gross
		}
	}
	return q.UpdateVariationBaseline(ctx, db.UpdateVariationBaselineParams{
		ID:                   variationID,
		BaselineStoneCarats:  calc.RoundCarats(baselineCarats),
		BaselineGrossWeightG: defaultGross,
	})
}

func netFor(v db.CadVariation, in *recalcInputs, target db.Metal) (decimal.Decimal, error) {
	if v.BaseVolumeMm3.Valid {
		return calc.NetFromVolume(v.BaseVolumeMm3.Decimal, target.SpecificGravity), nil
	}
	if !v.BaseRefWeightG.Valid || !v.BaseRefMetalID.Valid {
		return decimal.Zero, fmt.Errorf("variation %d has no base input", v.ID)
	}
	ref, ok := in.metalByID[v.BaseRefMetalID.Int64]
	if !ok {
		return decimal.Zero, fmt.Errorf("variation %d: unknown reference metal", v.ID)
	}
	return calc.NetFromReference(v.BaseRefWeightG.Decimal, target.SpecificGravity, ref.SpecificGravity)
}

// ProcessNextRecalcJob claims one pending job (FOR UPDATE SKIP LOCKED), takes
// the advisory lock and rebuilds every variation. Returns false when idle.
func ProcessNextRecalcJob(ctx context.Context, pool *pgxpool.Pool) (bool, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	q := db.New(tx)
	job, err := q.ClaimRecalcJob(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", recalcLockKey); err != nil {
		return true, err
	}

	runErr := recalcAll(ctx, q)
	if runErr != nil {
		// Roll back partial work, then record the failure in its own tx.
		tx.Rollback(ctx) //nolint:errcheck
		_ = db.New(pool).FinishRecalcJob(ctx, db.FinishRecalcJobParams{
			ID: job.ID, Status: "failed", Error: pgText(runErr.Error()),
		})
		return true, runErr
	}
	if err := q.FinishRecalcJob(ctx, db.FinishRecalcJobParams{ID: job.ID, Status: "done"}); err != nil {
		return true, err
	}
	return true, tx.Commit(ctx)
}

func recalcAll(ctx context.Context, q *db.Queries) error {
	in, err := loadRecalcInputs(ctx, q)
	if err != nil {
		return err
	}
	ids, err := q.ListAllVariationIDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := recalcVariation(ctx, q, in, id); err != nil {
			return fmt.Errorf("variation %d: %w", id, err)
		}
	}
	return nil
}

// RunRecalcWorker drains the queue, then polls until ctx is cancelled.
func RunRecalcWorker(ctx context.Context, pool *pgxpool.Pool, onErr func(error)) {
	t := newTicker()
	defer t.Stop()
	for {
		for {
			did, err := ProcessNextRecalcJob(ctx, pool)
			if err != nil && onErr != nil {
				onErr(err)
			}
			if !did || err != nil {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
