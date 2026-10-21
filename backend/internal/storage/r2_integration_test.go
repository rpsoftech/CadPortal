package storage

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// These tests talk to a real Cloudflare R2 bucket, so they run only when R2_TEST_* is set. They
// use their own variable names on purpose: a stray S3_* value from a production .env must never
// point a test at the live bucket. Use a scratch bucket and a token limited to it. Every object is
// written under r2-test/<random>/ and removed afterwards.
//
//	R2_TEST_ENDPOINT=https://<account>.r2.cloudflarestorage.com
//	R2_TEST_BUCKET=...  R2_TEST_ACCESS_KEY_ID=...  R2_TEST_SECRET_ACCESS_KEY=...
//	go test ./internal/storage -run R2 -v
func newTestR2(t *testing.T) (*R2, string) {
	t.Helper()
	endpoint, bucket := os.Getenv("R2_TEST_ENDPOINT"), os.Getenv("R2_TEST_BUCKET")
	id, secret := os.Getenv("R2_TEST_ACCESS_KEY_ID"), os.Getenv("R2_TEST_SECRET_ACCESS_KEY")
	if endpoint == "" || bucket == "" || id == "" || secret == "" {
		t.Skip("R2_TEST_ENDPOINT, R2_TEST_BUCKET, R2_TEST_ACCESS_KEY_ID and R2_TEST_SECRET_ACCESS_KEY not set")
	}
	r, err := NewR2(Config{Endpoint: endpoint, Region: "auto", Bucket: bucket, AccessKeyID: id, SecretAccessKey: secret})
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return r, "r2-test/" + hex.EncodeToString(b) + "/"
}

// putGzipped streams data through gzip into the store exactly as the upload path does (an
// io.Pipe, so the length is not known up front) and returns the SHA-256 of the original bytes.
func putGzipped(t *testing.T, r *R2, key string, data []byte) {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		gz := gzip.NewWriter(pw)
		_, err := io.Copy(gz, bytes.NewReader(data))
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()
	if err := r.Put(context.Background(), key, pr); err != nil {
		t.Fatalf("Put %s: %v", key, err)
	}
	t.Cleanup(func() { _ = r.Delete(context.Background(), key) })
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func get(t *testing.T, url string, acceptGzip bool) (*http.Response, []byte) {
	t.Helper()
	// DisableCompression stops Go from decoding for us, so the raw answer can be inspected.
	// With acceptGzip the client behaves like a browser: it asks for gzip and decodes it.
	c := &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DisableCompression: !acceptGzip}}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, body
}

func TestR2UploadAndDownloadRoundTrip(t *testing.T) {
	r, prefix := newTestR2(t)
	ctx := context.Background()
	// 1 KiB is one request; 23 MiB is several multipart parts of a stream whose length is unknown.
	for _, size := range []int{1 << 10, 23 << 20} {
		t.Run(fmt.Sprintf("%dKiB", size>>10), func(t *testing.T) {
			data := make([]byte, size)
			rand.Read(data)                                        //nolint:errcheck
			copy(data, bytes.Repeat([]byte("compressible "), 400)) // a mix, so gzip does real work
			key := fmt.Sprintf("%scad-%d.3dm.gz", prefix, size)
			putGzipped(t, r, key, data)

			name := "bague été \"v2\".3dm"
			url, err := r.PresignGet(ctx, key, name, time.Minute)
			if err != nil {
				t.Fatal(err)
			}

			// A browser: asks for gzip, and must end up with the original bytes and the right name.
			resp, body := get(t, url, true)
			if resp.StatusCode != 200 {
				t.Fatalf("download: %d %s", resp.StatusCode, body)
			}
			if sum(body) != sum(data) {
				t.Errorf("downloaded bytes differ from the original (%d vs %d bytes)", len(body), len(data))
			}
			if _, params, err := mime.ParseMediaType(resp.Header.Get("Content-Disposition")); err != nil || params["filename"] != name {
				t.Errorf("Content-Disposition = %q -> %q %v", resp.Header.Get("Content-Disposition"), params["filename"], err)
			}

			// A client that does not decode: the object really is gzip, flagged as such.
			resp, raw := get(t, url, false)
			if resp.Header.Get("Content-Encoding") != "gzip" {
				t.Errorf("Content-Encoding = %q, want gzip", resp.Header.Get("Content-Encoding"))
			}
			zr, err := gzip.NewReader(bytes.NewReader(raw))
			if err != nil {
				t.Fatalf("stored bytes are not gzip: %v", err)
			}
			if plain, _ := io.ReadAll(zr); sum(plain) != sum(data) {
				t.Error("gunzipped stored bytes differ from the original")
			}
		})
	}
}

