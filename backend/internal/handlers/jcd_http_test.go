package handlers

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// A .jcd (JewelCAD) file is kept in the native CAD slot, the one a .3dm uses, under its own name.
func TestJCDIsKeptInTheNativeSlotAndZippedUnderItsOwnExtension(t *testing.T) {
	f := newZipFixture(t)
	ctx := context.Background()
	vol := mustDec("200")
	v, err := f.svc.CreateVariation(ctx, f.d2, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}
	jcd := bytes.Repeat([]byte("JewelCAD data\n"), 500)
	uploadFile(t, f, v.ID, "ring three.JCD", jcd)
	uploadFile(t, f, v.ID, "ring three.stl", []byte("solid three\n")) // an stl and a jcd together

	got, err := f.svc.Q.GetVariation(ctx, v.ID)
	if err != nil || !got.File3dmName.Valid || got.File3dmName.String != "ring three.JCD" || !got.FileStlName.Valid {
		t.Fatalf("stored names: %+v %v", got, err)
	}

	// Download keeps the real file name.
	dl := fmt.Sprintf("/api/v1/variations/%d/download?kind=3dm", v.ID)
	if w := do(f.r, "GET", dl, token(t, auth.Production), "", nil); w.Code != 200 || !strings.Contains(w.Body.String(), "ring three.JCD") {
		t.Fatalf("download: %d %s", w.Code, w.Body)
	}

	// The same bytes under a .3dm name on another variation are a duplicate: one native slot, one checksum.
	body, ct := multipartBody(t, "file", "copy.3dm", jcd)
	if w := do(f.r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", f.v21), token(t, auth.Designer), ct, body); w.Code != 409 {
		t.Errorf("a .3dm with a .jcd's bytes: %d, want 409", w.Code)
	}

	// In a CAD ZIP the entry keeps .jcd, so it opens in the program that made it.
	w := zipPost(f, auth.Production, "/api/v1/downloads/cad-zip", map[string]any{"variation_ids": []int64{v.ID}, "kinds": []string{"3dm"}})
	if w.Code != 200 {
		t.Fatalf("zip: %d %s", w.Code, w.Body)
	}
	files, recs, _ := readZip(t, w.Body.Bytes())
	name := f.code2 + "-V2.jcd"
	if !bytes.Equal(files[name], jcd) {
		t.Errorf("zip entries %v, want %s with the original bytes", keysOf(files), name)
	}
	if _, bad := files[f.code2+"-V2.3dm"]; bad {
		t.Error("a .jcd must not be zipped as .3dm")
	}
	if len(recs) != 1 || recs[0]["kind"] != "3dm" || recs[0]["original_name"] != "ring three.JCD" || recs[0]["status"] != "ok" {
		t.Errorf("manifest = %v", recs)
	}

	// Anything else is still refused, and the message names all three types.
	body, ct = multipartBody(t, "file", "notes.txt", []byte("x"))
	w = do(f.r, "POST", fmt.Sprintf("/api/v1/variations/%d/upload-cad", v.ID), token(t, auth.Designer), ct, body)
	if w.Code != 400 || !strings.Contains(w.Body.String(), ".jcd") {
		t.Errorf("bad extension: %d %s", w.Code, w.Body)
	}
}

func keysOf(m map[string][]byte) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}
