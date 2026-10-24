package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// Orders: a selection of catalog variations (a quantity each) in one metal and one stone code, sent to
// production. Every line keeps an immutable snapshot taken when the line was added; the live catalog is
// never consulted again to read an order. No prices are stored anywhere in an order.

const (
	MaxOrderLines    = 500
	MaxLineQuantity  = 100000
	maxCriteriaBytes = 8 << 10
)

const (
	OrderDraft        = "draft"
	OrderPlaced       = "placed"
	OrderInProduction = "in_production"
	OrderDone         = "done"
	OrderCancelled    = "cancelled"
)

// orderTransitions are the only allowed status changes. Done and cancelled are final.
var orderTransitions = map[string][]string{
	OrderDraft:        {OrderPlaced, OrderCancelled},
	OrderPlaced:       {OrderInProduction, OrderCancelled},
	OrderInProduction: {OrderDone, OrderCancelled},
}

func validOrderStatus(s string) bool {
	switch s {
	case OrderDraft, OrderPlaced, OrderInProduction, OrderDone, OrderCancelled:
		return true
	}
	return false
}

// FormatOrderNumber builds ORD-0001; the number grows past four digits rather than being cut.
func FormatOrderNumber(seq int32) string { return fmt.Sprintf("ORD-%04d", seq) }

// ---------- Snapshot ----------

// FileSnapshot fingerprints a CAD file as it was. Storage keys are deliberately not here.
type FileSnapshot struct {
	Name      string `json:"name"`
	SHA256    string `json:"sha256"`
	SizeBytes int64  `json:"size_bytes"`
}

type CoverSnapshot struct {
	MediaID     int64  `json:"media_id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type"`
}

type BaseInputSnapshot struct {
	Kind         string           `json:"kind"` // volume or weight
	VolumeMM3    *decimal.Decimal `json:"volume_mm3,omitempty"`
	RefWeightG   *decimal.Decimal `json:"ref_weight_g,omitempty"`
	RefMetalCode string           `json:"ref_metal_code,omitempty"`
}

// LineSnapshot is everything the order remembers about one variation.
type LineSnapshot struct {
	SnapshotAt      time.Time         `json:"snapshot_at"`
	DesignCode      string            `json:"design_code"`
	DesignName      string            `json:"design_name"`
	CategoryName    string            `json:"category_name"`
	Tags            []string          `json:"tags"`
	VariationCode   string            `json:"variation_code"`
	MetalCode       string            `json:"metal_code"`
	StoneCode       string            `json:"stone_code"`
	SpecificGravity decimal.Decimal   `json:"specific_gravity"`
	BaseInput       BaseInputSnapshot `json:"base_input"`
	NetG            decimal.Decimal   `json:"net_weight_g"`
	StoneG          decimal.Decimal   `json:"stone_weight_g"`
	StoneCt         decimal.Decimal   `json:"stone_carats"`
	GrossG          decimal.Decimal   `json:"gross_weight_g"`
	StonePct        decimal.Decimal   `json:"stone_pct"` // stone weight as a percentage of gross weight, 2 dp
	TotalPcs        int               `json:"total_pcs"`
	BOM             []BOMLineOut      `json:"bom"`
	File3dm         *FileSnapshot     `json:"file_3dm"`
	FileStl         *FileSnapshot     `json:"file_stl"`
	Cover           *CoverSnapshot    `json:"cover"`
}

// lineInternal holds what only the server may see: where the files were stored. A later export reads
// them to build a ZIP; if the object has since been purged it simply cannot be fetched.
type lineInternal struct {
	File3dmKey string `json:"file_3dm_key,omitempty"`
	FileStlKey string `json:"file_stl_key,omitempty"`
	CoverThumb string `json:"cover_thumb_key,omitempty"`
}

// LineInternal is the parsed storage reference of a line, for server-side exports only.
type LineInternal = lineInternal

// OrderItem is one requested line.
type OrderItem struct {
	VariationID int64 `json:"variation_id"`
	Quantity    int   `json:"quantity"`
}

// OrderProblem names a variation that cannot be put on the order and why.
type OrderProblem struct {
	VariationID   int64              `json:"variation_id"`
	VariationCode string             `json:"variation_code,omitempty"`
	Message       string             `json:"message"`
	Missing       []calc.MissingPair `json:"missing,omitempty"`
}

