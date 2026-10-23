package export

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

var rectRe = regexp.MustCompile(`(-?[\d.]+) (-?[\d.]+) (-?[\d.]+) (-?[\d.]+) re f\n`)

const streamMark = "\nstream\n"

// pageStreams returns the uncompressed content stream of every page, in document order.
func pageStreams(t *testing.T, pdf []byte) []string {
	t.Helper()
	var out []string
	rest := string(pdf)
	for {
		i := strings.Index(rest, streamMark)
		if i < 0 {
			return out
		}
		rest = rest[i+len(streamMark):]
		j := strings.Index(rest, "endstream")
		if j < 0 {
			t.Fatal("unterminated stream")
		}
		if s := rest[:j]; strings.HasPrefix(s, "0 J") {
			out = append(out, s)
		}
		rest = rest[j:]
	}
}

// scanQR paints the filled squares inside the QR box of one page onto a bitmap, the way a viewer would,
// and decodes it with ZXing (a decoder independent of the encoder that made the code).
func scanQR(t *testing.T, stream string) string {
	t.Helper()
	const pxPerMM = 20.0
	const pageHmm = 297.0
	side := int(qrBoxMM * pxPerMM)
	img := image.NewGray(image.Rect(0, 0, side, side))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	n := 0
	for _, m := range rectRe.FindAllStringSubmatch(stream, -1) {
		f := func(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }
		k := 72 / 25.4
		x, w, h := f(m[1])/k, f(m[3])/k, math.Abs(f(m[4]))/k
		y := pageHmm - f(m[2])/k // fpdf writes the top-left corner and a negative height
		if x < qrX-0.01 || y < qrY-0.01 || x+w > qrX+qrBoxMM+0.01 || y+h > qrY+qrBoxMM+0.01 {
			continue
		}
		n++
		x0, x1 := int(math.Round((x-qrX)*pxPerMM)), int(math.Round((x+w-qrX)*pxPerMM))
		y0, y1 := int(math.Round((y-qrY)*pxPerMM)), int(math.Round((y+h-qrY)*pxPerMM))
		for py := y0; py < y1; py++ {
			for px := x0; px < x1; px++ {
				img.SetGray(px, py, color.Gray{0})
			}
		}
	}
	if n == 0 {
		t.Fatal("no QR squares on the page")
	}
	bmp, err := gozxing.NewBinaryBitmapFromImage(img)
	if err != nil {
		t.Fatal(err)
	}
	res, err := qrcode.NewQRCodeReader().Decode(bmp, map[gozxing.DecodeHintType]interface{}{gozxing.DecodeHintType_PURE_BARCODE: true})
	if err != nil {
		t.Fatalf("QR not readable: %v", err)
	}
	return res.GetText()
}

func TestJobBagQRDecodesToTheVariationReference(t *testing.T) {
	compress = false
	defer func() { compress = true }()

	long := "LR0001-V" + strings.Repeat("9", 40)
	cases := []struct{ name, qr, want string }{
		{"plain code", "", "LR0001-V1"},
		{"plain code explicit", "LR0001-V1", "LR0001-V1"},
		{"link", "https://cad.example.com/designs/LR0001?v=LR0001-V1", "https://cad.example.com/designs/LR0001?v=LR0001-V1"},
		{"long link", "https://cad.example.com/designs/LR0001?v=" + long, "https://cad.example.com/designs/LR0001?v=" + long},
		{"long code", long, long},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			jb := jobBagFixture()
			jb.DesignName = "Ünïcode ✿ Lotus – 蓮花" // names never reach the QR
			jb.QR = c.qr
			if c.want == long {
				jb.VariationCode = long
			}
			pdf, err := RenderJobBagPDF(jb)
			if err != nil {
				t.Fatal(err)
			}
			pages := pageStreams(t, pdf)
			if len(pages) != 1 {
				t.Fatalf("pages = %d", len(pages))
			}
			if got := scanQR(t, pages[0]); got != c.want {
				t.Errorf("decoded %q, want %q", got, c.want)
			}
		})
	}
}

func TestJobBagQRIsBigEnoughAndTheCodeIsPrintedBeneathIt(t *testing.T) {
	compress = false
	defer func() { compress = true }()
	jb := jobBagFixture()
	jb.QR = "https://cad.example.com/designs/LR0001?v=LR0001-V1"
	bc, err := qrMatrix(jb.QR)
	if err != nil {
		t.Fatal(err)
	}
	n := bc.Bounds().Dx()
	if symbol := qrBoxMM * float64(n) / float64(n+2*qrQuiet); symbol < 22 {
		t.Errorf("symbol is %.1f mm, want at least 22", symbol)
	}
	pdf, _ := RenderJobBagPDF(jb)
	if !bytes.Contains(pdf, []byte("(LR0001-V1)Tj")) {
		t.Error("the readable code is not printed")
	}
}

func TestSeveralBagsEachCarryTheirOwnQRAndPageNumbers(t *testing.T) {
	compress = false
	defer func() { compress = true }()
	b := NewJobBagsPDF("test")
	var want []string
	for i := 1; i <= 4; i++ {
		jb := jobBagFixture()
		jb.VariationCode = fmt.Sprintf("LR0001-V%d", i)
		jb.QR = "https://cad.example.com/designs/LR0001?v=" + jb.VariationCode
		want = append(want, jb.QR)
		if err := b.AddBag(jb); err != nil {
			t.Fatal(err)
		}
	}
	b.AddSkippedPage([]service.JobBagFailure{{VariationCode: "LR0003-V1", DesignCode: "LR0003", StoneCode: "2HD"}})
	pdf, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	pages := pageStreams(t, pdf)
	if len(pages) != 5 {
		t.Fatalf("pages = %d, want 5", len(pages))
	}
	for i, w := range want {
		if got := scanQR(t, pages[i]); got != w {
			t.Errorf("page %d decoded %q, want %q", i+1, got, w)
		}
		if !strings.Contains(pages[i], fmt.Sprintf("(Page %d of 5)Tj", i+1)) {
			t.Errorf("page %d lacks its number", i+1)
		}
	}
	if !strings.Contains(pages[4], "NOT PRINTED") || !strings.Contains(pages[4], "LR0003-V1") {
		t.Error("skipped page missing")
	}
}

func TestTwoHundredBagsStayModest(t *testing.T) {
	// A photo of noise does not compress; the same photo on many pages must be stored once.
	img := image.NewRGBA(image.Rect(0, 0, 800, 600))
	for i := range img.Pix {
		img.Pix[i] = uint8(i*7919>>3) ^ uint8(i>>5)
	}
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	photo := jpg.Bytes()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	b := NewJobBagsPDF("200 bags")
	for i := 1; i <= 200; i++ {
		jb := jobBagFixture()
		jb.VariationCode = fmt.Sprintf("LR0001-V%d", i)
		jb.Photo = photo // the caller reads one photo at a time and keeps none
		if err := b.AddBag(jb); err != nil {
			t.Fatal(err)
		}
	}
	pdf, err := b.Bytes()
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	t.Logf("200 bags with a %d KB photo: PDF %d KB, live heap grew %d KB", len(photo)/1024, len(pdf)/1024, (int64(after.HeapAlloc)-int64(before.HeapAlloc))/1024)
	if len(pdf) > 3*len(photo)+200*8*1024 {
		t.Errorf("the photo seems to be stored more than once: PDF is %d KB for a %d KB photo", len(pdf)/1024, len(photo)/1024)
	}
	if n := bytes.Count(pdf, []byte("/DCTDecode")); n != 1 {
		t.Errorf("photo stored %d times", n)
	}
}
