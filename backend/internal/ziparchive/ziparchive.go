// Package ziparchive streams one ZIP of CAD files from the object store to an io.Writer.
//
// It is the reusable core of the "download many CAD files" feature. The caller decides which files go in
// (a selection of designs today; the lines of an order later) and passes them as Entry values; this package
// reads each object, gunzips it (CAD objects are stored gzipped), checks the SHA-256 of the original bytes
// while they pass through, writes them into the ZIP and finishes with a MANIFEST.csv that says what happened
// to every entry.
//
// Memory is bounded: an object is copied through a fixed buffer, one at a time, so neither a whole file nor
// the whole ZIP is ever held (a slow client simply slows the copy down). The context is checked while
// copying, so a client that goes away stops the work within one buffer.
//
// Hook for orders: an order line records, at order time, the file's hash and size. To download an order the
// caller builds one Entry per line and kind with SHA256 and Size taken from that snapshot, Key being the
// storage key of the object that still holds that hash (or "" with Detail set when it is gone), and Meta
// carrying the order columns (order number, quantity, metal, ...) named in Options.MetaColumns. A file
// that changed since is then reported as hash-mismatch instead of silently shipping a different file.
package ziparchive

import (
	"archive/zip"
	"compress/flate"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

// ManifestName is the name of the manifest entry, written last.
const ManifestName = "MANIFEST.csv"

// Status of one entry, as written in the manifest.
const (
	StatusOK           = "ok"
	StatusMissing      = "missing"       // no file recorded, or the object is gone from the store
	StatusHashMismatch = "hash-mismatch" // the bytes read do not match the recorded hash or size
	StatusError        = "error"         // the object could not be read to the end
	StatusUnverified   = "unverified"    // no expected hash was given, so nothing could be checked
)

// Entry is one file to put in the ZIP.
type Entry struct {
	// Name wanted inside the ZIP. It is made safe and unique (see UniqueName): a path, a clash or a
	// reserved name never reaches the archive as given.
	Name string
	// OriginalName is the file name as it was uploaded; it only appears in the manifest.
	OriginalName string
	// Key is the object in the store (gzipped CAD object). Empty means there is no file: the entry is
	// not written and the manifest says missing, with Detail as the reason.
	Key string
	// SHA256 is the expected hash of the ORIGINAL bytes (hex). Empty = unverified.
	SHA256 string
	// Size is the expected number of original bytes; 0 = unknown. It bounds the copy (one byte more than
	// this is never read) and is compared at the end.
	Size int64
	// Meta are the caller's manifest columns, in the order of Options.MetaColumns.
	Meta []string
	// Detail is a reason shown in the manifest for an entry that is not written (Key empty).
	Detail string
}

// Options for Write.
type Options struct {
	// MetaColumns name the leading manifest columns filled from Entry.Meta.
	MetaColumns []string
	// Store holds the objects. Their ORIGINAL bytes are read with storage.OpenOriginal.
	Store storage.Store
	// Comment is written as the ZIP archive comment (shown by `unzip -z`); a warning about problems is
	// appended to it automatically.
	Comment string
}

// Outcome is what happened to one entry.
type Outcome struct {
	Name      string // name inside the ZIP (empty when the entry was not written)
	Status    string
	Detail    string
	SHA256    string // computed from the bytes read ("" when nothing was read)
	Bytes     int64  // original bytes written
	Requested Entry
}

// Result summarises a finished ZIP.
type Result struct {
	Outcomes []Outcome
	Files    int   // entries written to the ZIP (excluding the manifest)
	Bytes    int64 // original bytes written
	Problems int   // entries whose status is not ok (unverified counts as ok)
}

// Write streams the ZIP to w. It returns an error only when the archive itself cannot be completed: the
// context ended or w failed (the client left). Problems with single files are recorded in the manifest and
// in the Result, never returned.
func Write(ctx context.Context, w io.Writer, entries []Entry, o Options) (Result, error) {
	zw := zip.NewWriter(w)
	zw.RegisterCompressor(zip.Deflate, func(out io.Writer) (io.WriteCloser, error) {
		fw := flatePool.Get().(*flate.Writer)
		fw.Reset(out)
		return &pooledFlate{fw: fw}, nil
	})
	buf := bufPool.Get().(*[]byte)
	defer bufPool.Put(buf)

	names := newNamer()
	names.reserve(ManifestName)
	res := Result{Outcomes: make([]Outcome, 0, len(entries))}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		oc := Outcome{Requested: e}
		if e.Key == "" {
			oc.Status, oc.Detail = StatusMissing, e.Detail
			if oc.Detail == "" {
				oc.Detail = "no file"
			}
			res.Problems++
			res.Outcomes = append(res.Outcomes, oc)
			continue
		}
		oc.Name = names.unique(e.Name)
		if err := writeOne(ctx, zw, o.Store, e, &oc, *buf); err != nil {
			return res, err
		}
		if oc.Status != StatusOK && oc.Status != StatusUnverified {
			res.Problems++
		}
		if oc.Name != "" {
			res.Files++
		}
		res.Bytes += oc.Bytes
		res.Outcomes = append(res.Outcomes, oc)
	}

	// The manifest comes last, because only now is every outcome known.
	mw, err := zw.CreateHeader(&zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return res, err
	}
	if err := writeManifest(mw, o.MetaColumns, res.Outcomes); err != nil {
		return res, err
	}
	comment := o.Comment
	if res.Problems > 0 {
		warn := fmt.Sprintf("WARNING: %d file(s) are missing or could not be verified; see %s", res.Problems, ManifestName)
		if comment != "" {
			comment += " | "
		}
		comment += warn
	}
	if comment != "" {
		_ = zw.SetComment(comment)
	}
	if err := ctx.Err(); err != nil {
		return res, err
	}
	return res, zw.Close()
}