// OrderProblemsError lists every variation that could not be snapshotted; nothing is saved.
type OrderProblemsError struct{ Problems []OrderProblem }

func (e *OrderProblemsError) Error() string {
	parts := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		name := p.VariationCode
		if name == "" {
			name = fmt.Sprintf("#%d", p.VariationID)
		}
		parts[i] = name + ": " + p.Message
	}
	return "cannot add to the order: " + strings.Join(parts, "; ")
}

type builtLine struct {
	item     OrderItem
	designID int64
	snap     LineSnapshot
	internal lineInternal
}

func checkItems(items []OrderItem) error {
	if len(items) == 0 {
		return invalid("select at least one variation")
	}
	if len(items) > MaxOrderLines {
		return invalid("too many lines: an order holds at most %d", MaxOrderLines)
	}
	seen := map[int64]bool{}
	for _, it := range items {
		if it.VariationID <= 0 {
			return invalid("variation_id is required on every line")
		}
		if it.Quantity <= 0 || it.Quantity > MaxLineQuantity {
			return invalid("quantity must be between 1 and %d", MaxLineQuantity)
		}
		if seen[it.VariationID] {
			return invalid("variation %d is listed twice: give it one line with the total quantity", it.VariationID)
		}
		seen[it.VariationID] = true
	}
	return nil
}

func (s *Service) checkMetalAndStone(ctx context.Context, metalCode, stoneCode string) error {
	if metalCode == "" || stoneCode == "" {
		return invalid("metal_code and stone_code are required")
	}
	m, err := s.Q.GetMetalByCode(ctx, metalCode)
	if err != nil {
		if mapErr(err) == ErrNotFound {
			return invalid("unknown metal_code %q", metalCode)
		}
		return err
	}
	if !m.Active {
		return invalid("metal %q is not active", metalCode)
	}
	if _, err := s.Q.GetStoneCodeByCode(ctx, stoneCode); err != nil {
		if mapErr(err) == ErrNotFound {
			return invalid("unknown stone code %q", stoneCode)
		}
		return err
	}
	return nil
}

