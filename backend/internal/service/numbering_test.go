package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

func mustCategory(t *testing.T, s *Service, name string) int64 {
	t.Helper()
	cats, err := s.ListCategories(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range cats {
		if c.Name == name {
			return c.ID
		}
	}
	t.Fatalf("no category %q", name)
	return 0
}

func create(t *testing.T, s *Service, category string) string {
	t.Helper()
	d, err := s.CreateDesign(context.Background(), DesignInput{CategoryID: mustCategory(t, s, category)})
	if err != nil {
		t.Fatalf("create in %s: %v", category, err)
	}
	return d.Code
}

func TestFormatCode(t *testing.T) {
	for _, c := range []struct {
		prefix string
		n      int32
		want   string
	}{
		{"LR", 1, "LR0001"},
		{"LR", 42, "LR0042"},
		{"LR", 9999, "LR9999"},
		{"LR", 10000, "LR10000"}, // never truncated
		{"PS", 7, "PS0007"},
	} {
		if got := FormatCode(c.prefix, c.n); got != c.want {
			t.Errorf("FormatCode(%q, %d) = %q, want %q", c.prefix, c.n, got, c.want)
		}
	}
}

func TestNormalizePrefix(t *testing.T) {
	for in, want := range map[string]string{"lr": "LR", " pS ": "PS", "": "", "   ": "", "ABCDEF": "ABCDEF"} {
		if got, err := normalizePrefix(in); err != nil || got != want {
			t.Errorf("normalizePrefix(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"L R", "L1", "TOOLONG", "L-R", "LÉ"} {
		if _, err := normalizePrefix(in); !errors.Is(err, ErrInvalid) {
			t.Errorf("normalizePrefix(%q) accepted", in)
		}
	}
}

func TestDesignCodesAreSequentialPerCategory(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	got := []string{
		create(t, s, "Rings"), create(t, s, "Rings"), create(t, s, "Earrings"), create(t, s, "Rings"), create(t, s, "Earrings"),
	}
	want := []string{"LR0001", "LR0002", "LE0001", "LR0003", "LE0002"}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("code %d = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestDesignCodeWidthGrowsPastNineThousandNineHundredNinetyNine(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	if _, err := s.Pool.Exec(context.Background(), "UPDATE categories SET next_seq = 9998 WHERE name = 'Rings'"); err != nil {
		t.Fatal(err)
	}
	if a, b := create(t, s, "Rings"), create(t, s, "Rings"); a != "LR9999" || b != "LR10000" {
		t.Errorf("codes = %s, %s", a, b)
	}
}

func TestDesignWithoutAPrefixIsRefusedAndUsesNoNumber(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	_, err := s.CreateDesign(ctx, DesignInput{CategoryID: mustCategory(t, s, "Rings")})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v", err)
	}
	if _, err := s.CreateDesign(ctx, DesignInput{CategoryID: 999999}); !errors.Is(err, ErrInvalid) {
		t.Errorf("unknown category: %v", err)
	}
	setPrefixes(t, s)
	if c := create(t, s, "Rings"); c != "LR0001" {
		t.Errorf("first code = %s", c)
	}
}

func TestRolledBackCreationGivesTheNumberBack(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	ctx := context.Background()
	rings := mustCategory(t, s, "Rings")
	boom := errors.New("boom")
	err := s.inTx(ctx, func(q *db.Queries) error {
		code, err := allocateCode(ctx, q, rings)
		if err != nil || code != "LR0001" {
			t.Errorf("in transaction: %q %v", code, err)
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if c := create(t, s, "Rings"); c != "LR0001" {
		t.Errorf("after rollback the next code = %s, want LR0001 (no gap)", c)
	}
}

func TestCodesSkipNumbersAlreadyTaken(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	// Designs made by hand before the category had a prefix.
	newDesign(t, s, "LR0001")
	newDesign(t, s, "LR0002")
	del := newDesign(t, s, "LR0003")
	if err := s.DeleteDesign(ctx, del.ID); err != nil {
		t.Fatal(err)
	}
	cats, _ := s.ListCategories(ctx)
	var rings db.Category
	for _, c := range cats {
		if c.Name == "Rings" {
			rings = c
		}
	}
	if _, err := s.UpdateCategory(ctx, rings.ID, rings.Name, "lr"); err != nil {
		t.Fatal(err)
	}
	// 0001 and 0002 exist and 0003 is a deleted design whose code stays reserved.
	if c := create(t, s, "Rings"); c != "LR0004" {
		t.Errorf("code = %s, want LR0004", c)
	}
}

func TestChangingThePrefixRestartsTheNumbering(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	rings := mustCategory(t, s, "Rings")
	create(t, s, "Rings") // LR0001
	create(t, s, "Rings") // LR0002

	if _, err := s.UpdateCategory(ctx, rings, "Rings", "RG"); err != nil {
		t.Fatal(err)
	}
	if c := create(t, s, "Rings"); c != "RG0001" {
		t.Errorf("after the prefix change: %s", c)
	}
	// Saving the same prefix again must not reset anything.
	if _, err := s.UpdateCategory(ctx, rings, "Rings", "rg"); err != nil {
		t.Fatal(err)
	}
	if c := create(t, s, "Rings"); c != "RG0002" {
		t.Errorf("after re-saving the prefix: %s", c)
	}
	// Going back to LR starts at 1 but steps over the codes that exist.
	if _, err := s.UpdateCategory(ctx, rings, "Rings", "LR"); err != nil {
		t.Fatal(err)
	}
	if c := create(t, s, "Rings"); c != "LR0003" {
		t.Errorf("after going back: %s", c)
	}
}

func TestPrefixesAreValidatedAndUnique(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	setPrefixes(t, s)
	rings := mustCategory(t, s, "Rings")
	if _, err := s.UpdateCategory(ctx, rings, "Rings", "L1"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad prefix: %v", err)
	}
	if _, err := s.CreateCategory(ctx, "Anklets", "x y"); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad prefix on create: %v", err)
	}
	if _, err := s.UpdateCategory(ctx, mustCategory(t, s, "Bangles"), "Bangles", "LR"); !errors.Is(err, ErrConflict) {
		t.Errorf("two categories sharing a prefix: %v", err)
	}
	if c, err := s.CreateCategory(ctx, "Anklets", "an"); err != nil || c.CodePrefix.String != "AN" {
		t.Errorf("create with a lower-case prefix: %+v %v", c, err)
	}
}

func TestConcurrentCreationNeverRepeatsACode(t *testing.T) {
	s := newTestService(t)
	setPrefixes(t, s)
	const n = 20
	codes := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d, err := s.CreateDesign(context.Background(), DesignInput{CategoryID: mustCategory(t, s, "Rings")})
			if err != nil {
				t.Error(err)
				return
			}
			codes[i] = d.Code
		}()
	}
	wg.Wait()
	sort.Strings(codes)
	for i, c := range codes {
		if want := FormatCode("LR", int32(i+1)); c != want {
			t.Fatalf("codes = %v (want LR0001..LR0020 with no repeats or gaps)", codes)
		}
	}
}
