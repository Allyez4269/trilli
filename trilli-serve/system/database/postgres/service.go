package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"trilli/system/logging"
)

// Service represents a PostgreSQL database service
type Service struct {
	Client *Client
}

// NewService creates a new PostgreSQL service with the provided configuration
func NewService(cfg *Config) (*Service, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create PostgreSQL client: %w", err)
	}

	logging.Info(packageName, "PostgreSQL service initialized")
	return &Service{
		Client: client,
	}, nil
}

// Close closes the database connection
func (s *Service) Close() error {
	logging.Info(packageName, "Closing PostgreSQL connection")
	if s.Client != nil {
		return s.Client.Close()
	}
	return nil
}

// Ping checks the database connection
func (s *Service) Ping() error {
	logging.Debug(packageName, "Pinging PostgreSQL")
	return s.Client.Ping()
}

// ExecuteQuery executes a query that doesn't return rows
func (s *Service) ExecuteQuery(query string, args ...interface{}) error {
	logging.Info(packageName, "Executing query: %s", query)
	_, err := s.Client.Exec(query, args...)
	return err
}

// ExecuteQueryContext executes a query with context that doesn't return rows
func (s *Service) ExecuteQueryContext(ctx context.Context, query string, args ...interface{}) error {
	logging.Info(packageName, "Executing query with context: %s", query)
	_, err := s.Client.ExecContext(ctx, query, args...)
	return err
}

// Query executes a query and returns the rows
func (s *Service) Query(query string, args ...interface{}) (*sql.Rows, error) {
	logging.Info(packageName, "Executing query: %s", query)
	return s.Client.Query(query, args...)
}

// QueryContext executes a query with context and returns the rows
func (s *Service) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	logging.Info(packageName, "Executing query with context: %s", query)
	return s.Client.QueryContext(ctx, query, args...)
}

// QueryRow executes a query that is expected to return at most one row
func (s *Service) QueryRow(query string, args ...interface{}) *sql.Row {
	logging.Info(packageName, "Executing single row query: %s", query)
	return s.Client.QueryRow(query, args...)
}

// QueryRowContext executes a query that is expected to return at most one row with context
func (s *Service) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	logging.Info(packageName, "Executing single row query with context: %s", query)
	return s.Client.QueryRowContext(ctx, query, args...)
}

// QueryWithCallback executes a query and processes rows with a callback function
func (s *Service) QueryWithCallback(query string, callback func(rows *sql.Rows) error, args ...interface{}) error {
	ctx := context.Background()
	logging.Info(packageName, "Executing query with callback: %s", query)

	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		logging.Error(packageName, "Query failed: %v", err)
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	if err := callback(rows); err != nil {
		logging.Error(packageName, "Callback processing failed: %v", err)
		return fmt.Errorf("callback processing failed: %w", err)
	}

	return nil
}

// QueryWithCallbackContext executes a query with context and processes rows with a callback function
func (s *Service) QueryWithCallbackContext(ctx context.Context, query string, callback func(rows *sql.Rows) error, args ...interface{}) error {
	logging.Info(packageName, "Executing query with callback and context: %s", query)

	rows, err := s.Client.QueryContext(ctx, query, args...)
	if err != nil {
		logging.Error(packageName, "Query failed: %v", err)
		return fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	if err := callback(rows); err != nil {
		logging.Error(packageName, "Callback processing failed: %v", err)
		return fmt.Errorf("callback processing failed: %w", err)
	}

	return nil
}

// InsertData is a helper method for inserting data into a table
func (s *Service) InsertData(tableName string, columns []string, values [][]interface{}) error {
	logging.Info(packageName, "Inserting data into table: %s", tableName)
	return s.Client.InsertBatch(tableName, columns, values)
}

// InsertDataContext is a helper method for inserting data into a table with context
func (s *Service) InsertDataContext(ctx context.Context, tableName string, columns []string, values [][]interface{}) error {
	logging.Info(packageName, "Inserting data into table with context: %s", tableName)
	return s.Client.InsertBatchContext(ctx, tableName, columns, values)
}

// BeginTransaction starts a new database transaction
func (s *Service) BeginTransaction() (*sql.Tx, error) {
	logging.Info(packageName, "Beginning transaction")
	return s.Client.Begin()
}

// BeginTransactionContext starts a new database transaction with context
func (s *Service) BeginTransactionContext(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	logging.Info(packageName, "Beginning transaction with context")
	return s.Client.BeginTx(ctx, opts)
}

// ExecuteInTransaction executes a function within a database transaction
func (s *Service) ExecuteInTransaction(fn func(tx *sql.Tx) error) error {
	tx, err := s.BeginTransaction()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		} else if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				logging.Error(packageName, "Failed to rollback transaction: %v", rollbackErr)
			}
		} else {
			err = tx.Commit()
			if err != nil {
				logging.Error(packageName, "Failed to commit transaction: %v", err)
			}
		}
	}()

	err = fn(tx)
	return err
}

