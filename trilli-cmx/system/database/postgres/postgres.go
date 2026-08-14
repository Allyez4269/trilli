// Package postgres is the CMX database client. It is a port of the app's
// system/database/postgres client (same connection target — the shared TRILLI
// database) adapted to the trilli-cmx module.
//
// CMX is a second reader/writer on the same database as app.trilli.com. To keep
// the two services from clobbering each other's migration state, CMX migrations
// are tracked in a SEPARATE table (cmx_schema_migrations) — see migrate.go.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"trilli-cmx/system/logging"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	packageName = "database/postgres"
)

// Config holds the configuration for the PostgreSQL connection.
type Config struct {
	Host       string
	Port       int
	Database   string
	Username   string
	Password   string
	SearchPath string
	// Connection pool settings
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
	// SSL settings
	SSLMode string
}

// DefaultConfig returns a Config built from the environment (CMX points at the
// SAME database as the app, so the TRILLI_DB_* variables are shared):
//
//	TRILLI_DB_HOST      (default "localhost")
//	TRILLI_DB_PORT      (default 5432)
//	TRILLI_DB_NAME      (default "trilli")
//	TRILLI_DB_USER      (default "postgres")
//	TRILLI_DB_PASSWORD  (no default)
//	TRILLI_DB_SSLMODE   (default "require"; use "disable" for local dev)
//
// CMX is a lighter consumer than the app, so the pool is sized smaller.
func DefaultConfig() *Config {
	port := defaultPort
	if v := strings.TrimSpace(os.Getenv("TRILLI_DB_PORT")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			port = n
		}
	}
	return &Config{
		Host:            envOr("TRILLI_DB_HOST", defaultHost),
		Port:            port,
		Database:        envOr("TRILLI_DB_NAME", defaultDatabase),
		Username:        envOr("TRILLI_DB_USER", defaultUsername),
		Password:        os.Getenv("TRILLI_DB_PASSWORD"),
		SearchPath:      "public",
		MaxOpenConns:    defaultMaxOpenConns,
		MaxIdleConns:    defaultMaxIdleConns,
		ConnMaxLifetime: defaultConnMaxLifetime,
		ConnMaxIdleTime: defaultConnMaxIdleTime,
		SSLMode:         envOr("TRILLI_DB_SSLMODE", "require"),
	}
}

// envOr returns the trimmed value of the environment variable key, or fallback
// when unset/empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Connection defaults (overridable via the TRILLI_DB_* environment variables).
const (
	defaultHost            = "localhost"
	defaultPort            = 5432
	defaultDatabase        = "trilli"
	defaultUsername        = "postgres"
	defaultMaxOpenConns    = 25
	defaultMaxIdleConns    = 10
	defaultConnMaxLifetime = 1 * time.Hour
	defaultConnMaxIdleTime = 15 * time.Minute
)

// Client represents a PostgreSQL database client.
type Client struct {
	db  *sql.DB
	cfg *Config
}

// connString builds the pgx DSN for a Config.
func connString(cfg *Config) string {
	return fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s search_path=%s timezone=America/New_York",
		cfg.Host, cfg.Port, cfg.Database, cfg.Username, cfg.Password, cfg.SSLMode, cfg.SearchPath)
}

// openRaw opens a standalone *sql.DB from cfg. Used for the migrator's
// dedicated connection (which golang-migrate closes on m.Close()), so the main
// serving pool is never affected by a migration run.
func openRaw(cfg *Config) (*sql.DB, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}
	return sql.Open("pgx", connString(cfg))
}

// NewClient creates a new PostgreSQL client with the provided configuration.
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	logging.Debug(packageName, "Creating new PostgreSQL client with config: Host=%s, Database=%s", cfg.Host, cfg.Database)

	logging.Debug(packageName, "Opening connection to PostgreSQL at %s:%d", cfg.Host, cfg.Port)
	db, err := sql.Open("pgx", connString(cfg))
	if err != nil {
		logging.Error(packageName, "Failed to open connection to PostgreSQL: %v", err)
		return nil, fmt.Errorf("failed to open connection to PostgreSQL: %w", err)
	}

	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	logging.Debug(packageName, "Configured connection pool: MaxOpen=%d, MaxIdle=%d, Lifetime=%v, IdleTime=%v",
		cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logging.Debug(packageName, "Pinging PostgreSQL to verify connection")
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		logging.Error(packageName, "Failed to ping PostgreSQL: %v", err)
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	logging.Info(packageName, "Connected to PostgreSQL at %s:%d, database: %s", cfg.Host, cfg.Port, cfg.Database)

	return &Client{db: db, cfg: cfg}, nil
}

// Close closes the database connection.
func (c *Client) Close() error {
	if c.db != nil {
		logging.Debug(packageName, "Closing PostgreSQL connection")
		return c.db.Close()
	}
	return nil
}

// Ping checks the database connection.
func (c *Client) Ping() error {
	if c.db == nil {
		return fmt.Errorf("no connection available")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.db.PingContext(ctx); err != nil {
		logging.Error(packageName, "Ping failed: %v", err)
		return err
	}
	return nil
}

// Exec executes a query without returning any rows.
func (c *Client) Exec(query string, args ...interface{}) (sql.Result, error) {
	return c.ExecContext(context.Background(), query, args...)
}

// ExecContext executes a query without returning any rows with context.
func (c *Client) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no connection available")
	}
	logging.Debug(packageName, "Executing query: %s", query)
	result, err := c.db.ExecContext(ctx, query, args...)
	if err != nil {
		logging.Error(packageName, "Query execution failed: %v", err)
		return nil, err
	}
	return result, nil
}

// Query executes a query that returns rows.
func (c *Client) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return c.QueryContext(context.Background(), query, args...)
}

// QueryContext executes a query that returns rows with context.
func (c *Client) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no connection available")
	}
	logging.Debug(packageName, "Executing query: %s", query)
	rows, err := c.db.QueryContext(ctx, query, args...)
	if err != nil {
		logging.Error(packageName, "Query execution failed: %v", err)
		return nil, err
	}
	return rows, nil
}

// QueryRow executes a query that is expected to return at most one row.
func (c *Client) QueryRow(query string, args ...interface{}) *sql.Row {
	return c.QueryRowContext(context.Background(), query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row with context.
func (c *Client) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if c.db == nil {
		return &sql.Row{}
	}
	logging.Debug(packageName, "Executing single row query: %s", query)
	return c.db.QueryRowContext(ctx, query, args...)
}

// Begin starts a transaction.
func (c *Client) Begin() (*sql.Tx, error) {
	return c.BeginTx(context.Background(), nil)
}

// BeginTx starts a transaction with the given context and options.
func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no connection available")
	}
	tx, err := c.db.BeginTx(ctx, opts)
	if err != nil {
		logging.Error(packageName, "Failed to begin transaction: %v", err)
		return nil, err
	}
	return tx, nil
}

// GetDB returns the underlying sql.DB instance.
func (c *Client) GetDB() *sql.DB {
	return c.db
}

// Stats returns database statistics.
func (c *Client) Stats() sql.DBStats {
	if c.db == nil {
		return sql.DBStats{}
	}
	return c.db.Stats()
}

// quoteIdentifier quotes a PostgreSQL identifier using double quotes.
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

var _ = quoteIdentifier // reserved for future batch helpers
