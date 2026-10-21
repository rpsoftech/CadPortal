package export

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

// WriteMasterDataZip writes every master-data table as its own CSV inside one zip, so the data an
// admin entered (alloys with wastage, stone codes, the stone matrix with rates, category prefixes,
// tags) can be kept outside the database. The columns of stone_matrix.csv are exactly what the
// stone matrix import reads, so that file can be loaded back as it is. The other files are
// records to keep and to read; they are not importable.
func WriteMasterDataZip(w io.Writer, md service.MasterData, at time.Time) error {
	zw := zip.NewWriter(w)
	files := []struct {
		name  string
		write func(*csv.Writer) error
	}{
		{"alloys.csv", func(cw *csv.Writer) error {
			if err := cw.Write([]string{"code", "specific_gravity", "wastage_pct", "default_input", "active"}); err != nil {
				return err
			}
			for _, m := range md.Metals {
				wastage := ""
				if m.WastagePct.Valid {
					wastage = m.WastagePct.Decimal.String()
				}
				if err := cw.Write([]string{safeCell(m.Code), m.SpecificGravity.StringFixed(4), wastage,
					strconv.FormatBool(m.IsDefaultInput), strconv.FormatBool(m.Active)}); err != nil {
					return err
				}
			}
			return nil
		}},
		{"stone_codes.csv", func(cw *csv.Writer) error {
			if err := cw.Write([]string{"code", "baseline", "active"}); err != nil {
				return err
			}
			for _, c := range md.StoneCodes {
				if err := cw.Write([]string{safeCell(c.Code), strconv.FormatBool(c.IsDefaultBaseline), strconv.FormatBool(c.Active)}); err != nil {
					return err
				}
			}
			return nil
		}},
		{"stone_matrix.csv", func(cw *csv.Writer) error {
			// The import format: size_mm, stone_code, sieve, carats_per_stone, rate_per_ct.
			if err := cw.Write([]string{"size_mm", "stone_code", "sieve", "carats_per_stone", "rate_per_ct"}); err != nil {
				return err
			}
			for _, r := range md.Matrix {
				rate := ""
				if r.RatePerCt.Valid {
					rate = r.RatePerCt.Decimal.StringFixed(2)
				}
				if err := cw.Write([]string{r.SizeMm.StringFixed(2), safeCell(r.StoneCode), safeSieve(r.Sieve.String),
					r.CaratsPerStone.StringFixed(4), rate}); err != nil {
					return err
				}
			}
			return nil
		}},
		{"categories.csv", func(cw *csv.Writer) error {
			if err := cw.Write([]string{"name", "code_prefix", "last_number_used"}); err != nil {
				return err
			}
			for _, c := range md.Categories {
				if err := cw.Write([]string{safeCell(c.Name), safeCell(c.CodePrefix.String), strconv.Itoa(int(c.NextSeq))}); err != nil {
					return err
				}
			}
			return nil
		}},
		{"tags.csv", func(cw *csv.Writer) error {
			if err := cw.Write([]string{"group", "name", "active"}); err != nil {
				return err
			}
			for _, t := range md.Tags {
				if err := cw.Write([]string{safeCell(t.TagGroup), safeCell(t.Name), strconv.FormatBool(t.Active)}); err != nil {
					return err
				}
			}
			return nil
		}},
	}
	for _, f := range files {
		// Fixed timestamps would hide when the export was made; use the export time.
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: f.name, Method: zip.Deflate, Modified: at})
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		buf.Write([]byte{0xEF, 0xBB, 0xBF}) // a byte-order mark so Excel opens UTF-8 names correctly
		cw := csv.NewWriter(&buf)
		if err := f.write(cw); err != nil {
			return err
		}
		cw.Flush()
		if err := cw.Error(); err != nil {
			return err
		}
		if _, err := fw.Write(buf.Bytes()); err != nil {
			return err
		}
	}
	info, err := zw.CreateHeader(&zip.FileHeader{Name: "README.txt", Method: zip.Deflate, Modified: at})
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(info, `CadPortal master data, exported %s

alloys.csv        alloys with specific gravity and wastage %% (confidential: costing inputs)
stone_codes.csv   stone codes and which one is the baseline
stone_matrix.csv  carats per stone and rate per carat for each (size, stone code); this is the
                  stone matrix import format and can be loaded back as it is
categories.csv    categories and their code prefixes (designs are numbered PREFIX0001, ...)
tags.csv          the tag pool

%d alloys, %d stone codes, %d matrix rows, %d categories, %d tags.
This file contains wastage percentages and stone rates: store it as carefully as a backup.
`, at.UTC().Format("2006-01-02 15:04 UTC"), len(md.Metals), len(md.StoneCodes), len(md.Matrix), len(md.Categories), len(md.Tags))
	if err != nil {
		return err
	}
	return zw.Close()
}

// sieveRE matches sieve classes such as +0000 or -2: a sign and digits, never a formula.
var sieveRE = regexp.MustCompile(`^[+-]\d+$`)

// safeSieve leaves a plain sieve class untouched (it must survive a round trip through the
// matrix import) and protects any other text like the rest of the exports.
func safeSieve(s string) string {
	if sieveRE.MatchString(s) {
		return s
	}
	return safeCell(s)
}
