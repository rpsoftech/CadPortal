package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	res, err := computeWeights(v, seats, in)
	if err != nil {
		return err
	}

	if err := q.DeleteVariationWeights(ctx, variationID); err != nil {
		return err
	}
	for _, w := range res.weights {
		if err := q.UpsertVariationWeight(ctx, db.UpsertVariationWeightParams{
			VariationID: variationID, MetalID: w.metalID, NetWeightG: w.net, GrossWeightG: w.gross,
		}); err != nil {
			return err
		}
	}
	return q.UpdateVariationBaseline(ctx, db.UpdateVariationBaselineParams{
		ID:                   variationID,
		BaselineStoneCarats:  res.baselineCarats,
		BaselineGrossWeightG: res.defaultGross,
	})
}

type metalWeight struct {
	metalID int64
	net     decimal.Decimal
	gross   decimal.NullDecimal
}

// variationWeights is everything a rebuild stores for one variation.
type variationWeights struct {
	weights        []metalWeight // one per active metal
	baselineCarats decimal.Decimal
	defaultGross   decimal.NullDecimal // gross of the default input metal
}

// computeWeights is the calculation behind both the single-variation and the full rebuild: pure,
// no database access, rounded only here, at storage time.
func computeWeights(v db.CadVariation, seats []calc.Seat, in *recalcInputs) (variationWeights, error) {
	// Baseline stone carats (flexible seats use the baseline code). Gaps leave
	// the gross NULL rather than silently treating missing rows as zero.
	baselineCarats, baselineOK := decimal.Zero, true
	if len(seats) > 0 {
		if in.baselineCode == "" {
			baselineOK = false
		} else if bom, err := calc.ResolveBOM(seats, in.baselineCode, in.matrix); err != nil {
			var mme *calc.MissingMatrixError
			if !errors.As(err, &mme) {
				return variationWeights{}, err
			}
			baselineOK = false
		} else {
			baselineCarats = bom.TotalCarats
		}
	}

	out := variationWeights{baselineCarats: calc.RoundCarats(baselineCarats),
		weights: make([]metalWeight, 0, len(in.metals))}
	for _, m := range in.metals {
		net, err := netFor(v, in, m)
		if err != nil {
			return variationWeights{}, err
		}
		net = calc.RoundGrams(net)
		var gross decimal.NullDecimal
		if baselineOK {
			// Gross is derived from the rounded stored carats so the cache is self-consistent.
			gross = decimal.NullDecimal{Valid: true,
				Decimal: calc.RoundGrams(calc.Gross(net, calc.RoundCarats(baselineCarats)))}
		}
		out.weights = append(out.weights, metalWeight{metalID: m.ID, net: net, gross: gross})
		if m.IsDefaultInput {
			out.defaultGross = gross
		}
	}
	return out, nil
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

	runErr := recalcAll(ctx, q, tx)
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

// nullText renders a nullable decimal as text for the bulk statements; an empty string stands for NULL.
func nullText(d decimal.NullDecimal) string {
	if !d.Valid {
		return ""
	}
	return d.Decimal.String()
}

const bulkUpsertWeightsSQL = `
INSERT INTO variation_metal_weights (variation_id, metal_id, net_weight_g, gross_weight_g)
SELECT u.variation_id, u.metal_id, u.net::numeric, NULLIF(u.gross, '')::numeric
FROM unnest($1::bigint[], $2::bigint[], $3::text[], $4::text[]) AS u(variation_id, metal_id, net, gross)
ON CONFLICT (variation_id, metal_id) DO UPDATE
SET net_weight_g = EXCLUDED.net_weight_g, gross_weight_g = EXCLUDED.gross_weight_g
WHERE (variation_metal_weights.net_weight_g, variation_metal_weights.gross_weight_g)
      IS DISTINCT FROM (EXCLUDED.net_weight_g, EXCLUDED.gross_weight_g)`

// A metal that is no longer active keeps no cached weights (the per-variation rebuild removes them too).
const deleteInactiveWeightsSQL = `DELETE FROM variation_metal_weights WHERE metal_id <> ALL($1::bigint[])`

const bulkUpdateBaselinesSQL = `
UPDATE cad_variations v
SET baseline_stone_carats = u.carats::numeric, baseline_gross_weight_g = NULLIF(u.gross, '')::numeric
FROM unnest($1::bigint[], $2::text[], $3::text[]) AS u(id, carats, gross)
WHERE v.id = u.id
  AND (v.baseline_stone_carats, v.baseline_gross_weight_g)
      IS DISTINCT FROM (u.carats::numeric, NULLIF(u.gross, '')::numeric)`

// execer is the part of a pgx transaction the bulk writes need.
type execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// recalcAll rebuilds every variation set-based: it locks the variations, reads them and all their seats
// in two queries, computes in Go with exactly the single-variation math (computeWeights), and writes the
// results with three statements that skip rows whose values are unchanged. It runs inside the caller's
// transaction, so readers keep seeing the old, complete weights until it commits and a failure leaves
// nothing half-written.
func recalcAll(ctx context.Context, q *db.Queries, tx execer) error {
	// Variations first, weights second: the same lock order as a seat edit.
	vars, err := q.ListAllVariationsForRecalc(ctx)
	if err != nil {
		return err
	}
	in, err := loadRecalcInputs(ctx, q)
	if err != nil {
		return err
	}
	allSeats, err := q.ListAllSeatsForRecalc(ctx)
	if err != nil {
		return err
	}
	seatsOf := make(map[int64][]calc.Seat, len(vars))
	for _, s := range allSeats {
		seatsOf[s.VariationID] = append(seatsOf[s.VariationID],
			calc.Seat{SizeMM: s.SizeMm, Count: int(s.StoneCount), LockedCode: s.LockedCode.String})
	}

	n := len(vars) * len(in.metals)
	wVar, wMetal := make([]int64, 0, n), make([]int64, 0, n)
	wNet, wGross := make([]string, 0, n), make([]string, 0, n)
	bID, bCarats, bGross := make([]int64, 0, len(vars)), make([]string, 0, len(vars)), make([]string, 0, len(vars))
	for _, v := range vars {
		res, err := computeWeights(v, seatsOf[v.ID], in)
		if err != nil {
			return fmt.Errorf("variation %d: %w", v.ID, err)
		}
		for _, w := range res.weights {
			wVar, wMetal = append(wVar, v.ID), append(wMetal, w.metalID)
			wNet, wGross = append(wNet, w.net.String()), append(wGross, nullText(w.gross))
		}
		bID, bCarats = append(bID, v.ID), append(bCarats, res.baselineCarats.String())
		bGross = append(bGross, nullText(res.defaultGross))
	}

	activeIDs := make([]int64, len(in.metals))
	for i, m := range in.metals {
		activeIDs[i] = m.ID
	}
	if _, err := tx.Exec(ctx, deleteInactiveWeightsSQL, activeIDs); err != nil {
		return err
	}
	if len(wVar) > 0 {
		if _, err := tx.Exec(ctx, bulkUpsertWeightsSQL, wVar, wMetal, wNet, wGross); err != nil {
			return err
		}
	}
	if len(bID) > 0 {
		if _, err := tx.Exec(ctx, bulkUpdateBaselinesSQL, bID, bCarats, bGross); err != nil {
			return err
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
