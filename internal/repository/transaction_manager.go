package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// TransactionManager provides transaction management capabilities for repositories
type TransactionManager struct {
	db *sqlx.DB
}

// NewTransactionManager creates a new transaction manager
func NewTransactionManager(db *sqlx.DB) *TransactionManager {
	return &TransactionManager{db: db}
}

// TxOptions are options for starting a transaction
type TxOptions struct {
	IsolationLevel sql.IsolationLevel
	ReadOnly       bool
}

// DefaultTxOptions returns default transaction options with READ COMMITTED isolation
func DefaultTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelReadCommitted,
		ReadOnly:       false,
	}
}

// ReadOnlyTxOptions returns transaction options for read-only operations
func ReadOnlyTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelReadCommitted,
		ReadOnly:       true,
	}
}

// SerializableTxOptions returns transaction options with SERIALIZABLE isolation
func SerializableTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelSerializable,
		ReadOnly:       false,
	}
}

// WithTransaction executes a function within a database transaction
// If the function returns an error, the transaction is rolled back
// Otherwise, the transaction is committed
func (tm *TransactionManager) WithTransaction(ctx context.Context, opts *TxOptions, fn func(*sqlx.Tx) error) error {
	if opts == nil {
		opts = DefaultTxOptions()
	}

	sqlOpts := &sql.TxOptions{
		Isolation: opts.IsolationLevel,
		ReadOnly:  opts.ReadOnly,
	}

	tx, err := tm.db.BeginTxx(ctx, sqlOpts)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	// Ensure transaction is cleaned up
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p) // Re-panic after rollback
		}
	}()

	// Execute the function
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("transaction failed: %v, rollback failed: %w", err, rbErr)
		}
		return err
	}

	// Commit the transaction
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// WithSerializableTransaction executes a function within a serializable transaction
// This provides the highest isolation level and prevents phantom reads
func (tm *TransactionManager) WithSerializableTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	return tm.WithTransaction(ctx, SerializableTxOptions(), fn)
}

// WithReadOnlyTransaction executes a function within a read-only transaction
// This can improve performance for read-only operations
func (tm *TransactionManager) WithReadOnlyTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	return tm.WithTransaction(ctx, ReadOnlyTxOptions(), fn)
}

// Executor is an interface that both *sqlx.DB and *sqlx.Tx implement
// This allows repository methods to work with both regular connections and transactions
type Executor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

// GetExecutor returns either a transaction if one exists in the context, or the database connection
func GetExecutor(ctx context.Context, db *sqlx.DB) Executor {
	if tx := GetTxFromContext(ctx); tx != nil {
		return tx
	}
	return db
}

// Transaction context key type
type txContextKey struct{}

// WithTx adds a transaction to the context
func WithTx(ctx context.Context, tx *sqlx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// GetTxFromContext retrieves a transaction from the context if it exists
func GetTxFromContext(ctx context.Context) *sqlx.Tx {
	if tx, ok := ctx.Value(txContextKey{}).(*sqlx.Tx); ok {
		return tx
	}
	return nil
}

// RetryableError marks an error as retryable (e.g., for deadlock resolution)
type RetryableError struct {
	Err error
}

func (e RetryableError) Error() string {
	return fmt.Sprintf("retryable error: %v", e.Err)
}

// IsRetryable checks if an error is retryable
func IsRetryable(err error) bool {
	_, ok := err.(RetryableError)
	return ok
}

// WithRetry executes a transaction with automatic retry logic for deadlocks and serialization failures
func (tm *TransactionManager) WithRetry(ctx context.Context, maxRetries int, opts *TxOptions, fn func(*sqlx.Tx) error) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := tm.WithTransaction(ctx, opts, fn)
		if err == nil {
			return nil
		}

		lastErr = err

		// Check for retryable database errors (deadlocks, serialization failures)
		errStr := err.Error()
		isRetryable := false

		// PostgreSQL error codes
		if contains(errStr, "deadlock detected") ||
			contains(errStr, "could not serialize") ||
			contains(errStr, "concurrent update") {
			isRetryable = true
		}

		// Check for explicitly marked retryable errors
		if IsRetryable(err) {
			isRetryable = true
		}

		if !isRetryable {
			return err
		}

		// Exponential backoff could be added here if needed
	}

	return fmt.Errorf("transaction failed after %d retries: %w", maxRetries, lastErr)
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && len(substr) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
