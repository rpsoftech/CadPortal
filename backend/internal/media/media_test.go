package media

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"
)

func pngOf(w, h int) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), 128, 255})
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img) //nolint:errcheck
	return b.Bytes()
}

func jpegWithOrientation(w, h, orientation int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{200, 50, 50, 255})
		}
	}
	var b bytes.Buffer
	jpeg.Encode(&b, img, nil) //nolint:errcheck
	raw := b.Bytes()
	if orientation == 0 {
		return raw
	}
	// A minimal EXIF APP1 segment (little endian, one IFD entry: Orientation) after the SOI marker.
	tiff := new(bytes.Buffer)
	tiff.WriteString("II")
	binary.Write(tiff, binary.LittleEndian, uint16(42)) //nolint:errcheck
	binary.Write(tiff, binary.LittleEndian, uint32(8))  //nolint:errcheck
	binary.Write(tiff, binary.LittleEndian, uint16(1))  //nolint:errcheck
	binary.Write(tiff, binary.LittleEndian, uint16(0x0112))
	binary.Write(tiff, binary.LittleEndian, uint16(3))
	binary.Write(tiff, binary.LittleEndian, uint32(1))
	binary.Write(tiff, binary.LittleEndian, uint16(orientation))
	binary.Write(tiff, binary.LittleEndian, uint16(0))
	binary.Write(tiff, binary.LittleEndian, uint32(0))
	seg := append([]byte("Exif\x00\x00"), tiff.Bytes()...)
	app1 := []byte{0xFF, 0xE1, 0, 0}
	binary.BigEndian.PutUint16(app1[2:], uint16(len(seg)+2))
	out := append([]byte{0xFF, 0xD8}, app1...)
	out = append(out, seg...)
	return append(out, raw[2:]...)
}

func TestSniff(t *testing.T) {
	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	for _, c := range []struct {
		name string
		head []byte
		want Info
	}{
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0, 0, 0, 0, 0, 0, 0}, Info{KindImage, "image/jpeg", ".jpg"}},
		{"png", append([]byte("\x89PNG\r\n\x1a\n"), 0, 0, 0, 0), Info{KindImage, "image/png", ".png"}},
		{"webp", webp, Info{KindImage, "image/webp", ".webp"}},
		{"webm", []byte{0x1A, 0x45, 0xDF, 0xA3, 0, 0, 0, 0, 0, 0, 0, 0}, Info{KindVideo, "video/webm", ".webm"}},
		{"mp4", []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00"), Info{KindVideo, "video/mp4", ".mp4"}},
		{"mp4 v2", []byte("\x00\x00\x00\x18ftypmp42\x00\x00\x00\x00"), Info{KindVideo, "video/mp4", ".mp4"}},
		{"iphone mov", []byte("\x00\x00\x00\x14ftypqt  \x00\x00\x00\x00"), Info{KindVideo, "video/quicktime", ".mov"}},
	} {
		got, err := Sniff(c.head)
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v, want %+v", c.name, got, err, c.want)
		}
	}
	for name, head := range map[string][]byte{
		"heic photo (same container as video)": []byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00"),
		"avif":                                 []byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00"),
		"gif":                                  []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00\x00"),
		"pdf":                                  []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3"),
		"html pretending":                      []byte("<html><script>alert(1)</script>"),
		"svg":                                  []byte("<svg xmlns=\"http://www.w3.org/2000/svg\">"),
		"too short":                            []byte{0xFF},
		"empty":                                nil,
	} {
		if _, err := Sniff(head); !errors.Is(err, ErrUnsupported) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
}

func TestThumbnailShrinksAndKeepsAspect(t *testing.T) {
	for _, c := range []struct{ w, h, tw, th int }{
		{1000, 500, 480, 240},
		{500, 1000, 240, 480},
		{4000, 3000, 480, 360},
		{200, 100, 200, 100}, // already small: not enlarged
	} {
		jpg, w, h, err := Thumbnail(pngOf(c.w, c.h))
		if err != nil {
			t.Fatal(err)
		}
		if w != c.w || h != c.h {
			t.Errorf("%dx%d: reported size %dx%d", c.w, c.h, w, h)
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(jpg))
		if err != nil || format != "jpeg" || cfg.Width != c.tw || cfg.Height != c.th {
			t.Errorf("%dx%d: thumbnail %v %dx%d %v, want jpeg %dx%d", c.w, c.h, format, cfg.Width, cfg.Height, err, c.tw, c.th)
		}
	}
}

