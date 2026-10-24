package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// Things made out of an order: the summary totals, job bags, and costing. Weights and bags come from
// the order's own snapshot, never the live design; costing deliberately uses today's master data (see
// CostOrder) and is itself saved as an immutable sheet.

// MaxOrderJobBags is the most job bags one order may print at once: an order holds at most
// MaxOrderLines lines, so one bag per line always fits.
const MaxOrderJobBags = MaxOrderLines

// OrderTotals adds up an order from its line snapshots: every weight is the line's weight times its
// quantity. Nothing here reads the live catalog.
type OrderTotals struct {
	Lines  int `json:"lines"`
	Pieces int `json:"pieces"` // sum of the line quantities
	// StonePcs is the number of stones to set across all pieces.
	StonePcs int             `json:"stone_pcs"`
	NetG     decimal.Decimal `json:"net_weight_g"`
	StoneG   decimal.Decimal `json:"stone_weight_g"`
	StoneCt  decimal.Decimal `json:"stone_carats"`
	GrossG   decimal.Decimal `json:"gross_weight_g"`
	// StonePct is the stone weight as a percentage of the gross weight over the whole order, 2 dp;
	// zero only when the order has no gross weight at all.
	StonePct decimal.Decimal `json:"stone_pct"`
}

// ComputeOrderTotals sums the lines of an order.
func ComputeOrderTotals(d OrderDetail) OrderTotals {
	var t OrderTotals
	for _, l := range d.Lines {
		q := decimal.NewFromInt(int64(l.Quantity))
		t.Lines++
		t.Pieces += l.Quantity
		t.StonePcs += l.Snapshot.TotalPcs * l.Quantity
		t.NetG = t.NetG.Add(l.Snapshot.NetG.Mul(q))
		t.StoneG = t.StoneG.Add(l.Snapshot.StoneG.Mul(q))
		t.StoneCt = t.StoneCt.Add(l.Snapshot.StoneCt.Mul(q))
		t.GrossG = t.GrossG.Add(l.Snapshot.GrossG.Mul(q))
	}
	if t.GrossG.IsPositive() {
		t.StonePct = calc.StoneWeight(t.StoneCt).Div(t.GrossG).Mul(decimal.NewFromInt(100)).Round(2)
	}
	return t
}

// ---------- Job bags ----------

// OrderJobBags builds the job bags of an order from its snapshots, in line order. Without perPiece there
// is one bag per line (the quantity is printed on it); with perPiece every piece gets its own bag. Photos
// are not attached here. A purged variation does not stop its bag: the snapshot has everything on it.
func (s *Service) OrderJobBags(d OrderDetail, perPiece bool) ([]JobBag, error) {
	n := len(d.Lines)
	if perPiece {
		n = 0
		for _, l := range d.Lines {
			n += l.Quantity
		}
	}
	if n == 0 {
		return nil, invalid("the order has no lines")
	}
	if n > MaxOrderJobBags {
		if perPiece {
			return nil, invalid("one bag per piece would print %d bags; at most %d can be printed at once, print one bag per line instead", n, MaxOrderJobBags)
		}
		return nil, invalid("at most %d job bags can be printed at once", MaxOrderJobBags)
	}
	now := time.Now().UTC()
	out := make([]JobBag, 0, n)
	for _, l := range d.Lines {
		sn := l.Snapshot
		jb := JobBag{
			QR: s.jobBagQR(sn.DesignCode, sn.VariationCode), DesignCode: sn.DesignCode, DesignName: sn.DesignName,
			Category: sn.CategoryName, Tags: sn.Tags, VariationCode: sn.VariationCode, BaseInput: snapshotBaseInput(sn.BaseInput),
			MetalCode: sn.MetalCode, NetG: sn.NetG, GrossG: sn.GrossG, GeneratedAt: now,
			BOM: BOMResult{VariationCode: sn.VariationCode, StoneCode: sn.StoneCode, Lines: sn.BOM, TotalPcs: sn.TotalPcs,
				TotalCarats: sn.StoneCt, StoneWeightG: sn.StoneG},
		}
		if l.DesignID != nil {
			jb.DesignID = *l.DesignID
		}
		if l.VariationID != nil {
			jb.VariationID = *l.VariationID
		}
		if sn.File3dm != nil {
			jb.File3dmName, jb.File3dmSHA256 = sn.File3dm.Name, sn.File3dm.SHA256
		}
		if sn.FileStl != nil {
			jb.FileStlName, jb.FileStlSHA256 = sn.FileStl.Name, sn.FileStl.SHA256
		}
		if perPiece {
			for k := 1; k <= l.Quantity; k++ {
				b := jb
				b.OrderRef = fmt.Sprintf("%s, piece %d of %d", d.Number, k, l.Quantity)
				out = append(out, b)
			}
			continue
		}
		jb.OrderRef = fmt.Sprintf("%s, quantity %d", d.Number, l.Quantity)
		out = append(out, jb)
	}
	return out, nil
}

// snapshotBaseInput words a base input the way a live job bag does.
func snapshotBaseInput(b BaseInputSnapshot) string {
	switch {
	case b.VolumeMM3 != nil:
		return fmt.Sprintf("Volume %s mm3", b.VolumeMM3.StringFixed(3))
	case b.RefWeightG != nil:
		return fmt.Sprintf("Reference %s g in %s", b.RefWeightG.StringFixed(3), b.RefMetalCode)
	}
	return ""
}

