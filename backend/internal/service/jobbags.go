package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/rpsoftech/cad-portal/backend/internal/calc"
)

// MaxJobBags is the most job bags one PDF may hold.
const MaxJobBags = 200

// jobBagQR is what the QR code on a job bag holds. With a public address it is a link to the design
// page with the variation as a hint; without one it is the plain variation code. Only codes go in,
// never names, weights or prices.
func (s *Service) jobBagQR(designCode, variationCode string) string {
	if s.AppBaseURL == "" {
		return variationCode
	}
	return s.AppBaseURL + "/designs/" + url.PathEscape(designCode) + "?v=" + url.QueryEscape(variationCode)
}

// JobBagPhoto reads the thumbnail for one planned bag (nil when there is none).
func (s *Service) JobBagPhoto(ctx context.Context, jb JobBag) []byte {
	return s.jobBagPhoto(ctx, jb.DesignID, jb.VariationID)
}

// JobBagItem is one bag asked for; empty metal or stone code fall back to the request defaults.
type JobBagItem struct {
	VariationID int64
	MetalCode   string
	StoneCode   string
}

type JobBagsRequest struct {
	Items          []JobBagItem
	DesignIDs      []int64 // every non-deleted variation of these designs, after the items
	MetalCode      string  // defaults
	StoneCode      string
	SkipIncomplete bool
}

// JobBagFailure is a bag that cannot be built because data is missing.
type JobBagFailure struct {
	VariationID   int64              `json:"variation_id"`
	DesignCode    string             `json:"design_code"`
	VariationCode string             `json:"variation_code"`
	StoneCode     string             `json:"stone_code"`
	Missing       []calc.MissingPair `json:"missing"`
}

func (f JobBagFailure) String() string {
	ps := make([]string, len(f.Missing))
	for i, p := range f.Missing {
		ps[i] = fmt.Sprintf("(%s mm, %s)", p.SizeMM, p.Code)
	}
	return fmt.Sprintf("%s: no stone-weight row for %s", f.VariationCode, strings.Join(ps, ", "))
}

// JobBagsError lists every bag that could not be built, so nobody fixes them one at a time.
type JobBagsError struct{ Failures []JobBagFailure }

func (e *JobBagsError) Error() string {
	parts := make([]string, len(e.Failures))
	for i, f := range e.Failures {
		parts[i] = f.String()
	}
	return fmt.Sprintf("%d job bag(s) cannot be built: %s", len(e.Failures), strings.Join(parts, "; "))
}

// PlanJobBags resolves a request into the bags to print, without photos, in request order. Bags that
// miss stone-weight rows are reported together: as a *JobBagsError when none may be skipped, otherwise
// they come back as skipped. An unknown variation, metal or stone code fails the whole request.
func (s *Service) PlanJobBags(ctx context.Context, req JobBagsRequest) (bags []JobBag, skipped []JobBagFailure, err error) {
	type want struct {
		id           int64
		metal, stone string
	}
	var wants []want
	seen := map[int64]bool{}
	for i, it := range req.Items {
		w := want{it.VariationID, it.MetalCode, it.StoneCode}
		if w.metal == "" {
			w.metal = req.MetalCode
		}
		if w.stone == "" {
			w.stone = req.StoneCode
		}
		if w.id <= 0 || w.metal == "" || w.stone == "" {
			return nil, nil, invalid("item %d: variation_id, metal_code and stone_code are required (a shared metal_code and stone_code may be given for all)", i+1)
		}
		seen[w.id] = true
		wants = append(wants, w)
		if len(wants) > MaxJobBags {
			return nil, nil, invalid("at most %d job bags can be printed at once", MaxJobBags)
		}
	}
	doneDesign := map[int64]bool{}
	for _, did := range req.DesignIDs {
		if doneDesign[did] {
			continue
		}
		doneDesign[did] = true
		if req.MetalCode == "" || req.StoneCode == "" {
			return nil, nil, invalid("metal_code and stone_code are required with design_ids")
		}
		if _, err := s.Q.GetDesignByID(ctx, did); err != nil {
			return nil, nil, mapErr(err)
		}
		vs, err := s.Q.ListVariationsByDesign(ctx, did)
		if err != nil {
			return nil, nil, err
		}
		for _, v := range vs {
			if seen[v.ID] {
				continue
			}
			seen[v.ID] = true
			wants = append(wants, want{v.ID, req.MetalCode, req.StoneCode})
			if len(wants) > MaxJobBags {
				return nil, nil, invalid("at most %d job bags can be printed at once", MaxJobBags)
			}
		}
	}
	if len(wants) == 0 {
		return nil, nil, invalid("nothing to print: give items or design_ids with variations")
	}

	var failures []JobBagFailure
	for _, w := range wants {
		jb, err := s.JobBagData(ctx, w.id, w.metal, w.stone)
		if err == nil {
			bags = append(bags, jb)
			continue
		}
		var mme *calc.MissingMatrixError
		if !errors.As(err, &mme) {
			return nil, nil, err
		}
		f := JobBagFailure{VariationID: w.id, StoneCode: w.stone, Missing: mme.Pairs}
		v, err := s.Q.GetVariation(ctx, w.id)
		if err != nil {
			return nil, nil, mapErr(err)
		}
		f.VariationCode = v.Code
		d, err := s.Q.GetDesignByID(ctx, v.DesignID)
		if err != nil {
			return nil, nil, mapErr(err)
		}
		f.DesignCode = d.Code
		failures = append(failures, f)
	}
	if len(failures) > 0 && (!req.SkipIncomplete || len(bags) == 0) {
		return nil, nil, &JobBagsError{Failures: failures}
	}
	return bags, failures, nil
}
