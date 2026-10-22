package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/auth"
)

func TestStonePriceBasisOverHTTP(t *testing.T) {
	r, _, _ := newHTTPEnv(t)
	put := func(body string) int {
		return do(r, "PUT", "/api/v1/masters/stone-matrix", token(t, auth.Admin), "application/json", []byte(body)).Code
	}
	if c := put(`{"rows":[
		{"size_mm":"1.10","stone_code":"2HD","carats_per_stone":"0.0070","rate_per_pc":"100","rate_basis":"pc"},
		{"size_mm":"1.20","stone_code":"2HD","carats_per_stone":"0.0095","rate_per_ct":"50000"},
		{"size_mm":"1.30","stone_code":"2HD","carats_per_stone":"0.0100"}]}`); c != 202 {
		t.Fatalf("put: %d", c)
	}
	type row struct {
		Size      string  `json:"size_mm"`
		Basis     string  `json:"rate_basis"`
		RatePerPc *string `json:"rate_per_pc"`
		RatePerCt *string `json:"rate_per_ct"`
	}
	list := func(role auth.Role) map[string]row {
		w := do(r, "GET", "/api/v1/masters/stone-matrix", token(t, role), "", nil)
		var rows []row
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &rows) != nil {
			t.Fatalf("list: %d %s", w.Code, w.Body)
		}
		m := map[string]row{}
		for _, x := range rows {
			m[x.Size] = x
		}
		return m
	}
	a := list(auth.Admin)
	if a["1.1"].Basis != "pc" || a["1.1"].RatePerPc == nil || *a["1.1"].RatePerPc != "100" {
		t.Errorf("per piece row = %+v", a["1.1"])
	}
	// A per-carat price sent the old way (no basis) keeps meaning per carat.
	if a["1.2"].Basis != "ct" || a["1.2"].RatePerCt == nil {
		t.Errorf("legacy row = %+v", a["1.2"])
	}
	// No price at all: per piece, the default.
	if a["1.3"].Basis != "pc" || a["1.3"].RatePerPc != nil {
		t.Errorf("unpriced row = %+v", a["1.3"])
	}
	// Production sees how stones are priced but never a price.
	for _, x := range list(auth.Production) {
		if x.RatePerPc != nil || x.RatePerCt != nil {
			t.Errorf("production sees a price: %+v", x)
		}
	}
	bad := do(r, "PUT", "/api/v1/masters/stone-matrix", token(t, auth.Admin), "application/json",
		[]byte(`{"rows":[{"size_mm":"1.10","stone_code":"2HD","carats_per_stone":"0.007","rate_basis":"gram"}]}`))
	if bad.Code != 400 || !strings.Contains(bad.Body.String(), "rate_basis") {
		t.Errorf("unknown basis: %d %s", bad.Code, bad.Body)
	}
}
