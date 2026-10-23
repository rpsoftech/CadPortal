package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

func weightsOf(t *testing.T, s *Service, code string, seq int) VariationDetail {
	t.Helper()
	got, err := s.GetDesignByCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got.Variations {
		if int(v.Seq) == seq {
			return v
		}
	}
	t.Fatalf("%s has no variation with seq %d: %+v", code, seq, got.Variations)
	return VariationDetail{}
}

func TestDeletedVariationIsHiddenEverywhereAndRestoresIdentical(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR0800")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("900")})
	if _, err := s.SetSeats(ctx, v2.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v2.ID, "a.3dm", bytes.NewReader([]byte("3dm data"))); err != nil {
		t.Fatal(err)
	}
	vid := v2.ID
	if _, err := s.AddMedia(ctx, AddMediaInput{DesignID: des.ID, VariationID: &vid, Filename: "p.png", Body: bytes.NewReader(testPNG(40, 30))}); err != nil {
		t.Fatal(err)
	}
	before := weightsOf(t, s, des.Code, 2)
	seatsBefore, _ := s.ListSeats(ctx, v2.ID)
	objects := len(mem.Keys())

	if err := s.DeleteVariation(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != objects {
		t.Errorf("objects must be kept while the variation is in the trash: %v", mem.Keys())
	}

	// Invisible: design detail, photos, catalog count and weight range, and every edit path.
	got, _ := s.GetDesignByCode(ctx, des.Code)
	if len(got.Variations) != 1 || got.Variations[0].ID != v1.ID || len(got.Media) != 0 {
		t.Errorf("design detail still shows it: %+v media=%d", got.Variations, len(got.Media))
	}
	list, err := s.ListDesigns(ctx, DesignFilter{Query: des.Code, MetalCode: "18KR"})
	if err != nil || len(list) != 1 || list[0].VariationCount != 1 ||
		list[0].NetMax == nil || !list[0].NetMax.Equal(d("3.050")) {
		t.Errorf("catalog: %+v err=%v", list, err)
	}
	if list, _ := s.ListDesigns(ctx, DesignFilter{MetalCode: "18KR", MinNetWt: dp("10")}); len(list) != 0 {
		t.Errorf("a deleted variation still matches weight filters: %d", len(list))
	}
	if _, err := s.UpdateVariationBase(ctx, v2.ID, BaseInput{VolumeMM3: dp("5")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("update base: %v", err)
	}
	if _, err := s.SetSeats(ctx, v2.ID, fixtureSeats()); !errors.Is(err, ErrNotFound) {
		t.Errorf("set seats: %v", err)
	}
	if _, err := s.CloneSeats(ctx, v1.ID, v2.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("clone seats from it: %v", err)
	}
	if _, err := s.SimulateBOM(ctx, v2.ID, "2HD"); !errors.Is(err, ErrNotFound) {
		t.Errorf("simulate bom: %v", err)
	}
	if _, err := s.DownloadCAD(ctx, v2.ID, "3dm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("download: %v", err)
	}
	if _, err := s.UploadCAD(ctx, v2.ID, "b.stl", bytes.NewReader([]byte("x"))); !errors.Is(err, ErrNotFound) {
		t.Errorf("upload: %v", err)
	}
	if err := s.DeleteVariation(ctx, v2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}
	var weights int
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM variation_metal_weights WHERE variation_id=$1", v2.ID).Scan(&weights) //nolint:errcheck
	if weights != 0 {
		t.Errorf("a deleted variation keeps %d weight rows", weights)
	}
	gaps, _ := s.Q.ListMatrixGaps(ctx)
	if len(gaps) != 0 {
		t.Errorf("gaps = %+v", gaps)
	}

	// Its code and its checksum stay reserved.
	next, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("100")})
	if err != nil || next.Code != des.Code+"-V3" {
		t.Errorf("next code = %q err=%v", next.Code, err)
	}
	_, err = s.UploadCAD(ctx, next.ID, "same.3dm", bytes.NewReader([]byte("3dm data")))
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), des.Code+"-V2") || !strings.Contains(err.Error(), "deleted") {
		t.Errorf("same file again: %v", err)
	}

	// It is in the trash, and only there.
	tr, _ := s.ListTrash(ctx)
	if len(tr.Variations) != 1 || tr.Variations[0].Code != des.Code+"-V2" || tr.Variations[0].DesignCode != des.Code ||
		!tr.Variations[0].HasFiles || tr.Variations[0].DesignDeleted || tr.Variations[0].PurgeAfter == nil ||
		time.Until(*tr.Variations[0].PurgeAfter) < 29*24*time.Hour {
		t.Errorf("trash variations = %+v", tr.Variations)
	}
	if len(tr.Files) != 0 {
		t.Errorf("its files must not be listed a second time: %+v", tr.Files)
	}

	// Restoring brings back the very same thing, weights included.
	if _, err := s.RestoreVariation(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	after := weightsOf(t, s, des.Code, 2)
	if after.Code != before.Code || after.ID != before.ID || len(after.Weights) == 0 || len(after.Weights) != len(before.Weights) ||
		!after.BaselineStoneCarats.Equal(before.BaselineStoneCarats) || after.File3dmName.String != "a.3dm" {
		t.Fatalf("after = %+v before = %+v", after, before)
	}
	for i, w := range after.Weights {
		if !w.NetWeightG.Equal(before.Weights[i].NetWeightG) || w.GrossWeightG.Decimal.Cmp(before.Weights[i].GrossWeightG.Decimal) != 0 {
			t.Errorf("weight %s: %v vs %v", w.MetalCode, w, before.Weights[i])
		}
	}
	seatsAfter, _ := s.ListSeats(ctx, v2.ID)
	if len(seatsAfter) != 3 || len(seatsAfter) != len(seatsBefore) {
		t.Errorf("seats = %+v", seatsAfter)
	}
	if _, err := s.DownloadCAD(ctx, v2.ID, "3dm"); err != nil {
		t.Errorf("download after restore: %v", err)
	}
	if got, _ := s.GetDesignByCode(ctx, des.Code); len(got.Variations) != 3 || len(got.Media) != 1 {
		t.Errorf("after restore: %d variations, %d media", len(got.Variations), len(got.Media))
	}
	if tr, _ := s.ListTrash(ctx); len(tr.Variations) != 0 {
		t.Errorf("still in the trash: %+v", tr.Variations)
	}
	if _, err := s.RestoreVariation(ctx, v2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("restoring twice: %v", err)
	}
}

