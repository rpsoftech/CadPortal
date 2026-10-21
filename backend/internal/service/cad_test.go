package service

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func sha(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func gunzip(t *testing.T, b []byte) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func newCADService(t *testing.T) (*Service, *storage.Memory) {
	s := newTestService(t)
	mem := storage.NewMemory()
	s.Store = mem
	return s, mem
}

func randBytes(t *testing.T, n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestUploadCADHashesOriginalAndGzips(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR030")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	// Larger than a single io buffer, so streaming is exercised.
	data := randBytes(t, 3<<20)
	res, err := s.UploadCAD(ctx, v.ID, "ring.3DM", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if res.SHA256 != sha(data) || res.SizeBytes != int64(len(data)) || res.Kind != "3dm" {
		t.Errorf("result = %+v", res)
	}
	keys := mem.Keys()
	if len(keys) != 1 || !strings.HasPrefix(keys[0], "cad/LR030/LR030-V1/") || !strings.HasSuffix(keys[0], ".3dm.gz") {
		t.Fatalf("keys = %v", keys)
	}
	if !bytes.Equal(gunzip(t, mem.Objects[keys[0]]), data) {
		t.Error("stored object does not gunzip to the original bytes")
	}
	got, _ := s.Q.GetVariation(ctx, v.ID)
	if got.File3dmSha256.String != sha(data) || got.File3dmName.String != "ring.3DM" {
		t.Errorf("db row = %+v", got)
	}

	link, err := s.DownloadCAD(ctx, v.ID, "3dm")
	if err != nil || link.Filename != "ring.3DM" || !strings.Contains(link.URL, keys[0]) || link.ExpiresIn != 600 {
		t.Errorf("link = %+v err=%v", link, err)
	}
	if _, err := s.DownloadCAD(ctx, v.ID, "stl"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing stl: %v", err)
	}
	if _, err := s.DownloadCAD(ctx, v.ID, "obj"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
}

func TestUploadCADDuplicateIs409NamingExisting(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR031")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	data := randBytes(t, 1000)

	if _, err := s.UploadCAD(ctx, v1.ID, "a.stl", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	_, err := s.UploadCAD(ctx, v2.ID, "copy.stl", bytes.NewReader(data))
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "LR031-V1") {
		t.Fatalf("want conflict naming LR031-V1, got %v", err)
	}
	if len(mem.Keys()) != 1 {
		t.Errorf("duplicate object not cleaned up: %v", mem.Keys())
	}
	// The same bytes as a different kind do not collide (separate unique indexes).
	if _, err := s.UploadCAD(ctx, v2.ID, "copy.3dm", bytes.NewReader(data)); err != nil {
		t.Errorf("same bytes as 3dm should be allowed: %v", err)
	}
}

func TestUploadCADReplaceDeletesOldObject(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR032")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	if _, err := s.UploadCAD(ctx, v.ID, "a.3dm", bytes.NewReader(randBytes(t, 500))); err != nil {
		t.Fatal(err)
	}
	old := mem.Keys()[0]
	newData := randBytes(t, 700)
	if _, err := s.UploadCAD(ctx, v.ID, "b.3dm", bytes.NewReader(newData)); err != nil {
		t.Fatal(err)
	}
	keys := mem.Keys()
	if len(keys) != 1 || keys[0] == old {
		t.Errorf("keys after replace = %v (old %s)", keys, old)
	}
	// Re-uploading identical bytes to the same variation is a duplicate of itself.
	if _, err := s.UploadCAD(ctx, v.ID, "b2.3dm", bytes.NewReader(newData)); !errors.Is(err, ErrConflict) {
		t.Errorf("same-variation duplicate: %v", err)
	}
}

func TestUploadCADFailures(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR033")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	if _, err := s.UploadCAD(ctx, v.ID, "notes.txt", strings.NewReader("x")); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad extension: %v", err)
	}
	if _, err := s.UploadCAD(ctx, 99999, "a.3dm", strings.NewReader("x")); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing variation: %v", err)
	}

	mem.PutErr = errors.New("r2 down")
	if _, err := s.UploadCAD(ctx, v.ID, "a.3dm", strings.NewReader("data")); err == nil {
		t.Error("store failure should surface")
	}
	mem.PutErr = nil
	if got, _ := s.Q.GetVariation(ctx, v.ID); got.File3dmKey.Valid {
		t.Error("db must be untouched after a failed upload")
	}

	// A reader that fails mid-stream surfaces the error and leaves nothing behind.
	bad := io.MultiReader(strings.NewReader("partial"), errReader{})
	if _, err := s.UploadCAD(ctx, v.ID, "a.3dm", bad); err == nil {
		t.Error("reader failure should surface")
	}
	if got, _ := s.Q.GetVariation(ctx, v.ID); got.File3dmKey.Valid {
		t.Error("db must be untouched after a failed read")
	}

	s.Store = nil
	if _, err := s.UploadCAD(ctx, v.ID, "a.3dm", strings.NewReader("x")); !errors.Is(err, ErrStorageDisabled) {
		t.Errorf("no store: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("connection reset") }

func TestRemoveCADFreesTheChecksumAndObject(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR033")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	data := randBytes(t, 800)

	if _, err := s.UploadCAD(ctx, v1.ID, "wrong.3dm", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v1.ID, "keep.stl", bytes.NewReader(randBytes(t, 300))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v2.ID, "right.3dm", bytes.NewReader(data)); !errors.Is(err, ErrConflict) {
		t.Fatalf("setup: want conflict, got %v", err)
	}

	if err := s.RemoveCAD(ctx, v1.ID, "3dm"); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != 1 || !strings.HasSuffix(mem.Keys()[0], ".stl.gz") {
		t.Errorf("only the stl object should remain: %v", mem.Keys())
	}
	got, _ := s.Q.GetVariation(ctx, v1.ID)
	if got.File3dmKey.Valid || got.File3dmSha256.Valid || got.File3dmName.Valid || got.File3dmSizeBytes.Valid {
		t.Errorf("3dm columns not cleared: %+v", got)
	}
	if !got.FileStlKey.Valid {
		t.Error("removing the 3dm must not touch the stl")
	}
	// The checksum is free again, so the file can go on the other variation.
	if _, err := s.UploadCAD(ctx, v2.ID, "right.3dm", bytes.NewReader(data)); err != nil {
		t.Errorf("upload after removal: %v", err)
	}

	if err := s.RemoveCAD(ctx, v1.ID, "3dm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("removing twice: %v", err)
	}
	if err := s.RemoveCAD(ctx, v1.ID, "step"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad kind: %v", err)
	}
	if err := s.RemoveCAD(ctx, 999999, "3dm"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown variation: %v", err)
	}
}

func TestCleanFilename(t *testing.T) {
	long := strings.Repeat("a", 300) + ".3dm"
	for _, c := range []struct{ in, want string }{
		{"ring.3dm", "ring.3dm"},
		{"bague \u00e9t\u00e9.3dm", "bague \u00e9t\u00e9.3dm"},
		{`C:\Users\me\ring.3DM`, "ring.3DM"},
		{"/tmp/../etc/ring.stl", "ring.stl"},
		{"bad\xe9t\xe9.3dm", "bad_t_.3dm"}, // Latin-1 bytes from a legacy client
		{"line\r\nbreak\x00.3dm", "linebreak.3dm"},
		{"  spaced.3dm  ", "spaced.3dm"},
	} {
		if got := cleanFilename(c.in); got != c.want {
			t.Errorf("cleanFilename(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	got := cleanFilename(long)
	if len([]rune(got)) != maxFilenameRunes || !strings.HasSuffix(got, ".3dm") {
		t.Errorf("a long name keeps its extension and is cut to %d runes, got %d runes %q", maxFilenameRunes, len([]rune(got)), got[len(got)-8:])
	}
}

func TestUploadCADAcceptsANonUTF8AndAccentedFilename(t *testing.T) {
	s, mem := newCADService(t)
	ctx := context.Background()
	des := newDesign(t, s, "LR0600")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})

	// What a client in a Latin-1 code page sends: not valid UTF-8. It used to end in a 500 from Postgres.
	res, err := s.UploadCAD(ctx, v.ID, "bague \xe9t\xe9.3dm", bytes.NewReader(randBytes(t, 500)))
	if err != nil || res.Filename != "bague _t_.3dm" {
		t.Fatalf("latin-1 name: %+v %v", res, err)
	}
	res, err = s.UploadCAD(ctx, v.ID, "bague \u00e9t\u00e9.3dm", bytes.NewReader(randBytes(t, 600)))
	if err != nil || res.Filename != "bague \u00e9t\u00e9.3dm" {
		t.Fatalf("utf-8 name: %+v %v", res, err)
	}
	if len(mem.Keys()) != 1 {
		t.Errorf("objects = %v (replacing should leave one)", mem.Keys())
	}
}
