package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(b); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readAll(t *testing.T, rc io.ReadCloser) []byte {
	t.Helper()
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestOpenOriginalGunzipsMemoryAndLocal(t *testing.T) {
	orig := bytes.Repeat([]byte("solid x\n"), 5000)
	ctx := context.Background()

	mem := NewMemory()
	mem.Objects["a.stl.gz"] = gz(t, orig)
	rc, err := OpenOriginal(ctx, mem, "a.stl.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, rc), orig) {
		t.Error("memory store: not the original bytes")
	}

	l, _ := newLocal(t)
	if err := l.Put(ctx, "cad/a.stl.gz", bytes.NewReader(gz(t, orig))); err != nil {
		t.Fatal(err)
	}
	rc, err = OpenOriginal(ctx, l, "cad/a.stl.gz")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, rc), orig) {
		t.Error("local store: not the original bytes")
	}
}

func TestOpenOriginalMissingIsNotFound(t *testing.T) {
	ctx := context.Background()
	l, _ := newLocal(t)
	for name, s := range map[string]Store{"memory": NewMemory(), "local": l} {
		_, err := OpenOriginal(ctx, s, "cad/none.stl.gz")
		if err == nil || !IsNotFound(err) {
			t.Errorf("%s: err = %v, want a not-found error", name, err)
		}
	}
}

func TestOpenOriginalRejectsAnObjectThatIsNotGzip(t *testing.T) {
	mem := NewMemory()
	mem.Objects["bad"] = []byte("plain text, not gzip")
	if _, err := OpenOriginal(context.Background(), mem, "bad"); err == nil {
		t.Fatal("a non-gzip object was accepted")
	}
}

func TestDecodeStored(t *testing.T) {
	orig := []byte("original bytes")
	// The header is still there: the body is the gzip stream.
	rc, err := decodeStored(io.NopCloser(bytes.NewReader(gz(t, orig))), "gzip")
	if err != nil || !bytes.Equal(readAll(t, rc), orig) {
		t.Errorf("gzip body: %v", err)
	}
	// The transport already decoded it and dropped the header: the body is the original.
	rc, err = decodeStored(io.NopCloser(bytes.NewReader(orig)), "")
	if err != nil || !bytes.Equal(readAll(t, rc), orig) {
		t.Errorf("decoded body: %v", err)
	}
}

// fakeS3 answers GET /bucket/key like R2 does for a CAD object: gzip bytes with Content-Encoding: gzip.
func fakeS3(t *testing.T, objects map[string][]byte) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.URL.Path, "/bkt/")
		b, ok := objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>nope</Message></Error>`)
			return
		}
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// R2 serves CAD objects with Content-Encoding: gzip. Whatever the HTTP client does with that header, the
// caller of OpenOriginal must get the original bytes, and a missing object must be recognisable.
func TestR2StyleServerOpenOriginal(t *testing.T) {
	orig := bytes.Repeat([]byte("endsolid\x00\x01"), 3000)
	srv := fakeS3(t, map[string][]byte{"cad/x.stl.gz": gz(t, orig)})
	r, err := NewR2(Config{Endpoint: srv.URL, Bucket: "bkt", AccessKeyID: "id", SecretAccessKey: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	rc, err := OpenOriginal(context.Background(), r, "cad/x.stl.gz")
	if err != nil {
		t.Fatal(err)
	}
	got := readAll(t, rc)
	if sha := sha256.Sum256(got); hex.EncodeToString(sha[:]) != func() string { s := sha256.Sum256(orig); return hex.EncodeToString(s[:]) }() {
		t.Errorf("got %d bytes that differ from the original %d", len(got), len(orig))
	}

	_, err = OpenOriginal(context.Background(), r, "cad/none.stl.gz")
	if err == nil || !IsNotFound(err) {
		t.Errorf("missing object: err = %v, want not-found", err)
	}
}
