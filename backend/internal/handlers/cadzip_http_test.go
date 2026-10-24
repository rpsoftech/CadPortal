package handlers

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

type zipFixture struct {
	r              *gin.Engine
	svc            *service.Service
	mem            *storage.Memory
	d1, d2         int64
	v11, v12, v21  int64 // d1-V1 (stl+3dm), d1-V2 (3dm only), d2-V1 (stl)
	stl11, stl21   []byte
	dm11, dm12     []byte
	code1, code2   string
	deletedVariant int64
}

func uploadFile(t *testing.T, f *zipFixture, vid int64, name string, data []byte) {
	t.Helper()
	body, ct := multipartBody(t, "file", name, data)
	w := do(f.r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", vid), token(t, auth.Designer), ct, body)
	if w.Code != 201 {
		t.Fatalf("upload %s: %d %s", name, w.Code, w.Body)
	}
}

func newZipFixture(t *testing.T) *zipFixture {
	t.Helper()
	r, svc, mem := newHTTPEnv(t)
	ctx := context.Background()
	f := &zipFixture{r: r, svc: svc, mem: mem}
	cats, _ := svc.ListCategories(ctx)
	d1, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR070", Name: "=Hostile name", CategoryID: cats[0].ID})
	d2, _ := svc.CreateDesign(ctx, service.DesignInput{Code: "LR071", Name: "Second", CategoryID: cats[0].ID})
	vol := mustDec("200")
	v11, _ := svc.CreateVariation(ctx, d1.ID, service.BaseInput{VolumeMM3: &vol})
	v12, _ := svc.CreateVariation(ctx, d1.ID, service.BaseInput{VolumeMM3: &vol})
	v13, _ := svc.CreateVariation(ctx, d1.ID, service.BaseInput{VolumeMM3: &vol})
	v21, _ := svc.CreateVariation(ctx, d2.ID, service.BaseInput{VolumeMM3: &vol})
	f.d1, f.d2, f.v11, f.v12, f.v21, f.code1, f.code2 = d1.ID, d2.ID, v11.ID, v12.ID, v21.ID, d1.Code, d2.Code

	f.stl11 = bytes.Repeat([]byte("solid one\n"), 3000)
	f.stl21 = bytes.Repeat([]byte("solid two\n"), 2000)
	f.dm11 = []byte("3D Geometry File Format one one one one")
	f.dm12 = []byte("3D Geometry File Format two two two two")
	uploadFile(t, f, v11.ID, "ring one.stl", f.stl11)
	uploadFile(t, f, v11.ID, "ring one.3dm", f.dm11)
	uploadFile(t, f, v12.ID, "ring two.3dm", f.dm12)
	uploadFile(t, f, v21.ID, "other.stl", f.stl21)
	// A deleted variation with a file is not downloaded.
	uploadFile(t, f, v13.ID, "gone.stl", []byte("solid gone\n"))
	if err := svc.DeleteVariation(ctx, v13.ID); err != nil {
		t.Fatal(err)
	}
	f.deletedVariant = v13.ID
	return f
}

func zipPost(f *zipFixture, role auth.Role, path string, body any) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return do(f.r, "POST", path, token2(role), "application/json", b)
}

func token2(role auth.Role) string {
	s, _ := auth.Issue(testSecret, "u", role, time.Hour)
	return "Bearer " + s
}

func readZip(t *testing.T, b []byte) (map[string][]byte, []map[string]string, string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatalf("not a zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		files[f.Name] = data
	}
	rows, err := csv.NewReader(bytes.NewReader(files["MANIFEST.csv"])).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var recs []map[string]string
	for _, r := range rows[1:] {
		m := map[string]string{}
		for i, h := range rows[0] {
			m[h] = r[i]
		}
		recs = append(recs, m)
	}
	return files, recs, zr.Comment
}

