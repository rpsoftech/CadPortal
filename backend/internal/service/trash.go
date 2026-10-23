package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// DefaultRetention is how long a removed or replaced file is kept before it is deleted for good.
const DefaultRetention = 30 * 24 * time.Hour

// retired describes an object that a user's action took out of use.
type retired struct {
	Kind      string // cad or media
	ObjectKey string
	ThumbKey  string
	Label     string
	Reason    string // removed, replaced, variation_deleted
	Restore   any    // what is needed to put it back; nil when it cannot be
}

type cadRestore struct {
	VariationID int64  `json:"variation_id"`
	Kind        string `json:"kind"` // 3dm or stl
	SHA256      string `json:"sha256"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
}

type mediaRestore struct {
	DesignID    int64  `json:"design_id"`
	VariationID *int64 `json:"variation_id"`
	Kind        string `json:"kind"`
	ContentType string `json:"content_type"`
	Filename    string `json:"filename"`
	SizeBytes   int64  `json:"size_bytes"`
	Width       *int32 `json:"width"`
	Height      *int32 `json:"height"`
}

// retire takes objects out of use without destroying them: with a retention period they stay in the
// store, listed in trashed_files, until the purge job deletes them for good, so a wrong click can be
// undone. With no retention (Retention <= 0) they are deleted at once. Neither path can fail the
// user's action, which has already been done: if the record cannot be written, the object is simply
// left where it is (an object nobody lists is harmless; deleting it would be the one irreversible
// mistake).
func (s *Service) retire(ctx context.Context, items ...retired) {
	if s.Store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	for _, it := range items {
		if s.Retention <= 0 {
			_ = s.Store.Delete(ctx, it.ObjectKey)
			if it.ThumbKey != "" {
				_ = s.Store.Delete(ctx, it.ThumbKey)
			}
			continue
		}
		var restore []byte
		if it.Restore != nil {
			restore, _ = json.Marshal(it.Restore)
		}
		_, err := s.Q.InsertTrashedFile(ctx, db.InsertTrashedFileParams{
			Kind: it.Kind, ObjectKey: it.ObjectKey, ThumbKey: pgText(it.ThumbKey), Label: it.Label,
			Restore: restore, Reason: it.Reason, PurgeAfter: pgtype.Timestamptz{Time: time.Now().Add(s.Retention), Valid: true},
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) { // no rows: already recorded
			log.Printf("trash: could not record %s, leaving the object in place: %v", it.ObjectKey, err)
		}
	}
}

// TrashedItem is a removed file as the admin screen shows it.
type TrashedItem struct {
	ID           int64     `json:"id"`
	Kind         string    `json:"kind"`
	Label        string    `json:"label"`
	Reason       string    `json:"reason"`
	DeletedAt    time.Time `json:"deleted_at"`
	PurgeAfter   time.Time `json:"purge_after"`
	Restorable   bool      `json:"restorable"`
	HasThumbnail bool      `json:"has_thumbnail"`
}

// DeletedDesign is a soft-deleted design.
type DeletedDesign struct {
	ID             int64     `json:"id"`
	Code           string    `json:"code"`
	Name           string    `json:"name"`
	CategoryName   string    `json:"category_name"`
	DeletedAt      time.Time `json:"deleted_at"`
	VariationCount int       `json:"variation_count"`
}

// DeletedVariation is a soft-deleted variation. It cannot be restored while its design is deleted.
type DeletedVariation struct {
	ID            int64      `json:"id"`
	Code          string     `json:"code"`
	DesignCode    string     `json:"design_code"`
	DesignName    string     `json:"design_name"`
	DeletedAt     time.Time  `json:"deleted_at"`
	PurgeAfter    *time.Time `json:"purge_after"`
	HasFiles      bool       `json:"has_files"`
	DesignDeleted bool       `json:"design_deleted"`
}

// Trash is everything that can still be brought back.
type Trash struct {
	Designs    []DeletedDesign    `json:"designs"`
	Variations []DeletedVariation `json:"variations"`
	Files      []TrashedItem      `json:"files"`
}

func (s *Service) ListTrash(ctx context.Context) (Trash, error) {
	out := Trash{Designs: []DeletedDesign{}, Variations: []DeletedVariation{}, Files: []TrashedItem{}}
	ds, err := s.Q.ListDeletedDesigns(ctx)
	if err != nil {
		return out, mapErr(err)
	}
	for _, d := range ds {
		out.Designs = append(out.Designs, DeletedDesign{
			ID: d.ID, Code: d.Code, Name: d.Name, CategoryName: d.CategoryName,
			DeletedAt: d.DeletedAt.Time, VariationCount: int(d.VariationCount),
		})
	}
	vs, err := s.Q.ListDeletedVariations(ctx)
	if err != nil {
		return out, mapErr(err)
	}
	for _, v := range vs {
		dv := DeletedVariation{
			ID: v.ID, Code: v.Code, DesignCode: v.DesignCode, DesignName: v.DesignName,
			DeletedAt: v.DeletedAt.Time, HasFiles: v.HasFiles, DesignDeleted: v.DesignDeletedAt.Valid,
		}
		if v.PurgeAfter.Valid {
			t := v.PurgeAfter.Time
			dv.PurgeAfter = &t
		}
		out.Variations = append(out.Variations, dv)
	}
	fs, err := s.Q.ListTrashedFiles(ctx)
	if err != nil {
		return out, mapErr(err)
	}
	for _, f := range fs {
		out.Files = append(out.Files, TrashedItem{
			ID: f.ID, Kind: f.Kind, Label: f.Label, Reason: f.Reason, DeletedAt: f.DeletedAt.Time,
			PurgeAfter: f.PurgeAfter.Time, Restorable: f.Restore != nil, HasThumbnail: f.ThumbKey.Valid,
		})
	}
	return out, nil
}

// RestoreDesign brings a deleted design back, with its variations, seats and files (none of which
// were removed when it was deleted).
func (s *Service) RestoreDesign(ctx context.Context, id int64) (db.MasterDesign, error) {
	d, err := s.Q.GetDeletedDesign(ctx, id)
	if err != nil {
		return db.MasterDesign{}, mapErr(err)
	}
	n, err := s.Q.RestoreDesign(ctx, id)
	if err != nil {
		return db.MasterDesign{}, mapErr(err)
	}
	if n == 0 {
		return db.MasterDesign{}, ErrNotFound
	}
	return d, nil
}

// RestoreFile puts a removed or replaced file back where it was, if the place is free: a CAD file needs
// its variation to have no file of that kind and its checksum to be unused, a photo or video needs
// room (five per design or variation) and its design to be live.
func (s *Service) RestoreFile(ctx context.Context, id int64) (TrashedItem, error) {
	var item TrashedItem
	err := s.inTx(ctx, func(q *db.Queries) error {
		row, err := q.GetTrashedFileForUpdate(ctx, id)
		if err != nil {
			return err
		}
		item = TrashedItem{ID: row.ID, Kind: row.Kind, Label: row.Label}
		if row.Restore == nil {
			return invalid("this file cannot be put back: its variation was deleted. The stored copy is kept until %s, so ask whoever runs the server to recover it", row.PurgeAfter.Time.Format("2 Jan 2006"))
		}
		switch row.Kind {
		case "cad":
			var r cadRestore
			if err := json.Unmarshal(row.Restore, &r); err != nil {
				return err
			}
			cur, err := q.GetVariationForUpdate(ctx, r.VariationID)
			if errors.Is(err, pgx.ErrNoRows) {
				return invalid("its variation is deleted or no longer exists")
			}
			if err != nil {
				return err
			}
			if (r.Kind == "3dm" && cur.File3dmKey.Valid) || (r.Kind == "stl" && cur.FileStlKey.Valid) {
				return fmt.Errorf("%w: %s already has a .%s file; remove that one first", ErrConflict, cur.Code, r.Kind)
			}
			if err := s.restoreCAD(ctx, q, r, row.ObjectKey); err != nil {
				return err
			}
		case "media":
			var r mediaRestore
			if err := json.Unmarshal(row.Restore, &r); err != nil {
				return err
			}
			if err := s.restoreMedia(ctx, q, r, row); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unknown trashed kind %q", row.Kind)
		}
		return q.DeleteTrashedFile(ctx, row.ID)
	})
	return item, err
}

func (s *Service) restoreCAD(ctx context.Context, q *db.Queries, r cadRestore, key string) error {
	if r.Kind == "3dm" {
		if v, err := q.FindVariationCodeBySha3dm(ctx, pgText(r.SHA256)); err == nil {
			return fmt.Errorf("%w: an identical .3dm file is now stored on variation %s", ErrConflict, v.Code)
		}
		return q.SetVariationFile3dm(ctx, db.SetVariationFile3dmParams{
			ID: r.VariationID, File3dmSha256: pgText(r.SHA256), File3dmKey: pgText(key),
			File3dmName: pgText(r.Name), File3dmSizeBytes: pgtype.Int8{Int64: r.Size, Valid: true}})
	}
	if v, err := q.FindVariationCodeByShaStl(ctx, pgText(r.SHA256)); err == nil {
		return fmt.Errorf("%w: an identical .stl file is now stored on variation %s", ErrConflict, v.Code)
	}
	return q.SetVariationFileStl(ctx, db.SetVariationFileStlParams{
		ID: r.VariationID, FileStlSha256: pgText(r.SHA256), FileStlKey: pgText(key),
		FileStlName: pgText(r.Name), FileStlSizeBytes: pgtype.Int8{Int64: r.Size, Valid: true}})
}

func (s *Service) restoreMedia(ctx context.Context, q *db.Queries, r mediaRestore, row db.TrashedFile) error {
	if _, err := q.LockDesignForMedia(ctx, r.DesignID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return invalid("its design is deleted: restore the design first")
		}
		return err
	}
	var variation pgtype.Int8
	if r.VariationID != nil {
		if _, err := q.GetVariation(ctx, *r.VariationID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return invalid("its variation no longer exists")
			}
			return err
		}
		variation = pgtype.Int8{Int64: *r.VariationID, Valid: true}
	}
	n, err := q.CountMedia(ctx, db.CountMediaParams{DesignID: r.DesignID, VariationID: variation})
	if err != nil {
		return err
	}
	if n >= MaxMediaPerTarget {
		return fmt.Errorf("%w: at most %d photos and videos fit there; remove one first", ErrConflict, MaxMediaPerTarget)
	}
	pos, err := q.NextMediaPosition(ctx, db.NextMediaPositionParams{DesignID: r.DesignID, VariationID: variation})
	if err != nil {
		return err
	}
	p := db.InsertMediaParams{
		DesignID: r.DesignID, VariationID: variation, Kind: r.Kind, ContentType: r.ContentType,
		Filename: r.Filename, SizeBytes: r.SizeBytes, ObjectKey: row.ObjectKey, Position: pos,
	}
	if row.ThumbKey.Valid {
		p.ThumbKey = row.ThumbKey
	}
	if r.Width != nil && r.Height != nil {
		p.Width = pgtype.Int4{Int32: *r.Width, Valid: true}
		p.Height = pgtype.Int4{Int32: *r.Height, Valid: true}
	}
	_, err = q.InsertMedia(ctx, p)
	return err
}

// PurgeTrash deletes, for good, the files whose time is up, and reports how many. An object that
// cannot be deleted now keeps its record and is tried again on the next run.
func (s *Service) PurgeTrash(ctx context.Context) (int, error) {
	total, err := s.purgeVariations(ctx)
	if err != nil {
		return total, err
	}
	if s.Store == nil {
		return total, nil
	}
	for {
		n := 0
		err := s.inTx(ctx, func(q *db.Queries) error {
			rows, err := q.ClaimExpiredTrash(ctx)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if err := s.Store.Delete(ctx, r.ObjectKey); err != nil {
					log.Printf("trash: could not delete %s, will retry: %v", r.ObjectKey, err)
					continue
				}
				if r.ThumbKey.Valid {
					_ = s.Store.Delete(ctx, r.ThumbKey.String)
				}
				if err := q.DeleteTrashedFile(ctx, r.ID); err != nil {
					return err
				}
				n++
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

// purgeVariations deletes, for good, the deleted variations whose time is up, with their stored
// objects (rows first, then objects best effort, like every other deletion).
func (s *Service) purgeVariations(ctx context.Context) (int, error) {
	total := 0
	for {
		var keys []string
		n := 0
		err := s.inTx(ctx, func(q *db.Queries) error {
			rows, err := q.ClaimExpiredVariations(ctx)
			if err != nil {
				return err
			}
			for _, v := range rows {
				k, err := purgeVariationRow(ctx, q, v)
				if err != nil {
					return err
				}
				keys = append(keys, k...)
				n++
			}
			return nil
		})
		if err != nil {
			return total, err
		}
		s.deleteObjects(ctx, keys...)
		total += n
		if n == 0 {
			return total, nil
		}
	}
}

// RunTrashPurge deletes expired files once at start and then hourly, until ctx is cancelled.
func RunTrashPurge(ctx context.Context, s *Service) {
	run := func() {
		if n, err := s.PurgeTrash(ctx); err != nil {
			log.Printf("trash purge: %v", err)
		} else if n > 0 {
			log.Printf("trash purge: deleted %d expired file(s)", n)
		}
	}
	run()
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}
