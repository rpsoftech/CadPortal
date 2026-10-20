package service

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// SeatInput is one stone-seat row. LockedStoneCode is set only when the seat
// needs a lock; otherwise it follows the chosen stone code.
type SeatInput struct {
	SizeMM          decimal.Decimal
	StoneCount      int32
	LockedStoneCode string
}

func (s *Service) ListSeats(ctx context.Context, variationID int64) ([]db.ListVariationSeatsRow, error) {
	if _, err := s.Q.GetVariation(ctx, variationID); err != nil {
		return nil, mapErr(err)
	}
	rows, err := s.Q.ListVariationSeats(ctx, variationID)
	if rows == nil {
		rows = []db.ListVariationSeatsRow{}
	}
	return rows, err
}

// SetSeats replaces all seats of a variation and rebuilds its weights in the
// same transaction.
func (s *Service) SetSeats(ctx context.Context, variationID int64, seats []SeatInput) (VariationDetail, error) {
	for i, st := range seats {
		if !st.SizeMM.IsPositive() {
			return VariationDetail{}, invalid("seat %d: size_mm must be positive", i+1)
		}
		if st.StoneCount <= 0 {
			return VariationDetail{}, invalid("seat %d: stone_count must be positive", i+1)
		}
	}
	var out VariationDetail
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetVariationForUpdate(ctx, variationID); err != nil {
			return err
		}
		if err := q.DeleteVariationSeats(ctx, variationID); err != nil {
			return err
		}
		for i, st := range seats {
			var lock pgtype.Int8
			if st.LockedStoneCode != "" {
				sc, err := q.GetStoneCodeByCode(ctx, st.LockedStoneCode)
				if err != nil {
					return invalid("seat %d: unknown stone code %q", i+1, st.LockedStoneCode)
				}
				// The composite FK requires a matrix row; check up front for a clear message.
				ok, err := q.MatrixRowExists(ctx, db.MatrixRowExistsParams{SizeMm: st.SizeMM, StoneCodeID: sc.ID})
				if err != nil {
					return err
				}
				if !ok {
					return invalid("seat %d: cannot lock %s at %s mm: no stone_size_matrix row",
						i+1, st.LockedStoneCode, st.SizeMM.StringFixed(2))
				}
				lock = pgtype.Int8{Int64: sc.ID, Valid: true}
			}
			if err := q.InsertSeat(ctx, db.InsertSeatParams{
				VariationID: variationID, Position: int32(i + 1), SizeMm: st.SizeMM,
				StoneCount: st.StoneCount, LockedStoneCodeID: lock,
			}); err != nil {
				return err
			}
		}
		return s.reload(ctx, q, variationID, &out)
	})
	return out, err
}

// CloneSeats copies every seat (including locks) from another variation.
func (s *Service) CloneSeats(ctx context.Context, targetID, sourceID int64) (VariationDetail, error) {
	if targetID == sourceID {
		return VariationDetail{}, invalid("source and target variation are the same")
	}
	var out VariationDetail
	err := s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.GetVariationForUpdate(ctx, targetID); err != nil {
			return err
		}
		if _, err := q.GetVariation(ctx, sourceID); err != nil {
			return invalid("source variation %d not found", sourceID)
		}
		if err := q.DeleteVariationSeats(ctx, targetID); err != nil {
			return err
		}
		if err := q.CloneSeats(ctx, db.CloneSeatsParams{TargetID: targetID, SourceID: sourceID}); err != nil {
			return err
		}
		return s.reload(ctx, q, targetID, &out)
	})
	return out, err
}

type BOMLineOut struct {
	SizeMM         decimal.Decimal `json:"size_mm"`
	StoneCount     int             `json:"stone_count"`
	StoneCode      string          `json:"stone_code"`
	Locked         bool            `json:"locked"`
	CaratsPerStone decimal.Decimal `json:"carats_per_stone"`
	LineCarats     decimal.Decimal `json:"line_carats"`
}

type MetalTotals struct {
	MetalCode string          `json:"metal_code"`
	NetG      decimal.Decimal `json:"net_weight_g"`
	GrossG    decimal.Decimal `json:"gross_weight_g"`
}

type BOMResult struct {
	VariationCode string          `json:"variation_code"`
	StoneCode     string          `json:"stone_code"`
	Lines         []BOMLineOut    `json:"lines"`
	TotalPcs      int             `json:"total_pcs"`
	TotalCarats   decimal.Decimal `json:"total_carats"`
	StoneWeightG  decimal.Decimal `json:"stone_weight_g"`
	Metals        []MetalTotals   `json:"metals"`
}

// SimulateBOM resolves the stone BOM for a planner-chosen stone code. Flexible
// seats use that code; locked seats keep their own. Gaps in the matrix return
// a *calc.MissingMatrixError (never zero). Nothing is persisted.
func (s *Service) SimulateBOM(ctx context.Context, variationID int64, code string) (BOMResult, error) {
	if code == "" {
		return BOMResult{}, invalid("code query parameter is required")
	}
	if _, err := s.Q.GetStoneCodeByCode(ctx, code); err != nil {
		if mapErr(err) == ErrNotFound {
			return BOMResult{}, invalid("unknown stone code %q", code)
		}
		return BOMResult{}, err
	}
	v, err := s.Q.GetVariation(ctx, variationID)
	if err != nil {
		return BOMResult{}, mapErr(err)
	}
	dbSeats, err := s.Q.ListVariationSeats(ctx, variationID)
	if err != nil {
		return BOMResult{}, err
	}
	in, err := loadRecalcInputs(ctx, s.Q)
	if err != nil {
		return BOMResult{}, err
	}
	seats := make([]calc.Seat, len(dbSeats))
	for i, st := range dbSeats {
		seats[i] = calc.Seat{SizeMM: st.SizeMm, Count: int(st.StoneCount), LockedCode: st.LockedCode.String}
	}
	bom, err := calc.ResolveBOM(seats, code, in.matrix)
	if err != nil {
		return BOMResult{}, err
	}

	totalCt := calc.RoundCarats(bom.TotalCarats)
	res := BOMResult{
		VariationCode: v.Code, StoneCode: code, Lines: make([]BOMLineOut, 0, len(bom.Lines)),
		TotalPcs: bom.TotalPcs, TotalCarats: totalCt,
		StoneWeightG: calc.RoundGrams(calc.StoneWeight(totalCt)),
		Metals:       []MetalTotals{},
	}
	for _, l := range bom.Lines {
		res.Lines = append(res.Lines, BOMLineOut{
			SizeMM: l.SizeMM, StoneCount: l.Count, StoneCode: l.Code, Locked: l.Locked,
			CaratsPerStone: l.CaratsPerStone, LineCarats: calc.RoundCarats(l.LineCarats),
		})
	}
	for _, m := range in.metals {
		net, err := netFor(v, in, m)
		if err != nil {
			return BOMResult{}, err
		}
		net = calc.RoundGrams(net)
		res.Metals = append(res.Metals, MetalTotals{
			MetalCode: m.Code, NetG: net, GrossG: calc.RoundGrams(calc.Gross(net, totalCt)),
		})
	}
	return res, nil
}
