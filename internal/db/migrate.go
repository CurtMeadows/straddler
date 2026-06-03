package db

import (
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5" // registers the pgx/v5 driver
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// MigrateUp applies all pending migrations.
func MigrateUp(dsn string) error {
	m, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	if err := m.Up(); errors.Is(err, migrate.ErrNoChange) {
		return nil
	} else if err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}

// MigrateDown rolls back migrations.
// steps > 0 limits how many are rolled back; 0 rolls back all.
func MigrateDown(dsn string, steps int) error {
	m, err := newMigrator(dsn)
	if err != nil {
		return err
	}
	defer func() { _, _ = m.Close() }()

	var migrateErr error
	if steps > 0 {
		migrateErr = m.Steps(-steps)
	} else {
		migrateErr = m.Down()
	}

	if errors.Is(migrateErr, migrate.ErrNoChange) {
		return nil
	}
	if migrateErr != nil {
		return fmt.Errorf("migrate down: %w", migrateErr)
	}
	return nil
}

// MigrateVersion returns the current applied migration version.
// Returns (0, false, nil) when no migrations have been applied yet.
func MigrateVersion(dsn string) (uint, bool, error) {
	m, err := newMigrator(dsn)
	if err != nil {
		return 0, false, err
	}
	defer func() { _, _ = m.Close() }()

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get migration version: %w", err)
	}
	return v, dirty, nil
}

func newMigrator(dsn string) (*migrate.Migrate, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("load embedded migrations: %w", err)
	}
	// golang-migrate's pgx/v5 driver expects "pgx5://" scheme.
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+stripScheme(dsn))
	if err != nil {
		return nil, fmt.Errorf("create migrator: %w", err)
	}
	return m, nil
}

func stripScheme(dsn string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dsn, prefix) {
			return dsn[len(prefix):]
		}
	}
	return dsn
}
