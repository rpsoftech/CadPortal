package service

import (
	"context"
	"strings"
	"testing"

	"github.com/shopspring/decimal"
)

func setupKeys(t *testing.T, s *Service) map[string]SetupItem {
	t.Helper()
	st, err := s.SetupStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]SetupItem{}
	for _, it := range st.Items {
		m[it.Key] = it
	}
	if st.Complete != (len(st.Items) == 0) {
		t.Errorf("Complete = %v with %d items", st.Complete, len(st.Items))
	}
	return m
}

func TestSetupStatusFollowsTheDataEntered(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()

	// A fresh database: nothing the business decides has been entered.
	got := setupKeys(t, s)
	for _, k := range []string{"wastage", "default_alloy", "category_prefix", "matrix_empty"} {
		if _, ok := got[k]; !ok {
			t.Errorf("fresh database should report %q, got %v", k, got)
		}
	}
	if d := got["wastage"].Detail; !strings.Contains(d, "22KY") || !strings.Contains(d, "18KR") {
		t.Errorf("wastage detail = %q", d)
	}
	if got["wastage"].Severity != "required" || got["default_alloy"].Severity != "recommended" {
		t.Errorf("severities: %+v %+v", got["wastage"], got["default_alloy"])
	}
	if got["category_prefix"].Tab != "categories" || got["wastage"].Tab != "alloys" || got["matrix_empty"].Tab != "matrix" {
		t.Errorf("tabs: %+v", got)
	}

	// Alloys: wastage for all three and a default.
	metals, _ := s.ListMetals(ctx)
	for i, m := range metals {
		if _, err := s.UpdateMetal(ctx, m.ID, MetalUpdate{
			SpecificGravity: m.SpecificGravity, Active: true, IsDefaultInput: i == 0,
			WastagePct: decimal.NullDecimal{Decimal: decimal.RequireFromString("12.5"), Valid: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	got = setupKeys(t, s)
	if _, ok := got["wastage"]; ok {
		t.Error("wastage still reported after it was entered")
	}
	if _, ok := got["default_alloy"]; ok {
		t.Error("default alloy still reported after it was chosen")
	}

	// An inactive alloy does not need wastage.
	extra, err := s.CreateMetal(ctx, MetalCreate{Code: "14KY", SpecificGravity: decimal.RequireFromString("13.2"), Active: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := setupKeys(t, s)["wastage"]; !ok {
		t.Error("a new active alloy without wastage should be reported")
	}
	if _, err := s.UpdateMetal(ctx, extra.ID, MetalUpdate{SpecificGravity: extra.SpecificGravity, Active: false}); err != nil {
		t.Fatal(err)
	}
	if _, ok := setupKeys(t, s)["wastage"]; ok {
		t.Error("an inactive alloy should not be reported")
	}

	// Categories.
	setPrefixes(t, s)
	got = setupKeys(t, s)
	if d := got["category_prefix"].Detail; !strings.Contains(d, "Bangles") || strings.Contains(d, "Rings") {
		t.Errorf("category detail = %q (Rings has a prefix now, Bangles does not)", d)
	}
	cats, _ := s.ListCategories(ctx)
	for i, c := range cats {
		if !c.CodePrefix.Valid {
			if _, err := s.UpdateCategory(ctx, c.ID, c.Name, string(rune('A'+i))+"X"); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, ok := setupKeys(t, s)["category_prefix"]; ok {
		t.Error("prefixes entered but still reported")
	}

	// Stone matrix: a row without a rate is only a recommendation; a seat size the matrix lacks
	// is required.
	if err := s.UpdateStoneMatrix(ctx, []MatrixRowInput{
		{SizeMM: decimal.RequireFromString("1.10"), StoneCode: "1HD", CaratsPerStone: decimal.RequireFromString("0.0070")},
		{SizeMM: decimal.RequireFromString("1.20"), StoneCode: "1HD", CaratsPerStone: decimal.RequireFromString("0.0095"),
			RatePerCt: decimal.NullDecimal{Decimal: decimal.NewFromInt(50000), Valid: true}},
	}, ""); err != nil {
		t.Fatal(err)
	}
	got = setupKeys(t, s)
	if _, ok := got["matrix_empty"]; ok {
		t.Error("matrix has rows but is reported empty")
	}
	if it, ok := got["matrix_rates"]; !ok || it.Severity != "recommended" || !strings.Contains(it.Detail, "1 of 2") {
		t.Errorf("rates item = %+v", it)
	}

	des := newDesign(t, s, "LR0500")
	v, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	if _, err := s.SetSeats(ctx, v.ID, []SeatInput{{SizeMM: decimal.RequireFromString("1.30"), StoneCount: 4}}); err != nil {
		t.Fatal(err)
	}
	// A seat with no matrix row is accepted (it just cannot be priced), and shows up as a gap.
	it, ok := setupKeys(t, s)["matrix_gaps"]
	if !ok {
		t.Fatal("a seat size missing from the matrix should be reported")
	}
	if it.Severity != "required" || !strings.Contains(it.Detail, "1.3 mm") {
		t.Errorf("gap item = %+v", it)
	}
}
