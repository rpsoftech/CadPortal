package export

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func readZip(t *testing.T, b []byte) map[string][][]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(b), int64(len(b)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][][]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		var buf bytes.Buffer
		buf.ReadFrom(rc) //nolint:errcheck
		rc.Close()
		text := buf.String()
		if strings.HasSuffix(f.Name, ".csv") {
			if !strings.HasPrefix(text, "\xef\xbb\xbf") {
				t.Errorf("%s has no byte-order mark", f.Name)
			}
			recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(text, "\xef\xbb\xbf"))).ReadAll()
			if err != nil {
				t.Fatalf("%s: %v", f.Name, err)
			}
			out[f.Name] = recs
		} else {
			out[f.Name] = [][]string{{text}}
		}
	}
	return out
}

func TestWriteMasterDataZip(t *testing.T) {
	d := func(s string) decimal.Decimal { return decimal.RequireFromString(s) }
	md := service.MasterData{
		Metals: []db.Metal{
			{Code: "22KY", SpecificGravity: d("17.75"), WastagePct: decimal.NullDecimal{Decimal: d("10"), Valid: true}, IsDefaultInput: true, Active: true},
			{Code: "18KR", SpecificGravity: d("15.25"), Active: true}, // wastage not entered yet
		},
		StoneCodes: []db.StoneCode{{Code: "1HD", IsDefaultBaseline: true, Active: true}, {Code: "2HD", Active: false}},
		Matrix: []db.ListStoneMatrixRow{
			{SizeMm: d("1.1"), StoneCode: "2HD", Sieve: pgtype.Text{String: "+0000", Valid: true}, CaratsPerStone: d("0.007"),
				RatePerCt: decimal.NullDecimal{Decimal: d("50000"), Valid: true}, RateBasis: "ct"},
			{SizeMm: d("1.2"), StoneCode: "2HD", CaratsPerStone: d("0.0095"), RateBasis: "pc", RatePerPc: decimal.NullDecimal{Decimal: d("150"), Valid: true}}, // no sieve
		},
		Categories: []db.Category{
			{Name: "Rings", CodePrefix: pgtype.Text{String: "LR", Valid: true}, NextSeq: 41},
			{Name: "=Evil", CodePrefix: pgtype.Text{}, NextSeq: 0},
		},
		Tags: []db.Tag{{TagGroup: "Motif", Name: "Floral", Active: true}, {TagGroup: "Audience", Name: "Bridal", Active: false}},
	}
	var buf bytes.Buffer
	if err := WriteMasterDataZip(&buf, md, time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	got := readZip(t, buf.Bytes())
	for _, name := range []string{"alloys.csv", "stone_codes.csv", "stone_matrix.csv", "categories.csv", "tags.csv", "README.txt"} {
		if _, ok := got[name]; !ok {
			t.Errorf("zip is missing %s (has %d files)", name, len(got))
		}
	}
	eq := func(file string, want [][]string) {
		t.Helper()
		g := got[file]
		if len(g) != len(want) {
			t.Fatalf("%s = %v", file, g)
		}
		for i := range want {
			if strings.Join(g[i], "|") != strings.Join(want[i], "|") {
				t.Errorf("%s row %d = %v, want %v", file, i, g[i], want[i])
			}
		}
	}
	eq("alloys.csv", [][]string{
		{"code", "specific_gravity", "wastage_pct", "default_input", "active"},
		{"22KY", "17.7500", "10", "true", "true"},
		{"18KR", "15.2500", "", "false", "true"}, // an empty wastage stays empty, never 0
	})
	eq("stone_codes.csv", [][]string{{"code", "baseline", "active"}, {"1HD", "true", "true"}, {"2HD", "false", "false"}})
	// The stone matrix file is the import format, fixed decimals included.
	eq("stone_matrix.csv", [][]string{
		{"size_mm", "stone_code", "sieve", "carats_per_stone", "grams_per_stone", "rate_basis", "rate_per_pc", "rate_per_ct"},
		{"1.10", "2HD", "+0000", "0.0070", "0.00140", "ct", "", "50000.00"},
		{"1.20", "2HD", "", "0.0095", "0.00190", "pc", "150.00", ""},
	})
	eq("categories.csv", [][]string{
		{"name", "code_prefix", "last_number_used"}, {"Rings", "LR", "41"}, {"'=Evil", "", "0"},
	})
	eq("tags.csv", [][]string{{"group", "name", "active"}, {"Motif", "Floral", "true"}, {"Audience", "Bridal", "false"}})
	readme := got["README.txt"][0][0]
	for _, frag := range []string{"2026-10-21 12:00 UTC", "2 alloys, 2 stone codes, 2 matrix rows, 2 categories, 2 tags", "wastage"} {
		if !strings.Contains(readme, frag) {
			t.Errorf("README lacks %q:\n%s", frag, readme)
		}
	}
}

func TestWriteMasterDataZipWithNothingEntered(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteMasterDataZip(&buf, service.MasterData{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	got := readZip(t, buf.Bytes())
	if len(got["alloys.csv"]) != 1 || len(got["stone_matrix.csv"]) != 1 {
		t.Errorf("an empty database should still give header-only files: %v", got)
	}
}

func TestSafeSieveKeepsRealSievesAndNeutralisesFormulas(t *testing.T) {
	for in, want := range map[string]string{
		"+0000": "+0000", "-2": "-2", "": "", "1/2": "1/2",
		"=1+1": "'=1+1", "+cmd|' /C calc'!A0": "'+cmd|' /C calc'!A0", "@SUM(A1)": "'@SUM(A1)",
	} {
		if got := safeSieve(in); got != want {
			t.Errorf("safeSieve(%q) = %q, want %q", in, got, want)
		}
	}
}
