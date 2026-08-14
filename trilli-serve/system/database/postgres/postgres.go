package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"trilli/system/logging"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	packageName = "database/postgres"
)

// Config holds the configuration for the PostgreSQL connection
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

// DefaultConfig returns a Config built from the environment:
//
//	TRILLI_DB_HOST      (default "localhost")
//	TRILLI_DB_PORT      (default 5432)
//	TRILLI_DB_NAME      (default "trilli")
//	TRILLI_DB_USER      (default "postgres")
//	TRILLI_DB_PASSWORD  (no default)
//	TRILLI_DB_SSLMODE   (default "require"; use "disable" for local dev)
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
	defaultMaxOpenConns    = 100
	defaultMaxIdleConns    = 25
	defaultConnMaxLifetime = 1 * time.Hour
	defaultConnMaxIdleTime = 15 * time.Minute
)

// Client represents a PostgreSQL database client
type Client struct {
	db  *sql.DB
	cfg *Config
}

// NewClient creates a new PostgreSQL client with the provided configuration
func NewClient(cfg *Config) (*Client, error) {
	if cfg == nil {
		cfg = DefaultConfig()
	}

	logging.Debug(packageName, "Creating new PostgreSQL client with config: Host=%s, Database=%s", cfg.Host, cfg.Database)

	// Build connection string for PostgreSQL
	connString := fmt.Sprintf("host=%s port=%d dbname=%s user=%s password=%s sslmode=%s search_path=%s timezone=America/New_York",
		cfg.Host, cfg.Port, cfg.Database, cfg.Username, cfg.Password, cfg.SSLMode, cfg.SearchPath)

	// Open connection using pgx stdlib driver
	logging.Debug(packageName, "Opening connection to PostgreSQL at %s:%d", cfg.Host, cfg.Port)
	db, err := sql.Open("pgx", connString)
	if err != nil {
		logging.Error(packageName, "Failed to open connection to PostgreSQL: %v", err)
		return nil, fmt.Errorf("failed to open connection to PostgreSQL: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(cfg.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.ConnMaxLifetime)
	db.SetConnMaxIdleTime(cfg.ConnMaxIdleTime)

	logging.Debug(packageName, "Configured connection pool: MaxOpen=%d, MaxIdle=%d, Lifetime=%v, IdleTime=%v",
		cfg.MaxOpenConns, cfg.MaxIdleConns, cfg.ConnMaxLifetime, cfg.ConnMaxIdleTime)

	// Verify connection
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	logging.Debug(packageName, "Pinging PostgreSQL to verify connection")
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		logging.Error(packageName, "Failed to ping PostgreSQL: %v", err)
		return nil, fmt.Errorf("failed to ping PostgreSQL: %w", err)
	}

	logging.Info(packageName, "Connected to PostgreSQL at %s:%d, database: %s", cfg.Host, cfg.Port, cfg.Database)

	return &Client{
		db:  db,
		cfg: cfg,
	}, nil
}

// Close closes the database connection
func (c *Client) Close() error {
	if c.db != nil {
		logging.Debug(packageName, "Closing PostgreSQL connection")
		return c.db.Close()
	}
	return nil
}

// Ping checks the database connection
func (c *Client) Ping() error {
	if c.db == nil {
		return fmt.Errorf("no connection available")
	}

	logging.Debug(packageName, "Pinging PostgreSQL")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := c.db.PingContext(ctx)
	if err != nil {
		logging.Error(packageName, "Ping failed: %v", err)
	}
	return err
}

// Exec executes a query without returning any rows
func (c *Client) Exec(query string, args ...interface{}) (sql.Result, error) {
	return c.ExecContext(context.Background(), query, args...)
}

// ExecContext executes a query without returning any rows with context
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

// Query executes a query that returns rows
func (c *Client) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return c.QueryContext(context.Background(), query, args...)
}

// QueryContext executes a query that returns rows with context
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

// QueryRow executes a query that is expected to return at most one row
func (c *Client) QueryRow(query string, args ...interface{}) *sql.Row {
	return c.QueryRowContext(context.Background(), query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row with context
func (c *Client) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	if c.db == nil {
		return &sql.Row{}
	}

	logging.Debug(packageName, "Executing single row query: %s", query)
	return c.db.QueryRowContext(ctx, query, args...)
}

// Begin starts a transaction
func (c *Client) Begin() (*sql.Tx, error) {
	return c.BeginTx(context.Background(), nil)
}

// BeginTx starts a transaction with the given context and options
func (c *Client) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	if c.db == nil {
		return nil, fmt.Errorf("no connection available")
	}

	logging.Debug(packageName, "Beginning transaction")
	tx, err := c.db.BeginTx(ctx, opts)
	if err != nil {
		logging.Error(packageName, "Failed to begin transaction: %v", err)
		return nil, err
	}
	return tx, nil
}

// InsertBatch performs batch insert operations
func (c *Client) InsertBatch(tableName string, columns []string, values [][]interface{}) error {
	return c.InsertBatchContext(context.Background(), tableName, columns, values)
}

// InsertBatchContext performs batch insert operations with context
func (c *Client) InsertBatchContext(ctx context.Context, tableName string, columns []string, values [][]interface{}) error {
	if c.db == nil {
		return fmt.Errorf("no connection available")
	}

	if len(values) == 0 {
		logging.Debug(packageName, "No values to insert for table %s", tableName)
		return nil
	}

	logging.Debug(packageName, "Preparing batch insert for table %s with %d rows", tableName, len(values))

	// Build the INSERT statement with PostgreSQL syntax
	quotedColumns := make([]string, len(columns))
	for i, col := range columns {
		quotedColumns[i] = quoteIdentifier(col)
	}

	placeholders := make([]string, len(columns))
	for i := range placeholders {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		quoteIdentifier(tableName),
		strings.Join(quotedColumns, ", "),
		strings.Join(placeholders, ", "))

	logging.Debug(packageName, "Generated SQL query: %s", query)

	// Begin transaction for batch insert
	tx, err := c.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction for batch insert: %w", err)
	}
	defer tx.Rollback()

	// Prepare statement
	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		logging.Error(packageName, "Failed to prepare batch statement: %v", err)
		return fmt.Errorf("failed to prepare batch statement: %w", err)
	}
	defer stmt.Close()

	// Execute batch
	for i, row := range values {
		if len(row) != len(columns) {
			return fmt.Errorf("row %d has %d values, expected %d", i, len(row), len(columns))
		}

		_, err := stmt.ExecContext(ctx, row...)
		if err != nil {
			logging.Error(packageName, "Failed to execute batch row %d: %v", i, err)
			return fmt.Errorf("failed to execute batch row %d: %w", i, err)
		}
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		logging.Error(packageName, "Failed to commit batch transaction: %v", err)
		return fmt.Errorf("failed to commit batch transaction: %w", err)
	}

	logging.Info(packageName, "Successfully inserted %d rows into %s", len(values), tableName)
	return nil
}

// GetDB returns the underlying sql.DB instance
func (c *Client) GetDB() *sql.DB {
	return c.db
}

// Stats returns database statistics
func (c *Client) Stats() sql.DBStats {
	if c.db == nil {
		return sql.DBStats{}
	}
	return c.db.Stats()
}

// quoteIdentifier quotes a PostgreSQL identifier (table or column name)
// using double quotes, which is needed for reserved words like type, asc, abs, end, map
func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}
