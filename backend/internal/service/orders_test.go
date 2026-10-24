package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

// orderFixture is a service with the stone-matrix fixtures and one design with two variations: V1 has the
// fixture seats and both CAD files, V2 is a plain 100 mm3 piece with the 24 x 1.10 mm seat only.
type orderFixture struct {
	s      *Service
	mem    *storage.Memory
	design int64
	code   string
	v1, v2 int64
	d3dm   []byte
	dStl   []byte
}

func newOrderFixture(t *testing.T) *orderFixture {
	t.Helper()
	s, mem := newCADService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR0900")
	v1, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSeats(ctx, v1.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	v2, err := s.CreateVariation(ctx, des.ID, BaseInput{RefWeightG: dp("3.550"), RefMetalCode: "22KY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetSeats(ctx, v2.ID, []SeatInput{{SizeMM: d("1.10"), StoneCount: 24}}); err != nil {
		t.Fatal(err)
	}
	f := &orderFixture{s: s, mem: mem, design: des.ID, code: des.Code, v1: v1.ID, v2: v2.ID,
		d3dm: randBytes(t, 4096), dStl: randBytes(t, 2048)}
	if _, err := s.UploadCAD(ctx, v1.ID, "ring.3dm", bytes.NewReader(f.d3dm)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v1.ID, "ring.stl", bytes.NewReader(f.dStl)); err != nil {
		t.Fatal(err)
	}
	vid := v1.ID
	if _, err := s.AddMedia(ctx, AddMediaInput{DesignID: des.ID, VariationID: &vid, Filename: "front.png", Body: bytes.NewReader(testPNG(40, 30))}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *orderFixture) create(t *testing.T, items ...OrderItem) OrderDetail {
	t.Helper()
	o, err := f.s.CreateOrder(context.Background(), CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: items})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func snapshotsJSON(t *testing.T, o OrderDetail) string {
	t.Helper()
	type line struct {
		Quantity int          `json:"q"`
		Snapshot LineSnapshot `json:"s"`
	}
	var ls []line
	for _, l := range o.Lines {
		ls = append(ls, line{l.Quantity, l.Snapshot})
	}
	b, err := json.Marshal(ls)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestOrderSnapshotAgreesWithTheFixtures(t *testing.T) {
	f := newOrderFixture(t)
	o := f.create(t, OrderItem{f.v1, 5})
	if o.Number != "ORD-0001" || o.Status != OrderDraft || len(o.Lines) != 1 || o.TotalQuantity != 5 || len(o.History) != 1 {
		t.Fatalf("order = %+v", o)
	}
	l := o.Lines[0]
	sn := l.Snapshot
	if sn.DesignCode != f.code || sn.VariationCode != f.code+"-V1" || sn.CategoryName == "" || sn.MetalCode != "18KR" || sn.StoneCode != "2HD" {
		t.Errorf("identity = %+v", sn)
	}
	for name, c := range map[string][2]string{
		"net": {sn.NetG.StringFixed(3), "3.050"}, "gross": {sn.GrossG.StringFixed(3), "3.119"},
		"carats": {sn.StoneCt.StringFixed(4), "0.3470"}, "stone g": {sn.StoneG.StringFixed(3), "0.069"},
		"stone pct": {sn.StonePct.StringFixed(2), "2.23"},
	} {
		if c[0] != c[1] {
			t.Errorf("%s = %s, want %s", name, c[0], c[1])
		}
	}
	if sn.TotalPcs != 37 || len(sn.BOM) != 3 || sn.BaseInput.Kind != "volume" || sn.BaseInput.VolumeMM3 == nil || !sn.BaseInput.VolumeMM3.Equal(d("200")) {
		t.Errorf("bom/base = %+v", sn)
	}
	if sn.File3dm == nil || sn.File3dm.SHA256 != sha(f.d3dm) || sn.File3dm.Name != "ring.3dm" || sn.File3dm.SizeBytes != 4096 ||
		sn.FileStl == nil || sn.FileStl.SHA256 != sha(f.dStl) || sn.FileStl.SizeBytes != 2048 {
		t.Errorf("files = %+v %+v", sn.File3dm, sn.FileStl)
	}
	if sn.Cover == nil || sn.Cover.Filename != "front.png" {
		t.Errorf("cover = %+v", sn.Cover)
	}

	// Same numbers as simulate-bom, for the same metal and stone code.
	bom, err := f.s.SimulateBOM(context.Background(), f.v1, "2HD")
	if err != nil {
		t.Fatal(err)
	}
	if bom.TotalPcs != sn.TotalPcs || !bom.TotalCarats.Equal(sn.StoneCt) || !bom.StoneWeightG.Equal(sn.StoneG) {
		t.Errorf("bom = %+v", bom)
	}
	for _, m := range bom.Metals {
		if m.MetalCode == "18KR" && (!m.NetG.Equal(sn.NetG) || !m.GrossG.Equal(sn.GrossG)) {
			t.Errorf("metal totals differ from simulate-bom: %+v", m)
		}
	}

	// Reference-weight base input is kept as such; the weight in 18KR follows the SG ratio.
	o2 := f.create(t, OrderItem{f.v2, 1})
	if b := o2.Lines[0].Snapshot.BaseInput; b.Kind != "weight" || b.RefMetalCode != "22KY" || b.RefWeightG == nil {
		t.Errorf("base input = %+v", b)
	}

	// Storage keys never reach the client-facing JSON; they stay internal.
	raw, _ := json.Marshal(o)
	if bytes.Contains(raw, []byte("cad/")) || bytes.Contains(raw, []byte("thumb")) || bytes.Contains(raw, []byte("object_key")) {
		t.Errorf("storage reference leaked: %s", raw)
	}
	in, err := f.s.OrderLineInternals(context.Background(), o.ID)
	if err != nil || !strings.HasPrefix(in[l.ID].File3dmKey, "cad/") || in[l.ID].FileStlKey == "" || in[l.ID].CoverThumb == "" {
		t.Errorf("internals = %+v %v", in, err)
	}
}

func TestOrderSnapshotSurvivesEditsDeletionAndPurge(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	f.s.Retention = 0 // deleting a variation purges it at once
	o := f.create(t, OrderItem{f.v1, 5}, OrderItem{f.v2, 2})
	before := snapshotsJSON(t, o)

	read := func(step string) OrderDetail {
		t.Helper()
		got, err := f.s.GetOrder(ctx, o.ID)
		if err != nil {
			t.Fatalf("%s: %v", step, err)
		}
		if after := snapshotsJSON(t, got); after != before {
			t.Fatalf("%s changed the order:\nbefore %s\nafter  %s", step, before, after)
		}
		return got
	}

	// Edit the design name and category-free data, the seats, a metal's SG, and the stone matrix.
	if _, err := f.s.UpdateDesign(ctx, f.design, DesignInput{Name: "Renamed", CategoryID: mustCategory(t, f.s, "Pendants")}); err != nil {
		t.Fatal(err)
	}
	read("rename and recategorise")
	if _, err := f.s.SetSeats(ctx, f.v1, []SeatInput{{SizeMM: d("1.20"), StoneCount: 99}}); err != nil {
		t.Fatal(err)
	}
	read("new seats")
	metals, _ := f.s.ListMetals(ctx)
	for _, m := range metals {
		if m.Code == "18KR" {
			if _, err := f.s.UpdateMetal(ctx, m.ID, MetalUpdate{SpecificGravity: d("20.00"), WastagePct: m.WastagePct, IsDefaultInput: m.IsDefaultInput, Active: true}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := f.s.UpdateStoneMatrix(ctx, []MatrixRowInput{{SizeMM: d("1.10"), StoneCode: "2HD", CaratsPerStone: d("0.0999")}}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateVariationBase(ctx, f.v1, BaseInput{VolumeMM3: dp("999")}); err != nil {
		t.Fatal(err)
	}
	read("SG, matrix and base input")
	if err := f.s.RemoveCAD(ctx, f.v1, "3dm"); err != nil {
		t.Fatal(err)
	}
	read("removed CAD file")

	if err := f.s.DeleteDesign(ctx, f.design); err != nil {
		t.Fatal(err)
	}
	got := read("soft-deleted design")
	if got.Lines[0].VariationID == nil || got.Lines[0].DesignID == nil {
		t.Error("links must still be set while the design is only soft-deleted")
	}

	// Hard purge: the variation rows, then the design row itself.
	if _, err := f.s.Pool.Exec(ctx, "DELETE FROM cad_variations WHERE design_id = $1", f.design); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Pool.Exec(ctx, "DELETE FROM master_designs WHERE id = $1", f.design); err != nil {
		t.Fatal(err)
	}
	got = read("purged design and variations")
	for _, l := range got.Lines {
		if l.VariationID != nil || l.DesignID != nil {
			t.Errorf("links must become null after a purge: %+v", l)
		}
	}
	// And an order whose variations are gone can still be seen in the list.
	list, total, err := f.s.ListOrders(ctx, "", "", nil, nil, 50, 0)
	if err != nil || total != 1 || list[0].LineCount != 2 || list[0].TotalQuantity != 7 {
		t.Errorf("list = %+v %d %v", list, total, err)
	}
}

func TestMissingStoneDataIsRefusedWithAListingAndUsesNoNumber(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	// 1HD has no row for 2.50 mm, but only V1 locks 2HD at that size; V2 has a 1.10 seat only, so add a
	// flexible 3.00 mm seat (no matrix row for any code) to make V2 incomplete.
	if _, err := f.s.SetSeats(ctx, f.v2, []SeatInput{{SizeMM: d("3.00"), StoneCount: 2}}); err != nil {
		t.Fatal(err) // flexible seats are allowed without a matrix row; the gap shows at BOM time
	}
	_, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, 1}, {f.v2, 1}}})
	var pe *OrderProblemsError
	if !errors.As(err, &pe) || len(pe.Problems) != 1 || pe.Problems[0].VariationCode != f.code+"-V2" ||
		len(pe.Problems[0].Missing) == 0 || pe.Problems[0].Missing[0].SizeMM != "3.00" {
		t.Fatalf("err = %v (%+v)", err, pe)
	}
	if !strings.Contains(err.Error(), f.code+"-V2") {
		t.Errorf("message does not name the variation: %v", err)
	}
	// Nothing was created and no number was used.
	if _, total, _ := f.s.ListOrders(ctx, "", "", nil, nil, 50, 0); total != 0 {
		t.Errorf("orders = %d", total)
	}
	if o := f.create(t, OrderItem{f.v1, 1}); o.Number != "ORD-0001" {
		t.Errorf("number = %s", o.Number)
	}
	// 1HD is also missing 2.50 mm for V1's locked 2HD seat? No: locked seats keep their code, so it works.
	if _, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "1HD", Items: []OrderItem{{f.v1, 1}}}); err != nil {
		t.Errorf("1HD with a locked 2HD seat: %v", err)
	}
}

func TestOrderNumbersAreGapFreeUnderConcurrency(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	const n = 20
	nums := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, 1}}})
			if err != nil {
				t.Error(err)
				return
			}
			nums[i] = o.Number
		}()
	}
	// Failing creations in the middle (a missing variation) must not eat numbers either.
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{999999, 1}}}); err == nil {
				t.Error("order of a missing variation was accepted")
			}
		}()
	}
	wg.Wait()
	sort.Strings(nums)
	for i, got := range nums {
		if want := FormatOrderNumber(int32(i + 1)); got != want {
			t.Fatalf("numbers = %v (want ORD-0001..ORD-0020, no repeats, no gaps)", nums)
		}
	}
	// A rollback after the number was taken (the status history cannot be written) gives it back.
	if _, err := f.s.Pool.Exec(ctx, "ALTER TABLE order_status_history ADD CONSTRAINT boom CHECK (false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, 1}}}); err == nil {
		t.Fatal("expected the forced failure")
	}
	if _, err := f.s.Pool.Exec(ctx, "ALTER TABLE order_status_history DROP CONSTRAINT boom"); err != nil {
		t.Fatal(err)
	}
	if o := f.create(t, OrderItem{f.v1, 1}); o.Number != "ORD-0021" {
		t.Errorf("number after a rolled back create = %s, want ORD-0021", o.Number)
	}
}