func TestCADZipContentsAndManifest(t *testing.T) {
	f := newZipFixture(t)
	w := zipPost(f, auth.Production, "/api/v1/downloads/cad-zip", gin.H{"design_ids": []int64{f.d1, f.d2}, "kinds": []string{"stl"}})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/zip" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, "cad-stl-") || !strings.Contains(cd, ".zip") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	files, recs, comment := readZip(t, w.Body.Bytes())

	// The ORIGINAL bytes, not the gzip the vault stores.
	if !bytes.Equal(files["LR070-V1.stl"], f.stl11) || !bytes.Equal(files["LR071-V1.stl"], f.stl21) {
		t.Fatal("zip entries are not the original STL bytes")
	}
	// Deleted variation ignored; V2 has no STL: listed as missing, not silently dropped.
	if len(files) != 3 {
		t.Errorf("entries = %d, want 2 files + manifest", len(files))
	}
	if len(recs) != 3 {
		t.Fatalf("manifest rows = %d, want 3 (V1, V2 missing, d2 V1): %v", len(recs), recs)
	}
	byVar := map[string]map[string]string{}
	for _, r := range recs {
		byVar[r["variation"]] = r
	}
	if r := byVar["LR070-V1"]; r["status"] != "ok" || r["original_name"] != "ring one.stl" || r["design"] != "LR070" ||
		r["actual_sha256"] != hexSum(f.stl11) || r["bytes_written"] != fmt.Sprint(len(f.stl11)) {
		t.Errorf("V1 row = %v", r)
	}
	if r := byVar["LR070-V2"]; r["status"] != "missing" || r["file_in_zip"] != "" || !strings.Contains(r["detail"], "no stl file") {
		t.Errorf("V2 row = %v", r)
	}
	if byVar["LR070-V1"]["design_name"] != "'=Hostile name" {
		t.Errorf("design name not neutralised: %q", byVar["LR070-V1"]["design_name"])
	}
	if !strings.Contains(comment, "WARNING: 1 file(s)") {
		t.Errorf("zip comment = %q", comment)
	}

	// Both kinds for explicit variations; the deleted one asked for by id is ignored.
	w = zipPost(f, auth.Designer, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": []int64{f.v11, f.v12, f.deletedVariant}, "kinds": []string{"stl", "3dm"}})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	files, recs, _ = readZip(t, w.Body.Bytes())
	for name, want := range map[string][]byte{"LR070-V1.stl": f.stl11, "LR070-V1.3dm": f.dm11, "LR070-V2.3dm": f.dm12} {
		if !bytes.Equal(files[name], want) {
			t.Errorf("%s differs", name)
		}
	}
	if len(recs) != 4 || len(files) != 4 {
		t.Errorf("rows %d entries %d", len(recs), len(files))
	}
}

func hexSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestCADZipRolesAndBadRequests(t *testing.T) {
	f := newZipFixture(t)
	good := gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}}
	if w := do(f.r, "POST", "/api/v1/downloads/cad-zip", "", "application/json", []byte("{}")); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	for _, role := range []auth.Role{auth.Admin, auth.Designer, auth.Production} {
		if w := zipPost(f, role, "/api/v1/downloads/cad-zip", good); w.Code != 200 {
			t.Errorf("%s: %d %s", role, w.Code, w.Body)
		}
	}
	bad := map[string]gin.H{
		"no kinds":    {"variation_ids": []int64{f.v11}},
		"bad kind":    {"variation_ids": []int64{f.v11}, "kinds": []string{"pdf"}},
		"no ids":      {"kinds": []string{"stl"}},
		"negative id": {"variation_ids": []int64{-3}, "kinds": []string{"stl"}},
		"only gone":   {"variation_ids": []int64{f.deletedVariant, 99999}, "kinds": []string{"stl"}},
	}
	for name, body := range bad {
		if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", body); w.Code != 400 {
			t.Errorf("%s: %d %s, want 400", name, w.Code, w.Body)
		}
	}
	if w := do(f.r, "POST", "/api/v1/downloads/cad-zip", token2(auth.Admin), "application/json", []byte("{nope")); w.Code != 400 {
		t.Errorf("bad JSON: %d", w.Code)
	}
	// The same refusals apply to the link route.
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip/link", bad["bad kind"]); w.Code != 400 {
		t.Errorf("link bad kind: %d", w.Code)
	}
	f.svc.Store = nil
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", good); w.Code != 503 {
		t.Errorf("storage disabled: %d", w.Code)
	}
}

func TestCADZipCaps(t *testing.T) {
	f := newZipFixture(t)
	body := gin.H{"design_ids": []int64{f.d1, f.d2}, "kinds": []string{"stl", "3dm"}} // 4 files
	f.svc.CADZipMaxFiles = 3
	w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", body)
	if w.Code != 413 || !strings.Contains(w.Body.String(), "4 files") {
		t.Errorf("file cap: %d %s", w.Code, w.Body)
	}
	f.svc.CADZipMaxFiles = 0
	f.svc.CADZipMaxBytes = 10 << 10
	w = zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip/link", body)
	if w.Code != 413 {
		t.Errorf("byte cap on link: %d %s", w.Code, w.Body)
	}
	f.svc.CADZipMaxBytes = 0
	big := make([]int64, service.MaxCADZipIDs+1)
	for i := range big {
		big[i] = int64(i + 1)
	}
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": big, "kinds": []string{"stl"}}); w.Code != 400 {
		t.Errorf("too many ids: %d", w.Code)
	}
}

