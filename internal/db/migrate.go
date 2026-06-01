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

// Migrate applies or rolls back schema migrations.
//
//	direction = "up"   — apply all pending migrations
//	direction = "down" — roll back; steps > 0 limits how many, 0 means all
func Migrate(dsn, direction string, steps int) error {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return fmt.Errorf("load embedded migrations: %w", err)
	}

	// golang-migrate's pgx/v5 driver expects "pgx5://" scheme.
	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+stripScheme(dsn))
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	switch direction {
	case "up":
		err = m.Up()
	case "down":
		if steps > 0 {
			err = m.Steps(-steps)
		} else {
			err = m.Down()
		}
	default:
		return fmt.Errorf("direction must be 'up' or 'down', got %q", direction)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		return nil // nothing to do — not an error
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", direction, err)
	}
	return nil
}

// MigrateVersion returns the current applied migration version number.
// Returns (0, false, nil) when no migrations have been applied yet.
func MigrateVersion(dsn string) (uint, bool, error) {
	src, err := iofs.New(migrationsFS, "migrations")
	if err != nil {
		return 0, false, fmt.Errorf("load embedded migrations: %w", err)
	}

	m, err := migrate.NewWithSourceInstance("iofs", src, "pgx5://"+stripScheme(dsn))
	if err != nil {
		return 0, false, fmt.Errorf("create migrator: %w", err)
	}
	defer m.Close()

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("get migration version: %w", err)
	}
	return v, dirty, nil
}

// stripScheme removes any existing postgres scheme prefix so we can
// prepend "pgx5://" without doubling it.
func stripScheme(dsn string) string {
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dsn, prefix) {
			return dsn[len(prefix):]
		}
	}
	return dsn
}