// writeOne copies one object into the archive. A returned error means the archive is unusable (the context
// ended or the destination failed); everything else is recorded in oc.
func writeOne(ctx context.Context, zw *zip.Writer, store storage.Store, e Entry, oc *Outcome, buf []byte) error {
	rc, err := storage.OpenOriginal(ctx, store, e.Key)
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		oc.Name = ""
		if storage.IsNotFound(err) {
			oc.Status, oc.Detail = StatusMissing, "the stored object is gone"
		} else {
			oc.Status, oc.Detail = StatusError, "could not open the stored object: "+err.Error()
		}
		return nil
	}
	defer rc.Close()

	dst, err := zw.CreateHeader(&zip.FileHeader{Name: oc.Name, Method: zip.Deflate, Modified: time.Now()})
	if err != nil {
		return err
	}
	h := sha256.New()
	var src io.Reader = &ctxReader{ctx: ctx, r: rc}
	if e.Size > 0 {
		src = io.LimitReader(src, e.Size+1)
	}
	out := &errWriter{w: dst}
	n, copyErr := io.CopyBuffer(io.MultiWriter(out, h), src, buf)
	oc.Bytes = n
	oc.SHA256 = hex.EncodeToString(h.Sum(nil))
	if cerr := ctx.Err(); cerr != nil {
		return cerr
	}
	if copyErr != nil {
		if out.err != nil { // the client went away, not the store
			return out.err
		}
		oc.Status, oc.Detail = StatusError, "reading the stored object failed after "+fmt.Sprint(n)+" bytes: "+copyErr.Error()
		return nil
	}
	switch {
	case e.Size > 0 && n != e.Size:
		oc.Status = StatusHashMismatch
		oc.Detail = fmt.Sprintf("size is %d bytes in the file but %d were recorded", n, e.Size)
	case e.SHA256 == "":
		oc.Status, oc.Detail = StatusUnverified, "no checksum was recorded"
	case !strings.EqualFold(oc.SHA256, e.SHA256):
		oc.Status, oc.Detail = StatusHashMismatch, "the file does not match the checksum recorded for it"
	default:
		oc.Status = StatusOK
	}
	return nil
}