// snapshotLines builds the snapshot of each requested variation from the live catalog, with the same
// BOM and weight code as simulate-bom. Every problem is collected; if there is any, nothing is returned.
func (s *Service) snapshotLines(ctx context.Context, metalCode, stoneCode string, items []OrderItem) ([]builtLine, error) {
	metals, err := s.Q.ListMetals(ctx)
	if err != nil {
		return nil, err
	}
	metalByID := map[int64]db.Metal{}
	var metal db.Metal
	for _, m := range metals {
		metalByID[m.ID] = m
		if m.Code == metalCode {
			metal = m
		}
	}
	now := time.Now().UTC()
	var out []builtLine
	var problems []OrderProblem
	for _, it := range items {
		v, err := s.Q.GetVariation(ctx, it.VariationID)
		if err != nil {
			if mapErr(err) != ErrNotFound {
				return nil, err
			}
			problems = append(problems, OrderProblem{VariationID: it.VariationID, Message: "variation not found (it may have been deleted)"})
			continue
		}
		d, err := s.Q.GetDesignByID(ctx, v.DesignID)
		if err != nil {
			if mapErr(err) != ErrNotFound {
				return nil, err
			}
			problems = append(problems, OrderProblem{VariationID: v.ID, VariationCode: v.Code, Message: "its design has been deleted"})
			continue
		}
		bom, err := s.SimulateBOM(ctx, v.ID, stoneCode)
		if err != nil {
			var mme *calc.MissingMatrixError
			if errors.As(err, &mme) {
				problems = append(problems, OrderProblem{VariationID: v.ID, VariationCode: v.Code, Message: mme.Error(), Missing: mme.Pairs})
				continue
			}
			return nil, err
		}
		var net, gross decimal.Decimal
		found := false
		for _, m := range bom.Metals {
			if m.MetalCode == metalCode {
				net, gross, found = m.NetG, m.GrossG, true
			}
		}
		if !found {
			problems = append(problems, OrderProblem{VariationID: v.ID, VariationCode: v.Code, Message: fmt.Sprintf("no weight for metal %s", metalCode)})
			continue
		}
		catName, err := s.Q.GetCategoryName(ctx, d.CategoryID)
		if err != nil {
			return nil, err
		}
		tags, err := s.Q.ListDesignTags(ctx, d.ID)
		if err != nil {
			return nil, err
		}
		snap := LineSnapshot{
			SnapshotAt: now, DesignCode: d.Code, DesignName: d.Name, CategoryName: catName, Tags: []string{},
			VariationCode: v.Code, MetalCode: metalCode, StoneCode: stoneCode, SpecificGravity: metal.SpecificGravity,
			NetG: net, StoneG: bom.StoneWeightG, StoneCt: bom.TotalCarats, GrossG: gross,
			TotalPcs: bom.TotalPcs, BOM: bom.Lines,
		}
		if snap.BOM == nil {
			snap.BOM = []BOMLineOut{}
		}
		for _, t := range tags {
			snap.Tags = append(snap.Tags, t.Name)
		}
		if gross.IsPositive() {
			snap.StonePct = calc.StoneWeight(bom.TotalCarats).Div(gross).Mul(decimal.NewFromInt(100)).Round(2)
		}
		switch {
		case v.BaseVolumeMm3.Valid:
			vol := v.BaseVolumeMm3.Decimal
			snap.BaseInput = BaseInputSnapshot{Kind: "volume", VolumeMM3: &vol}
		case v.BaseRefWeightG.Valid:
			w := v.BaseRefWeightG.Decimal
			snap.BaseInput = BaseInputSnapshot{Kind: "weight", RefWeightG: &w, RefMetalCode: metalByID[v.BaseRefMetalID.Int64].Code}
		}
		var in lineInternal
		if v.File3dmSha256.Valid && v.File3dmKey.Valid {
			snap.File3dm = &FileSnapshot{Name: v.File3dmName.String, SHA256: v.File3dmSha256.String, SizeBytes: v.File3dmSizeBytes.Int64}
			in.File3dmKey = v.File3dmKey.String
		}
		if v.FileStlSha256.Valid && v.FileStlKey.Valid {
			snap.FileStl = &FileSnapshot{Name: v.FileStlName.String, SHA256: v.FileStlSha256.String, SizeBytes: v.FileStlSizeBytes.Int64}
			in.FileStlKey = v.FileStlKey.String
		}
		cov, err := s.Q.OrderCoverMedia(ctx, db.OrderCoverMediaParams{DesignID: d.ID, VariationID: pgtype.Int8{Int64: v.ID, Valid: true}})
		if err == nil {
			snap.Cover = &CoverSnapshot{MediaID: cov.ID, Filename: cov.Filename, ContentType: cov.ContentType}
			in.CoverThumb = cov.ThumbKey.String
		} else if mapErr(err) != ErrNotFound {
			return nil, err
		}
		out = append(out, builtLine{item: it, designID: d.ID, snap: snap, internal: in})
	}
	if len(problems) > 0 {
		return nil, &OrderProblemsError{Problems: problems}
	}
	return out, nil
}

func insertLines(ctx context.Context, q *db.Queries, orderID int64, lines []builtLine) error {
	for i, l := range lines {
		snap, _ := json.Marshal(l.snap)
		in, _ := json.Marshal(l.internal)
		if err := q.InsertOrderLine(ctx, db.InsertOrderLineParams{
			OrderID: orderID, Position: int32(i + 1), Quantity: int32(l.item.Quantity),
			DesignID:    pgtype.Int8{Int64: l.designID, Valid: true},
			VariationID: pgtype.Int8{Int64: l.item.VariationID, Valid: true},
			Snapshot:    snap, Internal: in,
		}); err != nil {
			return err
		}
	}
	return nil
}

// ---------- Create, edit ----------

// CreateOrderParams: Criteria is optional and informational ("how the selection was found").
type CreateOrderParams struct {
	Name      string          `json:"name"`
	Notes     string          `json:"notes"`
	MetalCode string          `json:"metal_code"`
	StoneCode string          `json:"stone_code"`
	Items     []OrderItem     `json:"items"`
	Criteria  json.RawMessage `json:"criteria,omitempty"`
	// Place makes the order "placed" at once instead of a draft.
	Place  bool  `json:"place"`
	UserID int64 `json:"-"`

	clonedFrom int64
}

