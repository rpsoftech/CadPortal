package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func trashService(t *testing.T) (*Service, *storage.Memory) {
	t.Helper()
	s, mem := newCADService(t)
	s.Retention = 30 * 24 * time.Hour
	return s, mem
}

func trashed(t *testing.T, s *Service) []TrashedItem {
	t.Helper()
	tr, err := s.ListTrash(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return tr.Files
}

func TestRemovedCADFileStaysRecoverableAndComesBack(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0700")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	data := randBytes(t, 4000)
	if _, err := s.UploadCAD(ctx, v.ID, "ring.3dm", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveCAD(ctx, v.ID, "3dm"); err != nil {
		t.Fatal(err)
	}
	// Gone from the variation, but the stored copy is still there and listed.
	if got, _ := s.Q.GetVariation(ctx, v.ID); got.File3dmKey.Valid {
		t.Error("the variation still points at the file")
	}
	if len(mem.Keys()) != 1 {
		t.Fatalf("the object was deleted: %v", mem.Keys())
	}
	items := trashed(t, s)
	if len(items) != 1 || items[0].Reason != "removed" || !items[0].Restorable || !strings.Contains(items[0].Label, "LR0700-V1 · ring.3dm") {
		t.Fatalf("trash = %+v", items)
	}
	if !items[0].PurgeAfter.After(time.Now().Add(29 * 24 * time.Hour)) {
		t.Errorf("kept until %v, want about 30 days", items[0].PurgeAfter)
	}

	if _, err := s.RestoreFile(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Q.GetVariation(ctx, v.ID)
	if !got.File3dmKey.Valid || got.File3dmName.String != "ring.3dm" || got.File3dmSha256.String != sha(data) || got.File3dmSizeBytes.Int64 != int64(len(data)) {
		t.Errorf("restored file = %+v", got)
	}
	if len(trashed(t, s)) != 0 {
		t.Error("a restored file must leave the trash")
	}
	link, err := s.DownloadCAD(ctx, v.ID, "3dm")
	if err != nil || link.Filename != "ring.3dm" {
		t.Errorf("download after restore: %+v %v", link, err)
	}
	if _, err := s.RestoreFile(ctx, items[0].ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("restoring twice: %v", err)
	}
}

func TestReplacedFileIsKeptButCannotOverwriteTheNewOne(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0701")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	first, second := randBytes(t, 3000), randBytes(t, 3500)
	s.UploadCAD(ctx, v.ID, "v1.3dm", bytes.NewReader(first))  //nolint:errcheck
	s.UploadCAD(ctx, v.ID, "v2.3dm", bytes.NewReader(second)) //nolint:errcheck
	if len(mem.Keys()) != 2 {
		t.Fatalf("both versions should be stored: %v", mem.Keys())
	}
	items := trashed(t, s)
	if len(items) != 1 || items[0].Reason != "replaced" {
		t.Fatalf("trash = %+v", items)
	}
	// The place is taken by the new file: say so, and change nothing.
	if _, err := s.RestoreFile(ctx, items[0].ID); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "remove that one first") {
		t.Fatalf("restore over a live file: %v", err)
	}
	if got, _ := s.Q.GetVariation(ctx, v.ID); got.File3dmName.String != "v2.3dm" {
		t.Error("the live file was disturbed")
	}
	// Remove the new one and the old version can come back.
	if err := s.RemoveCAD(ctx, v.ID, "3dm"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreFile(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Q.GetVariation(ctx, v.ID); got.File3dmName.String != "v1.3dm" || got.File3dmSha256.String != sha(first) {
		t.Errorf("restored = %+v", got)
	}
}

func TestRestoreRefusesAChecksumThatIsNowUsedElsewhere(t *testing.T) {
	s, _ := trashService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0702")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	data := randBytes(t, 2000)
	s.UploadCAD(ctx, v1.ID, "a.stl", bytes.NewReader(data)) //nolint:errcheck
	s.RemoveCAD(ctx, v1.ID, "stl")                          //nolint:errcheck
	// The checksum is free again, so the same file can go on the other variation...
	if _, err := s.UploadCAD(ctx, v2.ID, "a.stl", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	// ...and then the removed copy must not come back as a duplicate.
	items := trashed(t, s)
	if _, err := s.RestoreFile(ctx, items[0].ID); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "LR0702-V2") {
		t.Errorf("restore of a duplicate: %v", err)
	}
	if len(trashed(t, s)) != 1 {
		t.Error("a refused restore must keep the file in the trash")
	}
}

func TestDeletedPhotoComesBackWithItsThumbnailIfThereIsRoom(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	d, _ := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	p, err := add(t, s, d.ID, nil, "front.png", testPNG(120, 80))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteMedia(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != 2 {
		t.Fatalf("the photo and its thumbnail must stay: %v", mem.Keys())
	}
	items := trashed(t, s)
	if len(items) != 1 || items[0].Kind != "media" || !items[0].HasThumbnail || !strings.Contains(items[0].Label, d.Code) {
		t.Fatalf("trash = %+v", items)
	}
	// Fill the design, then restoring has no room.
	for i := 0; i < MaxMediaPerTarget; i++ {
		if _, err := add(t, s, d.ID, nil, "x.png", testPNG(30, 30)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.RestoreFile(ctx, items[0].ID); !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "at most 5") {
		t.Errorf("restore into a full design: %v", err)
	}
	all, _ := s.ListMedia(ctx, d.ID)
	if _, err := s.DeleteMedia(ctx, all[0].ID); err != nil {
		t.Fatal(err)
	}
	trashID := int64(0)
	for _, it := range trashed(t, s) {
		if strings.Contains(it.Label, "front.png") {
			trashID = it.ID
		}
	}
	if _, err := s.RestoreFile(ctx, trashID); err != nil {
		t.Fatal(err)
	}
	back, _ := s.ListMedia(ctx, d.ID)
	found := false
	for _, m := range back {
		if m.Filename == "front.png" {
			found = true
			if m.ThumbURL == "" || m.Width == nil || *m.Width != 120 || m.IsCover {
				t.Errorf("restored photo = %+v", m)
			}
		}
	}
	if !found {
		t.Error("the photo did not come back")
	}
}

func TestPhotoOfADeletedDesignWaitsForTheDesign(t *testing.T) {
	s, _ := trashService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	d, _ := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	p, _ := add(t, s, d.ID, nil, "a.png", testPNG(40, 40))
	s.DeleteMedia(ctx, p.ID) //nolint:errcheck
	if err := s.DeleteDesign(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	id := trashed(t, s)[0].ID
	if _, err := s.RestoreFile(ctx, id); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "restore the design first") {
		t.Fatalf("restore into a deleted design: %v", err)
	}
	if _, err := s.RestoreDesign(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RestoreFile(ctx, id); err != nil {
		t.Errorf("after restoring the design: %v", err)
	}
}

func TestDeletedDesignsAreListedAndRestoredWithEverythingInThem(t *testing.T) {
	s, _ := trashService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	d, _ := s.CreateDesign(ctx, DesignInput{Name: "Lotus", CategoryID: mustCategory(t, s, "Rings")})
	s.CreateVariation(ctx, d.ID, BaseInput{VolumeMM3: dp("200")}) //nolint:errcheck
	if tr, _ := s.ListTrash(ctx); len(tr.Designs) != 0 {
		t.Fatal("nothing is deleted yet")
	}
	if err := s.DeleteDesign(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDesignByCode(ctx, d.Code); !errors.Is(err, ErrNotFound) {
		t.Errorf("a deleted design must be hidden: %v", err)
	}
	tr, _ := s.ListTrash(ctx)
	if len(tr.Designs) != 1 || tr.Designs[0].Code != d.Code || tr.Designs[0].Name != "Lotus" || tr.Designs[0].VariationCount != 1 || tr.Designs[0].CategoryName != "Rings" {
		t.Fatalf("deleted designs = %+v", tr.Designs)
	}
	if _, err := s.RestoreDesign(ctx, d.ID); err != nil {
		t.Fatal(err)
	}
	back, err := s.GetDesignByCode(ctx, d.Code)
	if err != nil || len(back.Variations) != 1 || back.Variations[0].Code != d.Code+"-V1" || len(back.Variations[0].Weights) == 0 {
		t.Errorf("restored design = %+v %v", back, err)
	}
	if tr, _ := s.ListTrash(ctx); len(tr.Designs) != 0 {
		t.Error("a restored design must leave the list")
	}
	for _, id := range []int64{d.ID, 999999} {
		if _, err := s.RestoreDesign(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("restoring a live or unknown design (%d): %v", id, err)
		}
	}
	// The next number carries on: a restored design does not free its code.
	if next := create(t, s, "Rings"); next != "LR0002" {
		t.Errorf("next code = %s", next)
	}
}

type flakyStore struct {
	*storage.Memory
	failDelete bool
}

func (f *flakyStore) Delete(ctx context.Context, key string) error {
	if f.failDelete {
		return errors.New("bucket unreachable")
	}
	return f.Memory.Delete(ctx, key)
}

func TestPurgeDeletesOnlyWhatIsDueAndRetriesFailures(t *testing.T) {
	s, mem := trashService(t)
	ctx := context.Background()
	flaky := &flakyStore{Memory: mem}
	s.Store = flaky
	des := newDesign(t, s, "LR0704")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	s.UploadCAD(ctx, v.ID, "old.3dm", bytes.NewReader(randBytes(t, 1000))) //nolint:errcheck
	s.RemoveCAD(ctx, v.ID, "3dm")                                          //nolint:errcheck
	s.UploadCAD(ctx, v.ID, "new.stl", bytes.NewReader(randBytes(t, 1000))) //nolint:errcheck
	s.RemoveCAD(ctx, v.ID, "stl")                                          //nolint:errcheck
	if len(trashed(t, s)) != 2 || len(mem.Keys()) != 2 {
		t.Fatalf("setup: %d trashed, %v", len(trashed(t, s)), mem.Keys())
	}

	// Nothing is due yet.
	if n, err := s.PurgeTrash(ctx); err != nil || n != 0 || len(mem.Keys()) != 2 {
		t.Fatalf("early purge: %d %v %v", n, err, mem.Keys())
	}
	// One file is past its time.
	if _, err := s.Pool.Exec(ctx, "UPDATE trashed_files SET purge_after = now() - interval '1 minute' WHERE label LIKE '%old.3dm'"); err != nil {
		t.Fatal(err)
	}
	flaky.failDelete = true
	if n, err := s.PurgeTrash(ctx); err != nil || n != 0 {
		t.Fatalf("purge with an unreachable bucket: %d %v", n, err)
	}
	if len(trashed(t, s)) != 2 || len(mem.Keys()) != 2 {
		t.Error("a failed deletion must keep both the object and its record, to try again")
	}
	flaky.failDelete = false
	if n, err := s.PurgeTrash(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	left := trashed(t, s)
	if len(left) != 1 || !strings.Contains(left[0].Label, "new.stl") || len(mem.Keys()) != 1 {
		t.Errorf("after the purge: %+v, objects %v", left, mem.Keys())
	}
}

func TestNoRetentionDeletesAtOnce(t *testing.T) {
	s, mem := newCADService(t) // Retention is zero
	ctx := context.Background()
	des := newDesign(t, s, "LR0705")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	s.UploadCAD(ctx, v.ID, "a.3dm", bytes.NewReader(randBytes(t, 500))) //nolint:errcheck
	s.RemoveCAD(ctx, v.ID, "3dm")                                       //nolint:errcheck
	if len(mem.Keys()) != 0 || len(trashed(t, s)) != 0 {
		t.Errorf("with no retention the object goes at once: %v, trash %d", mem.Keys(), len(trashed(t, s)))
	}
}
