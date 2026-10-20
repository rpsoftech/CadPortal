package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testSecret = []byte("local-store-test-secret-0123456789abcdef")

func newLocal(t *testing.T) (*Local, string) {
	t.Helper()
	dir := t.TempDir()
	l, err := NewLocal(dir, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	return l, dir
}

func TestLocalPutOpenDelete(t *testing.T) {
	l, _ := newLocal(t)
	ctx := context.Background()
	key := "cad/LR001/LR001-V1/abc.3dm.gz"

	if err := l.Put(ctx, key, strings.NewReader("first")); err != nil {
		t.Fatal(err)
	}
	read := func() string {
		f, err := l.Open(key)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		return string(b)
	}
	if got := read(); got != "first" {
		t.Fatalf("got %q", got)
	}
	// Overwrite replaces atomically.
	if err := l.Put(ctx, key, strings.NewReader("second")); err != nil {
		t.Fatal(err)
	}
	if got := read(); got != "second" {
		t.Fatalf("after overwrite got %q", got)
	}
	if err := l.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Open(key); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("want not-exist after delete, got %v", err)
	}
	// Deleting twice is fine.
	if err := l.Delete(ctx, key); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}

func TestLocalRejectsEscapingKeys(t *testing.T) {
	l, dir := newLocal(t)
	ctx := context.Background()
	outside := filepath.Join(filepath.Dir(dir), "escaped.txt")
	for _, key := range []string{
		"", ".", "..", "../escaped.txt", "a/../../escaped.txt", "/etc/passwd",
		`..\escaped.txt`, `a\b`, "C:/x", "a//b", "a/./b",
	} {
		if err := l.Put(ctx, key, strings.NewReader("x")); !errors.Is(err, ErrBadKey) {
			t.Errorf("Put(%q) = %v, want ErrBadKey", key, err)
		}
		if _, err := l.Open(key); !errors.Is(err, ErrBadKey) {
			t.Errorf("Open(%q) = %v, want ErrBadKey", key, err)
		}
		if err := l.Delete(ctx, key); !errors.Is(err, ErrBadKey) {
			t.Errorf("Delete(%q) = %v, want ErrBadKey", key, err)
		}
		if _, err := l.PresignGet(ctx, key, "f", time.Minute); !errors.Is(err, ErrBadKey) {
			t.Errorf("PresignGet(%q) = %v, want ErrBadKey", key, err)
		}
		if l.Verify(key, "f", "9999999999", "00") {
			t.Errorf("Verify(%q) accepted", key)
		}
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a file was written outside the storage directory")
	}
}

type failingReader struct{ n int }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.n == 0 {
		return 0, errors.New("connection reset")
	}
	f.n--
	copy(p, "partial")
	return 7, nil
}

func TestLocalFailedPutLeavesNothing(t *testing.T) {
	l, dir := newLocal(t)
	key := "cad/x/y/z.3dm.gz"
	if err := l.Put(context.Background(), key, &failingReader{n: 2}); err == nil {
		t.Fatal("want error")
	}
	if _, err := l.Open(key); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("partial object visible: %v", err)
	}
	var leftovers []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, _ error) error { //nolint:errcheck
		if d != nil && !d.IsDir() {
			leftovers = append(leftovers, p)
		}
		return nil
	})
	if len(leftovers) != 0 {
		t.Errorf("temp files left behind: %v", leftovers)
	}
}

func TestLocalPutHonoursCancelledContext(t *testing.T) {
	l, _ := newLocal(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.Put(ctx, "a/b.gz", bytes.NewReader([]byte("data"))); !errors.Is(err, context.Canceled) {
		t.Errorf("want context.Canceled, got %v", err)
	}
	if _, err := l.Open("a/b.gz"); err == nil {
		t.Error("object must not exist after a cancelled put")
	}
}

func TestLocalSignedLinks(t *testing.T) {
	l, _ := newLocal(t)
	base := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }
	const key = "cad/LR001/LR001-V1/abc.3dm.gz"

	raw, err := l.PresignGet(context.Background(), key, "ring one.3dm", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, LocalURLPrefix+key+"?") {
		t.Fatalf("url = %s", raw)
	}
	u, _ := url.Parse(raw)
	q := u.Query()
	verify := func(key, filename, exp, sig string) bool { return l.Verify(key, filename, exp, sig) }

	if !verify(key, q.Get("filename"), q.Get("expires"), q.Get("sig")) {
		t.Fatal("valid link rejected")
	}
	if q.Get("filename") != "ring one.3dm" {
		t.Errorf("filename round trip = %q", q.Get("filename"))
	}
	// Tampering with any signed field fails.
	if verify(key+"x", q.Get("filename"), q.Get("expires"), q.Get("sig")) {
		t.Error("tampered key accepted")
	}
	if verify(key, "other.3dm", q.Get("expires"), q.Get("sig")) {
		t.Error("tampered filename accepted")
	}
	if verify(key, q.Get("filename"), "9999999999", q.Get("sig")) {
		t.Error("extended expiry accepted")
	}
	if verify(key, q.Get("filename"), q.Get("expires"), strings.Repeat("0", 64)) {
		t.Error("wrong signature accepted")
	}
	if verify(key, q.Get("filename"), q.Get("expires"), "not-hex") {
		t.Error("non-hex signature accepted")
	}
	// A link from a different secret is invalid.
	other, _ := NewLocal(t.TempDir(), []byte("a-completely-different-secret-0123456789"))
	defer other.Close()
	other.now = l.now
	if other.Verify(key, q.Get("filename"), q.Get("expires"), q.Get("sig")) {
		t.Error("link signed by another secret accepted")
	}
	// Expiry.
	l.now = func() time.Time { return base.Add(11 * time.Minute) }
	if verify(key, q.Get("filename"), q.Get("expires"), q.Get("sig")) {
		t.Error("expired link accepted")
	}
}

func TestNewLocalValidation(t *testing.T) {
	if _, err := NewLocal("", testSecret); err == nil {
		t.Error("empty dir accepted")
	}
	if _, err := NewLocal(t.TempDir(), []byte("short")); err == nil {
		t.Error("short secret accepted")
	}
}
