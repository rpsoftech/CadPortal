package ziparchive

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func gz(t *testing.T, b []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// put stores b gzipped under key and returns an Entry that expects exactly b.
func put(t *testing.T, m *storage.Memory, key, name string, b []byte) Entry {
	t.Helper()
	m.Objects[key] = gz(t, b)
	return Entry{Name: name, OriginalName: "orig-" + name, Key: key, SHA256: sum(b), Size: int64(len(b)), Meta: []string{"LR0001", "V1"}}
}

func build(t *testing.T, m storage.Store, es []Entry) (Result, *zip.Reader, map[string][]byte, []map[string]string) {
	t.Helper()
	var out bytes.Buffer
	res, err := Write(context.Background(), &out, es, Options{Store: m, MetaColumns: []string{"design", "variation"}})
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatalf("not a valid zip: %v", err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, err := io.ReadAll(rc) // also verifies each entry's CRC-32
		rc.Close()
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		files[f.Name] = b
	}
	rows, err := csv.NewReader(bytes.NewReader(files[ManifestName])).ReadAll()
	if err != nil {
		t.Fatalf("manifest: %v", err)
	}
	var recs []map[string]string
	for _, r := range rows[1:] {
		m := map[string]string{}
		for i, h := range rows[0] {
			m[h] = r[i]
		}
		recs = append(recs, m)
	}
	return res, zr, files, recs
}

func TestContentsHashesAndManifest(t *testing.T) {
	m := storage.NewMemory()
	a := bytes.Repeat([]byte("solid a\n"), 1000)
	b := []byte{0x00, 0x01, 0xff, 0x1f, 0x8b, 0x08} // starts with the gzip magic: must come through untouched
	es := []Entry{put(t, m, "k/a", "LR0001-V1.stl", a), put(t, m, "k/b", "LR0001-V2.stl", b)}
	res, zr, files, recs := build(t, m, es)

	if !bytes.Equal(files["LR0001-V1.stl"], a) || !bytes.Equal(files["LR0001-V2.stl"], b) {
		t.Fatal("zip entries are not the original bytes")
	}
	if len(zr.File) != 3 || zr.File[2].Name != ManifestName {
		t.Errorf("entries = %d, manifest not last", len(zr.File))
	}
	if res.Files != 2 || res.Problems != 0 || res.Bytes != int64(len(a)+len(b)) {
		t.Errorf("result = %+v", res)
	}
	if len(recs) != 2 {
		t.Fatalf("manifest rows = %d", len(recs))
	}
	r := recs[0]
	if r["design"] != "LR0001" || r["variation"] != "V1" || r["file_in_zip"] != "LR0001-V1.stl" ||
		r["original_name"] != "orig-LR0001-V1.stl" || r["expected_sha256"] != sum(a) || r["actual_sha256"] != sum(a) ||
		r["bytes_written"] != "8000" || r["status"] != StatusOK {
		t.Errorf("manifest row = %v", r)
	}
	if zr.Comment != "" {
		t.Errorf("comment = %q on a clean zip", zr.Comment)
	}
}

func TestNameClashesAndHostileNames(t *testing.T) {
	m := storage.NewMemory()
	var es []Entry
	for i, n := range []string{"LR0001-V1.stl", "LR0001-V1.stl", "lr0001-v1.STL", "../../etc/passwd", `..\..\win.ini`, "MANIFEST.csv", "manifest.CSV", "CON.stl", "", "a:b*c?.stl", "/abs/path.stl"} {
		es = append(es, put(t, m, "k/"+string(rune('a'+i)), n, []byte{byte(i), 1, 2}))
	}
	_, zr, _, _ := build(t, m, es)
	seen := map[string]bool{}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
		low := strings.ToLower(f.Name)
		if seen[low] {
			t.Errorf("duplicate entry name %q (case-insensitive)", f.Name)
		}
		seen[low] = true
		if strings.ContainsAny(f.Name, `/\:*?`) || strings.HasPrefix(f.Name, ".") {
			t.Errorf("unsafe entry name %q", f.Name)
		}
	}
	want := []string{"LR0001-V1.stl", "LR0001-V1 (2).stl", "lr0001-v1 (3).STL"}
	for i, w := range want {
		if names[i] != w {
			t.Errorf("name %d = %q, want %q (all: %q)", i, names[i], w, names)
		}
	}
	// The data manifest keeps the one genuine MANIFEST.csv.
	if names[len(names)-1] != ManifestName {
		t.Errorf("last entry = %q", names[len(names)-1])
	}
}

