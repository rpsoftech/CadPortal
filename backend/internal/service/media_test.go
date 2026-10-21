package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func testPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 90, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img) //nolint:errcheck
	return b.Bytes()
}

// testJPEG is noisy on purpose: a flat picture compresses to almost nothing.
func testJPEG(w, h int) []byte {
	rng := rand.New(rand.NewSource(1))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(rng.Intn(256)), uint8(rng.Intn(256)), uint8(rng.Intn(256)), 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil) //nolint:errcheck
	return b.Bytes()
}

// testMP4 is not a playable video, but it has the container signature the type check reads.
func testMP4(n int) []byte {
	return append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), bytes.Repeat([]byte{7}, n)...)
}

func mediaDesign(t *testing.T) (*Service, *storage.Memory, int64, string) {
	t.Helper()
	s, mem := newCADService(t)
	setPrefixes(t, s)
	d, err := s.CreateDesign(context.Background(), DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if err != nil {
		t.Fatal(err)
	}
	return s, mem, d.ID, d.Code
}

func add(t *testing.T, s *Service, design int64, variation *int64, name string, data []byte) (MediaItem, error) {
	t.Helper()
	return s.AddMedia(context.Background(), AddMediaInput{DesignID: design, VariationID: variation, Filename: name, Body: bytes.NewReader(data)})
}

func TestAddPhotoStoresOriginalAndThumbnail(t *testing.T) {
	s, mem, design, code := mediaDesign(t)
	photo := testPNG(1200, 800)
	it, err := add(t, s, design, nil, "ring front.png", photo)
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != "image" || it.ContentType != "image/png" || it.Filename != "ring front.png" || it.SizeBytes != int64(len(photo)) {
		t.Errorf("item = %+v", it)
	}
	if it.Width == nil || *it.Width != 1200 || it.Height == nil || *it.Height != 800 {
		t.Errorf("size = %v x %v", it.Width, it.Height)
	}
	if it.URL == "" || it.ThumbURL == "" || it.VariationID != nil || it.Position != 1 {
		t.Errorf("urls/target/position: %+v", it)
	}
	keys := mem.Keys()
	if len(keys) != 2 {
		t.Fatalf("keys = %v (want the original and its thumbnail)", keys)
	}
	for _, k := range keys {
		if !strings.HasPrefix(k, "media/"+code+"/") {
			t.Errorf("key %q is outside the design's folder", k)
		}
	}
	// The original is stored exactly as uploaded, not re-encoded.
	for k, v := range mem.Objects {
		if strings.HasSuffix(k, ".png") && !bytes.Equal(v, photo) {
			t.Error("the stored original differs from the upload")
		}
	}
}

func TestAddVideoStreamsToStorage(t *testing.T) {
	s, mem, design, _ := mediaDesign(t)
	video := testMP4(2 << 20)
	it, err := add(t, s, design, nil, "turntable.mp4", video)
	if err != nil {
		t.Fatal(err)
	}
	if it.Kind != "video" || it.ContentType != "video/mp4" || it.ThumbURL != "" || it.Width != nil || it.SizeBytes != int64(len(video)) {
		t.Errorf("item = %+v", it)
	}
	if len(mem.Keys()) != 1 {
		t.Errorf("keys = %v", mem.Keys())
	}
}

func TestMediaTypeComesFromTheContentNotTheName(t *testing.T) {
	s, mem, design, _ := mediaDesign(t)
	for name, data := range map[string][]byte{
		"photo.jpg (really a script)": []byte("<html><script>alert(1)</script></html>"),
		"cad.3dm":                     []byte("3D Geometry File Format"),
		"anim.gif":                    []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00"),
		"photo.heic":                  []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00\x00\x00\x00\x00"),
		"empty.png":                   nil,
	} {
		if _, err := add(t, s, design, nil, name, data); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: want a refusal, got %v", name, err)
		}
	}
	// An image that is really a video container is a video, whatever its name says.
	it, err := add(t, s, design, nil, "looks-like-a-photo.jpg", testMP4(100))
	if err != nil || it.Kind != "video" {
		t.Errorf("mp4 named .jpg: %+v %v", it, err)
	}
	// A broken photo (right signature, damaged data) is refused and leaves nothing behind.
	before := len(mem.Keys())
	if _, err := add(t, s, design, nil, "broken.png", testPNG(300, 300)[:200]); !errors.Is(err, ErrInvalid) {
		t.Errorf("truncated png: %v", err)
	}
	if len(mem.Keys()) != before {
		t.Errorf("a refused photo left objects behind: %v", mem.Keys())
	}
}