func writeManifest(w io.Writer, metaCols []string, outcomes []Outcome) error {
	cw := csv.NewWriter(w)
	header := append(append([]string{}, metaCols...),
		"file_in_zip", "original_name", "expected_sha256", "actual_sha256", "expected_bytes", "bytes_written", "status", "detail")
	if err := cw.Write(header); err != nil {
		return err
	}
	for _, oc := range outcomes {
		e := oc.Requested
		row := make([]string, 0, len(header))
		for i := range metaCols {
			v := ""
			if i < len(e.Meta) {
				v = e.Meta[i]
			}
			row = append(row, SafeCell(v))
		}
		expBytes := ""
		if e.Size > 0 {
			expBytes = fmt.Sprint(e.Size)
		}
		written := ""
		if oc.Name != "" {
			written = fmt.Sprint(oc.Bytes)
		}
		row = append(row, SafeCell(oc.Name), SafeCell(e.OriginalName), e.SHA256, oc.SHA256, expBytes, written, oc.Status, SafeCell(oc.Detail))
		if err := cw.Write(row); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// SafeCell neutralises spreadsheet formula injection: a cell starting with = + - @ (or tab/CR) would be
// executed by Excel when the CSV is opened. Same rule as the other CSV exports.
func SafeCell(s string) string {
	if s != "" && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		return "'" + s
	}
	return s
}

// ---------- names ----------

const maxNameRunes = 120

var reservedWin = map[string]bool{"CON": true, "PRN": true, "AUX": true, "NUL": true, "COM1": true, "COM2": true,
	"COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true}

// SafeName reduces a wanted name to a plain file name that is safe to extract anywhere: no directories (the
// ZIP is flat), no "..", no control characters, no characters Windows refuses, a bounded length that keeps
// the extension, and never empty or a reserved device name.
func SafeName(name string) string {
	name = strings.ToValidUTF8(name, "_")
	name = strings.Map(func(r rune) rune {
		switch {
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' || r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		case r < 0x20 || r == 0x7f || unicode.Is(unicode.Cf, r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r):
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	name = strings.TrimRight(name, ". ") // Windows drops trailing dots and spaces
	name = strings.TrimLeft(name, ".")   // no hidden files, no "..", no "."
	if name == "" {
		name = "file"
	}
	if r := []rune(name); len(r) > maxNameRunes {
		ext := ""
		if i := strings.LastIndex(name, "."); i > 0 && utf8.RuneCountInString(name[i:]) <= 10 {
			ext = name[i:]
		}
		name = string(r[:maxNameRunes-utf8.RuneCountInString(ext)]) + ext
	}
	stem := name
	if i := strings.Index(name, "."); i > 0 {
		stem = name[:i]
	}
	if reservedWin[strings.ToUpper(stem)] {
		name = "_" + name
	}
	return name
}

// namer hands out unique names. Uniqueness ignores case, because the ZIP is usually extracted on Windows
// or macOS where "a.stl" and "A.STL" are the same file.
type namer struct{ used map[string]bool }

func newNamer() *namer { return &namer{used: map[string]bool{}} }

func (n *namer) reserve(name string) { n.used[strings.ToLower(name)] = true }

// unique returns SafeName(name), or "stem (2).ext", "stem (3).ext", ... when that is already taken.
func (n *namer) unique(name string) string {
	name = SafeName(name)
	ext := ""
	stem := name
	if i := strings.LastIndex(name, "."); i > 0 {
		stem, ext = name[:i], name[i:]
	}
	cand := name
	for k := 2; n.used[strings.ToLower(cand)]; k++ {
		cand = fmt.Sprintf("%s (%d)%s", stem, k, ext)
	}
	n.used[strings.ToLower(cand)] = true
	return cand
}

// UniqueNames applies the same naming rules as Write to a list of wanted names, so a caller can show the
// final names (for example in a preview) before streaming.
func UniqueNames(wanted []string) []string {
	n := newNamer()
	n.reserve(ManifestName)
	out := make([]string, len(wanted))
	for i, w := range wanted {
		out[i] = n.unique(w)
	}
	return out
}

// ---------- plumbing ----------

// ctxReader stops a copy as soon as the context ends.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// errWriter remembers a write failure, to tell a broken destination from a broken source.
type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) Write(p []byte) (int, error) {
	n, err := e.w.Write(p)
	if err != nil {
		e.err = err
	}
	return n, err
}

var bufPool = sync.Pool{New: func() any { b := make([]byte, 256<<10); return &b }}

var flatePool = sync.Pool{New: func() any {
	fw, _ := flate.NewWriter(io.Discard, flate.BestSpeed) // BestSpeed is a valid level
	return fw
}}

// pooledFlate returns its compressor to the pool when closed, so a ZIP of hundreds of files does not
// allocate a fresh ~1 MB compressor for each of them.
type pooledFlate struct{ fw *flate.Writer }

func (p *pooledFlate) Write(b []byte) (int, error) { return p.fw.Write(b) }
func (p *pooledFlate) Close() error {
	err := p.fw.Close()
	p.fw.Reset(io.Discard)
	flatePool.Put(p.fw)
	return err
}

// ErrBusy is returned by Limiter.Acquire when the key already has a running download.
var ErrBusy = errors.New("ziparchive: a download is already running")

// Limiter allows one running download per key (the user). It is per process: with several API instances
// the limit applies per instance, which still bounds the load a single person can cause.
type Limiter struct {
	mu      sync.Mutex
	running map[string]bool
}

func NewLimiter() *Limiter { return &Limiter{running: map[string]bool{}} }

// Acquire takes the slot for key and returns the function that releases it, or ErrBusy.
func (l *Limiter) Acquire(key string) (release func(), err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running[key] {
		return nil, ErrBusy
	}
	l.running[key] = true
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.running, key)
			l.mu.Unlock()
		})
	}, nil
}
