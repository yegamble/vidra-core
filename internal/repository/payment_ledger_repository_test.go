package repository

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	"vidra-core/internal/domain"

	_ "github.com/lib/pq"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- sqlmock-based unit tests (query shape) ----

func newLedgerTestDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestPaymentLedgerRepository_Record_Success(t *testing.T) {
	db, mock := newLedgerTestDB(t)
	defer db.Close()
	repo := NewPaymentLedgerRepository(db)

	entry := &domain.PaymentLedgerEntry{
		UserID:         "user-1",
		EntryType:      domain.LedgerEntryTipIn,
		AmountSats:     5000,
		IdempotencyKey: "invoice-xyz-tip_in",
	}

	now := time.Now()
	mock.ExpectQuery("INSERT INTO payment_ledger").
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow("generated-uuid", now))

	err := repo.Record(context.Background(), entry)
	require.NoError(t, err)
	assert.Equal(t, "generated-uuid", entry.ID)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentLedgerRepository_Record_DuplicateKey(t *testing.T) {
	db, mock := newLedgerTestDB(t)
	defer db.Close()
	repo := NewPaymentLedgerRepository(db)

	entry := &domain.PaymentLedgerEntry{
		UserID:         "user-1",
		EntryType:      domain.LedgerEntryTipIn,
		AmountSats:     5000,
		IdempotencyKey: "dup-key",
	}

	// ON CONFLICT DO NOTHING + RETURNING yields no rows for a duplicate insert.
	mock.ExpectQuery("INSERT INTO payment_ledger").WillReturnError(sql.ErrNoRows)

	err := repo.Record(context.Background(), entry)
	assert.ErrorIs(t, err, domain.ErrLedgerDuplicate)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestPaymentLedgerRepository_Record_InvalidType(t *testing.T) {
	db, _ := newLedgerTestDB(t)
	defer db.Close()
	repo := NewPaymentLedgerRepository(db)

	err := repo.Record(context.Background(), &domain.PaymentLedgerEntry{
		UserID:         "user-1",
		EntryType:      domain.LedgerEntryType("bogus"),
		AmountSats:     1,
		IdempotencyKey: "k",
	})
	assert.ErrorIs(t, err, domain.ErrLedgerEntryInvalid)
}

func TestPaymentLedgerRepository_Record_RequiresIdempotencyKey(t *testing.T) {
	db, _ := newLedgerTestDB(t)
	defer db.Close()
	repo := NewPaymentLedgerRepository(db)

	err := repo.Record(context.Background(), &domain.PaymentLedgerEntry{
		UserID:     "user-1",
		EntryType:  domain.LedgerEntryTipIn,
		AmountSats: 1,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "idempotency_key is required")
}

// ---- Integration tests against a live Postgres (opt-in via DATABASE_URL) ----
//
// These run only when `DATABASE_URL` is set (as it is locally via `pnpm dev:full`).
// CI without the service should skip. They exercise the real UNIQUE constraint +
// FOREIGN KEY behaviours, which sqlmock cannot validate.

func maybeSkipLiveDB(t *testing.T) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://vidra_user:vidra_password@localhost:5432/vidra?sslmode=disable"
	}
	db, err := sqlx.Connect("postgres", dsn)
	if err != nil {
		t.Skipf("live Postgres unavailable: %v", err)
	}
	return db
}

func TestPaymentLedgerRepository_Live_IdempotentRecord(t *testing.T) {
	db := maybeSkipLiveDB(t)
	defer db.Close()

	// Seed a user (random UUID) so FK holds.
	ctx := context.Background()
	userID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', 'user')`,
		userID, "ledger-test-"+userID[:8], userID[:8]+"@test.local")
	if err != nil {
		t.Skipf("live users table schema diverged: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID) })

	repo := NewPaymentLedgerRepository(db)

	key := "ledgertest-" + userID
	first := &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: 1234,
		IdempotencyKey: key, Currency: "BTC",
	}
	require.NoError(t, repo.Record(ctx, first))
	require.NotEmpty(t, first.ID)

	// Replay the same idempotency key — must be a duplicate, balance unchanged.
	dup := &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: 1234,
		IdempotencyKey: key, Currency: "BTC",
	}
	err = repo.Record(ctx, dup)
	assert.ErrorIs(t, err, domain.ErrLedgerDuplicate)

	bal, err := repo.GetAvailableBalance(ctx, nil, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(1234), bal, "replay must not double-credit")
}

func TestPaymentLedgerRepository_Live_BalanceMath(t *testing.T) {
	db := maybeSkipLiveDB(t)
	defer db.Close()

	ctx := context.Background()
	userID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', 'user')`,
		userID, "bal-"+userID[:8], userID[:8]+"@test.local")
	if err != nil {
		t.Skipf("live users table schema diverged: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", userID) })

	repo := NewPaymentLedgerRepository(db)
	keyPrefix := "baltest-" + userID + "-"
	record := func(t *testing.T, et domain.LedgerEntryType, amt int64, key string) {
		t.Helper()
		require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
			UserID: userID, EntryType: et, AmountSats: amt,
			IdempotencyKey: keyPrefix + key, Currency: "BTC",
		}))
	}

	record(t, domain.LedgerEntryTipIn, 5000, "k1")
	record(t, domain.LedgerEntryTipIn, 3000, "k2")
	record(t, domain.LedgerEntryPayoutRequested, -2000, "k3") // reservation
	record(t, domain.LedgerEntryTipOut, -500, "k4")

	bal, err := repo.GetAvailableBalance(ctx, nil, userID)
	require.NoError(t, err)
	// 5000 + 3000 - 2000 - 500 = 5500
	assert.Equal(t, int64(5500), bal)

	// Compensate the reservation (reject path).
	record(t, domain.LedgerEntryPayoutRejected, 2000, "k3-compensated")
	bal, err = repo.GetAvailableBalance(ctx, nil, userID)
	require.NoError(t, err)
	assert.Equal(t, int64(7500), bal, "compensation restores balance")
}
