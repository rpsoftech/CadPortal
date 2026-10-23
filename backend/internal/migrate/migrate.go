// Package migrate applies the golang-migrate SQL migrations embedded from ../../migrations.
package migrate

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5"

	migrations "github.com/rpsoftech/cad-portal/backend/migrations"
)

// toURL converts a postgres:// URL to the pgx5:// scheme golang-migrate expects.
func toURL(dbURL string) string {
	for _, p := range []string{"postgresql://", "postgres://"} {
		if len(dbURL) > len(p) && dbURL[:len(p)] == p {
			return "pgx5://" + dbURL[len(p):]
		}
	}
	return dbURL
}

// lockKey is the session advisory lock that serialises Up across API instances starting together.
const lockKey int64 = 0x43414d47 // "CAMG"

// Up applies the pending migrations. Several API instances may call it at the same moment (a scaled
// start, a rolling restart): they queue on a Postgres advisory lock, so one migrates and the others find
// nothing left to do, instead of racing on the schema_migrations table.
func Up(dbURL string) error {
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	defer conn.Close(ctx) //nolint:errcheck
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("migrate lock: %w", err)
	}
	// Closing the connection releases the lock, even if the unlock itself fails.
	defer conn.Exec(ctx, "SELECT pg_advisory_unlock($1)", lockKey) //nolint:errcheck
	return up(dbURL)
}

func up(dbURL string) error {
	src, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return err
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, toURL(dbURL))
	if err != nil {
		return fmt.Errorf("migrate init: %w", err)
	}
	defer m.Close()
	if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