func TestCADZipHashMismatchIsReportedNotHidden(t *testing.T) {
	f := newZipFixture(t)
	// Someone swaps the stored object behind the recorded hash.
	for k, v := range f.mem.Objects {
		if strings.Contains(k, "/LR070-V1/") && strings.HasSuffix(k, ".stl.gz") {
			_ = v
			f.mem.Objects[k] = gzipBytes(t, []byte("solid tampered\n"))
		}
	}
	w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}})
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
	_, recs, comment := readZip(t, w.Body.Bytes())
	if recs[0]["status"] != "hash-mismatch" || recs[0]["expected_sha256"] != hexSum(f.stl11) {
		t.Errorf("row = %v", recs[0])
	}
	if !strings.Contains(comment, "WARNING") {
		t.Errorf("comment = %q", comment)
	}

	// An object that vanished from the store is missing, not an error for the whole download.
	for k := range f.mem.Objects {
		if strings.Contains(k, "/LR071-V1/") {
			delete(f.mem.Objects, k)
		}
	}
	w = zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": []int64{f.v21}, "kinds": []string{"stl"}})
	_, recs, _ = readZip(t, w.Body.Bytes())
	if w.Code != 200 || recs[0]["status"] != "missing" || !strings.Contains(recs[0]["detail"], "gone") {
		t.Errorf("vanished object: %d %v", w.Code, recs[0])
	}
}

func gzipBytes(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write(b)
	zw.Close()
	return buf.Bytes()
}

// blockingStore holds every Get until released, so a download can be kept running.
type blockingStore struct {
	*storage.Memory
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (b *blockingStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return b.Memory.Get(ctx, key)
}

func TestCADZipOneRunningDownloadPerUser(t *testing.T) {
	f := newZipFixture(t)
	bs := &blockingStore{Memory: f.mem, entered: make(chan struct{}), release: make(chan struct{})}
	f.svc.Store = bs
	body := gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}}

	var first *httptest.ResponseRecorder
	done := make(chan struct{})
	go func() { first = zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", body); close(done) }()
	select {
	case <-bs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first download never started")
	}

	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", body); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Errorf("same user, second download: %d %s", w.Code, w.Body)
	}
	// Another user is not held up (use a different subject).
	other, _ := auth.Issue(testSecret, "someone-else", auth.Designer, time.Hour)
	b, _ := json.Marshal(body)
	rel := make(chan *httptest.ResponseRecorder, 1)
	go func() { rel <- do(f.r, "POST", "/api/v1/downloads/cad-zip", "Bearer "+other, "application/json", b) }()

	close(bs.release)
	<-done
	if first.Code != 200 {
		t.Errorf("first: %d", first.Code)
	}
	if w := <-rel; w.Code != 200 {
		t.Errorf("other user: %d %s", w.Code, w.Body)
	}
	// The slot is free again.
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", body); w.Code != 200 {
		t.Errorf("after the first finished: %d", w.Code)
	}
}