func checkHeader(name, notes string) error {
	if len([]rune(name)) > 120 {
		return invalid("name is at most 120 characters")
	}
	if len([]rune(notes)) > 2000 {
		return invalid("notes are at most 2000 characters")
	}
	return nil
}

func checkCriteria(raw json.RawMessage) ([]byte, error) {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	if len(raw) > maxCriteriaBytes {
		return nil, invalid("criteria is too large")
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil, invalid("criteria must be a JSON object")
	}
	return raw, nil
}

func (s *Service) CreateOrder(ctx context.Context, p CreateOrderParams) (OrderDetail, error) {
	p.Name, p.Notes = strings.TrimSpace(p.Name), strings.TrimSpace(p.Notes)
	if err := checkHeader(p.Name, p.Notes); err != nil {
		return OrderDetail{}, err
	}
	criteria, err := checkCriteria(p.Criteria)
	if err != nil {
		return OrderDetail{}, err
	}
	if err := checkItems(p.Items); err != nil {
		return OrderDetail{}, err
	}
	if err := s.checkMetalAndStone(ctx, p.MetalCode, p.StoneCode); err != nil {
		return OrderDetail{}, err
	}
	lines, err := s.snapshotLines(ctx, p.MetalCode, p.StoneCode, p.Items)
	if err != nil {
		return OrderDetail{}, err
	}
	return s.storeOrder(ctx, p, criteria, lines)
}

func (s *Service) storeOrder(ctx context.Context, p CreateOrderParams, criteria []byte, lines []builtLine) (OrderDetail, error) {
	status := OrderDraft
	if p.Place {
		status = OrderPlaced
	}
	var id int64
	err := s.inTx(ctx, func(q *db.Queries) error {
		// The counter row stays locked until commit: concurrent creators queue, and a failure below
		// rolls the number back, so there are no gaps.
		seq, err := q.NextOrderNumber(ctx)
		if err != nil {
			return err
		}
		arg := db.InsertOrderParams{
			Seq: seq, Number: FormatOrderNumber(seq), Name: p.Name, Notes: p.Notes, Status: status,
			MetalCode: p.MetalCode, StoneCode: p.StoneCode, Criteria: criteria,
		}
		if p.UserID != 0 {
			arg.CreatedBy = pgtype.Int8{Int64: p.UserID, Valid: true}
		}
		if p.clonedFrom != 0 {
			arg.ClonedFrom = pgtype.Int8{Int64: p.clonedFrom, Valid: true}
		}
		o, err := q.InsertOrder(ctx, arg)
		if err != nil {
			return err
		}
		id = o.ID
		if err := insertLines(ctx, q, o.ID, lines); err != nil {
			return err
		}
		hist := db.InsertOrderStatusHistoryParams{OrderID: o.ID, ToStatus: status}
		if p.UserID != 0 {
			hist.ChangedBy = pgtype.Int8{Int64: p.UserID, Valid: true}
		}
		return q.InsertOrderStatusHistory(ctx, hist)
	})
	if err != nil {
		return OrderDetail{}, err
	}
	return s.GetOrder(ctx, id)
}

// UpdateOrderHeader changes the name and notes of an order that is not finished.
func (s *Service) UpdateOrderHeader(ctx context.Context, id int64, name, notes string) (OrderDetail, error) {
	name, notes = strings.TrimSpace(name), strings.TrimSpace(notes)
	if err := checkHeader(name, notes); err != nil {
		return OrderDetail{}, err
	}
	err := s.inTx(ctx, func(q *db.Queries) error {
		o, err := q.GetOrderForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if o.Status == OrderDone || o.Status == OrderCancelled {
			return fmt.Errorf("%w: a %s order cannot be edited", ErrConflict, strings.ReplaceAll(o.Status, "_", " "))
		}
		return q.UpdateOrderHeader(ctx, db.UpdateOrderHeaderParams{ID: id, Name: name, Notes: notes})
	})
	if err != nil {
		return OrderDetail{}, err
	}
	return s.GetOrder(ctx, id)
}

