package postgres

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"trilli/system/logging"
)

// MigrateUp applies all pending up migrations from the supplied embedded source.
// Idempotent: a no-op once the DB is at HEAD.
func (c *Client) MigrateUp(src fs.FS, root string) error {
	m, err := c.newMigrator(src, root)
	if err != nil {
		return err
	}
	defer m.Close()

	logging.Info(packageName, "Applying migrations from %s", root)
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logging.Info(packageName, "Schema already at HEAD; no migrations to apply")
			return nil
		}
		return fmt.Errorf("migration up failed: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil {
		return fmt.Errorf("failed to read migration version: %w", err)
	}
	logging.Info(packageName, "Migrations applied: version=%d dirty=%v", version, dirty)
	return nil
}

// MigrateDown rolls back N migration steps. Provided for operator use through the
// `trilli migrate down` subcommand; never call this automatically.
func (c *Client) MigrateDown(src fs.FS, root string, steps int) error {
	if steps <= 0 {
		return fmt.Errorf("steps must be positive, got %d", steps)
	}
	m, err := c.newMigrator(src, root)
	if err != nil {
		return err
	}
	defer m.Close()

	logging.Info(packageName, "Rolling back %d migration step(s)", steps)
	if err := m.Steps(-steps); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logging.Info(packageName, "Nothing to roll back")
			return nil
		}
		return fmt.Errorf("migration down failed: %w", err)
	}
	return nil
}

// MigrateVersion returns the current applied version and dirty flag.
// Returns (0, false, nil) when no migrations have ever been applied.
func (c *Client) MigrateVersion(src fs.FS, root string) (uint, bool, error) {
	m, err := c.newMigrator(src, root)
	if err != nil {
		return 0, false, err
	}
	defer m.Close()

	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return v, dirty, nil
}

func (c *Client) newMigrator(src fs.FS, root string) (*migrate.Migrate, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no connection available")
	}

	driver, err := migratepg.WithInstance(c.db, &migratepg.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to create postgres migration driver: %w", err)
	}

	source, err := iofs.New(src, root)
	if err != nil {
		return nil, fmt.Errorf("failed to create migration source: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "postgres", driver)
	if err != nil {
		return nil, fmt.Errorf("failed to construct migrator: %w", err)
	}
	return m, nil
}
