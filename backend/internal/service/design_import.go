package service

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// MaxImportRows bounds one import so a bad file cannot hold a transaction open for long.
const MaxImportRows = 1000

var importNumberRE = regexp.MustCompile(`^\d+(\.\d+)?$`)

// ImportRow is one line of an import file.
//
// A design starts on a row that has a category. Its Code is normally left empty and is assigned
// automatically (LR0001, LR0002, ...). A code may be given to bring over designs that already
// have numbers, for example an earlier export, but it must be the category's prefix followed by
// digits. A row with no category and no code adds a variation to the design above it, and so
// does a row repeating that design's code. Each row with a base input adds one variation.
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
	// Codes are the design codes of an import that was carried out, in file order.
	Codes []string `json:"codes"`
}

type importDesign struct {
	code     string // empty until assigned for an automatic design
	number   int32  // the numeric part of an explicit code
	name     string
	category db.Category
	tagIDs   []int64
	line     int
	bases    []BaseInput
}

// ImportDesigns validates every row and, unless dryRun, creates all designs and variations in
// one transaction. If any row has a problem nothing is written and every problem is returned,
// so the file can be fixed in one pass. Existing designs are never modified: a code that is
// already taken (deleted designs included) is an error.
func (s *Service) ImportDesigns(ctx context.Context, rows []ImportRow, dryRun bool) (ImportResult, error) {
	res := ImportResult{DryRun: dryRun, Errors: []ImportError{}, Codes: []string{}}
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
	catByName := map[string]db.Category{}
	for _, c := range cats {
		catByName[strings.ToLower(c.Name)] = c
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

	var all []*importDesign              // file order
	byCode := map[string]*importDesign{} // explicit codes
	var cur *importDesign                // the design a continuation row belongs to
	for i, r := range rows {
		line := r.Line
		if line <= 0 {
			line = i + 1
		}
		code := strings.TrimSpace(r.Code)
		name := strings.TrimSpace(r.Name)
		catName := strings.TrimSpace(r.Category)
		hasTags := strings.TrimSpace(r.Tags) != ""

		var d *importDesign
		switch {
		case code != "" && byCode[code] != nil:
			d = byCode[code] // another variation of a design given earlier
		case code == "" && catName == "" && name == "" && !hasTags:
			if cur == nil {
				bad(line, "the first row needs a category")
				continue
			}
			d = cur
		default:
			d = &importDesign{name: name, line: line}
			cat, known := catByName[strings.ToLower(catName)]
			switch {
			case !known:
				bad(line, "unknown category %q", catName)
			case !cat.CodePrefix.Valid || cat.CodePrefix.String == "":
				bad(line, "category %q has no code prefix; set one in Master data first", cat.Name)
			default:
				d.category = cat
			}
			if code != "" {
				d.code = code
				byCode[code] = d
				if d.category.ID != 0 {
					n, ok := codeNumber(d.category.CodePrefix.String, code)
					if !ok {
						bad(line, "code %q must be %s followed by digits, or left empty to be assigned", code, d.category.CodePrefix.String)
					}
					d.number = n
				}
			}
			d.tagIDs = resolveImportTags(r.Tags, tagByName, tagByQualified, func(msg string) { bad(line, "%s", msg) })
			all = append(all, d)
		}
		if d.line != line {
			// A continuation row: only the base input belongs on it.
			if name != "" && name != d.name {
				bad(line, "name %q differs from line %d (%q)", name, d.line, d.name)
			}
			if catName != "" && !strings.EqualFold(catName, d.category.Name) {
				bad(line, "category differs from line %d", d.line)
			}
			if hasTags {
				bad(line, "tags belong on the first row of the design (line %d)", d.line)
			}
		}
		cur = d
		base, has, msg := parseImportBase(r, metalOK)
		if msg != "" {
			bad(line, "%s", msg)
		} else if has {
			d.bases = append(d.bases, base)
		}
	}

	var explicit []string
	for _, d := range all {
		if d.code != "" {
			explicit = append(explicit, d.code)
		}
	}
	taken, err := s.Q.ListExistingDesignCodes(ctx, explicit)
	if err != nil {
		return res, mapErr(err)
	}
	for _, c := range taken {
		bad(byCode[c].line, "design %s already exists (codes of deleted designs stay reserved)", c)
	}

	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool { return errs[i].Line < errs[j].Line })
		res.Errors = errs
		return res, nil
	}
	for _, d := range all {
		res.Designs++
		res.Variations += len(d.bases)
	}
	if dryRun {
		return res, nil
	}

	err = s.inTx(ctx, func(q *db.Queries) error {
		// Designs that bring their own number go first and push the counter past it, so an
		// automatic number handed out later in this same import can never collide with one.
		for _, d := range all {
			if d.code == "" {
				continue
			}
			if err := q.RaiseCategoryNumber(ctx, db.RaiseCategoryNumberParams{ID: d.category.ID, NextSeq: d.number}); err != nil {
				return err
			}
			if err := s.createImported(ctx, q, d); err != nil {
				return err
			}
		}
		for _, d := range all {
			if d.code != "" {
				continue
			}
			code, err := allocateCode(ctx, q, d.category.ID)
			if err != nil {
				return err
			}
			d.code = code
			if err := s.createImported(ctx, q, d); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		res.Designs, res.Variations = 0, 0
		return res, err
	}
	for _, d := range all {
		res.Codes = append(res.Codes, d.code)
	}
	return res, nil
}

// createImported inserts one validated design with its tags and variations.
func (s *Service) createImported(ctx context.Context, q *db.Queries, d *importDesign) error {
	md, err := q.CreateDesign(ctx, db.CreateDesignParams{Code: d.code, Name: d.name, CategoryID: d.category.ID})
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
	return nil
}

// codeNumber returns the number in a code of the form PREFIX + digits.
func codeNumber(prefix, code string) (int32, bool) {
	rest, ok := strings.CutPrefix(code, prefix)
	if !ok || len(rest) == 0 || len(rest) > 9 {
		return 0, false
	}
	n, err := strconv.ParseInt(rest, 10, 32)
	if err != nil || n <= 0 {
		return 0, false
	}
	return int32(n), true
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
