package backup

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Real R2, scratch bucket only (the same R2_TEST_* values the storage tests use; skipped without them).
// It checks what the Backups screen relies on: listing dumps, a signed download link that returns the
// stored bytes as an attachment under the dump's name, and a link that stops working when tampered with.
func TestR2DumpListingAndSignedDownload(t *testing.T) {
	cfg := S3Config{
		Endpoint: os.Getenv("R2_TEST_ENDPOINT"), Bucket: os.Getenv("R2_TEST_BUCKET"),
		AccessKeyID: os.Getenv("R2_TEST_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("R2_TEST_SECRET_ACCESS_KEY"),
	}
	if cfg.Endpoint == "" || cfg.Bucket == "" || cfg.AccessKeyID == "" || cfg.SecretAccessKey == "" {
		t.Skip("R2_TEST_* not set")
	}
	s, err := NewS3(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// A dump-shaped name under the test prefix, so nothing real is touched.
	key := "r2-test/" + DumpKey(time.Now(), true)
	body := "encrypted bytes " + strings.Repeat("x", 2000)
	if err := s.Put(ctx, key, strings.NewReader(body), Meta{ContentType: "application/octet-stream"}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Delete(ctx, key) })

	objs, err := s.List(ctx, "r2-test/db/")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, o := range objs {
		found = found || (o.Key == key && o.Size == int64(len(body)))
	}
	if !found {
		t.Fatalf("the dump is not listed: %v", objs)
	}

	url, err := s.PresignGet(ctx, key, "cad_portal-test.dump.age", 2*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(got) != body {
		t.Fatalf("download: %d, %d bytes", resp.StatusCode, len(got))
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, "cad_portal-test.dump.age") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	bad, err := http.Get(strings.Replace(url, "r2-test", "r2-tesu", 1))
	if err != nil {
		t.Fatal(err)
	}
	bad.Body.Close()
	if bad.StatusCode == 200 {
		t.Error("a tampered link must not work")
	}
}
