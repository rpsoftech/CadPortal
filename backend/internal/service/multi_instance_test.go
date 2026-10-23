package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/db"
	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
	"github.com/rpsoftech/cad-portal/backend/internal/storage"
)

// These tests stand in for "two API instances": two Services (or goroutines) on one database, doing at
// the same moment what the instances' background jobs and requests do.

type countingStore struct {
	*storage.Memory
	mu      sync.Mutex
	deletes map[string]int
}

func (c *countingStore) Delete(ctx context.Context, key string) error {
	c.mu.Lock()
	c.deletes[key]++
	c.mu.Unlock()
	return c.Memory.Delete(ctx, key)
}

func TestTwoInstancesPurgeTrashWithoutDoubleDelete(t *testing.T) {
	a := newTestService(t)
	b := New(a.Pool)
	ctx := context.Background()
	store := &countingStore{Memory: storage.NewMemory(), deletes: map[string]int{}}
	a.Store, b.Store = store, store
	const n = 450
	if _, err := a.Pool.Exec(ctx, fmt.Sprintf(`INSERT INTO trashed_files (kind, object_key, label, reason, purge_after)
		SELECT 'cad', 'cad/x/' || i, 'f' || i, 'removed', now() - interval '1 minute' FROM generate_series(1, %d) i`, n)); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var total atomic.Int64
	for _, s := range []*Service{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := s.PurgeTrash(ctx)
			if err != nil {
				t.Errorf("purge: %v", err)
			}
			total.Add(int64(got))
		}()
	}
	wg.Wait()
	if total.Load() != n {
		t.Errorf("purges reported %d deletions, want exactly %d", total.Load(), n)
	}
	for k, c := range store.deletes {
		if c != 1 {
			t.Errorf("%s was deleted %d times", k, c)
		}
	}
	if len(store.deletes) != n {
		t.Errorf("%d objects deleted, want %d", len(store.deletes), n)
	}
}

func TestTwoInstancesPruneAuditOnce(t *testing.T) {
	a := newTestService(t)
	b := New(a.Pool)
	ctx := context.Background()
	if _, err := a.Pool.Exec(ctx, `INSERT INTO audit_log (user_email, action, entity_type, status, at)
		SELECT 'x@example.com', 'test.old', 'x', 200, now() - interval '400 days' FROM generate_series(1, 5000)`); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var total atomic.Int64
	for _, s := range []*Service{a, b, a, b} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := s.PruneAudit(ctx, time.Now().AddDate(0, 0, -30))
			if err != nil {
				t.Errorf("prune: %v", err)
			}
			total.Add(n)
		}()
	}
	wg.Wait()
	if total.Load() != 5000 {
		t.Errorf("pruned %d, want 5000", total.Load())
	}
}

func TestOnlyOneInstanceClaimsTheHeartbeatSlot(t *testing.T) {
	a := newTestService(t)
	ctx := context.Background()
	var wins atomic.Int64
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			n, err := db.New(a.Pool).ClaimMaintenanceSlot(ctx, db.ClaimMaintenanceSlotParams{Name: "heartbeat", MinGapSeconds: 270})
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			wins.Add(n)
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("%d instances claimed the slot, want 1", wins.Load())
	}
	// once the gap has passed the slot can be taken again (the next tick, or another instance if the first died)
	if _, err := a.Pool.Exec(ctx, `UPDATE maintenance_claims SET claimed_at = now() - interval '6 minutes'`); err != nil {
		t.Fatal(err)
	}
	n, err := db.New(a.Pool).ClaimMaintenanceSlot(ctx, db.ClaimMaintenanceSlotParams{Name: "heartbeat", MinGapSeconds: 270})
	if err != nil || n != 1 {
		t.Fatalf("claim after the gap: n=%d err=%v", n, err)
	}
}

func TestParallelDesignCreationsNeverShareACode(t *testing.T) {
	a := newTestService(t)
	b := New(a.Pool)
	ctx := context.Background()
	cat := int64(1)
	if _, err := a.Pool.Exec(ctx, `UPDATE categories SET code_prefix = 'LR' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	const n = 60
	var wg sync.WaitGroup
	codes := make(chan string, n)
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := a
			if i%2 == 1 {
				s = b
			}
			d, err := s.CreateDesign(ctx, DesignInput{CategoryID: cat})
			if err != nil {
				t.Errorf("create %d: %v", i, err)
				return
			}
			codes <- d.Code
		}()
	}
	wg.Wait()
	close(codes)
	seen := map[string]bool{}
	for c := range codes {
		if seen[c] {
			t.Errorf("code %s handed out twice", c)
		}
		seen[c] = true
	}
	if len(seen) != n {
		t.Errorf("%d distinct codes, want %d", len(seen), n)
	}
}

// Two instances starting together against an empty database both run migrate.Up.
func TestConcurrentMigrationsFromEmptyDatabase(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://cad:cad@localhost:5433/cad_portal_test?sslmode=disable"
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, url)
	if err != nil {
		dbUnavailable(t, err)
	}
	defer admin.Close()
	if err := admin.Ping(ctx); err != nil {
		dbUnavailable(t, err)
	}
	const name = "cad_portal_migrace_test"
	_, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name)
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(ctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)") })

	cfg, err := pgx.ParseConfig(url)
	if err != nil {
		t.Fatal(err)
	}
	target := strings.Replace(url, "/"+cfg.Database, "/"+name, 1)

	const instances = 4
	errs := make(chan error, instances)
	start := make(chan struct{})
	for range instances {
		go func() {
			<-start
			errs <- migrate.Up(target)
		}()
	}
	close(start)
	for range instances {
		if err := <-errs; err != nil {
			t.Errorf("migrate.Up: %v", err)
		}
	}
	pool, err := pgxpool.New(ctx, target)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var version int
	var dirty bool
	if err := pool.QueryRow(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || dirty || version < 17 {
		t.Fatalf("schema_migrations: version=%d dirty=%v err=%v", version, dirty, err)
	}
}

// dbUnavailable mirrors the rule of internal/testdb: in CI a database that is not there is a failure,
// elsewhere the test is skipped.
func dbUnavailable(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("test database unavailable in CI: %v", err)
	}
	t.Skipf("test database unavailable: %v", err)
}
