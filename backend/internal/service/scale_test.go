package service

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
)

// seedScale fills the test database with `designs` designs of three variations each, with 0 to 5 seats
// per variation (about one in ten locked to 2HD), a 1HD matrix that has deliberate gaps (so some
// variations get a NULL gross) and a mix of volume and reference-weight inputs. It is plain SQL so seeding
// 15,000 variations takes seconds, and deterministic for a given seed.
func seedScale(t *testing.T, s *Service, designs int, seed float64) {
	t.Helper()
	ctx := context.Background()
	stmts := []string{
		fmt.Sprintf("SELECT setseed(%v)", seed),
		`UPDATE metals SET is_default_input = (code = '18KR')`,
		// sizes 1.00 .. 4.00; 1HD lacks 3.70 and 3.90, 2HD has every size
		`INSERT INTO stone_size_matrix (size_mm, stone_code_id, carats_per_stone)
		 SELECT sz, sc.id, round((sz * sz / 400.0 + random() / 100)::numeric, 4)
		 FROM generate_series(100, 400, 10) AS g(n), LATERAL (SELECT (n / 100.0)::numeric(5,2) AS sz) x
		 CROSS JOIN stone_codes sc
		 WHERE NOT (sc.code = '1HD' AND sz IN (3.70, 3.90))`,
		fmt.Sprintf(`INSERT INTO master_designs (code, name, category_id)
		 SELECT 'SC' || lpad(n::text, 6, '0'), 'scale ' || n, (SELECT min(id) FROM categories)
		 FROM generate_series(1, %d) n`, designs),
		`INSERT INTO cad_variations (design_id, seq, code, base_volume_mm3, base_ref_weight_g, base_ref_metal_id)
		 SELECT d.id, v.seq, d.code || '-V' || v.seq,
		   CASE WHEN (d.id + v.seq) % 2 = 0 THEN round((50 + random() * 900)::numeric, 3) END,
		   CASE WHEN (d.id + v.seq) % 2 = 1 THEN round((1 + random() * 20)::numeric, 3) END,
		   CASE WHEN (d.id + v.seq) % 2 = 1 THEN (SELECT id FROM metals ORDER BY code OFFSET ((d.id + v.seq) / 2 % 3) LIMIT 1) END
		 FROM master_designs d CROSS JOIN generate_series(1, 3) v(seq) WHERE d.code LIKE 'SC%'`,
		`INSERT INTO variation_stone_seats (variation_id, position, size_mm, stone_count, locked_stone_code_id)
		 SELECT v.id, p.pos, x.sz, 1 + (random() * 30)::int,
		        CASE WHEN random() < 0.1 THEN (SELECT id FROM stone_codes WHERE code = '2HD') END
		 FROM cad_variations v
		 CROSS JOIN LATERAL generate_series(1, (random() * 5)::int) p(pos)
		 CROSS JOIN LATERAL (SELECT (100 + 10 * (random() * 30)::int) / 100.0 AS sz) x
		 WHERE v.code LIKE 'SC%'`,
		`ANALYZE`,
	}
	for _, q := range stmts {
		if _, err := s.Pool.Exec(ctx, q); err != nil {
			t.Fatalf("seed: %v\n%s", err, q)
		}
	}
}

// snapshotWeights returns a stable text dump of every cached weight and rollup for comparison.
func snapshotWeights(t *testing.T, s *Service) []string {
	t.Helper()
	rows, err := s.Pool.Query(context.Background(), `
		SELECT v.id::text || '|' || v.baseline_stone_carats::text || '|' || coalesce(v.baseline_gross_weight_g::text, 'NULL')
		       || '|' || coalesce((SELECT string_agg(w.metal_id::text || ':' || w.net_weight_g::text || ':' ||
		            coalesce(w.gross_weight_g::text, 'NULL'), ',' ORDER BY w.metal_id)
		          FROM variation_metal_weights w WHERE w.variation_id = v.id), '')
		FROM cad_variations v ORDER BY v.id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

// TestScaleRecalc is the reproducible scale benchmark. It is not part of the default run:
//
//	SCALE_TEST=1 SCALE_DESIGNS=5000 TEST_DATABASE_URL=... go test ./internal/service -run TestScaleRecalc -v -count=1
//
// It seeds SCALE_DESIGNS designs (three variations each; default 5000 = 15,000 variations), times a full
// rebuild queued the way an admin edit queues it (reason metal_sg), and times the catalog list queries.
func TestScaleRecalc(t *testing.T) {
	if os.Getenv("SCALE_TEST") == "" {
		t.Skip("set SCALE_TEST=1 to run the scale benchmark")
	}
	designs := 5000
	if v, err := strconv.Atoi(os.Getenv("SCALE_DESIGNS")); err == nil && v > 0 {
		designs = v
	}
	s := newTestService(t)
	ctx := context.Background()

	t0 := time.Now()
	seedScale(t, s, designs, 0.42)
	var nv, ns int
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM cad_variations`).Scan(&nv)
	_ = s.Pool.QueryRow(ctx, `SELECT count(*) FROM variation_stone_seats`).Scan(&ns)
	t.Logf("seeded %d designs, %d variations, %d seats in %v", designs, nv, ns, time.Since(t0).Round(time.Millisecond))

	rebuild := func(label string) {
		if _, err := db.New(s.Pool).EnqueueRecalcJob(ctx, "metal_sg"); err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		did, err := ProcessNextRecalcJob(ctx, s.Pool)
		if err != nil || !did {
			t.Fatalf("recalc: did=%v err=%v", did, err)
		}
		t.Logf("%s: full rebuild of %d variations took %v", label, nv, time.Since(start).Round(time.Millisecond))
	}
	rebuild("first (cache empty)")
	rebuild("second (cache warm, nothing changed)")

	// the same list queries the catalog screen sends
	cat := int64(1)
	for _, c := range []struct {
		name string
		f    DesignFilter
	}{
		{"list page 1", DesignFilter{Limit: 50}},
		{"list deep page", DesignFilter{Limit: 50, Offset: int32(designs - 100)}},
		{"list text search", DesignFilter{Query: "SC0042", Limit: 50}},
		{"list category+metal+weight", DesignFilter{CategoryID: &cat, MetalCode: "18KR", MinNetWt: dp("3"), MaxNetWt: dp("9"), Limit: 50}},
		{"list max stone cts", DesignFilter{MaxStoneCts: dp("0.5"), Limit: 50}},
		{"list metal only", DesignFilter{MetalCode: "18KR", Limit: 50}},
		{"list 18KR 2HD stone 2-6 pct", DesignFilter{MetalCode: "18KR", StoneCode: "2HD", MinStonePct: dp("2"), MaxStonePct: dp("6"), Limit: 50}},
		{"list 18KR 1HD stone 2-6 pct + weight", DesignFilter{MetalCode: "18KR", StoneCode: "1HD", MinStonePct: dp("2"), MaxStonePct: dp("6"), MinGrossWt: dp("3"), MaxGrossWt: dp("9"), Limit: 50}},
		{"list sort stone pct desc", DesignFilter{MetalCode: "22KY", StoneCode: "2HD", Sort: "stone_pct_desc", Limit: 50}},
		{"list sort net asc, deep page", DesignFilter{MetalCode: "22KY", Sort: "net_asc", Limit: 50, Offset: int32(designs - 100)}},
	} {
		start := time.Now()
		items, err := s.ListDesigns(ctx, c.f)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("%s: %d rows in %v", c.name, len(items), time.Since(start).Round(time.Millisecond))
	}
}