func TestOrderStatusTransitionsAreValidatedAndRecorded(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	for _, c := range []struct {
		path []string
		ok   bool
	}{
		{[]string{"placed", "in_production", "done"}, true},
		{[]string{"cancelled"}, true},
		{[]string{"placed", "cancelled"}, true},
		{[]string{"placed", "in_production", "cancelled"}, true},
		{[]string{"in_production"}, false},
		{[]string{"done"}, false},
		{[]string{"draft"}, false},
		{[]string{"placed", "done"}, false},
		{[]string{"placed", "draft"}, false},
		{[]string{"placed", "in_production", "done", "cancelled"}, false},
		{[]string{"placed", "in_production", "done", "placed"}, false},
		{[]string{"cancelled", "placed"}, false},
		{[]string{"bogus"}, false},
	} {
		o := f.create(t, OrderItem{f.v1, 1})
		var err error
		for _, st := range c.path {
			if o, err = f.s.ChangeOrderStatus(ctx, o.ID, st, "", 0); err != nil {
				break
			}
		}
		switch {
		case c.ok && err != nil:
			t.Errorf("%v refused: %v", c.path, err)
		case !c.ok && err == nil:
			t.Errorf("%v accepted", c.path)
		case !c.ok && !errors.Is(err, ErrConflict) && !errors.Is(err, ErrInvalid):
			t.Errorf("%v: unexpected error type %v", c.path, err)
		}
	}

	o := f.create(t, OrderItem{f.v1, 1})
	if _, err := f.s.ChangeOrderStatus(ctx, o.ID, "placed", "to the shop", 0); err != nil {
		t.Fatal(err)
	}
	o, err := f.s.ChangeOrderStatus(ctx, o.ID, "cancelled", "buyer changed mind", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(o.History) != 3 || o.History[0].ToStatus != "draft" || o.History[0].FromStatus != nil ||
		o.History[1].ToStatus != "placed" || *o.History[1].FromStatus != "draft" || o.History[1].Note != "to the shop" ||
		o.History[2].ToStatus != "cancelled" || *o.History[2].FromStatus != "placed" || o.History[2].ChangedAt.IsZero() {
		t.Errorf("history = %+v", o.History)
	}
	// Finished orders cannot be edited either, and a cancelled order keeps its lines (never deleted).
	if _, err := f.s.UpdateOrderHeader(ctx, o.ID, "x", "", ""); !errors.Is(err, ErrConflict) {
		t.Errorf("edit of a cancelled order: %v", err)
	}
	if len(o.Lines) != 1 {
		t.Errorf("lines = %d", len(o.Lines))
	}
}

func TestLinesCanOnlyChangeWhileDraftAndKeepTheirSnapshot(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	o := f.create(t, OrderItem{f.v1, 1})
	oldName := o.Lines[0].Snapshot.DesignName

	if _, err := f.s.UpdateDesign(ctx, f.design, DesignInput{Name: "Changed after ordering", CategoryID: mustCategory(t, f.s, "Rings")}); err != nil {
		t.Fatal(err)
	}
	// Add V2 and change V1's quantity: V1 keeps the snapshot it had, V2 is snapshotted now.
	o, err := f.s.SetOrderLines(ctx, o.ID, []OrderItem{{f.v2, 3}, {f.v1, 7}})
	if err != nil {
		t.Fatal(err)
	}
	if len(o.Lines) != 2 || o.Lines[0].Snapshot.VariationCode != f.code+"-V2" || o.Lines[0].Snapshot.DesignName != "Changed after ordering" ||
		o.Lines[1].Quantity != 7 || o.Lines[1].Snapshot.DesignName != oldName {
		t.Errorf("lines = %+v", o.Lines)
	}
	if o.TotalQuantity != 10 {
		t.Errorf("total quantity = %d", o.TotalQuantity)
	}
	// A bad replacement changes nothing.
	if _, err := f.s.SetOrderLines(ctx, o.ID, []OrderItem{{f.v1, 0}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("quantity 0: %v", err)
	}
	if got, _ := f.s.GetOrder(ctx, o.ID); len(got.Lines) != 2 {
		t.Errorf("a refused edit changed the order")
	}
	if _, err := f.s.ChangeOrderStatus(ctx, o.ID, "placed", "", 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetOrderLines(ctx, o.ID, []OrderItem{{f.v1, 1}}); !errors.Is(err, ErrConflict) {
		t.Errorf("lines of a placed order: %v", err)
	}
	// Name, notes and due date still change on a placed order.
	o, err = f.s.UpdateOrderHeader(ctx, o.ID, "Mehta wedding", "rush", "2026-12-31")
	if err != nil || o.Name != "Mehta wedding" || o.Notes != "rush" || o.DueDate == nil || *o.DueDate != "2026-12-31" {
		t.Errorf("header = %+v %v", o, err)
	}
	if _, err := f.s.UpdateOrderHeader(ctx, o.ID, "x", "", "31/12/2026"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad date: %v", err)
	}
}

func TestOrderValidation(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	one := []OrderItem{{f.v1, 1}}
	long := strings.Repeat("x", 121)
	for name, p := range map[string]CreateOrderParams{
		"no items":         {MetalCode: "18KR", StoneCode: "2HD"},
		"no metal":         {StoneCode: "2HD", Items: one},
		"no stone code":    {MetalCode: "18KR", Items: one},
		"unknown metal":    {MetalCode: "99KX", StoneCode: "2HD", Items: one},
		"unknown stone":    {MetalCode: "18KR", StoneCode: "ZZZ", Items: one},
		"quantity zero":    {MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, 0}}},
		"quantity huge":    {MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, MaxLineQuantity + 1}}},
		"duplicate lines":  {MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v1, 1}, {f.v1, 2}}},
		"criteria not obj": {MetalCode: "18KR", StoneCode: "2HD", Items: one, Criteria: json.RawMessage(`[1,2]`)},
		"criteria garbage": {MetalCode: "18KR", StoneCode: "2HD", Items: one, Criteria: json.RawMessage(`{`)},
		"name too long":    {MetalCode: "18KR", StoneCode: "2HD", Items: one, Name: long},
		"bad due date":     {MetalCode: "18KR", StoneCode: "2HD", Items: one, DueDate: "tomorrow"},
	} {
		if _, err := f.s.CreateOrder(ctx, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// Size cap, checked before any catalog work.
	big := make([]OrderItem, MaxOrderLines+1)
	for i := range big {
		big[i] = OrderItem{int64(i + 1), 1}
	}
	if _, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: big}); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "500") {
		t.Errorf("size cap: %v", err)
	}
	// Deleted designs and variations cannot be ordered.
	if err := f.s.DeleteVariation(ctx, f.v2); err != nil {
		t.Fatal(err)
	}
	var pe *OrderProblemsError
	if _, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []OrderItem{{f.v2, 1}}}); !errors.As(err, &pe) {
		t.Errorf("deleted variation: %v", err)
	}
	// Criteria is optional and stored as given.
	o, err := f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: one, Criteria: json.RawMessage(`{"category_id":1,"min_stone_pct":"2"}`)})
	if err != nil || !strings.Contains(string(o.Criteria), "min_stone_pct") {
		t.Errorf("criteria = %s %v", o.Criteria, err)
	}
	o, err = f.s.CreateOrder(ctx, CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: one, Place: true})
	if err != nil || o.Status != OrderPlaced || string(o.Criteria) != "null" || len(o.History) != 1 {
		t.Errorf("placed at once = %+v %v", o, err)
	}
}