func TestCADZipLinkFlow(t *testing.T) {
	f := newZipFixture(t)
	body := gin.H{"design_ids": []int64{f.d1}, "kinds": []string{"stl", "3dm"}}
	w := zipPost(f, auth.Production, "/api/v1/downloads/cad-zip/link", body)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var link struct {
		URL                string `json:"url"`
		Filename           string `json:"filename"`
		Variations, Files  int
		Missing            int
		Bytes              int64
		ExpiresInSecondsFn int `json:"expires_in_seconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	// d1 has V1 (stl+3dm) and V2 (3dm only); the deleted V3 does not count.
	wantBytes := int64(len(f.stl11) + len(f.dm11) + len(f.dm12))
	if link.Variations != 2 || link.Files != 3 || link.Missing != 1 || link.Bytes != wantBytes || !strings.HasSuffix(link.Filename, ".zip") {
		t.Errorf("estimate = %+v (want bytes %d)", link, wantBytes)
	}
	if !strings.HasPrefix(link.URL, "/api/v1/downloads/cad-zip/file?ticket=") || link.ExpiresInSecondsFn <= 0 {
		t.Errorf("link = %+v", link)
	}

	// The browser opens the link with no Authorization header at all.
	got := do(f.r, "GET", link.URL, "", "", nil)
	if got.Code != 200 || got.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("open link: %d %s", got.Code, got.Body)
	}
	files, recs, _ := readZip(t, got.Body.Bytes())
	if !bytes.Equal(files["LR070-V1.stl"], f.stl11) || len(recs) != 4 {
		t.Errorf("entries %d rows %d", len(files), len(recs))
	}

	// Tampered, truncated, expired and foreign links are refused; a link is not a login token.
	for name, u := range map[string]string{
		"tampered":  link.URL[:len(link.URL)-3] + "xyz",
		"empty":     "/api/v1/downloads/cad-zip/file",
		"garbage":   "/api/v1/downloads/cad-zip/file?ticket=abc.def.ghi",
		"login jwt": "/api/v1/downloads/cad-zip/file?ticket=" + strings.TrimPrefix(token2(auth.Admin), "Bearer "),
	} {
		if w := do(f.r, "GET", u, "", "", nil); w.Code != 403 {
			t.Errorf("%s: %d, want 403", name, w.Code)
		}
	}
	ticket := strings.TrimPrefix(link.URL, "/api/v1/downloads/cad-zip/file?ticket=")
	if w := do(f.r, "GET", "/api/v1/audit", "Bearer "+ticket, "", nil); w.Code != 401 {
		t.Errorf("link used as a login token: %d", w.Code)
	}

	// The download was audited under the caller, with counts and no file names.
	rows, _ := fetchAudit(t, f.r, token2(auth.Admin), "?action=download")
	a := findWhere(rows, func(r auditRow) bool { return r.Action == "download.cad_zip" && r.Status == 200 })
	if a == nil || !strings.Contains(a.Detail, "files=3") || !strings.Contains(a.Detail, "missing=1") || !strings.Contains(a.Detail, "kinds=stl+3dm") {
		t.Fatalf("audit = %+v", a)
	}
}

func TestCADZipLinkExpiry(t *testing.T) {
	f := newZipFixture(t)
	h := &H{JWTSecret: testSecret}
	// A link minted in the past, signed with the right key.
	past := time.Now().Add(-time.Hour)
	tk, err := jwt.NewWithClaims(jwt.SigningMethodHS256, cadZipClaims{
		Role: auth.Admin, IDs: "", Kinds: []string{"stl"},
		RegisteredClaims: jwt.RegisteredClaims{Subject: "u", IssuedAt: jwt.NewNumericDate(past.Add(-time.Minute)), ExpiresAt: jwt.NewNumericDate(past)},
	}).SignedString(h.ticketKey())
	if err != nil {
		t.Fatal(err)
	}
	if w := do(f.r, "GET", "/api/v1/downloads/cad-zip/file?ticket="+tk, "", "", nil); w.Code != 403 {
		t.Errorf("expired link: %d", w.Code)
	}
}

func TestCADZipLinkRefusedForADeactivatedUser(t *testing.T) {
	gin.SetMode(gin.TestMode)
	f := newZipFixture(t)
	r := gin.New()
	active := true
	(&H{S: f.svc, JWTSecret: testSecret, Validate: func(_ context.Context, c auth.Claims) (bool, error) {
		return active && c.IssuedAt != nil && c.Subject != "", nil
	}}).Register(r)
	f.r = r
	body := gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}}
	w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip/link", body)
	var link struct{ URL string }
	json.Unmarshal(w.Body.Bytes(), &link) //nolint:errcheck
	active = false
	if w := do(r, "GET", link.URL, "", "", nil); w.Code != 403 {
		t.Errorf("deactivated user: %d", w.Code)
	}
	active = true
	if w := do(r, "GET", link.URL, "", "", nil); w.Code != 200 {
		t.Errorf("active user: %d", w.Code)
	}
}

// A browser that closes the tab stops the work on the server: the store read ends, the slot is freed and the
// audit entry says the download was aborted.
func TestCADZipClientDisconnectStopsTheDownload(t *testing.T) {
	f := newZipFixture(t)
	bs := &blockingStore{Memory: f.mem, entered: make(chan struct{}), release: make(chan struct{})}
	f.svc.Store = bs
	srv := httptest.NewServer(f.r)
	defer srv.Close()

	b, _ := json.Marshal(gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", srv.URL+"/api/v1/downloads/cad-zip", bytes.NewReader(b))
	req.Header.Set("Authorization", token2(auth.Admin))
	req.Header.Set("Content-Type", "application/json")
	errc := make(chan error, 1)
	go func() {
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		errc <- err
	}()
	select {
	case <-bs.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	cancel()
	<-errc

	// The server notices, records it and frees the user's slot.
	deadline := time.Now().Add(5 * time.Second)
	for {
		rows, _ := fetchAudit(t, f.r, token2(auth.Admin), "?action=download")
		if a := findAction(rows, "download.cad_zip"); a != nil {
			if !strings.Contains(a.Detail, "aborted") {
				t.Errorf("audit detail = %q, want aborted", a.Detail)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no audit entry after the client left")
		}
		time.Sleep(50 * time.Millisecond)
	}
	close(bs.release)
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}}); w.Code != 200 {
		t.Errorf("slot not freed: %d", w.Code)
	}
}