// OrderQR is what the QR code on an order summary holds: a link to the order page when the portal has a
// public address, else the order number. Only the number and id go in, never names or weights.
func (s *Service) OrderQR(d OrderDetail) string {
	if s.AppBaseURL == "" {
		return d.Number
	}
	return fmt.Sprintf("%s/orders/%d?n=%s", s.AppBaseURL, d.ID, url.QueryEscape(d.Number))
}

// ---------- Costing ----------

// OrderCostParams: the metal and stone code come from the order, the rates from the person costing it.
type OrderCostParams struct {
	GoldRatePerG     decimal.Decimal `json:"gold_rate_per_g"`
	SettingRatePerPc decimal.Decimal `json:"setting_rate_per_pc"`
	SkipIncomplete   bool            `json:"skip_incomplete"`
	Name             string          `json:"name"`
}

// CostOrder costs the order's variations for their quantities in the order's metal and stone code, from
// the CURRENT master data and designs (wastage, stone prices, stone matrix, seats). The order's own
// snapshot is not what is costed; Drift tells how far the live designs have moved from it. A line whose
// variation has been purged cannot be costed and is reported as a problem, never as zero.
func (s *Service) CostOrder(ctx context.Context, id int64, p OrderCostParams) (CostResult, SheetRequest, OrderDetail, error) {
	d, err := s.GetOrder(ctx, id)
	if err != nil {
		return CostResult{}, SheetRequest{}, d, err
	}
	req := SheetRequest{MetalCode: d.MetalCode, StoneCode: d.StoneCode, GoldRatePerG: p.GoldRatePerG,
		SettingRatePerPc: p.SettingRatePerPc, SkipIncomplete: p.SkipIncomplete, Quantities: map[int64]int{}}
	var purged []DesignProblem
	for _, l := range d.Lines {
		if l.VariationID == nil {
			purged = append(purged, DesignProblem{DesignCode: l.Snapshot.DesignCode, DesignName: l.Snapshot.DesignName,
				Variations: []VariationProblem{{VariationCode: l.Snapshot.VariationCode, Message: "the variation has been purged"}}})
			continue
		}
		req.VariationIDs = append(req.VariationIDs, *l.VariationID)
		req.Quantities[*l.VariationID] = l.Quantity
	}
	if len(req.VariationIDs) == 0 {
		return CostResult{}, req, d, invalid("none of the order's variations exist any more")
	}
	res, err := s.CostSelection(ctx, req)
	if err != nil {
		return res, req, d, err
	}
	res.Problems = append(res.Problems, purged...)
	return res, req, d, nil
}

// OrderDrift compares each line's snapshot with a fresh one taken from the live catalog now, so costing
// (which uses the live catalog) can warn when the order no longer matches its designs. The order is not
// changed.
func (s *Service) OrderDrift(ctx context.Context, d OrderDetail) (OrderDiff, error) {
	diff := OrderDiff{SourceID: d.ID, Lines: []LineDiff{}}
	var items []OrderItem
	for _, l := range d.Lines {
		if l.VariationID != nil {
			items = append(items, OrderItem{VariationID: *l.VariationID, Quantity: l.Quantity})
		}
	}
	problems := map[int64]OrderProblem{}
	var built []builtLine
	for len(items) > 0 {
		bl, err := s.snapshotLines(ctx, d.MetalCode, d.StoneCode, items)
		if err == nil {
			built = bl
			break
		}
		var pe *OrderProblemsError
		if !errors.As(err, &pe) {
			return diff, err
		}
		bad := map[int64]bool{}
		for _, pr := range pe.Problems {
			problems[pr.VariationID] = pr
			bad[pr.VariationID] = true
		}
		var rest []OrderItem
		for _, it := range items {
			if !bad[it.VariationID] {
				rest = append(rest, it)
			}
		}
		items = rest
	}
	diff.fill(d, built, problems)
	return diff, nil
}

// ListOrderCostingSheets returns the sheets made from an order, newest first.
func (s *Service) ListOrderCostingSheets(ctx context.Context, orderID int64) ([]SheetSummary, error) {
	if _, err := s.Q.GetOrderRow(ctx, orderID); err != nil {
		return nil, mapErr(err)
	}
	rows, err := s.Q.ListCostingSheetsForOrder(ctx, pgtype.Int8{Int64: orderID, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]SheetSummary, 0, len(rows))
	for _, r := range rows {
		out = append(out, sheetSummaryFromOrderRow(r))
	}
	return out, nil
}

func sheetSummaryFromOrderRow(r db.ListCostingSheetsForOrderRow) SheetSummary {
	return SheetSummary{
		ID: r.ID, Name: r.Name, OrderID: int8Ptr(r.OrderID), CreatedAt: r.CreatedAt.Time, CreatedBy: int8Ptr(r.CreatedBy),
		CreatedByName: r.CreatedByName, RecostOf: int8Ptr(r.RecostOf), MetalCode: r.MetalCode,
		StoneCode: r.StoneCode, GoldRatePerG: r.GoldRatePerG, SettingRatePerPc: r.SettingRatePerPc,
		DesignCount: int(r.DesignCount), VariationCount: int(r.VariationCount),
		IncompleteCount: int(r.IncompleteCount), Total: r.Total,
	}
}
