package service

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/ziparchive"
)

// OrderZipMetaColumns are the leading manifest columns of an order download.
var OrderZipMetaColumns = []string{"order", "design", "variation", "metal", "stone_code", "quantity", "kind", "current_file"}

// What the catalog says about a file an order recorded, for the manifest column current_file.
const (
	CurrentSame      = "same"              // the variation still has exactly this file
	CurrentChanged   = "changed"           // the variation has a different file now
	CurrentRemoved   = "removed"           // the variation has no file of this kind now
	CurrentVarGone   = "variation-gone"    // the variation was deleted or purged
	CurrentNoneAtDay = "none-when-ordered" // nothing was recorded for this kind
)

// OrderZipPlan is the list of files an order download holds, worked out from the order's own snapshot.
type OrderZipPlan struct {
	Order       OrderDetail
	Entries     []ziparchive.Entry
	MetaColumns []string
	Kinds       []string
	Package     bool
	Extras      []ziparchive.Extra // small generated files known at planning time (the summary CSV)
	Comment     string
	Files       int   // entries that have a file recorded on the order
	Missing     int   // entries with nothing recorded (they appear in the manifest as missing)
	Changed     int   // files whose variation has a different file now, or is gone: the order's copy is shipped
	Bytes       int64 // sum of the recorded sizes
}

func (s *Service) orderZipKinds(files string) ([]string, error) {
	var in []string
	for _, k := range strings.Split(files, ",") {
		if k = strings.TrimSpace(strings.ToLower(k)); k != "" {
			in = append(in, k)
		}
	}
	if len(in) == 0 {
		return nil, invalid("files is required: stl, 3dm or stl,3dm")
	}
	return cadZipKinds(in)
}

// PlanOrderZip resolves an order download. The files come from the order's snapshot (the object recorded
// when the order was made, checked against the recorded hash and size while it streams), not from the
// variation as it is now; the live variation is only looked at to tell the manifest whether the catalog
// has moved on. files is "stl", "3dm" or "stl,3dm"; with pack the ZIP also gets the job-bag PDF and a
// summary CSV (the PDF is made by the caller at stream time, see OrderPackageBags). ErrTooLarge when the
// recorded files or bytes pass the caps.
func (s *Service) PlanOrderZip(ctx context.Context, orderID int64, files string, pack bool) (*OrderZipPlan, error) {
	if s.Store == nil {
		return nil, ErrStorageDisabled
	}
	kinds, err := s.orderZipKinds(files)
	if err != nil {
		return nil, err
	}
	od, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	if len(od.Lines) == 0 {
		return nil, invalid("%s has no lines", od.Number)
	}
	if pack && len(od.Lines) > MaxJobBags {
		return nil, fmt.Errorf("%w: %d lines is more than the %d job bags that fit in one PDF", ErrTooLarge, len(od.Lines), MaxJobBags)
	}
	internals, err := s.OrderLineInternals(ctx, orderID)
	if err != nil {
		return nil, err
	}
	maxFiles, maxBytes := s.cadZipCaps()
	plan := &OrderZipPlan{Order: od, MetaColumns: OrderZipMetaColumns, Kinds: kinds, Package: pack}

	for _, l := range od.Lines {
		var live *db.CadVariation
		if l.VariationID != nil {
			v, err := s.Q.GetVariation(ctx, *l.VariationID) // not found once deleted or purged
			switch {
			case err == nil:
				if _, derr := s.Q.GetDesignByID(ctx, v.DesignID); derr == nil { // a deleted design hides its variations
					live = &v
				} else if !errors.Is(mapErr(derr), ErrNotFound) {
					return nil, derr
				}
			case errors.Is(mapErr(err), ErrNotFound):
			default:
				return nil, err
			}
		}
		in := internals[l.ID]
		for _, kind := range kinds {
			snapFile, key := l.Snapshot.File3dm, in.File3dmKey
			if kind == "stl" {
				snapFile, key = l.Snapshot.FileStl, in.FileStlKey
			}
			e := ziparchive.Entry{
				Name: l.Snapshot.VariationCode + "." + kind,
				Meta: []string{od.Number, l.Snapshot.DesignCode, l.Snapshot.VariationCode, l.Snapshot.MetalCode,
					l.Snapshot.StoneCode, fmt.Sprint(l.Quantity), kind, ""},
			}
			cur := CurrentSame
			switch {
			case snapFile == nil || key == "":
				cur = CurrentNoneAtDay
				e.Detail = "the order recorded no " + kind + " file for this variation"
				plan.Missing++
			default:
				e.OriginalName, e.Key, e.SHA256, e.Size = snapFile.Name, key, snapFile.SHA256, snapFile.SizeBytes
				plan.Files++
				plan.Bytes += snapFile.SizeBytes
				cur, e.Note = currentFileState(live, kind, snapFile)
				if cur != CurrentSame {
					plan.Changed++
				}
			}
			e.Meta[len(e.Meta)-1] = cur
			plan.Entries = append(plan.Entries, e)
		}
	}
	if plan.Files > maxFiles {
		return nil, fmt.Errorf("%w: %d files is more than the %d that fit in one download", ErrTooLarge, plan.Files, maxFiles)
	}
	if plan.Bytes > maxBytes {
		return nil, fmt.Errorf("%w: %s is more than the %s that fit in one download", ErrTooLarge, megabytes(plan.Bytes), megabytes(maxBytes))
	}

	plan.Comment = fmt.Sprintf("%s: CAD files as recorded when the order was made; MANIFEST.csv lists every file", od.Number)
	if plan.Changed > 0 {
		plan.Comment += fmt.Sprintf(" | NOTE: %d file(s) have been replaced or removed in the catalog since; these are the files the order recorded", plan.Changed)
	}
	if pack {
		sum, err := orderSummaryCSV(od)
		if err != nil {
			return nil, err
		}
		plan.Extras = []ziparchive.Extra{{Name: od.Number + "-summary.csv", Data: sum}}
	}
	return plan, nil
}

