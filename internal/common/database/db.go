package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Config holds database configuration
type Config struct {
	URL             string        `envconfig:"DATABASE_URL" required:"true"`
	MaxConns        int32         `envconfig:"DATABASE_MAX_CONNS" default:"25"`
	MinConns        int32         `envconfig:"DATABASE_MIN_CONNS" default:"5"`
	MaxConnLifetime time.Duration `envconfig:"DATABASE_MAX_CONN_LIFETIME" default:"1h"`
	MaxConnIdleTime time.Duration `envconfig:"DATABASE_MAX_CONN_IDLE_TIME" default:"30m"`
}

// DB wraps pgxpool with convenience methods
type DB struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

// New creates a new database connection pool
func New(ctx context.Context, cfg Config, logger *slog.Logger) (*DB, error) {
	poolConfig, err := pgxpool.ParseConfig(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("parsing database URL: %w", err)
	}

	poolConfig.MaxConns = cfg.MaxConns
	poolConfig.MinConns = cfg.MinConns
	poolConfig.MaxConnLifetime = cfg.MaxConnLifetime
	poolConfig.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("creating connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("pinging database: %w", err)
	}

	logger.Info("database connection established",
		"max_conns", cfg.MaxConns,
		"min_conns", cfg.MinConns,
	)

	return &DB{pool: pool, logger: logger}, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	db.pool.Close()
}

// Pool returns the underlying pool
func (db *DB) Pool() *pgxpool.Pool {
	return db.pool
}

// Ping checks the database connection
func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

// Querier is the interface for database queries
type Querier interface {
	Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row
}

// Ensure both pool and tx implement Querier
var _ Querier = (*pgxpool.Pool)(nil)
var _ Querier = (pgx.Tx)(nil)

// Exec executes a query without returning rows
func (db *DB) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return db.pool.Exec(ctx, sql, args...)
}

// Query executes a query that returns rows
func (db *DB) Query(ctx context.Context, sql string, args ...interface{}) (pgx.Rows, error) {
	return db.pool.Query(ctx, sql, args...)
}

// QueryRow executes a query that returns a single row
func (db *DB) QueryRow(ctx context.Context, sql string, args ...interface{}) pgx.Row {
	return db.pool.QueryRow(ctx, sql, args...)
}

// TxOptions are options for transactions
type TxOptions struct {
	IsoLevel       pgx.TxIsoLevel
	AccessMode     pgx.TxAccessMode
	DeferrableMode pgx.TxDeferrableMode
}

// DefaultTxOptions returns default transaction options
func DefaultTxOptions() TxOptions {
	return TxOptions{
		IsoLevel:   pgx.ReadCommitted,
		AccessMode: pgx.ReadWrite,
	}
}

// SerializableTxOptions returns serializable transaction options
func SerializableTxOptions() TxOptions {
	return TxOptions{
		IsoLevel:   pgx.Serializable,
		AccessMode: pgx.ReadWrite,
	}
}

// WithTx executes a function within a transaction
func (db *DB) WithTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	return db.WithTxOptions(ctx, DefaultTxOptions(), fn)
}

// WithTxOptions executes a function within a transaction with options
func (db *DB) WithTxOptions(ctx context.Context, opts TxOptions, fn func(tx pgx.Tx) error) error {
	tx, err := db.pool.BeginTx(ctx, pgx.TxOptions{
		IsoLevel:       opts.IsoLevel,
		AccessMode:     opts.AccessMode,
		DeferrableMode: opts.DeferrableMode,
	})
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback(ctx)
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(ctx); rbErr != nil {
			db.logger.Error("failed to rollback transaction",
				"error", rbErr,
				"original_error", err,
			)
		}
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

// Common errors
var (
	ErrNotFound      = errors.New("not found")
	ErrAlreadyExists = errors.New("already exists")
	ErrConflict      = errors.New("conflict")
)

// IsNotFound checks if an error is a not found error
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, sql.ErrNoRows) || errors.Is(err, pgx.ErrNoRows)
}

// IsUniqueViolation checks if an error is a unique constraint violation
func IsUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// IsForeignKeyViolation checks if an error is a foreign key violation
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23503"
	}
	return false
}

// IsSerializationFailure checks if an error is a serialization failure
func IsSerializationFailure(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "40001"
	}
	return false
}

// Retry retries a function on serialization failure
func Retry(ctx context.Context, maxAttempts int, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsSerializationFailure(lastErr) {
			return lastErr
		}
		// Exponential backoff
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt*10) * time.Millisecond):
		}
	}
	return fmt.Errorf("max retries exceeded: %w", lastErr)
}

// BatchResult holds the result of a batch operation
type BatchResult struct {
	RowsAffected int64
}

// BulkInsert performs a bulk insert using COPY protocol
func (db *DB) BulkInsert(ctx context.Context, tableName string, columns []string, rows [][]interface{}) (int64, error) {
	copyCount, err := db.pool.CopyFrom(
		ctx,
		pgx.Identifier{tableName},
		columns,
		pgx.CopyFromRows(rows),
	)
	if err != nil {
		return 0, fmt.Errorf("bulk insert: %w", err)
	}
	return copyCount, nil
}

// HealthCheck performs a health check on the database
func (db *DB) HealthCheck(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	var result int
	err := db.pool.QueryRow(ctx, "SELECT 1").Scan(&result)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	return nil
}

// Stats returns pool statistics
func (db *DB) Stats() *pgxpool.Stat {
	return db.pool.Stat()
}