func TestCloneReSnapshotsAndReportsTheDifference(t *testing.T) {
	f := newOrderFixture(t)
	ctx := context.Background()
	src := f.create(t, OrderItem{f.v1, 4}, OrderItem{f.v2, 2})
	if _, err := f.s.ChangeOrderStatus(ctx, src.ID, "cancelled", "", 0); err != nil {
		t.Fatal(err)
	}
	// Nothing changed yet: every line is unchanged.
	cl, diff, err := f.s.CloneOrder(ctx, src.ID, CloneOrderParams{})
	if err != nil {
		t.Fatal(err)
	}
	if cl.Number != "ORD-0002" || cl.Status != OrderDraft || cl.ClonedFrom == nil || *cl.ClonedFrom != src.ID ||
		diff.Changed != 0 || diff.Unchanged != 2 || diff.Skipped != 0 || len(cl.Lines) != 2 || cl.Lines[0].Quantity != 4 {
		t.Fatalf("clone = %+v diff = %+v", cl, diff)
	}

	// Edit V1's seats and rename the design, then clone again.
	if _, err := f.s.SetSeats(ctx, f.v1, []SeatInput{{SizeMM: d("1.20"), StoneCount: 10}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateDesign(ctx, f.design, DesignInput{Name: "Sunburst", CategoryID: mustCategory(t, f.s, "Rings")}); err != nil {
		t.Fatal(err)
	}
	cl, diff, err = f.s.CloneOrder(ctx, src.ID, CloneOrderParams{Name: "Reorder"})
	if err != nil {
		t.Fatal(err)
	}
	if diff.Changed != 2 || diff.Lines[0].Status != "changed" || cl.Name != "Reorder" || cl.Lines[0].Snapshot.TotalPcs != 10 {
		t.Fatalf("diff = %+v", diff)
	}
	fields := map[string]LineChange{}
	for _, c := range diff.Lines[0].Changes {
		fields[c.Field] = c
	}
	if fields["stone_bom"].Old == fields["stone_bom"].New || fields["total_pcs"].Old != "37" || fields["total_pcs"].New != "10" ||
		fields["design_name"].New != "Sunburst" || fields["gross_weight_g"].Old != "3.119" {
		t.Errorf("changes = %+v", diff.Lines[0].Changes)
	}
	// The source order is untouched by all of this.
	if got, _ := f.s.GetOrder(ctx, src.ID); got.Lines[0].Snapshot.TotalPcs != 37 || got.Lines[0].Snapshot.DesignName == "Sunburst" || got.Status != OrderCancelled {
		t.Errorf("source changed: %+v", got.Lines[0].Snapshot)
	}

	// A gone variation fails the clone and is listed, unless unavailable lines may be skipped.
	f.s.Retention = 0
	if err := f.s.DeleteVariation(ctx, f.v2); err != nil {
		t.Fatal(err)
	}
	var pe *OrderProblemsError
	if _, _, err := f.s.CloneOrder(ctx, src.ID, CloneOrderParams{}); !errors.As(err, &pe) || len(pe.Problems) != 1 {
		t.Fatalf("clone with a deleted variation: %v", err)
	}
	cl, diff, err = f.s.CloneOrder(ctx, src.ID, CloneOrderParams{SkipUnavailable: true})
	if err != nil || len(cl.Lines) != 1 || diff.Skipped != 1 || diff.Lines[1].Status != "unavailable" || diff.Lines[1].Reason == "" {
		t.Fatalf("skip: %+v %+v %v", cl, diff, err)
	}
	// After a real purge the source line has no live link at all: same rule.
	if _, _, err := f.s.CloneOrder(ctx, src.ID, CloneOrderParams{}); err == nil {
		t.Error("clone of an order with purged variations must be refused without skip")
	}
}
