package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// MaxImportRows bounds one import so a bad file cannot hold a transaction open for long.
const MaxImportRows = 1000

var (
	importCodeRE   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,31}$`)
	importNumberRE = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// ImportRow is one line of an import file. Rows sharing a Code belong to one design: the
// first row defines name, category and tags (later rows may repeat the name and category or
// leave them blank) and each row with a base input adds one variation.
type ImportRow struct {
	Line       int    `json:"line"`
	Code       string `json:"code"`
	Name       string `json:"name"`
	Category   string `json:"category"`
	Tags       string `json:"tags"`         // names separated by ';', or "Group:Name"
	VolumeMM3  string `json:"volume_mm3"`   // either this ...
	RefWeightG string `json:"ref_weight_g"` // ... or this together with RefMetal
	RefMetal   string `json:"ref_metal"`
}

type ImportError struct {
	Line    int    `json:"line"`
	Message string `json:"message"`
}

type ImportResult struct {
	DryRun     bool          `json:"dry_run"`
	Designs    int           `json:"designs"`
	Variations int           `json:"variations"`
	Errors     []ImportError `json:"errors"`
}

type importDesign struct {
	code, name string
	categoryID int64
	tagIDs     []int64
	line       int
	bases      []BaseInput
}

// ImportDesigns validates every row and, unless dryRun, creates all designs and variations in
// one transaction. If any row has a problem nothing is written and every problem is returned,
// so the file can be fixed in one pass. Existing designs are never modified: a code that is
// already taken (deleted designs included) is an error.
func (s *Service) ImportDesigns(ctx context.Context, rows []ImportRow, dryRun bool) (ImportResult, error) {
	res := ImportResult{DryRun: dryRun, Errors: []ImportError{}}
	if len(rows) == 0 {
		return res, invalid("no rows to import")
	}
	if len(rows) > MaxImportRows {
		return res, invalid("too many rows: %d (limit %d)", len(rows), MaxImportRows)
	}
	cats, err := s.Q.ListCategories(ctx)
	if err != nil {
		return res, mapErr(err)
	}
	tags, err := s.Q.ListTags(ctx) // active tags only
	if err != nil {
		return res, mapErr(err)
	}
	metals, err := s.Q.ListMetals(ctx)
	if err != nil {
		return res, mapErr(err)
	}
	catByName := map[string]int64{}
	for _, c := range cats {
		catByName[strings.ToLower(c.Name)] = c.ID
	}
	tagByName := map[string][]int64{} // a bare name can exist in more than one group
	tagByQualified := map[string]int64{}
	for _, t := range tags {
		n := strings.ToLower(t.Name)
		tagByName[n] = append(tagByName[n], t.ID)
		tagByQualified[strings.ToLower(t.TagGroup)+":"+n] = t.ID
	}
	metalOK := map[string]bool{}
	for _, m := range metals {
		metalOK[m.Code] = true
	}

	var errs []ImportError
	bad := func(line int, format string, a ...any) {
		errs = append(errs, ImportError{Line: line, Message: fmt.Sprintf(format, a...)})
	}

	designs := map[string]*importDesign{}
	var order []string
	for i, r := range rows {
		line := r.Line
		if line <= 0 {
			line = i + 1
		}
		code := strings.TrimSpace(r.Code)
		if !importCodeRE.MatchString(code) {
			bad(line, "code %q must be 1 to 32 letters, digits, '-' or '_'", code)
			continue
		}
		name := strings.TrimSpace(r.Name)
		catName := strings.TrimSpace(r.Category)
		catID, catKnown := catByName[strings.ToLower(catName)]
		d, seen := designs[code]
		if !seen {
			d = &importDesign{code: code, name: name, line: line, categoryID: catID}
			designs[code] = d
			order = append(order, code)
			if !catKnown {
				bad(line, "unknown category %q", catName)
			}
			d.tagIDs = resolveImportTags(r.Tags, tagByName, tagByQualified, func(msg string) { bad(line, "%s", msg) })
		} else {
			if name != "" && name != d.name {
				bad(line, "name %q differs from line %d (%q)", name, d.line, d.name)
			}
			if catName != "" && (!catKnown || catID != d.categoryID) {
				bad(line, "category differs from line %d", d.line)
			}
			if strings.TrimSpace(r.Tags) != "" {
				bad(line, "tags belong on the first row of %s (line %d)", code, d.line)
			}
		}
		base, has, msg := parseImportBase(r, metalOK)
		if msg != "" {
			bad(line, "%s", msg)
		} else if has {
			d.bases = append(d.bases, base)
		}
	}

	taken, err := s.Q.ListExistingDesignCodes(ctx, order)
	if err != nil {
		return res, mapErr(err)
	}
	for _, c := range taken {
		bad(designs[c].line, "design %s already exists (codes of deleted designs stay reserved)", c)
	}

	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool { return errs[i].Line < errs[j].Line })
		res.Errors = errs
		return res, nil
	}
	for _, c := range order {
		res.Designs++
		res.Variations += len(designs[c].bases)
	}
	if dryRun {
		return res, nil
	}

	err = s.inTx(ctx, func(q *db.Queries) error {
		for _, c := range order {
			d := designs[c]
			md, err := q.CreateDesign(ctx, db.CreateDesignParams{Code: d.code, Name: d.name, CategoryID: d.categoryID})
			if err != nil {
				return friendly(err, fmt.Sprintf("design %s was created by someone else while importing; run the import again", d.code))
			}
			if err := setTags(ctx, q, md.ID, d.tagIDs); err != nil {
				return err
			}
			for _, b := range d.bases {
				vol, w, metal, err := s.resolveBase(ctx, q, b)
				if err != nil {
					return err
				}
				seq, err := q.NextVariationSeq(ctx, md.ID)
				if err != nil {
					return err
				}
				v, err := q.CreateVariation(ctx, db.CreateVariationParams{
					DesignID: md.ID, Seq: seq, Code: fmt.Sprintf("%s-V%d", md.Code, seq),
					BaseVolumeMm3: vol, BaseRefWeightG: w, BaseRefMetalID: metal,
				})
				if err != nil {
					return err
				}
				if err := RecalcVariation(ctx, q, v.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		res.Designs, res.Variations = 0, 0
		return res, err
	}
	return res, nil
}

func resolveImportTags(list string, byName map[string][]int64, qualified map[string]int64, bad func(string)) []int64 {
	var ids []int64
	seen := map[int64]bool{}
	for _, raw := range strings.Split(list, ";") {
		shown := strings.TrimSpace(raw)
		t := strings.ToLower(shown)
		if t == "" {
			continue
		}
		var id int64
		if group, name, ok := strings.Cut(t, ":"); ok {
			qid, found := qualified[strings.TrimSpace(group)+":"+strings.TrimSpace(name)]
			if !found {
				bad(fmt.Sprintf("unknown tag %q", shown))
				continue
			}
			id = qid
		} else {
			switch c := byName[t]; len(c) {
			case 0:
				bad(fmt.Sprintf("unknown tag %q", shown))
				continue
			case 1:
				id = c[0]
			default:
				bad(fmt.Sprintf("tag %q exists in several groups; write it as Group:Name", shown))
				continue
			}
		}
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	return ids
}

// parseImportBase reads the base input of a row. has is false when the row has none (a
// design without a variation); msg describes a problem.
func parseImportBase(r ImportRow, metals map[string]bool) (b BaseInput, has bool, msg string) {
	vol, ref, metal := strings.TrimSpace(r.VolumeMM3), strings.TrimSpace(r.RefWeightG), strings.TrimSpace(r.RefMetal)
	switch {
	case vol == "" && ref == "" && metal == "":
		return b, false, ""
	case vol != "" && (ref != "" || metal != ""):
		return b, false, "give either volume_mm3, or ref_weight_g with ref_metal, not both"
	case vol != "":
		d, err := importDecimal("volume_mm3", vol)
		if err != nil {
			return b, false, err.Error()
		}
		return BaseInput{VolumeMM3: &d}, true, ""
	}
	if ref == "" || metal == "" {
		return b, false, "ref_weight_g and ref_metal are both required"
	}
	d, err := importDecimal("ref_weight_g", ref)
	if err != nil {
		return b, false, err.Error()
	}
	if !metals[metal] {
		return b, false, fmt.Sprintf("unknown ref_metal %q", metal)
	}
	return BaseInput{RefWeightG: &d, RefMetalCode: metal}, true, ""
}

// importDecimal accepts a positive number with at most 3 decimal places (what is stored), so
// nothing is rounded silently.
func importDecimal(name, v string) (decimal.Decimal, error) {
	if !importNumberRE.MatchString(v) {
		return decimal.Zero, fmt.Errorf("%s %q is not a number", name, v)
	}
	d, err := decimal.NewFromString(v)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s %q is not a number", name, v)
	}
	if !d.IsPositive() {
		return d, fmt.Errorf("%s must be greater than 0", name)
	}
	if d.Exponent() < -3 {
		return d, fmt.Errorf("%s %q has more than 3 decimal places", name, v)
	}
	return d, nil
}

// ExportDesigns returns every live design in the shape ImportDesigns reads, so an export can
// be imported into a fresh installation. A design's name, category and tags appear on its first
// row only; each variation adds a row with its base input. Variation numbers are not kept: they
// are assigned again in file order on import.
func (s *Service) ExportDesigns(ctx context.Context) ([]ImportRow, error) {
	recs, err := s.Q.ListDesignExportRows(ctx)
	if err != nil {
		return nil, mapErr(err)
	}
	rows := make([]ImportRow, 0, len(recs))
	last := ""
	for _, r := range recs {
		row := ImportRow{Code: r.Code, RefMetal: r.RefMetal}
		if r.Code != last {
			row.Name, row.Category, row.Tags = r.Name, r.CategoryName, r.Tags
			last = r.Code
		}
		if r.BaseVolumeMm3.Valid {
			row.VolumeMM3 = r.BaseVolumeMm3.Decimal.String()
		}
		if r.BaseRefWeightG.Valid {
			row.RefWeightG = r.BaseRefWeightG.Decimal.String()
		}
		rows = append(rows, row)
	}
	return rows, nil
}
