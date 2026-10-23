// Package testdb gives every Go test package that touches Postgres its own database.
//
// `go test ./...` runs packages in parallel. A package that wipes and re-migrates a
// database must therefore never share it with another package. The database name is
// derived from the calling package's directory (the working directory of its test
// binary), so two packages cannot end up on the same database by accident, and the
// database named by TEST_DATABASE_URL is only used as the admin connection: it is
// never wiped itself.
package testdb

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rpsoftech/cad-portal/backend/internal/migrate"
)

const defaultURL = "postgres://cad:cad@localhost:5433/cad_portal_test?sslmode=disable"

// createLockKey serialises CREATE DATABASE across test binaries: Postgres copies
// template1 and fails when two sessions do that at the same moment.
const createLockKey = 7301203

var nonName = regexp.MustCompile(`[^a-z0-9]+`)

// Name returns the database name for the package whose directory is dir:
// the last two path elements, e.g. .../internal/service -> cad_portal_internal_service_test.
func Name(dir string) string {
	dir = filepath.ToSlash(filepath.Clean(dir))
	parts := strings.Split(dir, "/")
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	s := nonName.ReplaceAllString(strings.ToLower(strings.Join(parts, "_")), "_")
	s = strings.Trim(s, "_")
	return "cad_portal_" + s + "_test"
}

// unavailable skips locally when there is no test database, but fails in CI (GitHub sets
// CI=true): a Postgres service that failed to start must not turn into a green run that
// tested nothing.
func unavailable(t testing.TB, err error) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatalf("test database unavailable in CI: %v", err)
	}
	t.Skipf("test database unavailable: %v", err)
}

// DSN makes sure the calling package's own database exists and returns its URL. It does
// not touch the contents.
func DSN(t testing.TB) string {
	t.Helper()
	base := os.Getenv("TEST_DATABASE_URL")
	if base == "" {
		base = defaultURL
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	name := Name(wd)
	u, err := url.Parse(base)
	if err != nil {
		t.Fatalf("TEST_DATABASE_URL: %v", err)
	}
	u.Path = "/" + name

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, base)
	if err == nil {
		err = admin.Ping(ctx)
	}
	if err != nil {
		unavailable(t, err)
	}
	defer admin.Close()
	conn, err := admin.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", createLockKey); err != nil {
		t.Fatal(err)
	}
	defer conn.Exec(context.Background(), "SELECT pg_advisory_unlock($1)", createLockKey) //nolint:errcheck
	var exists bool
	if err := conn.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := conn.Exec(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, name)); err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
	}
	return u.String()
}

// Reset returns a pool on the calling package's database, wiped and freshly migrated.
func Reset(t testing.TB) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := DSN(t)
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// Guard: only ever wipe a database whose name ends in _test.
	var name string
	if err := pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to reset non-test database %q", name)
	}
	if _, err := pool.Exec(ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := migrate.Up(dsn); err != nil {
		t.Fatal(err)
	}
	return pool, dsn
}
