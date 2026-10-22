package handlers

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestDeletedDesignsAndFilesCanBeBroughtBackOverHTTP(t *testing.T) {
	r, svc, mem := newHTTPEnv(t)
	svc.Retention = 24 * time.Hour
	design, code, variation := mediaDesignOverHTTP(t, svc)

	// A CAD file and a photo, then remove both.
	body, ct := multipartBody(t, "file", "ring.3dm", make([]byte, 5000))
	if w := do(r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", variation), token(t, auth.Designer), ct, body); w.Code != 201 {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	pb, pct := mediaForm(t, "", "front.png", photoPNG(200, 100))
	pw := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/media", design), token(t, auth.Designer), pct, pb)
	var photo service.MediaItem
	if pw.Code != 201 || json.Unmarshal(pw.Body.Bytes(), &photo) != nil {
		t.Fatalf("photo: %d %s", pw.Code, pw.Body)
	}
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/variations/%d/cad?kind=3dm", variation), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("remove cad: %d", w.Code)
	}
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/media/%d", photo.ID), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("delete photo: %d", w.Code)
	}
	if w := do(r, "DELETE", fmt.Sprintf("/api/v1/designs/%d", design), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("delete design: %d", w.Code)
	}
	if len(mem.Keys()) != 3 { // the CAD file, the photo, its thumbnail: nothing was destroyed
		t.Errorf("stored objects = %v", mem.Keys())
	}

	// Production may not even look; designers and admins can.
	if w := do(r, "GET", "/api/v1/trash", "", "", nil); w.Code != 401 {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := do(r, "GET", "/api/v1/trash", token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production list: %d", w.Code)
	}
	var tr service.Trash
	w := do(r, "GET", "/api/v1/trash", token(t, auth.Designer), "", nil)
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &tr) != nil || len(tr.Designs) != 1 || len(tr.Files) != 2 || tr.Designs[0].Code != code {
		t.Fatalf("trash: %d %s", w.Code, w.Body)
	}

	// Restoring needs the right role and, for a photo, a live design.
	var cadID, photoID int64
	for _, f := range tr.Files {
		if f.Kind == "cad" {
			cadID = f.ID
		} else {
			photoID = f.ID
		}
	}
	file := func(id int64) string { return fmt.Sprintf("/api/v1/trash/files/%d/restore", id) }
	if w := do(r, "POST", file(cadID), token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production restore: %d", w.Code)
	}
	if w := do(r, "POST", file(photoID), token(t, auth.Designer), "", nil); w.Code != 400 {
		t.Errorf("photo of a deleted design: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/restore", design), token(t, auth.Production), "", nil); w.Code != 403 {
		t.Errorf("production design restore: %d", w.Code)
	}
	if w := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/restore", design), token(t, auth.Designer), "", nil); w.Code != 204 {
		t.Fatalf("restore design: %d %s", w.Code, w.Body)
	}
	if w := do(r, "POST", fmt.Sprintf("/api/v1/designs/%d/restore", design), token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("restoring a live design: %d", w.Code)
	}
	for _, id := range []int64{cadID, photoID} {
		if w := do(r, "POST", file(id), token(t, auth.Designer), "", nil); w.Code != 204 {
			t.Fatalf("restore file %d: %d %s", id, w.Code, w.Body)
		}
	}
	if w := do(r, "POST", file(cadID), token(t, auth.Designer), "", nil); w.Code != 404 {
		t.Errorf("restoring twice: %d", w.Code)
	}

	// Everything is back: the design, the CAD file (downloadable), the photo.
	g := do(r, "GET", "/api/v1/designs/"+code, token(t, auth.Production), "", nil)
	var detail struct {
		Media      []service.MediaItem
		Variations []struct {
			File3dmName *string `json:"file_3dm_name"`
		}
	}
	if g.Code != 200 || json.Unmarshal(g.Body.Bytes(), &detail) != nil || len(detail.Media) != 1 || len(detail.Variations) != 1 ||
		detail.Variations[0].File3dmName == nil || *detail.Variations[0].File3dmName != "ring.3dm" {
		t.Fatalf("restored design: %d %s", g.Code, g.Body)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", variation), token(t, auth.Production), "", nil); w.Code != 200 {
		t.Errorf("download after restore: %d", w.Code)
	}
	rows, _ := fetchAudit(t, r, token(t, auth.Admin), "?action=trash.&limit=50")
	if findAction(rows, "trash.restore_file") == nil {
		t.Error("restoring a file should be audited")
	}
	rows, _ = fetchAudit(t, r, token(t, auth.Admin), "?action=design.restore&limit=50")
	if findWhere(rows, func(a auditRow) bool { return a.Status == 204 }) == nil {
		t.Error("restoring a design should be audited")
	}
}
