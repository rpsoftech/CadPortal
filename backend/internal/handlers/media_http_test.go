package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func photoPNG(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x), uint8(y), 60, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img) //nolint:errcheck
	return b.Bytes()
}

// mediaForm builds a multipart body with the optional variation_id field BEFORE the file, the
// order the API requires.
func mediaForm(t *testing.T, variationID, filename string, data []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if variationID != "" {
		if err := mw.WriteField("variation_id", variationID); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data) //nolint:errcheck
	mw.Close()
	return buf.Bytes(), mw.FormDataContentType()
}

func mediaDesignOverHTTP(t *testing.T, svc *service.Service) (int64, string, int64) {
	t.Helper()
	ctx := context.Background()
	ringsPrefix(t, svc)
	var rings int64
	cats, _ := svc.ListCategories(ctx)
	for _, c := range cats {
		if c.Name == "Rings" {
			rings = c.ID
		}
	}
	d, err := svc.CreateDesign(ctx, service.DesignInput{CategoryID: rings})
	if err != nil {
		t.Fatal(err)
	}
	vol := mustDec("200")
	v, err := svc.CreateVariation(ctx, d.ID, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID, d.Code, v.ID
}

func TestMediaUploadRolesLimitsAndErrorsOverHTTP(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	design, code, variation := mediaDesignOverHTTP(t, svc)
	url := fmt.Sprintf("/api/v1/designs/%d/media", design)
	body, ct := mediaForm(t, "", "ring.png", photoPNG(300, 200))

	if w := do(r, "POST", url, "", ct, body); w.Code != 401 {
		t.Errorf("no token: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Production), ct, body); w.Code != 403 {
		t.Errorf("production may look, not upload: %d", w.Code)
	}
	w := do(r, "POST", url, token(t, auth.Designer), ct, body)
	if w.Code != 201 {
		t.Fatalf("designer upload: %d %s", w.Code, w.Body)
	}
	var first service.MediaItem
	if err := json.Unmarshal(w.Body.Bytes(), &first); err != nil || first.Kind != "image" || first.Width == nil || *first.Width != 300 || first.ThumbURL == "" {
		t.Fatalf("response = %s (%v)", w.Body, err)
	}

	// Refusals: not a photo or video (by content), a variation of another design, a bad field.
	bad, bct := mediaForm(t, "", "notes.jpg", []byte("<html>not a photo</html>"))
	if w := do(r, "POST", url, token(t, auth.Designer), bct, bad); w.Code != 400 || !strings.Contains(w.Body.String(), "unsupported file type") {
		t.Errorf("html named .jpg: %d %s", w.Code, w.Body)
	}
	for _, v := range []string{"abc", "0", "-3"} {
		b, c := mediaForm(t, v, "ring.png", photoPNG(20, 20))
		if w := do(r, "POST", url, token(t, auth.Designer), c, b); w.Code != 400 {
			t.Errorf("variation_id %q: %d", v, w.Code)
		}
	}
	b, c := mediaForm(t, "999999", "ring.png", photoPNG(20, 20))
	if w := do(r, "POST", url, token(t, auth.Designer), c, b); w.Code != 404 {
		t.Errorf("unknown variation: %d", w.Code)
	}
	if w := do(r, "POST", url, token(t, auth.Designer), "application/json", []byte("{}")); w.Code != 400 {
		t.Errorf("not multipart: %d", w.Code)
	}
	if w := do(r, "POST", "/api/v1/designs/999999/media", token(t, auth.Designer), ct, body); w.Code != 404 {
		t.Errorf("unknown design: %d", w.Code)
	}

	// Variation photos, and the limit of five per target answered as a readable 409.
	vb, vct := mediaForm(t, fmt.Sprint(variation), "detail.png", photoPNG(60, 40))
	for i := 0; i < 5; i++ {
		if w := do(r, "POST", url, token(t, auth.Designer), vct, vb); w.Code != 201 {
			t.Fatalf("variation photo %d: %d %s", i, w.Code, w.Body)
		}
	}
	if w := do(r, "POST", url, token(t, auth.Designer), vct, vb); w.Code != 409 || !strings.Contains(w.Body.String(), "at most 5") {
		t.Errorf("sixth variation photo: %d %s", w.Code, w.Body)
	}

	// Everyone, production included, sees the media with the design; the order is stable.
	g := do(r, "GET", "/api/v1/designs/"+code, token(t, auth.Production), "", nil)
	var detail struct{ Media []service.MediaItem }
	if g.Code != 200 || json.Unmarshal(g.Body.Bytes(), &detail) != nil || len(detail.Media) != 6 {
		t.Fatalf("design detail: %d %s", g.Code, g.Body)
	}
	if detail.Media[0].VariationID != nil || detail.Media[1].VariationID == nil {
		t.Errorf("design-level media should come first: %+v", detail.Media[:2])
	}

	// Cover and delete: editors only; a variation photo cannot be the cover.
	cover := fmt.Sprintf("/api/v1/media/%d/cover", first.ID)
	if w := do(r, "PUT", cover, token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production cover: %d", w.Code)
	}
	if w := do(r, "PUT", cover, token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Errorf("cover: %d %s", w.Code, w.Body)
	}
	if w := do(r, "PUT", fmt.Sprintf("/api/v1/media/%d/cover", detail.Media[1].ID), token(t, auth.Designer), "", nil); w.Code != 400 {
		t.Errorf("a variation photo as cover: %d", w.Code)
	}
	del := fmt.Sprintf("/api/v1/media/%d", first.ID)
	if w := do(r, "DELETE", del, token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production delete: %d", w.Code)
	}
	if w := do(r, "DELETE", del, token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Errorf("delete: %d", w.Code)
	}
	if w := do(r, "DELETE", del, token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("delete again: %d", w.Code)
	}

	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=media.&limit=100")
	seen := map[string]bool{}
	for _, a := range rows {
		if a.Status < 300 {
			seen[a.Action] = true
		}
	}
	for _, want := range []string{"media.upload", "media.cover", "media.delete"} {
		if !seen[want] {
			t.Errorf("no audit row for %s", want)
		}
	}
}

func TestMediaOnLocalStorageIsServedInlineWithRanges(t *testing.T) {
	r, svc, _ := newLocalEnv(t)
	design, code, _ := mediaDesignOverHTTP(t, svc)
	url := fmt.Sprintf("/api/v1/designs/%d/media", design)

	// A video: plain bytes (not gzip), the right type, shown inline, and seekable.
	video := append([]byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00isomiso2"), bytes.Repeat([]byte("0123456789"), 1000)...)
	vbody, vct := mediaForm(t, "", "turntable.mp4", video)
	w := do(r, "POST", url, token(t, auth.Designer), vct, vbody)
	if w.Code != 201 {
		t.Fatalf("video upload: %d %s", w.Code, w.Body)
	}
	pbody, pct := mediaForm(t, "", "front.png", photoPNG(900, 600))
	if w := do(r, "POST", url, token(t, auth.Designer), pct, pbody); w.Code != 201 {
		t.Fatalf("photo upload: %d %s", w.Code, w.Body)
	}

	g := do(r, "GET", "/api/v1/designs/"+code, token(t, auth.Production), "", nil)
	var detail struct{ Media []service.MediaItem }
	if err := json.Unmarshal(g.Body.Bytes(), &detail); err != nil || len(detail.Media) != 2 {
		t.Fatalf("detail: %s", g.Body)
	}
	var vid, pic service.MediaItem
	for _, m := range detail.Media {
		if m.Kind == "video" {
			vid = m
		} else {
			pic = m
		}
	}

	// The signed link itself is the credential: no token is sent.
	got := do(r, "GET", vid.URL, "", "", nil)
	if got.Code != 200 || got.Header().Get("Content-Type") != "video/mp4" || got.Header().Get("Content-Disposition") != "inline" {
		t.Fatalf("video view: %d %q %q", got.Code, got.Header().Get("Content-Type"), got.Header().Get("Content-Disposition"))
	}
	if got.Header().Get("Content-Encoding") != "" {
		t.Errorf("media must not be served as gzip: %q", got.Header().Get("Content-Encoding"))
	}
	if !bytes.Equal(got.Body.Bytes(), video) {
		t.Error("the video bytes served differ from the upload")
	}
	// A video needs range requests to seek.
	req := httptest.NewRequest("GET", vid.URL, nil)
	req.Header.Set("Range", "bytes=100-199")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusPartialContent || rec.Body.Len() != 100 || !bytes.Equal(rec.Body.Bytes(), video[100:200]) {
		t.Errorf("range request: %d, %d bytes", rec.Code, rec.Body.Len())
	}

	// A photo: its thumbnail is a small JPEG, its original the exact upload.
	th := do(r, "GET", pic.ThumbURL, "", "", nil)
	if th.Code != 200 || th.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("thumbnail: %d %q", th.Code, th.Header().Get("Content-Type"))
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(th.Body.Bytes()))
	if err != nil || format != "jpeg" || cfg.Width != 480 || cfg.Height != 320 {
		t.Errorf("thumbnail = %s %dx%d %v, want jpeg 480x320", format, cfg.Width, cfg.Height, err)
	}
	if orig := do(r, "GET", pic.URL, "", "", nil); orig.Code != 200 || !bytes.Equal(orig.Body.Bytes(), photoPNG(900, 600)) {
		t.Errorf("original photo: %d", orig.Code)
	}

	// A link for one object cannot be turned into another view of it or into a download.
	tampered := strings.Replace(vid.URL, "video%2Fmp4", "text%2Fhtml", 1)
	tampered = strings.Replace(tampered, "inline:video/mp4", "inline:text/html", 1)
	for _, bad := range []string{
		strings.Replace(vid.URL, "inline", "inlinx", 1),
		strings.Replace(vid.URL, "sig=", "sig=00", 1),
		tampered,
	} {
		if w := do(r, "GET", bad, "", "", nil); w.Code != 403 {
			t.Errorf("tampered link answered %d: %s", w.Code, bad)
		}
	}

	// Cards in the catalog get a link to the cover thumbnail.
	list := do(r, "GET", "/api/v1/designs", token(t, auth.Production), "", nil)
	var items []struct {
		CoverURL string `json:"cover_url"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &items); err != nil || len(items) != 1 || items[0].CoverURL == "" {
		t.Fatalf("catalog = %s", list.Body)
	}
	if c := do(r, "GET", items[0].CoverURL, "", "", nil); c.Code != 200 || c.Header().Get("Content-Type") != "image/jpeg" {
		t.Errorf("cover thumbnail: %d", c.Code)
	}
}

func TestJobBagPDFCarriesThePhotoOverHTTP(t *testing.T) {
	r, svc, _ := newHTTPEnv(t)
	design, _, variation := mediaDesignOverHTTP(t, svc)
	body := []byte(fmt.Sprintf(`{"variation_id":%d,"metal_code":"18KR","stone_code":"1HD"}`, variation))
	pdf := func() []byte {
		w := do(r, "POST", "/api/v1/exports/job-bag-pdf", token(t, auth.Production), "application/json", body)
		if w.Code != 200 || !bytes.HasPrefix(w.Body.Bytes(), []byte("%PDF-")) {
			t.Fatalf("job bag: %d", w.Code)
		}
		return w.Body.Bytes()
	}
	if bytes.Contains(pdf(), []byte("/Subtype /Image")) {
		t.Fatal("no photo yet, so no image")
	}
	b, ct := mediaForm(t, "", "front.png", photoPNG(600, 400))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/media", design), token(t, auth.Designer), ct, b); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	// Production, who may not edit anything, still gets the photo on the sheet it prints.
	if !bytes.Contains(pdf(), []byte("/Subtype /Image")) {
		t.Error("the job bag has no photo although the design has one")
	}
}