func TestThumbnailHonoursExifOrientation(t *testing.T) {
	// A landscape sensor image marked "rotate 90" is a portrait picture.
	for o, portrait := range map[int]bool{0: false, 1: false, 3: false, 6: true, 8: true, 5: true} {
		jpg, w, h, err := Thumbnail(jpegWithOrientation(600, 300, o))
		if err != nil {
			t.Fatal(err)
		}
		cfg, _, _ := image.DecodeConfig(bytes.NewReader(jpg))
		if portrait && (h <= w || cfg.Height <= cfg.Width) {
			t.Errorf("orientation %d should be portrait: reported %dx%d, thumbnail %dx%d", o, w, h, cfg.Width, cfg.Height)
		}
		if !portrait && (w <= h || cfg.Width <= cfg.Height) {
			t.Errorf("orientation %d should stay landscape: reported %dx%d, thumbnail %dx%d", o, w, h, cfg.Width, cfg.Height)
		}
	}
}

func TestOrientMovesPixelsLikeExifSays(t *testing.T) {
	// A 3x2 picture: the top-left pixel is red, the rest blue.
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	red, blue := color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255}
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			src.Set(x, y, blue)
		}
	}
	src.Set(0, 0, red)
	// Where the red pixel lands (and the new size) for every orientation.
	for _, c := range []struct {
		o, w, h, rx, ry int
	}{
		{1, 3, 2, 0, 0}, {2, 3, 2, 2, 0}, {3, 3, 2, 2, 1}, {4, 3, 2, 0, 1},
		{5, 2, 3, 0, 0}, {6, 2, 3, 1, 0}, {7, 2, 3, 1, 2}, {8, 2, 3, 0, 2},
	} {
		got := orient(src, c.o)
		b := got.Bounds()
		if b.Dx() != c.w || b.Dy() != c.h {
			t.Errorf("orientation %d: size %dx%d, want %dx%d", c.o, b.Dx(), b.Dy(), c.w, c.h)
			continue
		}
		if got.At(c.rx, c.ry) != red {
			t.Errorf("orientation %d: the red pixel is not at (%d,%d)", c.o, c.rx, c.ry)
		}
	}
}

func TestThumbnailRejectsBadAndHugeInput(t *testing.T) {
	if _, _, _, err := Thumbnail([]byte("not an image at all")); err == nil {
		t.Error("garbage accepted")
	}
	good := pngOf(50, 50)
	if _, _, _, err := Thumbnail(good[:len(good)/2]); err == nil {
		t.Error("a truncated PNG was accepted")
	}
	// A PNG header that declares 100000 x 100000 pixels: refused before any pixel is decoded.
	bomb := append([]byte{}, good[:16]...)
	binary.BigEndian.PutUint32(bomb[8:12], 100000)
	_ = bomb
	hdr := pngOf(1, 1)
	binary.BigEndian.PutUint32(hdr[16:20], 100000) // width in IHDR
	binary.BigEndian.PutUint32(hdr[20:24], 100000) // height
	if _, _, _, err := Thumbnail(hdr); err == nil {
		t.Error("a decompression bomb header was accepted")
	}
	webp, _ := base64.StdEncoding.DecodeString("UklGRhoAAABXRUJQVlA4TA0AAAAvAAAAEAcQERGIiP4HAA==")
	if jpg, w, h, err := Thumbnail(webp); err != nil || w != 1 || h != 1 || len(jpg) == 0 {
		t.Errorf("webp: %d %dx%d %v", len(jpg), w, h, err)
	}
}

func TestExifOrientationNeverPanicsOnDamagedData(t *testing.T) {
	base := jpegWithOrientation(40, 20, 6)
	if exifOrientation(base) != 6 {
		t.Fatal("setup: orientation 6 not read")
	}
	// Cut it everywhere, and flip bytes: it may return 1, but must never panic or read out of range.
	for i := 0; i < len(base) && i < 200; i++ {
		exifOrientation(base[:i])
		mut := append([]byte{}, base...)
		mut[i] ^= 0xFF
		exifOrientation(mut)
	}
	if got := exifOrientation(nil); got != 1 {
		t.Errorf("nil: %d", got)
	}
}