// ExecuteInTransactionContext executes a function within a database transaction with context
func (s *Service) ExecuteInTransactionContext(ctx context.Context, opts *sql.TxOptions, fn func(tx *sql.Tx) error) error {
	tx, err := s.BeginTransactionContext(ctx, opts)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			tx.Rollback()
			panic(p)
		} else if err != nil {
			if rollbackErr := tx.Rollback(); rollbackErr != nil {
				logging.Error(packageName, "Failed to rollback transaction: %v", rollbackErr)
			}
		} else {
			err = tx.Commit()
			if err != nil {
				logging.Error(packageName, "Failed to commit transaction: %v", err)
			}
		}
	}()

	err = fn(tx)
	return err
}

// GetStats returns database connection statistics
func (s *Service) GetStats() sql.DBStats {
	return s.Client.Stats()
}

// CheckTable checks if a table exists in the database
func (s *Service) CheckTable(tableName string) (bool, error) {
	query := `SELECT COUNT(*) FROM information_schema.tables WHERE table_name = $1 AND table_schema = 'public'`

	var count int
	row := s.Client.QueryRow(query, tableName)
	err := row.Scan(&count)
	if err != nil {
		logging.Error(packageName, "Failed to check table existence: %v", err)
		return false, fmt.Errorf("failed to check table existence: %w", err)
	}

	logging.Debug(packageName, "Table %s exists: %t", tableName, count > 0)
	return count > 0, nil
}

// GetTableColumns returns the column names for a given table
func (s *Service) GetTableColumns(tableName string) ([]string, error) {
	query := `SELECT column_name FROM information_schema.columns WHERE table_name = $1 AND table_schema = 'public' ORDER BY ordinal_position`

	rows, err := s.Client.Query(query, tableName)
	if err != nil {
		logging.Error(packageName, "Failed to get table columns: %v", err)
		return nil, fmt.Errorf("failed to get table columns: %w", err)
	}
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var columnName string
		if err := rows.Scan(&columnName); err != nil {
			logging.Error(packageName, "Failed to scan column name: %v", err)
			return nil, fmt.Errorf("failed to scan column name: %w", err)
		}
		columns = append(columns, columnName)
	}

	if err := rows.Err(); err != nil {
		logging.Error(packageName, "Error iterating column rows: %v", err)
		return nil, fmt.Errorf("error iterating column rows: %w", err)
	}

	logging.Debug(packageName, "Retrieved %d columns for table %s", len(columns), tableName)
	return columns, nil
}

// GetTableSchema returns detailed schema information for a table
func (s *Service) GetTableSchema(tableName string) ([]TableColumn, error) {
	query := `
		SELECT
			column_name,
			data_type,
			is_nullable,
			column_default,
			character_maximum_length,
			numeric_precision,
			numeric_scale,
			ordinal_position
		FROM information_schema.columns
		WHERE table_name = $1 AND table_schema = 'public'
		ORDER BY ordinal_position`

	rows, err := s.Client.Query(query, tableName)
	if err != nil {
		logging.Error(packageName, "Failed to get table schema: %v", err)
		return nil, fmt.Errorf("failed to get table schema: %w", err)
	}
	defer rows.Close()

	var schema []TableColumn
	for rows.Next() {
		var col TableColumn
		var maxLength, precision, scale sql.NullInt64
		var defaultValue sql.NullString

		err := rows.Scan(
			&col.Name,
			&col.DataType,
			&col.IsNullable,
			&defaultValue,
			&maxLength,
			&precision,
			&scale,
			&col.OrdinalPosition,
		)
		if err != nil {
			logging.Error(packageName, "Failed to scan table schema: %v", err)
			return nil, fmt.Errorf("failed to scan table schema: %w", err)
		}

		if defaultValue.Valid {
			col.DefaultValue = &defaultValue.String
		}
		if maxLength.Valid {
			col.MaxLength = &maxLength.Int64
		}
		if precision.Valid {
			col.Precision = &precision.Int64
		}
		if scale.Valid {
			col.Scale = &scale.Int64
		}

		schema = append(schema, col)
	}

	if err := rows.Err(); err != nil {
		logging.Error(packageName, "Error iterating schema rows: %v", err)
		return nil, fmt.Errorf("error iterating schema rows: %w", err)
	}

	logging.Debug(packageName, "Retrieved schema for table %s with %d columns", tableName, len(schema))
	return schema, nil
}

// TableColumn represents a database table column
type TableColumn struct {
	Name            string  `json:"name"`
	DataType        string  `json:"data_type"`
	IsNullable      string  `json:"is_nullable"`
	DefaultValue    *string `json:"default_value,omitempty"`
	MaxLength       *int64  `json:"max_length,omitempty"`
	Precision       *int64  `json:"precision,omitempty"`
	Scale           *int64  `json:"scale,omitempty"`
	OrdinalPosition int     `json:"ordinal_position"`
}