func TestSafeName(t *testing.T) {
	long := strings.Repeat("é", 300) + ".stl"
	cases := map[string]string{
		"a/b/c.stl": "a_b_c.stl", `..`: "file", ".hidden": "hidden", "  x.stl  ": "x.stl", "x.stl...": "x.stl",
		"con": "_con", "NUL.txt": "_NUL.txt", "tab\there.stl": "tabhere.stl", "": "file", "bad\xffutf.stl": "bad_utf.stl",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := SafeName(long); len([]rune(got)) != maxNameRunes || !strings.HasSuffix(got, ".stl") {
		t.Errorf("long name = %d runes %q", len([]rune(got)), got[len(got)-8:])
	}
}

func TestMissingMismatchAndUnreadable(t *testing.T) {
	m := storage.NewMemory()
	good := put(t, m, "k/good", "good.stl", []byte("good"))
	changed := put(t, m, "k/changed", "changed.stl", []byte("now different"))
	changed.SHA256 = sum([]byte("what the order recorded"))
	wrongSize := put(t, m, "k/size", "size.stl", []byte("12345"))
	wrongSize.Size = 3
	gone := Entry{Name: "gone.stl", Key: "k/gone", SHA256: sum([]byte("x")), Size: 1}
	none := Entry{Name: "none.stl", Detail: "no stl file on this variation"}
	m.Objects["k/notgzip"] = []byte("plain")
	broken := Entry{Name: "broken.stl", Key: "k/notgzip", SHA256: sum([]byte("x"))}
	unverified := put(t, m, "k/unv", "unv.stl", []byte("unv"))
	unverified.SHA256 = ""

	var out bytes.Buffer
	res, err := Write(context.Background(), &out, []Entry{good, changed, wrongSize, gone, none, broken, unverified}, Options{Store: m, Comment: "Selection"})
	if err != nil {
		t.Fatal(err)
	}
	status := map[string]string{}
	for _, oc := range res.Outcomes {
		status[oc.Requested.Name] = oc.Status
	}
	want := map[string]string{"good.stl": StatusOK, "changed.stl": StatusHashMismatch, "size.stl": StatusHashMismatch,
		"gone.stl": StatusMissing, "none.stl": StatusMissing, "broken.stl": StatusError, "unv.stl": StatusUnverified}
	for k, w := range want {
		if status[k] != w {
			t.Errorf("%s: status %q, want %q", k, status[k], w)
		}
	}
	if res.Problems != 5 {
		t.Errorf("problems = %d, want 5", res.Problems)
	}
	zr, _ := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	// Missing and unreadable entries are not in the zip; changed ones are, but flagged in the manifest.
	if got := strings.Join(names, ","); got != "good.stl,changed.stl,size.stl,unv.stl,MANIFEST.csv" {
		t.Errorf("entries = %s", got)
	}
	if !strings.Contains(zr.Comment, "Selection") || !strings.Contains(zr.Comment, "5 file(s)") {
		t.Errorf("zip comment = %q", zr.Comment)
	}
}

func TestManifestIsSafeAgainstFormulas(t *testing.T) {
	m := storage.NewMemory()
	e := put(t, m, "k/a", "=cmd.stl", []byte("x"))
	e.OriginalName = `=HYPERLINK("http://evil")`
	e.Meta = []string{"+SUM(A1)", "@x"}
	_, _, _, recs := build(t, m, []Entry{e})
	r := recs[0]
	for _, k := range []string{"design", "variation", "original_name"} {
		if !strings.HasPrefix(r[k], "'") {
			t.Errorf("%s = %q is not neutralised", k, r[k])
		}
	}
	if strings.HasPrefix(r["file_in_zip"], "=") {
		t.Errorf("file_in_zip = %q", r["file_in_zip"])
	}
}

func TestContextCancelStopsTheZip(t *testing.T) {
	m := storage.NewMemory()
	var es []Entry
	for i := 0; i < 5; i++ {
		data := make([]byte, 1<<20) // not compressible, so the output grows with the input
		(&patternReader{left: 1 << 20, x: uint64(i + 1)}).Read(data)
		es = append(es, put(t, m, "k/"+string(rune('a'+i)), "f"+string(rune('a'+i))+".stl", data))
	}
	ctx, cancel := context.WithCancel(context.Background())
	w := &cancelAfter{limit: 100 << 10, cancel: cancel}
	_, err := Write(ctx, w, es, Options{Store: m})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if w.n > 3<<20 {
		t.Errorf("kept writing after cancel: %d bytes", w.n)
	}
}

type cancelAfter struct {
	n      int64
	limit  int64
	cancel func()
}

func (c *cancelAfter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	if c.n > c.limit {
		c.cancel()
	}
	return len(p), nil
}

func TestClientGoneStopsTheZip(t *testing.T) {
	m := storage.NewMemory()
	data := make([]byte, 4<<20)
	(&patternReader{left: 4 << 20, x: 7}).Read(data)
	es := []Entry{put(t, m, "k/a", "a.stl", data)}
	_, err := Write(context.Background(), &failAfter{limit: 10 << 10}, es, Options{Store: m})
	if err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("err = %v, want the write error", err)
	}
}