// SetOrderLines replaces the lines of a DRAFT order. A variation that is already on the order keeps its
// existing snapshot (only its quantity changes); a newly added one is snapshotted now.
func (s *Service) SetOrderLines(ctx context.Context, id int64, items []OrderItem) (OrderDetail, error) {
	if err := checkItems(items); err != nil {
		return OrderDetail{}, err
	}
	o, err := s.Q.GetOrderRow(ctx, id)
	if err != nil {
		return OrderDetail{}, mapErr(err)
	}
	existing, err := s.Q.ListOrderLines(ctx, id)
	if err != nil {
		return OrderDetail{}, err
	}
	have := map[int64]db.OrderLine{}
	for _, l := range existing {
		if l.VariationID.Valid {
			have[l.VariationID.Int64] = l
		}
	}
	var fresh []OrderItem
	for _, it := range items {
		if _, ok := have[it.VariationID]; !ok {
			fresh = append(fresh, it)
		}
	}
	built := map[int64]builtLine{}
	if len(fresh) > 0 {
		bl, err := s.snapshotLines(ctx, o.MetalCode, o.StoneCode, fresh)
		if err != nil {
			return OrderDetail{}, err
		}
		for _, b := range bl {
			built[b.item.VariationID] = b
		}
	}
	err = s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetOrderForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if cur.Status != OrderDraft {
			return fmt.Errorf("%w: lines can only be changed while the order is a draft", ErrConflict)
		}
		// Re-read under the lock: another editor may have changed the lines since the first read.
		now, err := q.ListOrderLines(ctx, id)
		if err != nil {
			return err
		}
		keep := map[int64]db.OrderLine{}
		for _, l := range now {
			if l.VariationID.Valid {
				keep[l.VariationID.Int64] = l
			}
		}
		var lines []db.InsertOrderLineParams
		for i, it := range items {
			arg := db.InsertOrderLineParams{OrderID: id, Position: int32(i + 1), Quantity: int32(it.Quantity)}
			if l, ok := keep[it.VariationID]; ok {
				arg.DesignID, arg.VariationID, arg.Snapshot, arg.Internal = l.DesignID, l.VariationID, l.Snapshot, l.Internal
			} else if b, ok := built[it.VariationID]; ok {
				snap, _ := json.Marshal(b.snap)
				in, _ := json.Marshal(b.internal)
				arg.DesignID = pgtype.Int8{Int64: b.designID, Valid: true}
				arg.VariationID = pgtype.Int8{Int64: it.VariationID, Valid: true}
				arg.Snapshot, arg.Internal = snap, in
			} else {
				return fmt.Errorf("%w: the order changed while you were editing it, reload and try again", ErrConflict)
			}
			lines = append(lines, arg)
		}
		if err := q.DeleteOrderLines(ctx, id); err != nil {
			return err
		}
		for _, l := range lines {
			if err := q.InsertOrderLine(ctx, l); err != nil {
				return err
			}
		}
		return q.TouchOrder(ctx, id)
	})
	if err != nil {
		return OrderDetail{}, err
	}
	return s.GetOrder(ctx, id)
}