func TestFiveItemsPerTargetAndTheOverflowIsNotEvenRead(t *testing.T) {
	s, mem, design, _ := mediaDesign(t)
	ctx := context.Background()
	v, err := s.CreateVariation(ctx, design, BaseInput{VolumeMM3: dp("200")})
	if err != nil {
		t.Fatal(err)
	}
	vid := v.ID

	// Five on the design (photos and videos together), five more on the variation.
	for i := 0; i < 4; i++ {
		if _, err := add(t, s, design, nil, "p.png", testPNG(40, 30)); err != nil {
			t.Fatalf("photo %d: %v", i, err)
		}
	}
	if _, err := add(t, s, design, nil, "v.mp4", testMP4(50)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if _, err := add(t, s, design, &vid, "pv.png", testPNG(40, 30)); err != nil {
			t.Fatalf("variation photo %d: %v", i, err)
		}
	}
	// The sixth on either target is refused, and the refusal comes before any bytes are read,
	// so nobody uploads a 300 MB video to be told the shelf is full.
	r := &failIfRead{}
	for _, target := range []*int64{nil, &vid} {
		_, err := s.AddMedia(ctx, AddMediaInput{DesignID: design, VariationID: target, Filename: "x.mp4", Body: r})
		if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "at most 5") {
			t.Errorf("sixth item (variation %v): %v", target, err)
		}
	}
	if r.read {
		t.Error("the body was read although the target is full")
	}
	items, _ := s.ListMedia(ctx, design)
	if len(items) != 10 {
		t.Fatalf("items = %d", len(items))
	}
	// Design-level first, then the variation's, each in upload order.
	if items[0].VariationID != nil || items[4].VariationID != nil || items[5].VariationID == nil {
		t.Errorf("order: %+v", items)
	}
	for i := 1; i < 5; i++ {
		if items[i].Position != items[i-1].Position+1 {
			t.Errorf("positions: %d then %d", items[i-1].Position, items[i].Position)
		}
	}
	// Removing one makes room again, and only for that target.
	if _, err := s.DeleteMedia(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := add(t, s, design, nil, "again.png", testPNG(40, 30)); err != nil {
		t.Errorf("after a removal: %v", err)
	}
	if _, err := add(t, s, design, &vid, "no.png", testPNG(40, 30)); !errors.Is(err, ErrConflict) {
		t.Errorf("the variation is still full: %v", err)
	}
	// 5 + 5 photos/videos: objects are 2 per photo + 1 per video (4 photos, 1 video, 5 variation photos)
	// minus the removed one, plus the new one.
	if want := (4*2 + 1 + 5*2); len(mem.Keys()) != want {
		t.Errorf("objects = %d, want %d", len(mem.Keys()), want)
	}
}

type failIfRead struct{ read bool }

func (f *failIfRead) Read([]byte) (int, error) { f.read = true; return 0, errors.New("read") }

func TestConcurrentUploadsCannotBeatTheLimit(t *testing.T) {
	s, _, design, _ := mediaDesign(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, full := 0, 0
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := add(t, s, design, nil, "c.png", testPNG(30, 30))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrConflict):
				full++
			default:
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if ok != MaxMediaPerTarget || ok+full != 12 {
		t.Errorf("%d stored, %d refused: want exactly %d stored", ok, full, MaxMediaPerTarget)
	}
	if items, _ := s.ListMedia(context.Background(), design); len(items) != MaxMediaPerTarget {
		t.Errorf("stored items = %d", len(items))
	}
}

func TestMediaSizeLimits(t *testing.T) {
	s, mem, design, _ := mediaDesign(t)
	oldImg, oldVid := imageLimit, videoLimit
	imageLimit, videoLimit = 4<<10, 10<<10
	defer func() { imageLimit, videoLimit = oldImg, oldVid }()

	big := testJPEG(400, 400)
	if int64(len(big)) <= imageLimit {
		t.Fatalf("setup: the test photo is only %d bytes", len(big))
	}
	if _, err := add(t, s, design, nil, "big.jpg", big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big photo: %v", err)
	}
	if _, err := add(t, s, design, nil, "big.mp4", testMP4(20<<10)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("big video: %v", err)
	}
	if len(mem.Keys()) != 0 {
		t.Errorf("refused files left objects behind: %v", mem.Keys())
	}
	if _, err := add(t, s, design, nil, "ok.mp4", testMP4(5<<10)); err != nil {
		t.Errorf("a video under the limit: %v", err)
	}
}