// currentFileState compares the file an order recorded with what the variation holds now.
func currentFileState(live *db.CadVariation, kind string, snap *FileSnapshot) (state, note string) {
	if live == nil {
		return CurrentVarGone, "the variation was deleted or purged since; this is the file the order recorded"
	}
	sha, name := live.File3dmSha256, live.File3dmName
	if kind == "stl" {
		sha, name = live.FileStlSha256, live.FileStlName
	}
	switch {
	case !sha.Valid:
		return CurrentRemoved, "the variation has no " + kind + " file now; this is the file the order recorded"
	case !strings.EqualFold(sha.String, snap.SHA256):
		return CurrentChanged, fmt.Sprintf("the variation has a different %s file now (%s); this is the file the order recorded", kind, name.String)
	}
	return CurrentSame, ""
}

// orderSummaryCSV is the one-sheet summary of an order for the package: what to make, how many, and the
// weights and file fingerprints recorded when the order was made. No prices exist on an order.
func orderSummaryCSV(od OrderDetail) ([]byte, error) {
	var b bytes.Buffer
	cw := csv.NewWriter(&b)
	head := []string{"order", "order_name", "status", "line", "design", "design_name", "variation", "metal", "stone_code", "quantity",
		"total_pcs", "net_g", "stone_g", "gross_g", "stone_pct", "stl_file", "stl_sha256", "3dm_file", "3dm_sha256"}
	if err := cw.Write(head); err != nil {
		return nil, err
	}
	file := func(f *FileSnapshot) (string, string) {
		if f == nil {
			return "", ""
		}
		return ziparchive.SafeCell(f.Name), f.SHA256
	}
	var qty int64
	for _, l := range od.Lines {
		sn := l.Snapshot
		stlN, stlH := file(sn.FileStl)
		dmN, dmH := file(sn.File3dm)
		qty += int64(l.Quantity)
		row := []string{od.Number, ziparchive.SafeCell(od.Name), od.Status, fmt.Sprint(l.Position), sn.DesignCode, ziparchive.SafeCell(sn.DesignName),
			sn.VariationCode, sn.MetalCode, sn.StoneCode, fmt.Sprint(l.Quantity), fmt.Sprint(sn.TotalPcs),
			sn.NetG.StringFixed(3), sn.StoneG.StringFixed(3), sn.GrossG.StringFixed(3), sn.StonePct.StringFixed(2), stlN, stlH, dmN, dmH}
		if err := cw.Write(row); err != nil {
			return nil, err
		}
	}
	total := make([]string, len(head))
	total[0], total[3], total[9] = od.Number, "TOTAL", fmt.Sprint(qty)
	if err := cw.Write(total); err != nil {
		return nil, err
	}
	cw.Flush()
	return b.Bytes(), cw.Error()
}

// OrderBag is a job bag built from an order's snapshot, with the key of its cover thumbnail.
type OrderBag struct {
	JobBag
	ThumbKey string
}

// OrderPackageBags builds one job bag per order line (with its cover thumbnail key, for the order package) from the snapshot taken when the order was made, so the
// sheet shows the weights and BOM that were agreed even if the masters changed since, and cannot fail on a
// stone-weight row that was removed later. Photos are not read here (see OrderBagPhoto).
func (s *Service) OrderPackageBags(ctx context.Context, orderID int64) ([]OrderBag, error) {
	od, err := s.GetOrder(ctx, orderID)
	if err != nil {
		return nil, err
	}
	internals, err := s.OrderLineInternals(ctx, orderID)
	if err != nil {
		return nil, err
	}
	bags := make([]OrderBag, 0, len(od.Lines))
	for _, l := range od.Lines {
		sn := l.Snapshot
		jb := JobBag{
			QR: s.jobBagQR(sn.DesignCode, sn.VariationCode), DesignCode: sn.DesignCode, DesignName: sn.DesignName, Category: sn.CategoryName,
			Tags: sn.Tags, VariationCode: sn.VariationCode, BaseInput: snapshotBaseInput(sn.BaseInput), MetalCode: sn.MetalCode,
			NetG: sn.NetG, GrossG: sn.GrossG, GeneratedAt: sn.SnapshotAt,
			BOM: BOMResult{VariationCode: sn.VariationCode, StoneCode: sn.StoneCode, Lines: sn.BOM, TotalPcs: sn.TotalPcs,
				TotalCarats: sn.StoneCt, StoneWeightG: sn.StoneG},
		}
		if sn.File3dm != nil {
			jb.File3dmName, jb.File3dmSHA256 = sn.File3dm.Name, sn.File3dm.SHA256
		}
		if sn.FileStl != nil {
			jb.FileStlName, jb.FileStlSHA256 = sn.FileStl.Name, sn.FileStl.SHA256
		}
		bags = append(bags, OrderBag{JobBag: jb, ThumbKey: internals[l.ID].CoverThumb})
	}
	return bags, nil
}

// OrderBagPhoto reads the cover thumbnail recorded for a line (nil when there is none or it is gone).
func (s *Service) OrderBagPhoto(ctx context.Context, key string) []byte {
	if s.Store == nil || key == "" {
		return nil
	}
	rc, err := s.Store.Get(ctx, key)
	if err != nil {
		return nil
	}
	defer rc.Close()
	b, err := io.ReadAll(io.LimitReader(rc, maxJobBagPhotoBytes+1))
	if err != nil || len(b) == 0 || len(b) > maxJobBagPhotoBytes {
		return nil
	}
	return b
}