func TestRestoreVariationNeedsItsDesign(t *testing.T) {
	s, _ := trashService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0801")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if err := s.DeleteVariation(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteDesign(ctx, des.ID); err != nil {
		t.Fatal(err)
	}
	if tr, _ := s.ListTrash(ctx); len(tr.Variations) != 1 || !tr.Variations[0].DesignDeleted {
		t.Errorf("trash = %+v", tr.Variations)
	}
	if _, err := s.RestoreVariation(ctx, v.ID); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restore the design first") {
		t.Errorf("restore under a deleted design: %v", err)
	}
	if _, err := s.RestoreDesign(ctx, des.ID); err != nil {
		t.Fatal(err)
	}
	// Restoring the design does not bring the variation back by itself...
	if got, _ := s.GetDesignByCode(ctx, des.Code); len(got.Variations) != 0 {
		t.Errorf("variations = %+v", got.Variations)
	}
	// ...but now it can be.
	if _, err := s.RestoreVariation(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetDesignByCode(ctx, des.Code); len(got.Variations) != 1 || len(got.Variations[0].Weights) == 0 {
		t.Errorf("variations = %+v", got.Variations)
	}
}

func TestPurgeDeletesExpiredVariationsForGood(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0802")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	s.UploadCAD(ctx, v1.ID, "keep.3dm", bytes.NewReader(randBytes(t, 500))) //nolint:errcheck
	s.UploadCAD(ctx, v2.ID, "gone.3dm", bytes.NewReader(randBytes(t, 500))) //nolint:errcheck
	vid := v2.ID
	if _, err := s.AddMedia(ctx, AddMediaInput{DesignID: des.ID, VariationID: &vid, Filename: "p.png", Body: bytes.NewReader(testPNG(40, 30))}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVariation(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeTrash(ctx); err != nil || n != 0 || len(mem.Keys()) != 4 {
		t.Fatalf("early purge: %d %v %v", n, err, mem.Keys())
	}
	if _, err := s.Pool.Exec(ctx, "UPDATE cad_variations SET purge_after = now() - interval '1 minute' WHERE id=$1", v2.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := s.PurgeTrash(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if keys := mem.Keys(); len(keys) != 1 || !strings.Contains(keys[0], des.Code+"-V1") {
		t.Errorf("objects left: %v", keys)
	}
	var rows int
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM cad_variations WHERE id=$1", v2.ID).Scan(&rows) //nolint:errcheck
	if rows != 0 {
		t.Error("the row must be gone")
	}
	if _, err := s.RestoreVariation(ctx, v2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("restore after purge: %v", err)
	}
}

func TestRetentionZeroDeletesAVariationAtOnce(t *testing.T) {
	s, mem := newCADService(t) // Retention is 0
	ctx := context.Background()
	des := newDesign(t, s, "LR0803")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	s.UploadCAD(ctx, v.ID, "a.3dm", bytes.NewReader(randBytes(t, 500))) //nolint:errcheck
	if err := s.DeleteVariation(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	var rows int
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM cad_variations WHERE id=$1", v.ID).Scan(&rows) //nolint:errcheck
	if rows != 0 || len(mem.Keys()) != 0 {
		t.Errorf("rows=%d objects=%v", rows, mem.Keys())
	}
	if tr, _ := s.ListTrash(ctx); len(tr.Variations) != 0 {
		t.Errorf("trash = %+v", tr.Variations)
	}
}

func TestDuplicateDesign(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	setPrefixes(t, s)
	rings := mustCategory(t, s, "Rings")
	tag, err := s.CreateTag(ctx, "Bridal", "Audience")
	if err != nil {
		t.Fatal(err)
	}
	src, err := s.CreateDesign(ctx, DesignInput{Name: "Leaf ring", CategoryID: rings, TagIDs: []int64{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := s.CreateVariation(ctx, src.ID, BaseInput{VolumeMM3: dp("200")})
	b, _ := s.CreateVariation(ctx, src.ID, BaseInput{RefWeightG: dp("3.116"), RefMetalCode: "18KY"})
	c, _ := s.CreateVariation(ctx, src.ID, BaseInput{VolumeMM3: dp("400")})
	for _, v := range []VariationDetail{a, b} {
		if _, err := s.SetSeats(ctx, v.ID, fixtureSeats()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.UploadCAD(ctx, a.ID, "a.3dm", bytes.NewReader([]byte("3dm data"))); err != nil {
		t.Fatal(err)
	}
	aid := a.ID
	if _, err := s.AddMedia(ctx, AddMediaInput{DesignID: src.ID, VariationID: &aid, Filename: "p.png", Body: bytes.NewReader(testPNG(40, 30))}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteVariation(ctx, c.ID); err != nil { // deleted variations are never copied
		t.Fatal(err)
	}
	objects := len(mem.Keys())

	cases := []struct {
		name     string
		in       DuplicateInput
		wantName string
		wantVars []int64 // source variation ids, in order
		wantTags int
	}{
		{"defaults: all live variations, name with (copy)", DuplicateInput{CopyTags: true}, "Leaf ring (copy)", []int64{a.ID, b.ID}, 1},
		{"own name, no tags, one variation", DuplicateInput{Name: "  Leaf ring II ", VariationIDs: []int64{b.ID}}, "Leaf ring II", []int64{b.ID}, 0},
		{"order follows the source, duplicates ignored", DuplicateInput{VariationIDs: []int64{b.ID, a.ID, b.ID}, CopyTags: true}, "Leaf ring (copy)", []int64{a.ID, b.ID}, 1},
		{"no variations at all", DuplicateInput{VariationIDs: []int64{}}, "Leaf ring (copy)", nil, 0},
	}
	prev := src.Code
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.DuplicateDesign(ctx, src.ID, tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if got.Code == src.Code || got.Code <= prev || !strings.HasPrefix(got.Code, "LR") || got.Name != tc.wantName ||
				got.CategoryID != rings || len(got.Tags) != tc.wantTags || len(got.Variations) != len(tc.wantVars) || len(got.Media) != 0 {
				t.Fatalf("got %+v", got)
			}
			prev = got.Code
			for i, srcID := range tc.wantVars {
				nv := got.Variations[i]
				if nv.Code != got.Code+"-V"+string(rune('1'+i)) {
					t.Errorf("code = %s", nv.Code)
				}
				if nv.File3dmKey.Valid || nv.File3dmSha256.Valid || nv.FileStlKey.Valid {
					t.Errorf("CAD file copied: %+v", nv)
				}
				orig := weightsOfID(t, s, src.Code, srcID)
				if len(nv.Weights) == 0 || len(nv.Weights) != len(orig.Weights) || !nv.BaselineStoneCarats.Equal(orig.BaselineStoneCarats) {
					t.Fatalf("weights %+v vs %+v", nv.Weights, orig.Weights)
				}
				for j, w := range nv.Weights {
					o := orig.Weights[j]
					if w.MetalCode != o.MetalCode || !w.NetWeightG.Equal(o.NetWeightG) || w.GrossWeightG.Decimal.Cmp(o.GrossWeightG.Decimal) != 0 {
						t.Errorf("weight %s: %v vs %v", w.MetalCode, w, o)
					}
				}
				ns, _ := s.ListSeats(ctx, nv.ID)
				os, _ := s.ListSeats(ctx, srcID)
				if len(ns) != len(os) {
					t.Fatalf("seats %d vs %d", len(ns), len(os))
				}
				for j := range ns {
					if !ns[j].SizeMm.Equal(os[j].SizeMm) || ns[j].StoneCount != os[j].StoneCount || ns[j].LockedCode != os[j].LockedCode {
						t.Errorf("seat %d: %+v vs %+v", j, ns[j], os[j])
					}
				}
			}
		})
	}
	if len(mem.Keys()) != objects {
		t.Errorf("duplicating touched stored files: %v", mem.Keys())
	}
	if orig, _ := s.GetDesignByCode(ctx, src.Code); len(orig.Variations) != 2 || !orig.Variations[0].File3dmKey.Valid {
		t.Errorf("the source changed: %+v", orig.Variations)
	}

	// Refusals.
	if _, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{VariationIDs: []int64{c.ID}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("deleted variation: %v", err)
	}
	other := newDesign(t, s, "LR0900")
	ov, _ := s.CreateVariation(ctx, other.ID, BaseInput{VolumeMM3: dp("200")})
	if _, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{VariationIDs: []int64{ov.ID}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("foreign variation: %v", err)
	}
	if _, err := s.DuplicateDesign(ctx, 999999, DuplicateInput{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown design: %v", err)
	}
	if err := s.DeleteDesign(ctx, src.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted source: %v", err)
	}
}

func weightsOfID(t *testing.T, s *Service, code string, id int64) VariationDetail {
	t.Helper()
	got, err := s.GetDesignByCode(context.Background(), code)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range got.Variations {
		if v.ID == id {
			return v
		}
	}
	t.Fatalf("variation %d not in %s", id, code)
	return VariationDetail{}
}

func TestDuplicateDesignIsAllOrNothing(t *testing.T) {
	s, _ := trashService(t)
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	setPrefixes(t, s)
	rings := mustCategory(t, s, "Rings")
	src, _ := s.CreateDesign(ctx, DesignInput{Name: "x", CategoryID: rings})
	a, _ := s.CreateVariation(ctx, src.ID, BaseInput{VolumeMM3: dp("200")})
	s.CreateVariation(ctx, src.ID, BaseInput{VolumeMM3: dp("300")}) //nolint:errcheck
	_ = a
	count := func() (designs, variations int) {
		s.Pool.QueryRow(ctx, "SELECT count(*) FROM master_designs").Scan(&designs)    //nolint:errcheck
		s.Pool.QueryRow(ctx, "SELECT count(*) FROM cad_variations").Scan(&variations) //nolint:errcheck
		return
	}
	d0, v0 := count()
	// An unknown id in the middle of the list fails the whole request: nothing is left behind and
	// the category counter does not skip a number.
	if _, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{VariationIDs: []int64{a.ID, 424242}}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	// A failure after the variations were created (a trigger forces one) rolls everything back too.
	if _, err := s.Pool.Exec(ctx, `CREATE FUNCTION fail_second() RETURNS trigger AS $$ BEGIN
		IF NEW.seq = 2 THEN RAISE EXCEPTION 'boom'; END IF; RETURN NEW; END $$ LANGUAGE plpgsql;
		CREATE TRIGGER fail_second BEFORE INSERT ON cad_variations FOR EACH ROW EXECUTE FUNCTION fail_second()`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{CopyTags: true}); err == nil {
		t.Fatal("expected the forced failure")
	}
	s.Pool.Exec(ctx, "DROP TRIGGER fail_second ON cad_variations; DROP FUNCTION fail_second()") //nolint:errcheck
	if d1, v1 := count(); d1 != d0 || v1 != v0 {
		t.Errorf("left behind: designs %d->%d variations %d->%d", d0, d1, v0, v1)
	}
	got, err := s.DuplicateDesign(ctx, src.ID, DuplicateInput{})
	if err != nil || got.Code != FormatCode("LR", 2) {
		t.Errorf("the failed attempts must not use up a number: %v %v", got.Code, err)
	}
}

var _ = db.CadVariation{}
