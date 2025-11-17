package database

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

// PoolConfig contains database connection pool configuration
type PoolConfig struct {
	MaxOpenConns    int           `json:"max_open_conns"`
	MaxIdleConns    int           `json:"max_idle_conns"`
	ConnMaxLifetime time.Duration `json:"conn_max_lifetime"`
	ConnMaxIdleTime time.Duration `json:"conn_max_idle_time"`
}

// Validate checks that the pool configuration is valid
func (c PoolConfig) Validate() error {
	if c.MaxOpenConns <= 0 {
		return fmt.Errorf("MaxOpenConns must be greater than 0")
	}
	if c.MaxIdleConns < 0 {
		return fmt.Errorf("MaxIdleConns cannot be negative")
	}
	if c.MaxIdleConns > c.MaxOpenConns {
		return fmt.Errorf("MaxIdleConns cannot exceed MaxOpenConns")
	}
	if c.ConnMaxLifetime <= 0 {
		return fmt.Errorf("ConnMaxLifetime must be greater than 0")
	}
	if c.ConnMaxIdleTime <= 0 {
		return fmt.Errorf("ConnMaxIdleTime must be greater than 0")
	}
	if c.ConnMaxIdleTime > c.ConnMaxLifetime {
		return fmt.Errorf("ConnMaxIdleTime cannot exceed ConnMaxLifetime")
	}
	return nil
}

// DefaultPoolConfig returns the default pool configuration per CLAUDE.md specs
func DefaultPoolConfig() PoolConfig {
	return PoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    5,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// Pool wraps sqlx.DB with additional configuration management
type Pool struct {
	db *sqlx.DB
}

// NewPool creates a new configured database connection pool
func NewPool(db *sqlx.DB, config PoolConfig) (*Pool, error) {
	if db == nil {
		return nil, fmt.Errorf("database cannot be nil")
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	// Configure the underlying sql.DB connection pool
	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)

	// Ping database to verify connectivity
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping failed: %w", err)
	}

	return &Pool{
		db: db,
	}, nil
}

// GetDB returns the underlying sqlx.DB instance
func (p *Pool) GetDB() *sqlx.DB {
	return p.db
}

// Stats returns database connection pool statistics
func (p *Pool) Stats() sql.DBStats {
	return p.db.Stats()
}

// Query executes a query that returns rows
func (p *Pool) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return p.db.Query(query, args...)
}

// QueryContext executes a query with context that returns rows
func (p *Pool) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return p.db.QueryContext(ctx, query, args...)
}

// Exec executes a query without returning any rows
func (p *Pool) Exec(query string, args ...interface{}) (sql.Result, error) {
	return p.db.Exec(query, args...)
}

// ExecContext executes a query with context without returning any rows
func (p *Pool) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return p.db.ExecContext(ctx, query, args...)
}

// Close closes the database connection pool
func (p *Pool) Close() error {
	return p.db.Close()
}

// Ping verifies the database connection
func (p *Pool) Ping() error {
	return p.db.Ping()
}

// PingContext verifies the database connection with context
func (p *Pool) PingContext(ctx context.Context) error {
	return p.db.PingContext(ctx)
}

// Begin starts a database transaction
func (p *Pool) Begin() (*sql.Tx, error) {
	return p.db.Begin()
}

// BeginTx starts a database transaction with context
func (p *Pool) BeginTx(ctx context.Context, opts *sql.TxOptions) (*sql.Tx, error) {
	return p.db.BeginTx(ctx, opts)
}

// BeginTxx starts a sqlx transaction
func (p *Pool) BeginTxx(ctx context.Context, opts *sql.TxOptions) (*sqlx.Tx, error) {
	return p.db.BeginTxx(ctx, opts)
}

// Get executes a query and scans the result into dest using sqlx
func (p *Pool) Get(dest interface{}, query string, args ...interface{}) error {
	return p.db.Get(dest, query, args...)
}

// GetContext executes a query with context and scans the result into dest using sqlx
func (p *Pool) GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return p.db.GetContext(ctx, dest, query, args...)
}

// Select executes a query and scans the results into dest using sqlx
func (p *Pool) Select(dest interface{}, query string, args ...interface{}) error {
	return p.db.Select(dest, query, args...)
}

// SelectContext executes a query with context and scans the results into dest using sqlx
func (p *Pool) SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
	return p.db.SelectContext(ctx, dest, query, args...)
}