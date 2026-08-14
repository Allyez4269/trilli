package postgres

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/golang-migrate/migrate/v4"
	migratepg "github.com/golang-migrate/migrate/v4/database/postgres"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"trilli-cmx/system/logging"
)

// migrationsTable is CMX's OWN migration bookkeeping table. It is deliberately
// distinct from the app's default `schema_migrations` so that running CMX
// migrations against the shared TRILLI database never reads, writes, or
// truncates the app's migration state. The two services version independently.
const migrationsTable = "cmx_schema_migrations"

// MigrateUp applies all pending CMX up migrations from the supplied embedded
// source. Idempotent: a no-op once the CMX schema is at HEAD.
func (c *Client) MigrateUp(src fs.FS, root string) error {
	m, err := c.newMigrator(src, root)
	if err != nil {
		return err
	}
	defer m.Close()

	logging.Info(packageName, "Applying CMX migrations from %s (table=%s)", root, migrationsTable)
	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logging.Info(packageName, "CMX schema already at HEAD; no migrations to apply")
			return nil
		}
		return fmt.Errorf("migration up failed: %w", err)
	}

	version, dirty, err := m.Version()
	if err != nil {
		return fmt.Errorf("failed to read migration version: %w", err)
	}
	logging.Info(packageName, "CMX migrations applied: version=%d dirty=%v", version, dirty)
	return nil
}

// MigrateDown rolls back N CMX migration steps. Operator-only.
func (c *Client) MigrateDown(src fs.FS, root string, steps int) error {
	if steps <= 0 {
		return fmt.Errorf("steps must be positive, got %d", steps)
	}
	m, err := c.newMigrator(src, root)
	if err != nil {
		return err
	}
	defer m.Close()

	logging.Info(packageName, "Rolling back %d CMX migration step(s)", steps)
	if err := m.Steps(-steps); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			logging.Info(packageName, "Nothing to roll back")
			return nil
		}
		return fmt.Errorf("migration down failed: %w", err)
	}
	return nil
}

// MigrateVersion returns the current applied CMX version and dirty flag.
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
	if c.cfg == nil {
		return nil, fmt.Errorf("no configuration available")
	}

	// IMPORTANT: golang-migrate's m.Close() closes the *sql.DB it is handed. So
	// the migrator gets its OWN short-lived connection, never the main serving
	// pool — otherwise running migrations on boot would close the pool the HTTP
	// server depends on.
	mdb, err := openRaw(c.cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open migration connection: %w", err)
	}

	driver, err := migratepg.WithInstance(mdb, &migratepg.Config{
		MigrationsTable: migrationsTable,
	})
	if err != nil {
		mdb.Close()
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