type failAfter struct{ n, limit int64 }

func (f *failAfter) Write(p []byte) (int, error) {
	f.n += int64(len(p))
	if f.n > f.limit {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

func TestLimiterOnePerKey(t *testing.T) {
	l := NewLimiter()
	rel, err := l.Acquire("u1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Acquire("u1"); !errors.Is(err, ErrBusy) {
		t.Errorf("second acquire = %v, want ErrBusy", err)
	}
	if rel2, err := l.Acquire("u2"); err != nil {
		t.Errorf("another user was refused: %v", err)
	} else {
		rel2()
	}
	rel()
	rel() // releasing twice is harmless
	rel3, err := l.Acquire("u1")
	if err != nil {
		t.Errorf("after release: %v", err)
	}
	rel3()
}

// genStore serves gzipped pseudo-random data of a fixed size without ever holding it: the object is made on
// the fly through a pipe. It stands for a store of very large CAD files.
type genStore struct {
	storage.Memory
	size int64
}

func (g *genStore) Get(_ context.Context, key string) (io.ReadCloser, error) {
	pr, pw := io.Pipe()
	go func() {
		zw, _ := gzip.NewWriterLevel(pw, gzip.BestSpeed)
		_, err := io.Copy(zw, &patternReader{left: g.size})
		if cerr := zw.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err)
	}()
	return pr, nil
}

type patternReader struct {
	left int64
	x    uint64
}

func (p *patternReader) Read(b []byte) (int, error) {
	if p.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(b)) > p.left {
		b = b[:p.left]
	}
	for i := range b { // xorshift: not compressible to nothing, not random enough to be slow
		p.x ^= p.x << 13
		p.x ^= p.x >> 7
		p.x ^= p.x << 17
		if p.x == 0 {
			p.x = 88172645463325252
		}
		b[i] = byte(p.x >> 24)
	}
	p.left -= int64(len(b))
	return len(b), nil
}

type countWriter struct{ n atomic.Int64 }

func (c *countWriter) Write(p []byte) (int, error) { c.n.Add(int64(len(p))); return len(p), nil }

// Memory stays flat although the files are large: three 120 MiB files go through, and the heap in use never
// grows by anything close to a file.
func TestLargeFilesStayInBoundedMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("large-file memory test")
	}
	const size = 120 << 20
	g := &genStore{size: size}
	es := []Entry{{Name: "a.stl", Key: "a"}, {Name: "b.3dm", Key: "b"}, {Name: "c.stl", Key: "c"}}
	for i := range es {
		es[i].Size = size
		// the expected hash of the generated data
		h := sha256.New()
		io.Copy(h, &patternReader{left: size})
		es[i].SHA256 = hex.EncodeToString(h.Sum(nil))
	}

	runtime.GC()
	var base runtime.MemStats
	runtime.ReadMemStats(&base)
	var peak atomic.Uint64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		var ms runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(15 * time.Millisecond):
				runtime.ReadMemStats(&ms)
				if ms.HeapInuse > peak.Load() {
					peak.Store(ms.HeapInuse)
				}
			}
		}
	}()

	cw := &countWriter{}
	res, err := Write(context.Background(), cw, es, Options{Store: g})
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	if res.Problems != 0 || res.Files != 3 || res.Bytes != 3*size {
		t.Fatalf("result = files %d bytes %d problems %d", res.Files, res.Bytes, res.Problems)
	}
	growth := int64(peak.Load()) - int64(base.HeapInuse)
	t.Logf("zip of %d MiB written (%d MiB out); heap growth at peak: %d MiB", res.Bytes>>20, cw.n.Load()>>20, growth>>20)
	if growth > 48<<20 {
		t.Errorf("heap grew by %d MiB for 120 MiB files: the files are being buffered", growth>>20)
	}
}
