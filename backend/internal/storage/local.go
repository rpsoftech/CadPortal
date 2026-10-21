package storage

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// LocalURLPrefix is where the API serves signed local downloads.
const LocalURLPrefix = "/api/v1/local-files/"

// ErrBadKey is returned for object keys that are not plain relative paths.
var ErrBadKey = errors.New("storage: invalid object key")

// Local stores objects as files under one directory. It is meant for development
// and tests; production uses R2. Every file operation goes through os.Root, so a
// crafted key can never reach outside the directory.
//
// Downloads use signed, expiring URLs (HMAC-SHA256) served by the API, mirroring
// R2's presigned links.
type Local struct {
	root *os.Root
	key  []byte
	now  func() time.Time
}

// NewLocal creates dir if needed and signs download links with a key derived
// from secret (domain-separated, so it never equals the raw secret).
func NewLocal(dir string, secret []byte) (*Local, error) {
	if dir == "" {
		return nil, errors.New("storage: local directory is required")
	}
	if len(secret) < 32 {
		return nil, errors.New("storage: local signing secret must be at least 32 bytes")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte("cadportal/local-files/v1"))
	return &Local{root: root, key: mac.Sum(nil), now: time.Now}, nil
}

// Close releases the directory handle.
func (l *Local) Close() error { return l.root.Close() }

func validKey(key string) error {
	if key == "" || key == "." || !fs.ValidPath(key) || strings.ContainsAny(key, `\:`) {
		return fmt.Errorf("%w: %q", ErrBadKey, key)
	}
	return nil
}

// ctxReader stops a copy as soon as the context is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

// Put streams r into a temporary file and renames it into place, so a failed or
// interrupted upload never leaves a partial object under key.
func (l *Local) Put(ctx context.Context, key string, r io.Reader) (err error) {
	if err := validKey(key); err != nil {
		return err
	}
	dir := path.Dir(key)
	if dir != "." {
		if err := l.root.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	tmp := path.Join(dir, ".tmp-"+hex.EncodeToString(suffix))
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			l.root.Remove(tmp) //nolint:errcheck // best effort
		}
	}()
	if _, err = io.Copy(f, ctxReader{ctx, r}); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return l.root.Rename(tmp, key)
}

// Delete removes an object; a missing object is not an error.
func (l *Local) Delete(_ context.Context, key string) error {
	if err := validKey(key); err != nil {
		return err
	}
	if err := l.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Open returns the stored (gzipped) bytes for key.
func (l *Local) Open(key string) (*os.File, error) {
	if err := validKey(key); err != nil {
		return nil, err
	}
	return l.root.Open(key)
}

func (l *Local) sign(key, filename string, expires int64) string {
	mac := hmac.New(sha256.New, l.key)
	fmt.Fprintf(mac, "%s\n%s\n%d", key, filename, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

// PresignGet returns a relative, signed URL served by the API.
func (l *Local) PresignGet(_ context.Context, key, filename string, ttl time.Duration) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	exp := l.now().Add(ttl).Unix()
	q := url.Values{}
	q.Set("expires", strconv.FormatInt(exp, 10))
	q.Set("filename", filename)
	q.Set("sig", l.sign(key, filename, exp))
	return LocalURLPrefix + (&url.URL{Path: key}).EscapedPath() + "?" + q.Encode(), nil
}

// Verify reports whether the link parameters are authentic and unexpired.
func (l *Local) Verify(key, filename, expires, sig string) bool {
	if validKey(key) != nil {
		return false
	}
	exp, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || l.now().Unix() > exp {
		return false
	}
	got, err := hex.DecodeString(sig)
	if err != nil {
		return false
	}
	want, _ := hex.DecodeString(l.sign(key, filename, exp))
	return hmac.Equal(got, want)
}

// inlinePrefix marks a signed link that shows the object (instead of downloading it). It travels in
// the signed filename parameter as "inline:<content type>". A real file name never looks like that,
// because upload names are reduced to their last path segment and a content type contains a slash.
const inlinePrefix = "inline:"

// PutMedia stores an object as it is. Local files are plain bytes either way; what differs is how
// the API serves them, chosen by the signed link (see PresignView).
func (l *Local) PutMedia(ctx context.Context, key, _ string, r io.Reader) error {
	return l.Put(ctx, key, r)
}

// PresignView returns a signed link that serves the object inline with contentType.
func (l *Local) PresignView(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	return l.PresignGet(ctx, key, inlinePrefix+contentType, ttl)
}

// InlineType reports whether a verified link is an inline view, and its content type.
func InlineType(filename string) (string, bool) {
	return strings.CutPrefix(filename, inlinePrefix)
}
