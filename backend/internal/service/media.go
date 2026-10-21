package service

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/media"
)

const (
	// MaxMediaPerTarget is how many photos and videos together a design, or one of its variations,
	// can hold.
	MaxMediaPerTarget = 5
	MaxImageBytes     = 20 << 20
	MaxVideoBytes     = 300 << 20
	// MediaLinkTTL is how long a signed viewing link stays valid. The page asks again when it
	// reloads, so this only has to outlast a session of looking at one screen.
	MediaLinkTTL = time.Hour
)

// ErrTooLarge is returned when a file is over its size limit.
var ErrTooLarge = errors.New("file too large")

// thumbSem bounds how many photos are decoded at once: a large photo expands to tens of megabytes
// of pixels, so a burst of uploads must queue instead of competing for memory.
var thumbSem = make(chan struct{}, 2)

// MediaItem is one photo or video as the API shows it.
type MediaItem struct {
	ID          int64  `json:"id"`
	DesignID    int64  `json:"design_id"`
	VariationID *int64 `json:"variation_id"` // null: the media belongs to the whole design
	Kind        string `json:"kind"`         // image or video
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       *int   `json:"width"`
	Height      *int   `json:"height"`
	Position    int    `json:"position"`
	IsCover     bool   `json:"is_cover"`
	// URL shows the original in the page and expires (see MediaLinkTTL); ThumbURL is the small
	// JPEG of a photo.
	URL      string `json:"url"`
	ThumbURL string `json:"thumb_url,omitempty"`
}

func (s *Service) mediaItem(ctx context.Context, m db.DesignMedium) (MediaItem, error) {
	it := MediaItem{
		ID: m.ID, DesignID: m.DesignID, Kind: m.Kind, ContentType: m.ContentType, Filename: m.Filename,
		SizeBytes: m.SizeBytes, Position: int(m.Position), IsCover: m.IsCover,
	}
	if m.VariationID.Valid {
		v := m.VariationID.Int64
		it.VariationID = &v
	}
	if m.Width.Valid {
		w := int(m.Width.Int32)
		it.Width = &w
	}
	if m.Height.Valid {
		h := int(m.Height.Int32)
		it.Height = &h
	}
	if s.Store == nil {
		return it, nil
	}
	var err error
	if it.URL, err = s.Store.PresignView(ctx, m.ObjectKey, m.ContentType, MediaLinkTTL); err != nil {
		return it, err
	}
	if m.ThumbKey.Valid {
		if it.ThumbURL, err = s.Store.PresignView(ctx, m.ThumbKey.String, "image/jpeg", MediaLinkTTL); err != nil {
			return it, err
		}
	}
	return it, nil
}

// ListMedia returns a design's photos and videos, the design's own first and then each
// variation's, each group in the order it was arranged.
func (s *Service) ListMedia(ctx context.Context, designID int64) ([]MediaItem, error) {
	rows, err := s.Q.ListMediaByDesign(ctx, designID)
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]MediaItem, 0, len(rows))
	for _, r := range rows {
		it, err := s.mediaItem(ctx, r)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, nil
}

// AddMediaInput describes one upload. VariationID nil attaches the file to the whole design.
type AddMediaInput struct {
	DesignID    int64
	VariationID *int64
	Filename    string
	Body        io.Reader
	UserID      int64 // who uploaded it; 0 if unknown
}

func nullInt8(p *int64) pgtype.Int8 {
	if p == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *p, Valid: true}
}

