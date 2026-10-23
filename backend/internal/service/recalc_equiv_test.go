package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// TestBulkRecalcMatchesPerVariation proves the set-based rebuild stores exactly what the per-variation
// calculation stores, on a few thousand generated variations (random volumes and reference weights,
// 0 to 5 seats each, locked seats, matrix gaps) under several master-data changes.
func TestBulkRecalcMatchesPerVariation(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	seedScale(t, s, 100, 0.17)

	scenarios := []struct{ name, sql string }{
		{"as seeded", `SELECT 1`},
		{"metal SG changed", `UPDATE metals SET specific_gravity = specific_gravity + 0.37 WHERE code = '18KY'`},
		{"baseline code is 2HD", `UPDATE stone_codes SET is_default_baseline = (code = '2HD')`},
		{"baseline matrix carats changed", `UPDATE stone_size_matrix SET carats_per_stone = carats_per_stone + 0.0013
			WHERE stone_code_id = (SELECT id FROM stone_codes WHERE is_default_baseline)`},
		{"a metal made inactive", `UPDATE metals SET active = false WHERE code = '22KY'`},
		{"no default input metal", `UPDATE metals SET is_default_input = false`},
		{"no baseline code", `UPDATE stone_codes SET is_default_baseline = false`},
		{"metal back, baseline back", `UPDATE metals SET active = true, is_default_input = (code = '18KY');
			UPDATE stone_codes SET is_default_baseline = (code = '1HD')`},
	}
	for _, sc := range scenarios {
		if _, err := s.Pool.Exec(ctx, sc.sql); err != nil {
			t.Fatalf("%s: %v", sc.name, err)
		}

		// reference: the single-variation rebuild, one variation at a time
		ids, err := s.Q.ListAllVariationIDs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range ids {
			if err := s.inTx(ctx, func(q *db.Queries) error { return RecalcVariation(ctx, q, id) }); err != nil {
				t.Fatalf("%s: variation %d: %v", sc.name, id, err)
			}
		}
		want := snapshotWeights(t, s)

		// scramble the cache so the bulk path has to repair every row, then run it as the worker does
		if _, err := s.Pool.Exec(ctx, `DELETE FROM variation_metal_weights WHERE variation_id % 3 = 0;
			UPDATE variation_metal_weights SET net_weight_g = 999 WHERE variation_id % 3 = 1;
			UPDATE cad_variations SET baseline_stone_carats = 7, baseline_gross_weight_g = 7 WHERE id % 2 = 0`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Q.EnqueueRecalcJob(ctx, "metal_sg"); err != nil {
			t.Fatal(err)
		}
		if did, err := ProcessNextRecalcJob(ctx, s.Pool); err != nil || !did {
			t.Fatalf("%s: job did=%v err=%v", sc.name, did, err)
		}
		got := snapshotWeights(t, s)
		if !reflect.DeepEqual(got, want) {
			for i := range want {
				if i >= len(got) || got[i] != want[i] {
					t.Fatalf("%s: variation row %d differs\n per-variation: %s\n bulk:          %s", sc.name, i, want[i], got[min(i, len(got)-1)])
				}
			}
			t.Fatalf("%s: row counts differ: %d vs %d", sc.name, len(want), len(got))
		}

		// independent check of the volume-based nets against SQL's own arithmetic
		var bad int
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM cad_variations v
			JOIN variation_metal_weights w ON w.variation_id = v.id JOIN metals m ON m.id = w.metal_id
			WHERE v.base_volume_mm3 IS NOT NULL AND w.net_weight_g <> round(v.base_volume_mm3 * m.specific_gravity / 1000, 3)`).Scan(&bad); err != nil {
			t.Fatal(err)
		}
		if bad != 0 {
			t.Fatalf("%s: %d volume nets differ from volume x SG / 1000", sc.name, bad)
		}
		var nw, nm int
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM variation_metal_weights`).Scan(&nw)
		_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM metals WHERE active`).Scan(&nm)
		if nv := len(want); nw != nv*nm {
			t.Fatalf("%s: %d weight rows, want %d variations x %d active metals", sc.name, nw, nv, nm)
		}
		t.Logf("%s: %d variations identical", sc.name, len(want))
	}
}
