package service

import (
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// ErrStorageDisabled is returned when no object store is configured.
var ErrStorageDisabled = errors.New("file storage is not configured")

const DefaultPresignTTL = 10 * time.Minute

// UploadResult describes a stored CAD file.
type UploadResult struct {
	VariationCode string `json:"variation_code"`
	Kind          string `json:"kind"`
	Filename      string `json:"filename"`
	SHA256        string `json:"sha256"`
	SizeBytes     int64  `json:"size_bytes"`
}

// KindFromFilename maps .3dm / .stl (case-insensitive) to "3dm" / "stl".
func KindFromFilename(name string) (string, bool) {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".3dm":
		return "3dm", true
	case ".stl":
		return "stl", true
	}
	return "", false
}

func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// UploadCAD streams r through SHA-256 (of the original bytes) and gzip into
// the object store, never holding the whole file in memory. A file whose hash
// already exists on any variation is rejected with ErrConflict naming that
// variation; the freshly stored object is removed. Re-uploading a kind
// replaces the previous object.
func (s *Service) UploadCAD(ctx context.Context, variationID int64, filename string, r io.Reader) (UploadResult, error) {
	if s.Store == nil {
		return UploadResult{}, ErrStorageDisabled
	}
	filename = cleanFilename(filename)
	kind, ok := KindFromFilename(filename)
	if !ok {
		return UploadResult{}, invalid("file must have a .3dm or .stl extension")
	}
	v, err := s.Q.GetVariation(ctx, variationID)
	if err != nil {
		return UploadResult{}, mapErr(err)
	}
	design, err := s.Q.GetDesignByID(ctx, v.DesignID)
	if err != nil {
		return UploadResult{}, mapErr(err)
	}
	id, err := randomID()
	if err != nil {
		return UploadResult{}, err
	}
	key := fmt.Sprintf("cad/%s/%s/%s.%s.gz", design.Code, v.Code, id, kind)

	// Hash and count the original bytes on their way into gzip.
	hasher := sha256.New()
	pr, pw := io.Pipe()
	var size int64
	done := make(chan struct{})
	go func() {
		defer close(done)
		gz := gzip.NewWriter(pw)
		n, err := io.Copy(io.MultiWriter(gz, hasher), r)
		size = n
		if cerr := gz.Close(); err == nil {
			err = cerr
		}
		pw.CloseWithError(err) // nil err closes cleanly (EOF)
	}()
	putErr := s.Store.Put(ctx, key, pr)
	pr.CloseWithError(putErr) // unblocks the copier if Put stopped early
	<-done                    // size and hash are final after this
	if putErr != nil {
		_ = s.Store.Delete(context.WithoutCancel(ctx), key)
		return UploadResult{}, putErr
	}
	sum := hex.EncodeToString(hasher.Sum(nil))
	res := UploadResult{VariationCode: v.Code, Kind: kind, Filename: filename, SHA256: sum, SizeBytes: size}

	cleanup := func() { _ = s.Store.Delete(context.WithoutCancel(ctx), key) }
	// Reject duplicates up front, including a re-upload to the same variation.
	// The unique index below remains the backstop for concurrent uploads.
	if dup := s.duplicateError(ctx, kind, sum, nil); dup != nil {
		cleanup()
		return UploadResult{}, dup
	}
	var old *retired
	err = s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetVariationForUpdate(ctx, variationID)
		if err != nil {
			return err
		}
		old = cadRetired(cur, kind, "replaced")
		if kind == "3dm" {
			return q.SetVariationFile3dm(ctx, db.SetVariationFile3dmParams{
				ID: variationID, File3dmSha256: pgText(sum), File3dmKey: pgText(key),
				File3dmName: pgText(filename), File3dmSizeBytes: pgtype.Int8{Int64: size, Valid: true}})
		}
		return q.SetVariationFileStl(ctx, db.SetVariationFileStlParams{
			ID: variationID, FileStlSha256: pgText(sum), FileStlKey: pgText(key),
			FileStlName: pgText(filename), FileStlSizeBytes: pgtype.Int8{Int64: size, Valid: true}})
	})
	if err != nil {
		cleanup()
		if errors.Is(err, ErrConflict) {
			return UploadResult{}, s.duplicateError(ctx, kind, sum, err)
		}
		return UploadResult{}, err
	}
	if old != nil {
		s.retire(ctx, *old) // the file it replaced stays recoverable for the retention period
	}
	return res, nil
}