func TestR2LinksExpireAndCannotBeTampered(t *testing.T) {
	r, prefix := newTestR2(t)
	ctx := context.Background()
	key := prefix + "link.stl.gz"
	putGzipped(t, r, key, []byte("solid x\nendsolid x\n"))

	short, err := r.PresignGet(ctx, key, "x.stl", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, short, true); resp.StatusCode != 200 {
		t.Fatalf("fresh link: %d", resp.StatusCode)
	}
	time.Sleep(3 * time.Second)
	if resp, _ := get(t, short, true); resp.StatusCode != 403 {
		t.Errorf("expired link: %d, want 403", resp.StatusCode)
	}

	good, _ := r.PresignGet(ctx, key, "x.stl", time.Minute)
	// Same signature, different object: must not work.
	other := strings.Replace(good, "link.stl.gz", "other.stl.gz", 1)
	if resp, _ := get(t, other, true); resp.StatusCode != 403 && resp.StatusCode != 404 {
		t.Errorf("link for another key: %d, want 403 or 404", resp.StatusCode)
	}
	// Same object, different filename in the signed query: the signature no longer matches.
	renamed := strings.Replace(good, "x.stl", "evil.stl", 1)
	if resp, _ := get(t, renamed, true); resp.StatusCode != 403 {
		t.Errorf("link with an altered filename: %d, want 403", resp.StatusCode)
	}
}

func TestR2DeleteRemovesTheObject(t *testing.T) {
	r, prefix := newTestR2(t)
	ctx := context.Background()
	key := prefix + "gone.3dm.gz"
	putGzipped(t, r, key, []byte("bytes"))
	url, _ := r.PresignGet(ctx, key, "gone.3dm", time.Minute)
	if resp, _ := get(t, url, true); resp.StatusCode != 200 {
		t.Fatalf("before delete: %d", resp.StatusCode)
	}
	if err := r.Delete(ctx, key); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, url, true); resp.StatusCode != 404 {
		t.Errorf("after delete: %d, want 404", resp.StatusCode)
	}
	// Removing what is not there is not an error: replacing a file deletes the old one best-effort.
	if err := r.Delete(ctx, key); err != nil {
		t.Errorf("deleting a missing object: %v", err)
	}
	if err := r.Delete(ctx, prefix+"never-existed"); err != nil {
		t.Errorf("deleting an object that never existed: %v", err)
	}
}

func TestR2PutFailsCleanlyWhenTheSourceBreaks(t *testing.T) {
	r, prefix := newTestR2(t)
	key := prefix + "broken.3dm.gz"
	pr, pw := io.Pipe()
	go func() {
		pw.Write(bytes.Repeat([]byte("x"), 7<<20)) //nolint:errcheck // more than one part
		pw.CloseWithError(fmt.Errorf("connection reset by peer"))
	}()
	if err := r.Put(context.Background(), key, pr); err == nil {
		_ = r.Delete(context.Background(), key)
		t.Fatal("Put succeeded although the source failed midway")
	}
	// No half-written object may be left behind.
	url, _ := r.PresignGet(context.Background(), key, "b.3dm", time.Minute)
	if resp, _ := get(t, url, true); resp.StatusCode == 200 {
		t.Error("an object exists after a failed upload")
	}
}

func TestR2MediaIsServedInlineWithItsTypeAndRanges(t *testing.T) {
	r, prefix := newTestR2(t)
	ctx := context.Background()
	// 12 MiB, larger than one multipart part: the video path streams without knowing the length.
	data := make([]byte, 12<<20)
	rand.Read(data) //nolint:errcheck
	key := prefix + "turntable.mp4"
	pr, pw := io.Pipe()
	go func() { pw.Write(data); pw.Close() }() //nolint:errcheck
	if err := r.PutMedia(ctx, key, "video/mp4", pr); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Delete(ctx, key) })

	url, err := r.PresignView(ctx, key, "video/mp4", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Even a client that accepts gzip gets the plain bytes back: the object is not gzip.
	resp, body := get(t, url, true)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "video/mp4" || resp.Header.Get("Content-Disposition") != "inline" {
		t.Fatalf("view: %d %q %q", resp.StatusCode, resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
	if resp.Header.Get("Content-Encoding") != "" {
		t.Errorf("Content-Encoding = %q, want none", resp.Header.Get("Content-Encoding"))
	}
	if sum(body) != sum(data) {
		t.Errorf("served bytes differ (%d vs %d)", len(body), len(data))
	}

	// A video player seeks with Range requests.
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Range", "bytes=1000-1999")
	rr, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(rr.Body)
	rr.Body.Close()
	if rr.StatusCode != http.StatusPartialContent || !bytes.Equal(part, data[1000:2000]) {
		t.Errorf("range: %d, %d bytes", rr.StatusCode, len(part))
	}

	// The type shown follows what was stored, so a photo is an image and not a download.
	pkey := prefix + "front.png"
	if err := r.PutMedia(ctx, pkey, "image/png", bytes.NewReader([]byte("\x89PNG\r\n\x1a\nxx"))); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Delete(ctx, pkey) })
	purl, _ := r.PresignView(ctx, pkey, "image/png", time.Minute)
	if resp, _ := get(t, purl, true); resp.Header.Get("Content-Type") != "image/png" || resp.Header.Get("Content-Disposition") != "inline" {
		t.Errorf("photo headers: %q %q", resp.Header.Get("Content-Type"), resp.Header.Get("Content-Disposition"))
	}
}