// ChangeOrderStatus moves an order along the allowed transitions and records who did it.
func (s *Service) ChangeOrderStatus(ctx context.Context, id int64, to, note string, userID int64) (OrderDetail, error) {
	note = strings.TrimSpace(note)
	if !validOrderStatus(to) {
		return OrderDetail{}, invalid("status must be one of draft, placed, in_production, done, cancelled")
	}
	if len([]rune(note)) > 500 {
		return OrderDetail{}, invalid("note is at most 500 characters")
	}
	err := s.inTx(ctx, func(q *db.Queries) error {
		o, err := q.GetOrderForUpdate(ctx, id)
		if err != nil {
			return err
		}
		allowed := false
		for _, t := range orderTransitions[o.Status] {
			if t == to {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("%w: an order cannot go from %s to %s", ErrConflict, o.Status, to)
		}
		if err := q.SetOrderStatus(ctx, db.SetOrderStatusParams{ID: id, Status: to}); err != nil {
			return err
		}
		h := db.InsertOrderStatusHistoryParams{OrderID: id, ToStatus: to, Note: note, FromStatus: pgtype.Text{String: o.Status, Valid: true}}
		if userID != 0 {
			h.ChangedBy = pgtype.Int8{Int64: userID, Valid: true}
		}
		return q.InsertOrderStatusHistory(ctx, h)
	})
	if err != nil {
		return OrderDetail{}, err
	}
	return s.GetOrder(ctx, id)
}

// ---------- Read ----------

type OrderLine struct {
	ID          int64        `json:"id"`
	Position    int          `json:"position"`
	DesignID    *int64       `json:"design_id"`    // null once the design has been purged
	VariationID *int64       `json:"variation_id"` // null once the variation has been purged
	Quantity    int          `json:"quantity"`
	Snapshot    LineSnapshot `json:"snapshot"`
}

type OrderHistoryEntry struct {
	ID            int64     `json:"id"`
	FromStatus    *string   `json:"from_status"`
	ToStatus      string    `json:"to_status"`
	ChangedBy     *int64    `json:"changed_by"`
	ChangedByName string    `json:"changed_by_name"`
	ChangedAt     time.Time `json:"changed_at"`
	Note          string    `json:"note"`
}

type OrderSummary struct {
	ID            int64     `json:"id"`
	Number        string    `json:"number"`
	Name          string    `json:"name"`
	Status        string    `json:"status"`
	MetalCode     string    `json:"metal_code"`
	StoneCode     string    `json:"stone_code"`
	ClonedFrom    *int64    `json:"cloned_from"`
	CreatedBy     *int64    `json:"created_by"`
	CreatedByName string    `json:"created_by_name"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	LineCount     int       `json:"line_count"`
	TotalQuantity int64     `json:"total_quantity"`
}

type OrderDetail struct {
	OrderSummary
	Notes    string              `json:"notes"`
	Criteria json.RawMessage     `json:"criteria"`
	Lines    []OrderLine         `json:"lines"`
	History  []OrderHistoryEntry `json:"history"`
}

func (s *Service) ListOrders(ctx context.Context, status, q string, limit, offset int32) ([]OrderSummary, int64, error) {
	if status != "" && !validOrderStatus(status) {
		return nil, 0, invalid("unknown status %q", status)
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.Q.ListOrders(ctx, db.ListOrdersParams{Status: status, Q: strings.TrimSpace(q), Lim: limit, Off: offset})
	if err != nil {
		return nil, 0, err
	}
	out := make([]OrderSummary, 0, len(rows))
	var total int64
	for _, r := range rows {
		total = r.TotalCount
		out = append(out, OrderSummary{
			ID: r.ID, Number: r.Number, Name: r.Name, Status: r.Status, MetalCode: r.MetalCode, StoneCode: r.StoneCode,
			ClonedFrom: int8Ptr(r.ClonedFrom), CreatedBy: int8Ptr(r.CreatedBy), CreatedByName: r.CreatedByName,
			CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time, LineCount: int(r.LineCount), TotalQuantity: r.TotalQuantity,
		})
	}
	return out, total, nil
}

// GetOrder reads an order from its own tables only: nothing here depends on the live catalog.
func (s *Service) GetOrder(ctx context.Context, id int64) (OrderDetail, error) {
	r, err := s.Q.GetOrderRow(ctx, id)
	if err != nil {
		return OrderDetail{}, mapErr(err)
	}
	d := OrderDetail{
		OrderSummary: OrderSummary{
			ID: r.ID, Number: r.Number, Name: r.Name, Status: r.Status, MetalCode: r.MetalCode, StoneCode: r.StoneCode,
			ClonedFrom: int8Ptr(r.ClonedFrom), CreatedBy: int8Ptr(r.CreatedBy), CreatedByName: r.CreatedByName,
			CreatedAt: r.CreatedAt.Time, UpdatedAt: r.UpdatedAt.Time,
		},
		Notes: r.Notes, Criteria: json.RawMessage("null"), Lines: []OrderLine{}, History: []OrderHistoryEntry{},
	}
	if len(r.Criteria) > 0 {
		d.Criteria = r.Criteria
	}
	lines, err := s.Q.ListOrderLines(ctx, id)
	if err != nil {
		return d, err
	}
	for _, l := range lines {
		ol := OrderLine{ID: l.ID, Position: int(l.Position), DesignID: int8Ptr(l.DesignID), VariationID: int8Ptr(l.VariationID), Quantity: int(l.Quantity)}
		if err := json.Unmarshal(l.Snapshot, &ol.Snapshot); err != nil {
			return d, fmt.Errorf("order %d line %d: %w", id, l.ID, err)
		}
		d.TotalQuantity += int64(l.Quantity)
		d.Lines = append(d.Lines, ol)
	}
	d.LineCount = len(d.Lines)
	hist, err := s.Q.ListOrderStatusHistory(ctx, id)
	if err != nil {
		return d, err
	}
	for _, h := range hist {
		e := OrderHistoryEntry{ID: h.ID, ToStatus: h.ToStatus, ChangedBy: int8Ptr(h.ChangedBy), ChangedByName: h.ChangedByName, ChangedAt: h.ChangedAt.Time, Note: h.Note}
		if h.FromStatus.Valid {
			e.FromStatus = &h.FromStatus.String
		}
		d.History = append(d.History, e)
	}
	return d, nil
}

// OrderLineInternals returns the storage references of an order's lines, for server-side exports.
// Never send these to a client.
func (s *Service) OrderLineInternals(ctx context.Context, orderID int64) (map[int64]LineInternal, error) {
	lines, err := s.Q.ListOrderLines(ctx, orderID)
	if err != nil {
		return nil, err
	}
	out := map[int64]LineInternal{}
	for _, l := range lines {
		var in LineInternal
		if err := json.Unmarshal(l.Internal, &in); err != nil {
			return nil, err
		}
		out[l.ID] = in
	}
	return out, nil
}

// ---------- Clone / reorder ----------

// LineChange is one field that differs between the old snapshot and a fresh one.
type LineChange struct {
	Field string `json:"field"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

// LineDiff compares one line of the source order with its fresh snapshot.
type LineDiff struct {
	VariationID   *int64       `json:"variation_id"`
	DesignCode    string       `json:"design_code"`
	VariationCode string       `json:"variation_code"`
	Quantity      int          `json:"quantity"`
	Status        string       `json:"status"` // unchanged, changed or unavailable
	Reason        string       `json:"reason,omitempty"`
	Changes       []LineChange `json:"changes"`
}

type OrderDiff struct {
	SourceID  int64      `json:"source_id"`
	Changed   int        `json:"changed"`
	Unchanged int        `json:"unchanged"`
	Skipped   int        `json:"unavailable"`
	Lines     []LineDiff `json:"lines"`
}

// CloneOrderParams: Metal / stone code default to the source order's. SkipUnavailable drops lines whose
// variation is gone or has missing stone data and lists them in the diff; without it they fail the clone.
type CloneOrderParams struct {
	Name            string `json:"name"`
	MetalCode       string `json:"metal_code"`
	StoneCode       string `json:"stone_code"`
	SkipUnavailable bool   `json:"skip_unavailable"`
	Place           bool   `json:"place"`
	UserID          int64  `json:"-"`
}

// CloneOrder makes a new draft (or placed) order from an old one, re-snapshotting every line from the
// current catalog, and reports what differs from the old snapshots. The source order is untouched.
func (s *Service) CloneOrder(ctx context.Context, sourceID int64, p CloneOrderParams) (OrderDetail, OrderDiff, error) {
	src, err := s.GetOrder(ctx, sourceID)
	if err != nil {
		return OrderDetail{}, OrderDiff{}, err
	}
	metal, stone := p.MetalCode, p.StoneCode
	if metal == "" {
		metal = src.MetalCode
	}
	if stone == "" {
		stone = src.StoneCode
	}
	if err := s.checkMetalAndStone(ctx, metal, stone); err != nil {
		return OrderDetail{}, OrderDiff{}, err
	}
	diff := OrderDiff{SourceID: sourceID, Lines: []LineDiff{}}
	var items []OrderItem
	var purged []OrderProblem
	for _, l := range src.Lines {
		if l.VariationID == nil {
			purged = append(purged, OrderProblem{VariationCode: l.Snapshot.VariationCode, Message: "the variation has been purged"})
			continue
		}
		items = append(items, OrderItem{VariationID: *l.VariationID, Quantity: l.Quantity})
	}
	if len(purged) > 0 && !p.SkipUnavailable {
		return OrderDetail{}, OrderDiff{}, &OrderProblemsError{Problems: purged}
	}
	problems := map[int64]OrderProblem{}
	var built []builtLine
	for len(items) > 0 {
		bl, err := s.snapshotLines(ctx, metal, stone, items)
		if err == nil {
			built = bl
			break
		}
		var pe *OrderProblemsError
		if !errors.As(err, &pe) {
			return OrderDetail{}, OrderDiff{}, err
		}
		if !p.SkipUnavailable {
			return OrderDetail{}, OrderDiff{}, err
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
	if len(built) == 0 && len(items) == 0 {
		diff.fill(src, nil, problems)
		return OrderDetail{}, diff, invalid("none of the lines can be ordered again")
	}
	diff.fill(src, built, problems)
	name := p.Name
	if name == "" {
		name = src.Name
	}
	cp := CreateOrderParams{Name: name, Notes: src.Notes, MetalCode: metal, StoneCode: stone, Items: items,
		Criteria: src.Criteria, Place: p.Place, UserID: p.UserID, clonedFrom: sourceID}
	if err := checkHeader(cp.Name, cp.Notes); err != nil {
		return OrderDetail{}, diff, err
	}
	criteria, err := checkCriteria(cp.Criteria)
	if err != nil {
		return OrderDetail{}, diff, err
	}
	o, err := s.storeOrder(ctx, cp, criteria, built)
	return o, diff, err
}

// fill compares each source line with its fresh snapshot, or records why it has none, in the source
// order's line order.
func (d *OrderDiff) fill(src OrderDetail, built []builtLine, problems map[int64]OrderProblem) {
	fresh := map[int64]builtLine{}
	for _, b := range built {
		fresh[b.item.VariationID] = b
	}
	d.Lines = []LineDiff{}
	for _, l := range src.Lines {
		ld := LineDiff{VariationID: l.VariationID, DesignCode: l.Snapshot.DesignCode, VariationCode: l.Snapshot.VariationCode, Quantity: l.Quantity, Changes: []LineChange{}}
		switch {
		case l.VariationID == nil:
			ld.Status, ld.Reason = "unavailable", "the variation has been purged"
			d.Skipped++
		case problems[*l.VariationID].Message != "":
			ld.Status, ld.Reason = "unavailable", problems[*l.VariationID].Message
			d.Skipped++
		default:
			ld.Changes = diffSnapshots(l.Snapshot, fresh[*l.VariationID].snap)
			if len(ld.Changes) == 0 {
				ld.Status = "unchanged"
				d.Unchanged++
			} else {
				ld.Status = "changed"
				d.Changed++
			}
		}
		d.Lines = append(d.Lines, ld)
	}
}

func fileText(f *FileSnapshot) string {
	if f == nil {
		return "none"
	}
	return fmt.Sprintf("%s (%s, %d bytes)", f.Name, f.SHA256, f.SizeBytes)
}

func bomText(b []BOMLineOut) string {
	parts := make([]string, len(b))
	for i, l := range b {
		parts[i] = fmt.Sprintf("%d x %s mm %s", l.StoneCount, l.SizeMM.StringFixed(2), l.StoneCode)
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

func baseText(b BaseInputSnapshot) string {
	switch {
	case b.VolumeMM3 != nil:
		return b.VolumeMM3.String() + " mm3"
	case b.RefWeightG != nil:
		return b.RefWeightG.String() + " g " + b.RefMetalCode
	}
	return "none"
}

// diffSnapshots lists the fields that differ between an old and a fresh snapshot. The snapshot time is
// not one of them.
func diffSnapshots(a, b LineSnapshot) []LineChange {
	out := []LineChange{}
	add := func(field, x, y string) {
		if x != y {
			out = append(out, LineChange{Field: field, Old: x, New: y})
		}
	}
	add("design_name", a.DesignName, b.DesignName)
	add("category", a.CategoryName, b.CategoryName)
	add("tags", strings.Join(a.Tags, ", "), strings.Join(b.Tags, ", "))
	add("base_input", baseText(a.BaseInput), baseText(b.BaseInput))
	add("net_weight_g", a.NetG.StringFixed(3), b.NetG.StringFixed(3))
	add("stone_weight_g", a.StoneG.StringFixed(3), b.StoneG.StringFixed(3))
	add("gross_weight_g", a.GrossG.StringFixed(3), b.GrossG.StringFixed(3))
	add("stone_pct", a.StonePct.StringFixed(2), b.StonePct.StringFixed(2))
	add("total_pcs", fmt.Sprint(a.TotalPcs), fmt.Sprint(b.TotalPcs))
	add("stone_bom", bomText(a.BOM), bomText(b.BOM))
	add("file_3dm", fileText(a.File3dm), fileText(b.File3dm))
	add("file_stl", fileText(a.FileStl), fileText(b.FileStl))
	return out
}