// duplicateError names the variation that already holds this hash.
func (s *Service) duplicateError(ctx context.Context, kind, sum string, orig error) error {
	var code string
	var err error
	if kind == "3dm" {
		code, err = s.Q.FindVariationCodeBySha3dm(ctx, pgText(sum))
	} else {
		code, err = s.Q.FindVariationCodeByShaStl(ctx, pgText(sum))
	}
	if err != nil {
		return orig // not found (nil) or lookup failure: fall back to the original error
	}
	return fmt.Errorf("%w: identical %s file already stored on variation %s", ErrConflict, kind, code)
}

type DownloadLink struct {
	URL       string `json:"url"`
	Filename  string `json:"filename"`
	ExpiresIn int    `json:"expires_in_seconds"`
}

// DownloadCAD returns a short-lived presigned URL for one of the variation's files.
func (s *Service) DownloadCAD(ctx context.Context, variationID int64, kind string) (DownloadLink, error) {
	if s.Store == nil {
		return DownloadLink{}, ErrStorageDisabled
	}
	if kind != "3dm" && kind != "stl" {
		return DownloadLink{}, invalid("kind must be 3dm or stl")
	}
	v, err := s.Q.GetVariation(ctx, variationID)
	if err != nil {
		return DownloadLink{}, mapErr(err)
	}
	key, name := v.File3dmKey, v.File3dmName
	if kind == "stl" {
		key, name = v.FileStlKey, v.FileStlName
	}
	if !key.Valid {
		return DownloadLink{}, ErrNotFound
	}
	ttl := s.PresignTTL
	if ttl <= 0 {
		ttl = DefaultPresignTTL
	}
	url, err := s.Store.PresignGet(ctx, key.String, name.String, ttl)
	if err != nil {
		return DownloadLink{}, err
	}
	return DownloadLink{URL: url, Filename: name.String, ExpiresIn: int(ttl.Seconds())}, nil
}

// RemoveCAD detaches one kind of file from a variation and deletes its stored
// object, which frees the checksum so the right file can go on another variation.
// Removing a file that is not there is ErrNotFound.
func (s *Service) RemoveCAD(ctx context.Context, variationID int64, kind string) error {
	if kind != "3dm" && kind != "stl" {
		return invalid("kind must be 3dm or stl")
	}
	var old *retired
	err := s.inTx(ctx, func(q *db.Queries) error {
		cur, err := q.GetVariationForUpdate(ctx, variationID)
		if err != nil {
			return err
		}
		key := cur.File3dmKey
		if kind == "stl" {
			key = cur.FileStlKey
		}
		if !key.Valid {
			return ErrNotFound
		}
		old = cadRetired(cur, kind, "removed")
		if kind == "3dm" {
			return q.ClearVariationFile3dm(ctx, variationID)
		}
		return q.ClearVariationFileStl(ctx, variationID)
	})
	if err != nil {
		return mapErr(err)
	}
	if old != nil {
		s.retire(ctx, *old)
	}
	return nil
}

const maxFilenameRunes = 200

// cleanFilename makes an uploaded file name safe to store and to put in a header: only the base
// name (browsers send that, but a script may send a path with either kind of separator), valid
// UTF-8 (a client in a legacy code page can send Latin-1 bytes, which Postgres refuses), no
// control characters, and a bounded length that keeps the extension.
func cleanFilename(name string) string {
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.ToValidUTF8(name, "_")
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSpace(name)
	if r := []rune(name); len(r) > maxFilenameRunes {
		ext := filepath.Ext(name)
		if len([]rune(ext)) > 10 {
			ext = ""
		}
		name = string(r[:maxFilenameRunes-len([]rune(ext))]) + ext
	}
	return name
}

// cadRetired describes the file of one kind that a variation currently has, as it is about to be
// replaced or removed; nil when the variation has none.
func cadRetired(v db.CadVariation, kind, reason string) *retired {
	key, name, sha, size := v.File3dmKey, v.File3dmName, v.File3dmSha256, v.File3dmSizeBytes
	if kind == "stl" {
		key, name, sha, size = v.FileStlKey, v.FileStlName, v.FileStlSha256, v.FileStlSizeBytes
	}
	if !key.Valid {
		return nil
	}
	return &retired{
		Kind: "cad", ObjectKey: key.String, Reason: reason, Label: v.Code + " · " + name.String,
		Restore: cadRestore{VariationID: v.ID, Kind: kind, SHA256: sha.String, Name: name.String, Size: size.Int64},
	}
}
