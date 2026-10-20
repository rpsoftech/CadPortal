package handlers

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

// newLocalEnv is newHTTPEnv with the local-disk store wired the way main does.
func newLocalEnv(t *testing.T) (*gin.Engine, *service.Service, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	dsn := httpTestDB(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	local, err := storage.NewLocal(dir, testSecret)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { local.Close() })
	svc := service.New(pool)
	svc.Store = local
	r := gin.New()
	(&H{S: svc, JWTSecret: testSecret, LocalFiles: local}).Register(r)
	return r, svc, dir
}

func TestLocalStorageUploadAndSignedDownload(t *testing.T) {
	r, svc, dir := newLocalEnv(t)
	ctx := context.Background()
	cats, _ := svc.ListCategories(ctx)
	des, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR070", CategoryID: cats[0].ID})
	vol := mustDec("200")
	v, _ := svc.CreateVariation(ctx, des.ID, service.BaseInput{VolumeMM3: &vol})

	data := make([]byte, 50<<10)
	rand.Read(data) //nolint:errcheck
	body, ct := multipartBody(t, "file", "ring one.3dm", data)
	up := fmt.Sprintf("/api/v1/variations/%d/upload-cad", v.ID)
	if w := do(r, "POST", up, token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}

	// The object is on disk, gzipped, under the variation's key.
	var onDisk []string
	entries, _ := os.ReadDir(dir + "/cad/LR070/LR070-V1")
	for _, e := range entries {
		onDisk = append(onDisk, e.Name())
	}
	if len(onDisk) != 1 || !strings.HasSuffix(onDisk[0], ".3dm.gz") {
		t.Fatalf("files on disk = %v", onDisk)
	}

	// Download link: authenticated route returns a relative signed URL.
	w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", v.ID), token(t, auth.Production), "", nil)
	if w.Code != 200 {
		t.Fatalf("link: %d %s", w.Code, w.Body)
	}
	var link struct {
		URL      string `json:"url"`
		Filename string `json:"filename"`
	}
	json.Unmarshal(w.Body.Bytes(), &link) //nolint:errcheck
	if !strings.HasPrefix(link.URL, storage.LocalURLPrefix+"cad/LR070/LR070-V1/") {
		t.Fatalf("url = %s", link.URL)
	}

	// The signed link needs no token and streams the gzipped bytes with the right headers.
	w = do(r, "GET", link.URL, "", "", nil)
	if w.Code != 200 {
		t.Fatalf("signed download: %d %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Encoding") != "gzip" || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Disposition"), "ring one.3dm") || !strings.Contains(h.Get("Cache-Control"), "no-store") {
		t.Errorf("headers = %v", h)
	}
	zr, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(zr)
	if !bytes.Equal(got, data) {
		t.Error("downloaded bytes do not gunzip to the uploaded file")
	}

	// Tampering: wrong signature, swapped key, altered filename, expired, and traversal all fail.
	u, _ := url.Parse(link.URL)
	bad := func(mutate func(q url.Values, p *string)) int {
		q := u.Query()
		p := u.Path
		mutate(q, &p)
		return do(r, "GET", p+"?"+q.Encode(), "", "", nil).Code
	}
	if c := bad(func(q url.Values, _ *string) { q.Set("sig", strings.Repeat("0", 64)) }); c != 403 {
		t.Errorf("wrong sig: %d", c)
	}
	if c := bad(func(q url.Values, _ *string) { q.Set("filename", "evil.3dm") }); c != 403 {
		t.Errorf("altered filename: %d", c)
	}
	if c := bad(func(q url.Values, _ *string) { q.Set("expires", "1") }); c != 403 {
		t.Errorf("expired: %d", c)
	}
	if c := bad(func(_ url.Values, p *string) { *p = storage.LocalURLPrefix + "cad/LR070/LR070-V1/other.3dm.gz" }); c != 403 {
		t.Errorf("swapped key: %d", c)
	}
	if c := bad(func(_ url.Values, p *string) { *p = storage.LocalURLPrefix + "../../etc/passwd" }); c != 403 && c != 404 && c != 301 {
		t.Errorf("traversal: %d", c)
	}
	if c := do(r, "GET", storage.LocalURLPrefix+"cad/x", "", "", nil).Code; c != 403 {
		t.Errorf("no signature: %d", c)
	}

	// Upload and download still enforce roles; the signed route alone is open.
	if w := do(r, "POST", up, token(t, auth.Production), ct, body); w.Code != 403 {
		t.Errorf("production upload: %d", w.Code)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", v.ID), "", "", nil); w.Code != 401 {
		t.Errorf("link without token: %d", w.Code)
	}
}

func TestLocalRouteAbsentWithoutLocalStore(t *testing.T) {
	r, _, _ := newHTTPEnv(t) // in-memory store: no local-files route
	if w := do(r, "GET", storage.LocalURLPrefix+"cad/x?sig=0&expires=9999999999", "", "", nil); w.Code != 404 {
		t.Errorf("route should not exist, got %d", w.Code)
	}
}
