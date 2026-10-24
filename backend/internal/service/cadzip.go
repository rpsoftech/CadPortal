package service

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/ziparchive"
)

const (
	// DefaultCADZipMaxFiles and DefaultCADZipMaxBytes cap one ZIP when the Service does not set its own.
	DefaultCADZipMaxFiles       = 1000
	DefaultCADZipMaxBytes int64 = 5 << 30
	// MaxCADZipIDs bounds the ids in one request (designs plus variations), before anything is looked up.
	MaxCADZipIDs = 2000
)

// CADZipRequest says what to download. A design stands for all its live variations.
type CADZipRequest struct {
	VariationIDs []int64
	DesignIDs    []int64
	Kinds        []string // "stl" and/or "3dm"
}

// CADZipPlan is the list of files a ZIP would hold, worked out from the live data, with the totals the
// caller shows before it starts (and checks against the caps).
type CADZipPlan struct {
	Entries     []ziparchive.Entry
	MetaColumns []string
	Kinds       []string
	// VariationIDs are the live variations the plan covers, in order.
	VariationIDs []int64
	Variations   int
	Files        int   // entries that have a file
	Missing      int   // entries with no file on the variation
	Bytes        int64 // sum of the original sizes of those files
}

// CADZipMetaColumns are the leading manifest columns of a selection download.
var CADZipMetaColumns = []string{"design", "design_name", "variation", "kind"}

func (s *Service) cadZipCaps() (int, int64) {
	files, bytes := s.CADZipMaxFiles, s.CADZipMaxBytes
	if files <= 0 {
		files = DefaultCADZipMaxFiles
	}
	if bytes <= 0 {
		bytes = DefaultCADZipMaxBytes
	}
	return files, bytes
}

// PlanCADZip resolves a request against the live catalog. Deleted variations (and variations of deleted
// designs) are ignored; a variation that lacks a requested kind still gets an entry, which the manifest then
// lists as missing, so the ZIP never silently has fewer files than asked for. It fails with ErrInvalid for
// an empty or malformed request and with ErrTooLarge when the files or bytes pass the caps.
func (s *Service) PlanCADZip(ctx context.Context, req CADZipRequest) (*CADZipPlan, error) {
	if s.Store == nil {
		return nil, ErrStorageDisabled
	}
	kinds, err := cadZipKinds(req.Kinds)
	if err != nil {
		return nil, err
	}
	if len(req.VariationIDs)+len(req.DesignIDs) == 0 {
		return nil, invalid("choose at least one design or variation")
	}
	if len(req.VariationIDs)+len(req.DesignIDs) > MaxCADZipIDs {
		return nil, invalid("at most %d designs and variations can be chosen at once", MaxCADZipIDs)
	}
	maxFiles, maxBytes := s.cadZipCaps()

	var vars []db.CadVariation
	seen := map[int64]bool{}
	add := func(v db.CadVariation) {
		if !seen[v.ID] {
			seen[v.ID] = true
			vars = append(vars, v)
		}
	}
	for _, id := range req.VariationIDs {
		if id <= 0 {
			return nil, invalid("invalid variation id %d", id)
		}
		v, err := s.Q.GetVariation(ctx, id) // a deleted variation is not found, and is ignored
		if err != nil {
			if errors.Is(mapErr(err), ErrNotFound) {
				continue
			}
			return nil, err
		}
		add(v)
	}
	for _, id := range req.DesignIDs {
		if id <= 0 {
			return nil, invalid("invalid design id %d", id)
		}
		vs, err := s.Q.ListVariationsByDesign(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, v := range vs {
			add(v)
		}
	}

	designs := map[int64]db.MasterDesign{}
	plan := &CADZipPlan{MetaColumns: CADZipMetaColumns, Kinds: kinds}
	for _, v := range vars {
		d, ok := designs[v.DesignID]
		if !ok {
			d, err = s.Q.GetDesignByID(ctx, v.DesignID) // a deleted design hides its variations too
			if err != nil {
				if errors.Is(mapErr(err), ErrNotFound) {
					continue
				}
				return nil, err
			}
			designs[v.DesignID] = d
		}
		plan.Variations++
		plan.VariationIDs = append(plan.VariationIDs, v.ID)
		for _, kind := range kinds {
			key, name, sha, size := v.File3dmKey, v.File3dmName, v.File3dmSha256, v.File3dmSizeBytes
			if kind == "stl" {
				key, name, sha, size = v.FileStlKey, v.FileStlName, v.FileStlSha256, v.FileStlSizeBytes
			}
			e := ziparchive.Entry{
				Name:         v.Code + "." + kind,
				OriginalName: name.String,
				Meta:         []string{d.Code, d.Name, v.Code, kind},
			}
			if key.Valid {
				e.Key, e.SHA256, e.Size = key.String, sha.String, size.Int64
				plan.Files++
				plan.Bytes += size.Int64
			} else {
				e.Detail = "no " + kind + " file on this variation"
				plan.Missing++
			}
			plan.Entries = append(plan.Entries, e)
		}
	}
	if plan.Variations == 0 {
		return nil, invalid("none of the chosen designs has a variation to download")
	}
	if plan.Files > maxFiles {
		return nil, fmt.Errorf("%w: %d files is more than the %d that fit in one download; choose fewer designs", ErrTooLarge, plan.Files, maxFiles)
	}
	if plan.Bytes > maxBytes {
		return nil, fmt.Errorf("%w: %s is more than the %s that fit in one download; choose fewer designs", ErrTooLarge, megabytes(plan.Bytes), megabytes(maxBytes))
	}
	return plan, nil
}

func cadZipKinds(in []string) ([]string, error) {
	if len(in) == 0 {
		return nil, invalid("kinds is required: stl, 3dm or both")
	}
	var out []string
	for _, k := range in {
		if k != "stl" && k != "3dm" {
			return nil, invalid("kind must be stl or 3dm, got %q", k)
		}
		if !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	return out, nil
}