func TestVariationMustBelongToTheDesign(t *testing.T) {
	s, _, design, _ := mediaDesign(t)
	ctx := context.Background()
	other, err := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if err != nil {
		t.Fatal(err)
	}
	ov, _ := s.CreateVariation(ctx, other.ID, BaseInput{VolumeMM3: dp("100")})
	id := ov.ID
	if _, err := add(t, s, design, &id, "x.png", testPNG(20, 20)); !errors.Is(err, ErrInvalid) {
		t.Errorf("another design's variation: %v", err)
	}
	if _, err := add(t, s, 999999, nil, "x.png", testPNG(20, 20)); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown design: %v", err)
	}
	missing := int64(999999)
	if _, err := add(t, s, design, &missing, "x.png", testPNG(20, 20)); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown variation: %v", err)
	}
}

func TestCoverIsADesignPhotoAndShowsOnTheCatalogCard(t *testing.T) {
	s, _, design, code := mediaDesign(t)
	ctx := context.Background()
	list := func() DesignListItem {
		items, err := s.ListDesigns(ctx, DesignFilter{Limit: 10})
		if err != nil || len(items) != 1 {
			t.Fatalf("list: %v %v", items, err)
		}
		return items[0]
	}
	if list().CoverURL != "" {
		t.Error("a design without photos has no cover")
	}
	v, _ := s.CreateVariation(ctx, design, BaseInput{VolumeMM3: dp("200")})
	vid := v.ID
	first, _ := add(t, s, design, nil, "a.png", testPNG(60, 40))
	second, _ := add(t, s, design, nil, "b.png", testPNG(60, 40))
	onVariation, _ := add(t, s, design, &vid, "c.png", testPNG(60, 40))
	video, _ := add(t, s, design, nil, "d.mp4", testMP4(100))

	// With no cover chosen, the first design photo is used.
	if !strings.Contains(list().CoverURL, "thumb") {
		t.Errorf("default cover url = %q", list().CoverURL)
	}
	if _, err := s.SetCover(ctx, onVariation.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("a variation photo as cover: %v", err)
	}
	if _, err := s.SetCover(ctx, video.ID); !errors.Is(err, ErrInvalid) {
		t.Errorf("a video as cover: %v", err)
	}
	if _, err := s.SetCover(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown media: %v", err)
	}
	if _, err := s.SetCover(ctx, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetCover(ctx, second.ID); err != nil { // choosing the same one again is fine
		t.Fatal(err)
	}
	if _, err := s.SetCover(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	d, err := s.GetDesignByCode(ctx, code)
	if err != nil {
		t.Fatal(err)
	}
	covers := 0
	for _, m := range d.Media {
		if m.IsCover {
			covers++
			if m.ID != first.ID {
				t.Errorf("the cover is %d, want %d", m.ID, first.ID)
			}
		}
	}
	if covers != 1 {
		t.Errorf("%d covers (exactly one is allowed)", covers)
	}
	// Deleting the cover falls back to the next photo.
	if _, err := s.DeleteMedia(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if list().CoverURL == "" {
		t.Error("the card lost its picture although the design still has photos")
	}
}

func TestDeletingMediaOrAVariationRemovesTheStoredObjects(t *testing.T) {
	s, mem, design, _ := mediaDesign(t)
	ctx := context.Background()
	v, _ := s.CreateVariation(ctx, design, BaseInput{VolumeMM3: dp("200")})
	vid := v.ID
	p, _ := add(t, s, design, nil, "p.png", testPNG(30, 30))
	add(t, s, design, &vid, "v1.png", testPNG(30, 30)) //nolint:errcheck
	add(t, s, design, &vid, "v2.mp4", testMP4(100))    //nolint:errcheck
	if len(mem.Keys()) != 5 {
		t.Fatalf("setup: %v", mem.Keys())
	}
	if _, err := s.DeleteMedia(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != 3 {
		t.Errorf("after deleting a photo (original and thumbnail): %v", mem.Keys())
	}
	if _, err := s.DeleteMedia(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting twice: %v", err)
	}
	if err := s.DeleteVariation(ctx, vid); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != 0 {
		t.Errorf("a deleted variation left its photos and video in storage: %v", mem.Keys())
	}
	if items, _ := s.ListMedia(ctx, design); len(items) != 0 {
		t.Errorf("rows left behind: %d", len(items))
	}
}

func TestMediaNeedsFileStorage(t *testing.T) {
	s, _, design, _ := mediaDesign(t)
	s.Store = nil
	if _, err := add(t, s, design, nil, "p.png", testPNG(20, 20)); !errors.Is(err, ErrStorageDisabled) {
		t.Errorf("no storage: %v", err)
	}
	// Reading a design must keep working without storage.
	if _, err := s.ListMedia(context.Background(), design); err != nil {
		t.Errorf("list without storage: %v", err)
	}
}
