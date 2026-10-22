package service

import (
	"context"
	"fmt"
	"strings"
)

// SetupItem is one thing an admin still has to enter before the portal is fully usable.
type SetupItem struct {
	Key string `json:"key"`
	// Severity is "required" when something does not work without it (costing refuses to run,
	// designs cannot be created) and "recommended" otherwise.
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	// Tab is the Master data tab where it is fixed: alloys, matrix or categories.
	Tab string `json:"tab"`
}

type SetupStatus struct {
	// Complete is true when nothing is missing, recommended items included.
	Complete bool        `json:"complete"`
	Items    []SetupItem `json:"items"`
}

// SetupStatus lists the master data that is still missing. It reads data only an admin
// maintains (wastage and rates are never shown to the production role), so callers must
// restrict it to admins.
func (s *Service) SetupStatus(ctx context.Context) (SetupStatus, error) {
	items := []SetupItem{}
	add := func(key, severity, tab, title, format string, a ...any) {
		items = append(items, SetupItem{Key: key, Severity: severity, Tab: tab, Title: title, Detail: fmt.Sprintf(format, a...)})
	}

	metals, err := s.Q.ListMetals(ctx)
	if err != nil {
		return SetupStatus{}, mapErr(err)
	}
	var noWastage []string
	hasDefault := false
	for _, m := range metals {
		if !m.Active {
			continue
		}
		if !m.WastagePct.Valid {
			noWastage = append(noWastage, m.Code)
		}
		if m.IsDefaultInput {
			hasDefault = true
		}
	}
	if len(noWastage) > 0 {
		add("wastage", "required", "alloys", "Wastage % is missing",
			"%s: costing refuses to run for an alloy without wastage %%, it never assumes zero.", strings.Join(noWastage, ", "))
	}
	if !hasDefault && len(metals) > 0 {
		add("default_alloy", "recommended", "alloys", "No default input alloy",
			"Choose the alloy that a reference weight is normally given in.")
	}

	cats, err := s.Q.ListCategories(ctx)
	if err != nil {
		return SetupStatus{}, mapErr(err)
	}
	var noPrefix []string
	for _, c := range cats {
		if !c.CodePrefix.Valid || c.CodePrefix.String == "" {
			noPrefix = append(noPrefix, c.Name)
		}
	}
	if len(noPrefix) > 0 {
		add("category_prefix", "required", "categories", "Category code prefixes are missing",
			"No designs can be created in: %s. Design codes are the prefix plus a number, for example LR0001.", strings.Join(noPrefix, ", "))
	}

	codes, err := s.Q.ListStoneCodes(ctx)
	if err != nil {
		return SetupStatus{}, mapErr(err)
	}
	baseline := ""
	for _, c := range codes {
		if c.IsDefaultBaseline {
			baseline = c.Code
		}
	}
	matrix, err := s.Q.ListStoneMatrix(ctx)
	if err != nil {
		return SetupStatus{}, mapErr(err)
	}
	switch {
	case baseline == "":
		add("baseline", "required", "matrix", "No baseline stone code",
			"Gross weights in the catalog use the baseline stone code; choose one.")
	case len(matrix) == 0:
		add("matrix_empty", "required", "matrix", "The stone matrix is empty",
			"Add the stone sizes (weight per stone and price) or import a CSV. Until then gross weights show a dash.")
	}

	if len(matrix) > 0 {
		noRate := 0
		for _, r := range matrix {
			// the price that costing would use for this row: per piece unless the row is priced per carat
			if (r.RateBasis == "ct" && !r.RatePerCt.Valid) || (r.RateBasis != "ct" && !r.RatePerPc.Valid) {
				noRate++
			}
		}
		if noRate > 0 {
			add("matrix_rates", "recommended", "matrix", "Stone rates are missing",
				"%d of %d matrix rows have no price (per piece or per carat, as each row is set). Costing needs a price for every stone it prices.", noRate, len(matrix))
		}
		gaps, err := s.Q.ListMatrixGaps(ctx)
		if err != nil {
			return SetupStatus{}, mapErr(err)
		}
		if len(gaps) > 0 {
			var sizes []string
			for i, g := range gaps {
				if i == 5 {
					sizes = append(sizes, "…")
					break
				}
				sizes = append(sizes, g.SizeMm.String()+" mm")
			}
			add("matrix_gaps", "required", "matrix", "Seat sizes have no matrix row",
				"%d size(s) used by seats are missing for the baseline code %s: %s. Those variations show no gross weight.", len(gaps), baseline, strings.Join(sizes, ", "))
		}
	}
	return SetupStatus{Complete: len(items) == 0, Items: items}, nil
}
