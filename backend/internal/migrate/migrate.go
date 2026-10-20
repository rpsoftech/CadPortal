// Package migrate applies the golang-migrate SQL migrations embedded from ../../migrations.
package migrate

import (
	"errors"
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

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

func Up(dbURL string) error {
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
