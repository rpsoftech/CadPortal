// Package media recognises the photos and videos attached to designs and makes thumbnails of the
// photos. Nothing here trusts a file name or a declared content type: the kind of a file is
// decided from its first bytes.
package media

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png" // registers the PNG decoder for image.Decode

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // registers the WebP decoder
)

const (
	KindImage = "image"
	KindVideo = "video"

	// ThumbMax is the longest side of a thumbnail in pixels.
	ThumbMax = 480
	// MaxPixels bounds the size of a photo that is decoded, so a small file that expands to a huge
	// bitmap (a decompression bomb) cannot exhaust memory. 50 megapixels covers current phones.
	MaxPixels = 50_000_000
)

// Info says what a file is.
type Info struct {
	Kind        string // KindImage or KindVideo
	ContentType string
	Ext         string // with the dot
}

// ErrUnsupported is returned for anything that is not a JPEG, PNG or WebP photo, or an MP4,
// QuickTime or WebM video.
var ErrUnsupported = errors.New("unsupported file type: use JPEG, PNG or WebP photos, or MP4, MOV or WebM video")

// videoBrands are the "ftyp" brands of MP4 and QuickTime files. HEIC and AVIF photos use the same
// container with other brands and must not be mistaken for video.
var videoBrands = map[string]bool{
	"isom": true, "iso2": true, "iso3": true, "iso4": true, "iso5": true, "iso6": true,
	"mp41": true, "mp42": true, "avc1": true, "M4V ": true, "dash": true,
	"mmp4": true, "f4v ": true, "3gp4": true, "3gp5": true, "3gp6": true, "3g2a": true,
}

// Sniff decides what a file is from its first bytes (at least 12 are needed).
func Sniff(head []byte) (Info, error) {
	switch {
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}):
		return Info{KindImage, "image/jpeg", ".jpg"}, nil
	case bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n")):
		return Info{KindImage, "image/png", ".png"}, nil
	case len(head) >= 12 && string(head[0:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return Info{KindImage, "image/webp", ".webp"}, nil
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return Info{KindVideo, "video/webm", ".webm"}, nil
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		brand := string(head[8:12])
		if brand == "qt  " {
			return Info{KindVideo, "video/quicktime", ".mov"}, nil
		}
		if videoBrands[brand] {
			return Info{KindVideo, "video/mp4", ".mp4"}, nil
		}
	}
	return Info{}, ErrUnsupported
}

// Thumbnail decodes a photo and returns a JPEG no larger than ThumbMax on its longest side, plus
// the width and height of the original as a viewer shows it (after any EXIF rotation). The photo
// itself is stored untouched; only the thumbnail is re-encoded.
func Thumbnail(data []byte) (jpegBytes []byte, width, height int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("not a readable photo: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, 0, 0, errors.New("not a readable photo: empty image")
	}
	if cfg.Width*cfg.Height > MaxPixels {
		return nil, 0, 0, fmt.Errorf("photo is too large: %d x %d pixels (limit %d megapixels)", cfg.Width, cfg.Height, MaxPixels/1_000_000)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, fmt.Errorf("not a readable photo: %w", err)
	}
	orientation := exifOrientation(data)

	// Shrink first, then rotate the small image: it is the cheaper order.
	small := shrink(img, ThumbMax)
	oriented := orient(small, orientation)

	w, h := cfg.Width, cfg.Height
	if orientation >= 5 && orientation <= 8 {
		w, h = h, w
	}

	// JPEG has no transparency: put the photo on white.
	flat := image.NewRGBA(oriented.Bounds())
	draw.Draw(flat, flat.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	draw.Draw(flat, flat.Bounds(), oriented, oriented.Bounds().Min, draw.Over)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, flat, &jpeg.Options{Quality: 82}); err != nil {
		return nil, 0, 0, err
	}
	return buf.Bytes(), w, h, nil
}

// shrink scales img down so its longest side is at most max; a smaller image is kept as it is.
func shrink(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, max*h/w
	if h > w {
		nw, nh = max*w/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Over, nil)
	return dst
}

// orient applies an EXIF orientation (1 to 8) so the picture is upright. Orientation 1 and any
// unknown value leave it as it is.
func orient(img image.Image, o int) image.Image {
	if o < 2 || o > 8 {
		return img
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var nx, ny int
			switch o {
			case 2: // mirrored left to right
				nx, ny = w-1-x, y
			case 3: // rotated 180 degrees
				nx, ny = w-1-x, h-1-y
			case 4: // mirrored top to bottom
				nx, ny = x, h-1-y
			case 5: // transposed
				nx, ny = y, x
			case 6: // rotated 90 degrees clockwise
				nx, ny = h-1-y, x
			case 7: // transversed
				nx, ny = h-1-y, w-1-x
			case 8: // rotated 90 degrees counter-clockwise
				nx, ny = y, w-1-x
			}
			dst.Set(nx, ny, img.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return dst
}
