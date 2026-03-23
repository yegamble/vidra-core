package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/jmoiron/sqlx"
)

type TransactionManager struct {
	db *sqlx.DB
}

func NewTransactionManager(db *sqlx.DB) *TransactionManager {
	return &TransactionManager{db: db}
}

type TxOptions struct {
	IsolationLevel sql.IsolationLevel
	ReadOnly       bool
}

func DefaultTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelReadCommitted,
		ReadOnly:       false,
	}
}

func ReadOnlyTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelReadCommitted,
		ReadOnly:       true,
	}
}

func SerializableTxOptions() *TxOptions {
	return &TxOptions{
		IsolationLevel: sql.LevelSerializable,
		ReadOnly:       false,
	}
}

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

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("transaction failed: %v, rollback failed: %w", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

func (tm *TransactionManager) WithSerializableTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	return tm.WithTransaction(ctx, SerializableTxOptions(), fn)
}

func (tm *TransactionManager) WithReadOnlyTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	return tm.WithTransaction(ctx, ReadOnlyTxOptions(), fn)
}

type Executor interface {
	ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row
	GetContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
	SelectContext(ctx context.Context, dest interface{}, query string, args ...interface{}) error
}

func GetExecutor(ctx context.Context, db *sqlx.DB) Executor {
	if tx := GetTxFromContext(ctx); tx != nil {
		return tx
	}
	return db
}

type txContextKey struct{}

func WithTx(ctx context.Context, tx *sqlx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func GetTxFromContext(ctx context.Context) *sqlx.Tx {
	if tx, ok := ctx.Value(txContextKey{}).(*sqlx.Tx); ok {
		return tx
	}
	return nil
}

type RetryableError struct {
	Err error
}

func (e RetryableError) Error() string {
	return fmt.Sprintf("retryable error: %v", e.Err)
}

func IsRetryable(err error) bool {
	_, ok := err.(RetryableError)
	return ok
}

func (tm *TransactionManager) WithRetry(ctx context.Context, maxRetries int, opts *TxOptions, fn func(*sqlx.Tx) error) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		err := tm.WithTransaction(ctx, opts, fn)
		if err == nil {
			return nil
		}

		lastErr = err

		errStr := err.Error()
		isRetryable := false

		if strings.Contains(errStr, "deadlock detected") ||
			strings.Contains(errStr, "could not serialize") ||
			strings.Contains(errStr, "concurrent update") {
			isRetryable = true
		}

		if IsRetryable(err) {
			isRetryable = true
		}

		if !isRetryable {
			return err
		}
	}

	return fmt.Errorf("transaction failed after %d retries: %w", maxRetries, lastErr)
}
