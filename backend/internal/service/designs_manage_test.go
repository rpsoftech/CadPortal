package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

func mkDesign(t *testing.T, s *Service, code, name string) int64 {
	t.Helper()
	cats, _ := s.ListCategories(context.Background())
	d, err := s.CreateDesign(context.Background(), DesignInput{Code: code, Name: name, CategoryID: cats[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	return d.ID
}

func codes(items []DesignListItem) string {
	out := make([]string, len(items))
	for i, it := range items {
		out[i] = it.Code
	}
	return strings.Join(out, ",")
}

func TestListDesignsSearch(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	mkDesign(t, s, "LR001", "Rose Halo Ring")
	mkDesign(t, s, "LR002", "Petal Studs")
	mkDesign(t, s, "ER010", "Sale 50% Special")
	mkDesign(t, s, "ER011", "Sale 500 Special")
	mkDesign(t, s, "ER012", "snake_case name")
	mkDesign(t, s, "ER013", "snakeXcase name")

	tests := []struct {
		name, q, want string
	}{
		{"empty matches everything", "", "ER010,ER011,ER012,ER013,LR001,LR002"},
		{"blank is empty", "   ", "ER010,ER011,ER012,ER013,LR001,LR002"},
		{"name, any case", "rose", "LR001"},
		{"name substring", "HALO", "LR001"},
		{"code prefix", "lr0", "LR001,LR002"},
		{"code or name", "er01", "ER010,ER011,ER012,ER013"},
		{"percent is literal", "50%", "ER010"},
		{"underscore is literal", "snake_case", "ER012"},
		{"bang is literal", "50!", ""},
		{"no match", "zzz", ""},
		{"surrounding spaces ignored", "  petal  ", "LR002"},
		{"sql-ish input is just text", "'; DROP TABLE master_designs; --", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := s.ListDesigns(ctx, DesignFilter{Query: tc.q})
			if err != nil {
				t.Fatal(err)
			}
			if c := codes(got); c != tc.want {
				t.Errorf("q=%q -> %q, want %q", tc.q, c, tc.want)
			}
		})
	}
	// An over-long query is trimmed, not rejected or crashing.
	if _, err := s.ListDesigns(ctx, DesignFilter{Query: strings.Repeat("a", 5000)}); err != nil {
		t.Errorf("long query: %v", err)
	}
	// The table survived the injection attempt.
	if all, _ := s.ListDesigns(ctx, DesignFilter{}); len(all) != 6 {
		t.Errorf("designs after injection attempt = %d", len(all))
	}
}

func TestListDesignsPagingAndTotal(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	for i := 1; i <= 5; i++ {
		mkDesign(t, s, fmt.Sprintf("LR%03d", i), "Ring")
	}
	mkDesign(t, s, "ER001", "Earring")

	page := func(f DesignFilter) ([]DesignListItem, int64) {
		items, err := s.ListDesigns(ctx, f)
		if err != nil {
			t.Fatal(err)
		}
		var total int64
		if len(items) > 0 {
			total = items[0].TotalCount
		}
		return items, total
	}

	items, total := page(DesignFilter{Limit: 4})
	if len(items) != 4 || total != 6 || codes(items) != "ER001,LR001,LR002,LR003" {
		t.Errorf("page 1: %s total=%d", codes(items), total)
	}
	items, total = page(DesignFilter{Limit: 4, Offset: 4})
	if codes(items) != "LR004,LR005" || total != 6 {
		t.Errorf("page 2: %s total=%d", codes(items), total)
	}
	// The total follows the filter, not the whole table.
	items, total = page(DesignFilter{Query: "LR", Limit: 2})
	if len(items) != 2 || total != 5 {
		t.Errorf("filtered: %s total=%d (want 2 rows of 5)", codes(items), total)
	}
	// Past the end: no rows (the handler reports a total of 0).
	if items, total = page(DesignFilter{Limit: 4, Offset: 40}); len(items) != 0 || total != 0 {
		t.Errorf("past the end: %d rows total=%d", len(items), total)
	}
}

func TestDuplicateDesignCodeMessage(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	id := mkDesign(t, s, "LR001", "First")
	cats, _ := s.ListCategories(ctx)
	_, err := s.CreateDesign(ctx, DesignInput{Code: "LR001", CategoryID: cats[0].ID})
	if !errors.Is(err, ErrConflict) || !strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "Key (") {
		t.Fatalf("duplicate: %v", err)
	}
	// A deleted design keeps its code reserved.
	if err := s.DeleteDesign(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateDesign(ctx, DesignInput{Code: "LR001", CategoryID: cats[0].ID}); !errors.Is(err, ErrConflict) {
		t.Errorf("reusing a deleted design's code: %v", err)
	}
	if list, _ := s.ListDesigns(ctx, DesignFilter{}); len(list) != 0 {
		t.Errorf("deleted design still listed: %s", codes(list))
	}
	if _, err := s.GetDesignByCode(ctx, "LR001"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted design still fetchable: %v", err)
	}
}

func TestDeleteVariation(t *testing.T) {
	s := newTestService(t)
	mem := storage.NewMemory()
	s.Store = mem
	ctx := context.Background()
	seedFixtureMatrix(t, s)
	des := newDesign(t, s, "LR060")
	v1, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("200")})
	v2, _ := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("300")})
	if _, err := s.SetSeats(ctx, v2.ID, fixtureSeats()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v2.ID, "a.3dm", bytes.NewReader([]byte("3dm data"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v2.ID, "a.stl", bytes.NewReader([]byte("stl data"))); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UploadCAD(ctx, v1.ID, "keep.3dm", bytes.NewReader([]byte("other"))); err != nil {
		t.Fatal(err)
	}
	if len(mem.Keys()) != 3 {
		t.Fatalf("objects before delete = %d", len(mem.Keys()))
	}

	if err := s.DeleteVariation(ctx, v2.ID); err != nil {
		t.Fatal(err)
	}
	// Its stored objects are removed; the sibling's file is untouched.
	keys := mem.Keys()
	if len(keys) != 1 || !strings.Contains(keys[0], "LR060-V1") {
		t.Errorf("objects after delete = %v", keys)
	}
	// Seats and weights went with the row.
	var seats, weights int
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM variation_stone_seats WHERE variation_id=$1", v2.ID).Scan(&seats)     //nolint:errcheck
	s.Pool.QueryRow(ctx, "SELECT count(*) FROM variation_metal_weights WHERE variation_id=$1", v2.ID).Scan(&weights) //nolint:errcheck
	if seats != 0 || weights != 0 {
		t.Errorf("orphans: %d seats, %d weights", seats, weights)
	}
	got, _ := s.GetDesignByCode(ctx, "LR060")
	if len(got.Variations) != 1 || got.Variations[0].Code != "LR060-V1" {
		t.Errorf("variations = %+v", got.Variations)
	}
	if err := s.DeleteVariation(ctx, v2.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("delete again: %v", err)
	}

	// The freed number is reused for the next variation (seq = highest + 1).
	again, err := s.CreateVariation(ctx, des.ID, BaseInput{VolumeMM3: dp("100")})
	if err != nil || again.Code != "LR060-V2" {
		t.Errorf("next variation = %+v err=%v", again.Code, err)
	}

	// Without a store configured, deleting still works.
	s.Store = nil
	if err := s.DeleteVariation(ctx, again.ID); err != nil {
		t.Errorf("delete without a store: %v", err)
	}
}
