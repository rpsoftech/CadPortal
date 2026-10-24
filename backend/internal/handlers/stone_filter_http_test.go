package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/service"
)

func TestCatalogStoneFilterOverHTTP(t *testing.T) {
	r, svc := newAuthEnv(t)
	mkUser(t, svc, "production@example.com", "production")
	tok := bearer(t, r, "production@example.com")
	ctx := context.Background()
	cats, _ := svc.ListCategories(ctx)
	if err := svc.UpdateStoneMatrix(ctx, []service.MatrixRowInput{
		{SizeMM: mustDec("1.10"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0070")},
		{SizeMM: mustDec("1.20"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0095")},
		{SizeMM: mustDec("2.50"), StoneCode: "2HD", CaratsPerStone: mustDec("0.0650")},
	}, "2HD"); err != nil {
		t.Fatal(err)
	}
	des, err := svc.CreateDesign(ctx, service.DesignInput{Code: "LR001", Name: "Ring", CategoryID: cats[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	vol := mustDec("200")
	v, err := svc.CreateVariation(ctx, des.ID, service.BaseInput{VolumeMM3: &vol})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SetSeats(ctx, v.ID, []service.SeatInput{
		{SizeMM: mustDec("1.10"), StoneCount: 24},
		{SizeMM: mustDec("1.20"), StoneCount: 12},
		{SizeMM: mustDec("2.50"), StoneCount: 1, LockedStoneCode: "2HD"},
	}); err != nil {
		t.Fatal(err)
	}

	type card struct {
		Code     string
		StonePct *string `json:"stone_pct"`
		GrossG   *string `json:"gross_g"`
		Status   string  `json:"stone_status"`
		Shown    any     `json:"shown"`
	}
	get := func(query string) (int, []card, string) {
		w := do(r, "GET", "/api/v1/designs"+query, tok, "", nil)
		var rows []card
		if w.Code == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, rows, w.Body.String()
	}

	code, rows, body := get("?metal_code=18KR&stone_code=2HD&min_stone_pct=2.23&max_stone_pct=2.23")
	if code != 200 || len(rows) != 1 || rows[0].StonePct == nil || *rows[0].StonePct != "2.23" ||
		rows[0].GrossG == nil || *rows[0].GrossG != "3.119" || rows[0].Status != "ok" {
		t.Fatalf("production list: %d %s", code, body)
	}
	if rows[0].Shown != nil {
		t.Errorf("the raw shown column must not leak: %s", body)
	}
	for _, money := range []string{"wastage", "rate_per", "price", "cost"} {
		if strings.Contains(strings.ToLower(body), money) {
			t.Errorf("production list shows %q: %s", money, body)
		}
	}
	if _, rows, _ = get("?metal_code=18KR&stone_code=2HD&min_stone_pct=2.24"); len(rows) != 0 {
		t.Errorf("min above the value matched %d rows", len(rows))
	}
	if _, rows, _ = get("?metal_code=18KR&stone_code=1HD&min_stone_pct=0"); len(rows) != 0 {
		t.Errorf("1HD has no matrix rows: the design must be excluded, got %d", len(rows))
	}
	if _, rows, _ = get("?metal_code=18KR&stone_code=1HD"); len(rows) != 1 || rows[0].Status != "no_data" || rows[0].StonePct != nil {
		t.Errorf("no data must be flagged, not 0: %+v", rows)
	}
	for _, bad := range []string{
		"?metal_code=18KR&min_stone_pct=abc", "?metal_code=18KR&max_stone_pct=101", "?metal_code=18KR&stone_code=ZZZ",
		"?min_stone_pct=1", "?sort=stone_pct_asc", "?metal_code=18KR&sort=weight",
	} {
		if c, _, _ := get(bad); c != 400 {
			t.Errorf("GET %s = %d, want 400", bad, c)
		}
	}
}
