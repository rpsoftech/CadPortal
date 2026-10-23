package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
	"github.com/rpsoftech/cad-portal/backend/internal/testdb"
)

var testSecret = []byte("handler-test-secret-handler-test-secret")

// httpTestDB returns the URL of this package's own database (internal/testdb), so it never
// races another package's tests when packages run in parallel.
func httpTestDB(t *testing.T) string { return testdb.DSN(t) }

func newHTTPEnv(t *testing.T) (*gin.Engine, *service.Service, *storage.Memory) {
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
	svc := service.New(pool)
	mem := storage.NewMemory()
	svc.Store = mem
	r := gin.New()
	(&H{S: svc, JWTSecret: testSecret, MaxUploadBytes: 64 << 10}).Register(r)
	return r, svc, mem
}

func token(t *testing.T, role auth.Role) string {
	t.Helper()
	s, err := auth.Issue(testSecret, "u", role, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return "Bearer " + s
}

func do(r http.Handler, method, path, authz, ctype string, body []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if authz != "" {
		req.Header.Set("Authorization", authz)
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func multipartBody(t *testing.T, field, filename string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(field, filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data) //nolint:errcheck
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

func TestCADUploadDownloadOverHTTP(t *testing.T) {
	r, svc, mem := newHTTPEnv(t)
	ctx := context.Background()

	cats, _ := svc.ListCategories(ctx)
	d1, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR040", CategoryID: cats[0].ID})
	vol := mustDec("200")
	v1, _ := svc.CreateVariation(ctx, d1.ID, service.BaseInput{VolumeMM3: &vol})
	v2, _ := svc.CreateVariation(ctx, d1.ID, service.BaseInput{VolumeMM3: &vol})

	data := make([]byte, 20<<10)
	rand.Read(data) //nolint:errcheck
	up := fmt.Sprintf("/api/v1/variations/%d/upload-cad", v1.ID)
	body, ct := multipartBody(t, "file", "ring.3dm", data)

	// Roles: no token 401, production 403, designer 201.
	if w := do(r, "POST", up, "", ct, body); w.Code != 401 {
		t.Fatalf("no token: %d", w.Code)
	}
	if w := do(r, "POST", up, token(t, auth.Production), ct, body); w.Code != 403 {
		t.Fatalf("production upload: %d", w.Code)
	}
	w := do(r, "POST", up, token(t, auth.Designer), ct, body)
	if w.Code != 201 {
		t.Fatalf("designer upload: %d %s", w.Code, w.Body)
	}
	var res struct{ SHA256, Kind string }
	json.Unmarshal(w.Body.Bytes(), &res) //nolint:errcheck
	if len(res.SHA256) != 64 || res.Kind != "" && res.Kind != "3dm" {
		t.Errorf("body = %s", w.Body)
	}
	if len(mem.Keys()) != 1 {
		t.Fatalf("keys = %v", mem.Keys())
	}

	// Duplicate on another variation: 409 naming the first.
	up2 := fmt.Sprintf("/api/v1/variations/%d/upload-cad", v2.ID)
	w = do(r, "POST", up2, token(t, auth.Admin), ct, body)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "LR040-V1") {
		t.Fatalf("duplicate: %d %s", w.Code, w.Body)
	}

	// Bad inputs.
	if w := do(r, "POST", up2, token(t, auth.Admin), "application/json", []byte("{}")); w.Code != 400 {
		t.Errorf("non-multipart: %d", w.Code)
	}
	nb, nct := multipartBody(t, "other", "x.3dm", data)
	if w := do(r, "POST", up2, token(t, auth.Admin), nct, nb); w.Code != 400 {
		t.Errorf("missing file part: %d", w.Code)
	}
	tb, tct := multipartBody(t, "file", "x.stl", make([]byte, 200<<10))
	if w := do(r, "POST", up2, token(t, auth.Admin), tct, tb); w.Code != 413 {
		t.Errorf("oversize: %d %s", w.Code, w.Body)
	}

	// Download: production allowed; missing kind 404; no token 401.
	dl := fmt.Sprintf("/api/v1/variations/%d/download", v1.ID)
	if w := do(r, "GET", dl, "", "", nil); w.Code != 401 {
		t.Errorf("download no token: %d", w.Code)
	}
	w = do(r, "GET", dl+"?kind=3dm", token(t, auth.Production), "", nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "ring.3dm") {
		t.Fatalf("download: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", dl+"?kind=stl", token(t, auth.Production), "", nil); w.Code != 404 {
		t.Errorf("missing stl: %d", w.Code)
	}
}

func TestCADStorageDisabledIs503(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	svc.Store = nil
	body, ct := multipartBody(t, "file", "a.3dm", []byte("x"))
	if w := do(r, "POST", "/api/v1/variations/1/upload-cad", token(t, auth.Admin), ct, body); w.Code != 503 {
		t.Errorf("status = %d", w.Code)
	}
}

func TestRemoveCADOverHTTP(t *testing.T) {
	r, svc, mem := newHTTPEnv(t)
	ctx := context.Background()
	cats, _ := svc.ListCategories(ctx)
	d, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR041", CategoryID: cats[0].ID})
	vol := mustDec("200")
	v, _ := svc.CreateVariation(ctx, d.ID, service.BaseInput{VolumeMM3: &vol})
	body, ct := multipartBody(t, "file", "ring.3dm", make([]byte, 2<<10))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", v.ID), token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}

	rm := fmt.Sprintf("/api/v1/variations/%d/cad?kind=3dm", v.ID)
	if w := do(r, "DELETE", rm, "", "", nil); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	if w := do(r, "DELETE", rm, token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production: %d", w.Code)
	}
	if len(mem.Keys()) != 1 {
		t.Fatalf("refused requests must not delete: %v", mem.Keys())
	}
	if w := do(r, "DELETE", strings.Replace(rm, "3dm", "x", 1), token(t, auth.Designer), "", nil); w.Code != 400 {
		t.Errorf("bad kind: %d", w.Code)
	}
	if w := do(r, "DELETE", rm, token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("designer: %d %s", w.Code, w.Body)
	}
	if len(mem.Keys()) != 0 {
		t.Errorf("object not deleted: %v", mem.Keys())
	}
	if w := do(r, "DELETE", rm, token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("second delete: %d", w.Code)
	}
	dl := fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", v.ID)
	if w := do(r, "GET", dl, token(t, auth.Production), "", nil); w.Code != 404 {
		t.Errorf("download after removal: %d", w.Code)
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=cad.")
	if a := findWhere(rows, func(r auditRow) bool { return r.Action == "cad.remove" && r.Status == 204 }); a == nil || a.Detail != "3dm" {
		t.Errorf("audit row = %+v", a)
	}
}
