package repository

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTransactionManagerTest(t *testing.T) (*TransactionManager, sqlmock.Sqlmock, *sqlx.DB) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	tm := NewTransactionManager(sqlxDB)

	return tm, mock, sqlxDB
}

func TestDefaultTxOptions(t *testing.T) {
	opts := DefaultTxOptions()
	assert.NotNil(t, opts)
	assert.Equal(t, sql.LevelReadCommitted, opts.IsolationLevel)
	assert.False(t, opts.ReadOnly)
}

func TestReadOnlyTxOptions(t *testing.T) {
	opts := ReadOnlyTxOptions()
	assert.NotNil(t, opts)
	assert.Equal(t, sql.LevelReadCommitted, opts.IsolationLevel)
	assert.True(t, opts.ReadOnly)
}

func TestSerializableTxOptions(t *testing.T) {
	opts := SerializableTxOptions()
	assert.NotNil(t, opts)
	assert.Equal(t, sql.LevelSerializable, opts.IsolationLevel)
	assert.False(t, opts.ReadOnly)
}

func TestTransactionManager_WithTransaction_Success(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectCommit()

	err := tm.WithTransaction(context.Background(), nil, func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithTransaction_Rollback(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	expectedErr := errors.New("operation failed")

	mock.ExpectBegin()
	mock.ExpectRollback()

	err := tm.WithTransaction(context.Background(), nil, func(tx *sqlx.Tx) error {
		return expectedErr
	})

	require.Error(t, err)
	assert.Equal(t, expectedErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithTransaction_BeginError(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

	err := tm.WithTransaction(context.Background(), nil, func(tx *sqlx.Tx) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to begin transaction")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithTransaction_CommitError(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))

	err := tm.WithTransaction(context.Background(), nil, func(tx *sqlx.Tx) error {
		return nil
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to commit transaction")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithTransaction_RollbackError(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	fnErr := errors.New("function error")
	rollbackErr := errors.New("rollback failed")

	mock.ExpectBegin()
	mock.ExpectRollback().WillReturnError(rollbackErr)

	err := tm.WithTransaction(context.Background(), nil, func(tx *sqlx.Tx) error {
		return fnErr
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "transaction failed")
	assert.Contains(t, err.Error(), "rollback failed")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithTransaction_CustomOptions(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	opts := &TxOptions{
		IsolationLevel: sql.LevelSerializable,
		ReadOnly:       true,
	}

	mock.ExpectBegin()
	mock.ExpectCommit()

	err := tm.WithTransaction(context.Background(), opts, func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithSerializableTransaction(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectCommit()

	err := tm.WithSerializableTransaction(context.Background(), func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithReadOnlyTransaction(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectCommit()

	err := tm.WithReadOnlyTransaction(context.Background(), func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestWithTx_GetTxFromContext(t *testing.T) {
	_, mock, sqlxDB := setupTransactionManagerTest(t)

	mock.ExpectBegin()

	tx, err := sqlxDB.Beginx()
	require.NoError(t, err)

	ctx := WithTx(context.Background(), tx)

	retrievedTx := GetTxFromContext(ctx)
	assert.NotNil(t, retrievedTx)
	assert.Equal(t, tx, retrievedTx)

	mock.ExpectRollback()
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetTxFromContext_NoTransaction(t *testing.T) {
	ctx := context.Background()
	tx := GetTxFromContext(ctx)
	assert.Nil(t, tx)
}

func TestGetExecutor_WithTransaction(t *testing.T) {
	_, mock, sqlxDB := setupTransactionManagerTest(t)

	mock.ExpectBegin()

	tx, err := sqlxDB.Beginx()
	require.NoError(t, err)

	ctx := WithTx(context.Background(), tx)
	executor := GetExecutor(ctx, sqlxDB)

	assert.Equal(t, tx, executor)

	mock.ExpectRollback()
	_ = tx.Rollback()
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetExecutor_WithoutTransaction(t *testing.T) {
	_, _, sqlxDB := setupTransactionManagerTest(t)

	ctx := context.Background()
	executor := GetExecutor(ctx, sqlxDB)

	assert.Equal(t, sqlxDB, executor)
}

func TestTransactionManager_WithRetry_Success(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectCommit()

	err := tm.WithRetry(context.Background(), 3, nil, func(tx *sqlx.Tx) error {
		return nil
	})

	require.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithRetry_DeadlockRetry(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectCommit()

	attemptCount := 0
	err := tm.WithRetry(context.Background(), 3, nil, func(tx *sqlx.Tx) error {
		attemptCount++
		if attemptCount == 1 {
			return errors.New("deadlock detected")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, attemptCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithRetry_SerializationRetry(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectCommit()

	attemptCount := 0
	err := tm.WithRetry(context.Background(), 3, nil, func(tx *sqlx.Tx) error {
		attemptCount++
		if attemptCount == 1 {
			return errors.New("could not serialize access")
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, attemptCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithRetry_MaxRetriesExceeded(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		mock.ExpectBegin()
		mock.ExpectRollback()
	}

	err := tm.WithRetry(context.Background(), maxRetries, nil, func(tx *sqlx.Tx) error {
		return errors.New("deadlock detected")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed after 3 retries")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithRetry_NonRetryableError(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	nonRetryableErr := errors.New("constraint violation")

	mock.ExpectBegin()
	mock.ExpectRollback()

	err := tm.WithRetry(context.Background(), 3, nil, func(tx *sqlx.Tx) error {
		return nonRetryableErr
	})

	require.Error(t, err)
	assert.Equal(t, nonRetryableErr, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTransactionManager_WithRetry_RetryableErrorType(t *testing.T) {
	tm, mock, _ := setupTransactionManagerTest(t)

	mock.ExpectBegin()
	mock.ExpectRollback()

	mock.ExpectBegin()
	mock.ExpectCommit()

	attemptCount := 0
	err := tm.WithRetry(context.Background(), 3, nil, func(tx *sqlx.Tx) error {
		attemptCount++
		if attemptCount == 1 {
			return RetryableError{Err: errors.New("custom retryable error")}
		}
		return nil
	})

	require.NoError(t, err)
	assert.Equal(t, 2, attemptCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestRetryableError_Error(t *testing.T) {
	innerErr := errors.New("original error")
	retryErr := RetryableError{Err: innerErr}

	assert.Contains(t, retryErr.Error(), "retryable error")
	assert.Contains(t, retryErr.Error(), "original error")
}

func TestIsRetryable(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "retryable error",
			err:  RetryableError{Err: errors.New("test")},
			want: true,
		},
		{
			name: "non-retryable error",
			err:  errors.New("test"),
			want: false,
		},
		{
			name: "wrapped retryable error - not detected",
			err:  errors.New("wrapped: retryable error: test"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsRetryable(tt.err)
			assert.Equal(t, tt.want, result)
		})
	}
}

func TestContains(t *testing.T) {
	tests := []struct {
		name   string
		s      string
		substr string
		want   bool
	}{
		{
			name:   "substring found",
			s:      "deadlock detected",
			substr: "deadlock",
			want:   true,
		},
		{
			name:   "substring not found",
			s:      "some error",
			substr: "deadlock",
			want:   false,
		},
		{
			name:   "exact match",
			s:      "deadlock",
			substr: "deadlock",
			want:   true,
		},
		{
			name:   "empty substring",
			s:      "test",
			substr: "",
			want:   true,
		},
		{
			name:   "empty string",
			s:      "",
			substr: "test",
			want:   false,
		},
		{
			name:   "substring at end",
			s:      "error: could not serialize",
			substr: "serialize",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := strings.Contains(tt.s, tt.substr)
			assert.Equal(t, tt.want, result)
		})
	}
}
