package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// orderZipFixture is the selection-download fixture plus an order of three lines: d1-V1 (stl + 3dm, quantity 4),
// d1-V2 (3dm only, 2) and d2-V1 (stl, 1).
func newOrderZipFixture(t *testing.T) (*zipFixture, service.OrderDetail) {
	t.Helper()
	f := newZipFixture(t)
	od, err := f.svc.CreateOrder(context.Background(), service.CreateOrderParams{
		Name: "=Hostile order", MetalCode: "18KR", StoneCode: "2HD", Place: true,
		Items: []service.OrderItem{{VariationID: f.v11, Quantity: 4}, {VariationID: f.v12, Quantity: 2}, {VariationID: f.v21, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return f, od
}

func orderGet(f *zipFixture, role auth.Role, path string) *httptest.ResponseRecorder {
	return do(f.r, "GET", path, token2(role), "", nil)
}

func byVariation(recs []map[string]string, kind string) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, r := range recs {
		if r["kind"] == kind {
			out[r["variation"]] = r
		}
	}
	return out
}

func TestOrderZipContentsAndManifest(t *testing.T) {
	f, od := newOrderZipFixture(t)
	w := orderGet(f, auth.Production, fmt.Sprintf("/api/v1/orders/%d/download?files=stl,3dm", od.ID))
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if cd := w.Header().Get("Content-Disposition"); !strings.Contains(cd, od.Number+"-stl-3dm-") {
		t.Errorf("Content-Disposition = %q", cd)
	}
	files, recs, comment := readZip(t, w.Body.Bytes())
	for name, want := range map[string][]byte{"LR070-V1.stl": f.stl11, "LR070-V1.3dm": f.dm11, "LR070-V2.3dm": f.dm12, "LR071-V1.stl": f.stl21} {
		if !bytes.Equal(files[name], want) {
			t.Errorf("%s is not the original bytes", name)
		}
	}
	if len(files) != 5 || len(recs) != 6 { // 4 files + manifest; V2 stl and d2-V1 3dm missing
		t.Fatalf("entries %d rows %d", len(files), len(recs))
	}
	stl := byVariation(recs, "stl")
	r := stl["LR070-V1"]
	if r["order"] != od.Number || r["quantity"] != "4" || r["metal"] != "18KR" || r["stone_code"] != "2HD" || r["design"] != "LR070" ||
		r["status"] != "ok" || r["current_file"] != "same" || r["actual_sha256"] != hexSum(f.stl11) || r["expected_sha256"] != hexSum(f.stl11) ||
		r["bytes_written"] != fmt.Sprint(len(f.stl11)) || r["original_name"] != "ring one.stl" {
		t.Errorf("row = %v", r)
	}
	if r := stl["LR070-V2"]; r["status"] != "missing" || r["current_file"] != "none-when-ordered" || !strings.Contains(r["detail"], "recorded no stl") {
		t.Errorf("missing row = %v", r)
	}
	if !strings.Contains(comment, od.Number) || !strings.Contains(comment, "WARNING: 2 file(s)") || strings.Contains(comment, "replaced or removed") {
		t.Errorf("comment = %q", comment)
	}

	// One kind only; the package route needs the same files parameter and adds the PDF and the summary.
	w = orderGet(f, auth.Designer, fmt.Sprintf("/api/v1/orders/%d/download?files=3dm", od.ID))
	files, _, _ = readZip(t, w.Body.Bytes())
	if w.Code != 200 || len(files) != 3 {
		t.Errorf("3dm only: %d, %d entries", w.Code, len(files))
	}
	w = orderGet(f, auth.Admin, fmt.Sprintf("/api/v1/orders/%d/package?files=stl,3dm", od.ID))
	if w.Code != 200 {
		t.Fatalf("package: %d %s", w.Code, w.Body)
	}
	files, recs, _ = readZip(t, w.Body.Bytes())
	pdf := files[od.Number+"-job-bags.pdf"]
	if !bytes.HasPrefix(pdf, []byte("%PDF")) {
		t.Fatalf("job-bag PDF missing or not a PDF (%d bytes)", len(pdf))
	}
	sum, err := csv.NewReader(bytes.NewReader(files[od.Number+"-summary.csv"])).ReadAll()
	if err != nil || len(sum) != 5 { // header, three lines, total
		t.Fatalf("summary: %v rows=%d", err, len(sum))
	}
	if sum[1][0] != od.Number || sum[1][9] != "4" || sum[4][3] != "TOTAL" || sum[4][9] != "7" || sum[1][2] != "placed" || sum[1][1] != "'=Hostile order" {
		t.Errorf("summary rows = %v", sum)
	}
	if !bytes.Equal(files["LR070-V1.stl"], f.stl11) || len(files) != 7 { // 4 CAD + pdf + summary + manifest
		t.Errorf("package entries = %d", len(files))
	}
	var extras int
	for _, r := range recs {
		if r["order"] == "" && r["status"] == "ok" {
			extras++
		}
	}
	if extras != 2 {
		t.Errorf("the PDF and the summary should be listed in the manifest, got %d rows", extras)
	}
}

func TestOrderZipRolesAndBadRequests(t *testing.T) {
	f, od := newOrderZipFixture(t)
	base := fmt.Sprintf("/api/v1/orders/%d", od.ID)
	for _, p := range []string{"/download?files=stl", "/package?files=stl", "/download/link?files=stl", "/package/link?files=stl"} {
		if w := do(f.r, "GET", base+p, "", "", nil); w.Code != 401 {
			t.Errorf("%s anonymous: %d", p, w.Code)
		}
		for _, role := range []auth.Role{auth.Admin, auth.Designer, auth.Production} {
			if w := orderGet(f, role, base+p); w.Code != 200 {
				t.Errorf("%s as %s: %d %s", p, role, w.Code, w.Body)
			}
		}
	}
	for name, p := range map[string]string{
		"no files":     base + "/download",
		"bad kind":     base + "/download?files=pdf",
		"unknown":      "/api/v1/orders/999999/download?files=stl",
		"bad id":       "/api/v1/orders/abc/download?files=stl",
		"link no kind": base + "/download/link",
	} {
		w := orderGet(f, auth.Admin, p)
		want := 400
		if name == "unknown" {
			want = 404
		}
		if w.Code != want {
			t.Errorf("%s: %d %s, want %d", name, w.Code, w.Body, want)
		}
	}
	f.svc.Store = nil
	if w := orderGet(f, auth.Admin, base+"/download?files=stl"); w.Code != 503 {
		t.Errorf("storage disabled: %d", w.Code)
	}
}

func TestOrderZipCaps(t *testing.T) {
	f, od := newOrderZipFixture(t)
	p := fmt.Sprintf("/api/v1/orders/%d/download?files=stl,3dm", od.ID) // 4 recorded files
	f.svc.CADZipMaxFiles = 3
	if w := orderGet(f, auth.Admin, p); w.Code != 413 || !strings.Contains(w.Body.String(), "4 files") {
		t.Errorf("file cap: %d %s", w.Code, w.Body)
	}
	f.svc.CADZipMaxFiles = 0
	f.svc.CADZipMaxBytes = 10 << 10
	if w := orderGet(f, auth.Admin, strings.Replace(p, "/download", "/download/link", 1)); w.Code != 413 {
		t.Errorf("byte cap on link: %d %s", w.Code, w.Body)
	}
}

func TestOrderZipFlagsChangedFilesAndKeepsTheRecordedOne(t *testing.T) {
	f, od := newOrderZipFixture(t)
	ctx := context.Background()
	p := fmt.Sprintf("/api/v1/orders/%d/download?files=stl,3dm", od.ID)

	// The designer replaces d1-V1's STL and deletes d2-V1 for good after the order was placed.
	newSTL := []byte("solid replaced\nsolid replaced\n")
	uploadFile(t, f, f.v11, "ring one v2.stl", newSTL)
	if err := f.svc.RemoveCAD(ctx, f.v12, "3dm"); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.DeleteVariation(ctx, f.v21); err != nil {
		t.Fatal(err)
	}

	w := orderGet(f, auth.Production, p)
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	files, recs, comment := readZip(t, w.Body.Bytes())
	// What the order recorded is what ships, not the new file.
	if !bytes.Equal(files["LR070-V1.stl"], f.stl11) || !bytes.Equal(files["LR070-V2.3dm"], f.dm12) || !bytes.Equal(files["LR071-V1.stl"], f.stl21) {
		t.Fatal("the ZIP must hold the files the order recorded")
	}
	stl := byVariation(recs, "stl")
	if r := stl["LR070-V1"]; r["status"] != "ok" || r["current_file"] != "changed" || !strings.Contains(r["detail"], "different stl file now (ring one v2.stl)") {
		t.Errorf("changed row = %v", r)
	}
	if r := byVariation(recs, "3dm")["LR070-V2"]; r["status"] != "ok" || r["current_file"] != "removed" {
		t.Errorf("removed row = %v", r)
	}
	if r := stl["LR071-V1"]; r["status"] != "ok" || r["current_file"] != "variation-gone" {
		t.Errorf("deleted variation row = %v", r)
	}
	if !strings.Contains(comment, "3 file(s) have been replaced or removed in the catalog since") {
		t.Errorf("comment = %q", comment)
	}

	// The link says so before the download starts.
	var link struct{ Changed, Files, Missing int }
	_ = json.Unmarshal(orderGet(f, auth.Production, strings.Replace(p, "/download", "/download/link", 1)).Body.Bytes(), &link)
	if link.Changed != 3 || link.Files != 4 || link.Missing != 2 {
		t.Errorf("link = %+v", link)
	}
}

func TestOrderZipFileKeptWhileAnOpenOrderNeedsIt(t *testing.T) {
	f, od := newOrderZipFixture(t)
	ctx := context.Background()
	p := fmt.Sprintf("/api/v1/orders/%d/download?files=stl", od.ID)

	// Replace the STL (retention is zero here: without the protection the old object would go at once), delete
	// the variation, and purge everything that is due.
	uploadFile(t, f, f.v11, "ring one v2.stl", []byte("solid replaced\nsolid replaced\n"))
	if err := f.svc.DeleteVariation(ctx, f.v21); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.PurgeTrash(ctx); err != nil {
		t.Fatal(err)
	}
	w := orderGet(f, auth.Admin, p)
	files, recs, _ := readZip(t, w.Body.Bytes())
	if !bytes.Equal(files["LR070-V1.stl"], f.stl11) || recs[0]["status"] != "ok" {
		t.Fatalf("the replaced file was purged although an open order recorded it: %v", recs)
	}
	// The same for a variation purged for good: its row is gone, the order's copy of the file is not.
	if !bytes.Equal(files["LR071-V1.stl"], f.stl21) || byVariation(recs, "stl")["LR071-V1"]["current_file"] != "variation-gone" {
		t.Fatalf("the file of a purged variation was deleted although an open order recorded it: %v", recs)
	}

	// Once the order is cancelled nothing holds the old file and the next purge removes it.
	if _, err := f.svc.ChangeOrderStatus(ctx, od.ID, service.OrderCancelled, "", 0); err != nil {
		t.Fatal(err)
	}
	if n, err := f.svc.PurgeTrash(ctx); err != nil || n == 0 {
		t.Fatalf("purge after cancel: %d, %v", n, err)
	}
	w = orderGet(f, auth.Admin, p)
	_, recs, comment := readZip(t, w.Body.Bytes())
	got := byVariation(recs, "stl")["LR070-V1"]
	if gone := byVariation(recs, "stl")["LR071-V1"]; gone["status"] != "missing" {
		t.Errorf("the purged variation's file should go too once the order is cancelled: %v", gone)
	}
	if w.Code != 200 || got["status"] != "missing" || !strings.Contains(got["detail"], "gone") || !strings.Contains(comment, "WARNING") {
		t.Errorf("purged file: %d %v %q", w.Code, got, comment)
	}
}

func TestOrderZipLinkFlowAndAudit(t *testing.T) {
	f, od := newOrderZipFixture(t)
	w := orderGet(f, auth.Production, fmt.Sprintf("/api/v1/orders/%d/package/link?files=stl", od.ID))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var link struct {
		URL, Filename, Order  string
		Lines, Files, Missing int
		Bytes                 int64
		Package               bool
	}
	if err := json.Unmarshal(w.Body.Bytes(), &link); err != nil {
		t.Fatal(err)
	}
	if link.Order != od.Number || link.Lines != 3 || link.Files != 2 || link.Missing != 1 || link.Bytes != int64(len(f.stl11)+len(f.stl21)) ||
		!link.Package || !strings.Contains(link.Filename, od.Number+"-package-") || !strings.HasPrefix(link.URL, "/api/v1/downloads/order-zip/file?ticket=") {
		t.Errorf("link = %+v", link)
	}
	got := do(f.r, "GET", link.URL, "", "", nil) // a browser: no Authorization header
	if got.Code != 200 || got.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("open link: %d %s", got.Code, got.Body)
	}
	files, _, _ := readZip(t, got.Body.Bytes())
	if len(files) != 5 || !bytes.HasPrefix(files[od.Number+"-job-bags.pdf"], []byte("%PDF")) { // stl x2, pdf, summary, manifest
		t.Errorf("entries = %d", len(files))
	}

	for name, u := range map[string]string{
		"tampered":      link.URL[:len(link.URL)-3] + "xyz",
		"empty":         "/api/v1/downloads/order-zip/file",
		"login jwt":     "/api/v1/downloads/order-zip/file?ticket=" + strings.TrimPrefix(token2(auth.Admin), "Bearer "),
		"selection key": "/api/v1/downloads/order-zip/file?ticket=" + cadSelectionTicket(t, f),
	} {
		if w := do(f.r, "GET", u, "", "", nil); w.Code != 403 {
			t.Errorf("%s: %d, want 403", name, w.Code)
		}
	}
	ticket := strings.TrimPrefix(link.URL, "/api/v1/downloads/order-zip/file?ticket=")
	if w := do(f.r, "GET", "/api/v1/audit", "Bearer "+ticket, "", nil); w.Code != 401 {
		t.Errorf("link used as a login token: %d", w.Code)
	}
	if w := do(f.r, "GET", "/api/v1/downloads/cad-zip/file?ticket="+ticket, "", "", nil); w.Code != 403 {
		t.Errorf("order link used on the selection route: %d", w.Code)
	}

	rows, _ := fetchAudit(t, f.r, token2(auth.Admin), "?action=order.download")
	a := findWhere(rows, func(r auditRow) bool { return r.Status == 200 && strings.Contains(r.Detail, "package=true") })
	if a == nil || a.EntityType != "order" || a.EntityID != fmt.Sprint(od.ID) || !strings.Contains(a.Detail, "files=4") || !strings.Contains(a.Detail, "kinds=stl") ||
		!strings.Contains(a.Detail, od.Number) {
		t.Fatalf("audit = %+v", a)
	}
	// Only runs are recorded, not the link checks: the link above produced one entry per streamed ZIP.
	for _, r := range rows {
		if strings.Contains(r.Detail, "files=0") {
			t.Errorf("a link check was audited: %+v", r)
		}
	}
}

func cadSelectionTicket(t *testing.T, f *zipFixture) string {
	t.Helper()
	w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip/link", gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}})
	var l struct{ URL string }
	_ = json.Unmarshal(w.Body.Bytes(), &l)
	return strings.TrimPrefix(l.URL, "/api/v1/downloads/cad-zip/file?ticket=")
}

func TestOrderZipOneRunningDownloadPerUserIsSharedWithSelectionDownloads(t *testing.T) {
	f, od := newOrderZipFixture(t)
	bs := &blockingStore{Memory: f.mem, entered: make(chan struct{}), release: make(chan struct{})}
	f.svc.Store = bs
	p := fmt.Sprintf("/api/v1/orders/%d/download?files=stl", od.ID)

	done := make(chan *httptest.ResponseRecorder, 1)
	go func() { done <- orderGet(f, auth.Admin, p) }()
	<-bs.entered
	if w := orderGet(f, auth.Admin, p); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Errorf("second order download: %d %s", w.Code, w.Body)
	}
	if w := zipPost(f, auth.Admin, "/api/v1/downloads/cad-zip", gin.H{"variation_ids": []int64{f.v11}, "kinds": []string{"stl"}}); w.Code != 429 {
		t.Errorf("selection download while an order download runs: %d", w.Code)
	}
	close(bs.release)
	if w := <-done; w.Code != 200 {
		t.Errorf("first: %d", w.Code)
	}
}

func TestOrderWithoutLinesOrFilesIsRefusedClearly(t *testing.T) {
	f, _ := newOrderZipFixture(t)
	// An order of a variation with no file at all still downloads: the manifest says so for each line.
	vol := mustDec("50")
	v, err := f.svc.CreateVariation(context.Background(), f.d1, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}
	od, err := f.svc.CreateOrder(context.Background(), service.CreateOrderParams{MetalCode: "18KR", StoneCode: "2HD", Items: []service.OrderItem{{VariationID: v.ID, Quantity: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	w := orderGet(f, auth.Admin, fmt.Sprintf("/api/v1/orders/%d/download?files=stl,3dm", od.ID))
	files, recs, comment := readZip(t, w.Body.Bytes())
	if w.Code != 200 || len(files) != 1 || len(recs) != 2 || recs[0]["status"] != "missing" || !strings.Contains(comment, "WARNING: 2 file(s)") {
		t.Errorf("%d entries=%d rows=%v comment=%q", w.Code, len(files), recs, comment)
	}
}