// AddMedia validates a photo or video by its content, stores it (and a thumbnail for a photo) and
// records it. At most MaxMediaPerTarget items fit on a design or a variation; that is checked
// before the upload starts, so nobody sends 300 MB to be told no, and again under a lock.
func (s *Service) AddMedia(ctx context.Context, in AddMediaInput) (MediaItem, error) {
	if s.Store == nil {
		return MediaItem{}, ErrStorageDisabled
	}
	design, err := s.Q.GetDesignByID(ctx, in.DesignID)
	if err != nil {
		return MediaItem{}, mapErr(err)
	}
	if in.VariationID != nil {
		v, err := s.Q.GetVariation(ctx, *in.VariationID)
		if err != nil {
			return MediaItem{}, mapErr(err)
		}
		if v.DesignID != design.ID {
			return MediaItem{}, invalid("that variation does not belong to this design")
		}
	}
	full := func(n int32) error {
		if n >= MaxMediaPerTarget {
			return fmt.Errorf("%w: at most %d photos and videos fit here; remove one first", ErrConflict, MaxMediaPerTarget)
		}
		return nil
	}
	n, err := s.Q.CountMedia(ctx, db.CountMediaParams{DesignID: design.ID, VariationID: nullInt8(in.VariationID)})
	if err != nil {
		return MediaItem{}, mapErr(err)
	}
	if err := full(n); err != nil {
		return MediaItem{}, err
	}

	br := bufio.NewReaderSize(in.Body, 64<<10)
	head, _ := br.Peek(16)
	info, err := media.Sniff(head)
	if err != nil {
		return MediaItem{}, invalid("%v", err)
	}
	id, err := randomID()
	if err != nil {
		return MediaItem{}, err
	}
	base := fmt.Sprintf("media/%s/%s", design.Code, id)
	key := base + info.Ext
	var thumbKey string
	var width, height int
	var size int64
	var stored []string // objects written so far, removed again if anything later fails
	cleanup := func() {
		for _, k := range stored {
			_ = s.Store.Delete(context.WithoutCancel(ctx), k)
		}
	}

	if info.Kind == media.KindImage {
		data, err := io.ReadAll(io.LimitReader(br, imageLimit+1))
		if err != nil {
			return MediaItem{}, err
		}
		if int64(len(data)) > imageLimit {
			return MediaItem{}, fmt.Errorf("%w: a photo can be at most %s", ErrTooLarge, megabytes(imageLimit))
		}
		select {
		case thumbSem <- struct{}{}:
		case <-ctx.Done():
			return MediaItem{}, ctx.Err()
		}
		thumb, w, h, terr := media.Thumbnail(data)
		<-thumbSem
		if terr != nil {
			return MediaItem{}, invalid("%v", terr)
		}
		width, height, size = w, h, int64(len(data))
		thumbKey = base + ".thumb.jpg"
		if err := s.Store.PutMedia(ctx, key, info.ContentType, bytes.NewReader(data)); err != nil {
			return MediaItem{}, err
		}
		stored = append(stored, key)
		if err := s.Store.PutMedia(ctx, thumbKey, "image/jpeg", bytes.NewReader(thumb)); err != nil {
			cleanup()
			return MediaItem{}, err
		}
		stored = append(stored, thumbKey)
	} else {
		cr := &countingLimitReader{r: br, max: videoLimit}
		if err := s.Store.PutMedia(ctx, key, info.ContentType, cr); err != nil {
			_ = s.Store.Delete(context.WithoutCancel(ctx), key)
			if errors.Is(err, errOverLimit) || errors.Is(cr.err, errOverLimit) {
				return MediaItem{}, fmt.Errorf("%w: a video can be at most %s", ErrTooLarge, megabytes(videoLimit))
			}
			return MediaItem{}, err
		}
		stored = append(stored, key)
		size = cr.n
		if size == 0 {
			cleanup()
			return MediaItem{}, invalid("the file is empty")
		}
	}

	var row db.DesignMedium
	err = s.inTx(ctx, func(q *db.Queries) error {
		if _, err := q.LockDesignForMedia(ctx, design.ID); err != nil {
			return err
		}
		n, err := q.CountMedia(ctx, db.CountMediaParams{DesignID: design.ID, VariationID: nullInt8(in.VariationID)})
		if err != nil {
			return err
		}
		if err := full(n); err != nil {
			return err
		}
		pos, err := q.NextMediaPosition(ctx, db.NextMediaPositionParams{DesignID: design.ID, VariationID: nullInt8(in.VariationID)})
		if err != nil {
			return err
		}
		p := db.InsertMediaParams{
			DesignID: design.ID, VariationID: nullInt8(in.VariationID), Kind: info.Kind,
			ContentType: info.ContentType, Filename: cleanFilename(in.Filename), SizeBytes: size,
			ObjectKey: key, Position: pos,
		}
		if in.UserID > 0 {
			p.CreatedBy = pgtype.Int8{Int64: in.UserID, Valid: true}
		}
		if thumbKey != "" {
			p.ThumbKey = pgtype.Text{String: thumbKey, Valid: true}
			p.Width = pgtype.Int4{Int32: int32(width), Valid: true}
			p.Height = pgtype.Int4{Int32: int32(height), Valid: true}
		}
		row, err = q.InsertMedia(ctx, p)
		return err
	})
	if err != nil {
		cleanup()
		return MediaItem{}, err
	}
	return s.mediaItem(ctx, row)
}

var errOverLimit = errors.New("over the size limit")

// countingLimitReader counts the bytes passing through and fails once more than max have.
type countingLimitReader struct {
	r   io.Reader
	max int64
	n   int64
	err error
}

func (c *countingLimitReader) Read(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.r.Read(p)
	c.n += int64(n)
	if c.n > c.max {
		c.err = errOverLimit
		return n, c.err
	}
	return n, err
}

// DeleteMedia removes one photo or video and its stored objects.
func (s *Service) DeleteMedia(ctx context.Context, id int64) (db.DesignMedium, error) {
	row, err := s.Q.DeleteMedia(ctx, id)
	if err != nil {
		return db.DesignMedium{}, mapErr(err)
	}
	s.deleteMediaObjects(ctx, row)
	return row, nil
}

func (s *Service) deleteMediaObjects(ctx context.Context, rows ...db.DesignMedium) {
	if s.Store == nil {
		return
	}
	for _, r := range rows {
		_ = s.Store.Delete(context.WithoutCancel(ctx), r.ObjectKey) // best effort; an orphan is harmless
		if r.ThumbKey.Valid {
			_ = s.Store.Delete(context.WithoutCancel(ctx), r.ThumbKey.String)
		}
	}
}

// SetCover makes a design-level photo the picture on the design's catalog card.
func (s *Service) SetCover(ctx context.Context, id int64) (db.DesignMedium, error) {
	var row db.DesignMedium
	err := s.inTx(ctx, func(q *db.Queries) error {
		var err error
		if row, err = q.GetMedia(ctx, id); err != nil {
			return err
		}
		if row.Kind != media.KindImage || row.VariationID.Valid {
			return invalid("only a photo of the whole design can be the cover")
		}
		if err := q.ClearCover(ctx, row.DesignID); err != nil {
			return err
		}
		return q.SetCover(ctx, id)
	})
	row.IsCover = err == nil
	return row, err
}

// The limits as variables, so tests can lower them instead of uploading hundreds of megabytes.
var (
	imageLimit int64 = MaxImageBytes
	videoLimit int64 = MaxVideoBytes
)

func megabytes(n int64) string {
	if n >= 1<<20 {
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d KB", n>>10)
}
