package payments

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"

	_ "github.com/lib/pq"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func walletLiveDB(t *testing.T) *sqlx.DB {
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

func seedWalletUser(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	id := uuid.New().String()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', 'user')`,
		id, "wallet-"+id[:8], id[:8]+"@test.local")
	if err != nil {
		t.Skipf("users table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id) })
	return id
}

func authedReq(method, path, userID string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, userID)
	return r.WithContext(ctx)
}

func TestWalletHandler_GetBalance_Empty(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	repo := repository.NewPaymentLedgerRepository(db)
	h := NewWalletHandler(repo)
	userID := seedWalletUser(t, db)

	w := httptest.NewRecorder()
	h.GetBalance(w, authedReq("GET", "/wallet/balance", userID))

	require.Equal(t, http.StatusOK, w.Code)
	var env struct {
		Data    domain.WalletBalance `json:"data"`
		Success bool                 `json:"success"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&env))
	assert.True(t, env.Success)
	assert.Equal(t, int64(0), env.Data.AvailableSats)
	assert.Equal(t, int64(0), env.Data.PendingPayoutSats)
	assert.Equal(t, "BTC", env.Data.Currency)
}

func TestWalletHandler_GetBalance_Unauthenticated(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	h := NewWalletHandler(repository.NewPaymentLedgerRepository(db))

	w := httptest.NewRecorder()
	h.GetBalance(w, httptest.NewRequest("GET", "/wallet/balance", nil))
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestWalletHandler_GetBalance_PendingNotDoubleCounted(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	repo := repository.NewPaymentLedgerRepository(db)
	h := NewWalletHandler(repo)
	userID := seedWalletUser(t, db)

	ctx := context.Background()
	keyP := "wallet-test-" + userID + "-"

	// Tip in 10000.
	require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: 10000,
		IdempotencyKey: keyP + "tipin", Currency: "BTC",
	}))

	// Create a pending payout row + reservation entry tying back to it.
	payoutID := uuid.New().String()
	_, err := db.ExecContext(ctx,
		`INSERT INTO btcpay_payouts (id, requester_user_id, amount_sats, destination, destination_type, status)
		 VALUES ($1, $2, $3, $4, 'on_chain', 'pending')`,
		payoutID, userID, int64(3000), "bcrt1qfake")
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM btcpay_payouts WHERE id = $1", payoutID) })

	require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryPayoutRequested, AmountSats: -3000,
		PayoutID:       sql.NullString{String: payoutID, Valid: true},
		IdempotencyKey: keyP + "reservation", Currency: "BTC",
	}))

	w := httptest.NewRecorder()
	h.GetBalance(w, authedReq("GET", "/wallet/balance", userID))
	require.Equal(t, http.StatusOK, w.Code)

	var env struct {
		Data domain.WalletBalance `json:"data"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&env))
	// Available = 10000 - 3000 = 7000 (reservation already folded in)
	// Pending = 3000 (informational)
	// CRITICAL: pending is NOT subtracted again
	assert.Equal(t, int64(7000), env.Data.AvailableSats)
	assert.Equal(t, int64(3000), env.Data.PendingPayoutSats)
}

func TestWalletHandler_GetTransactions_DirectionFilter(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	repo := repository.NewPaymentLedgerRepository(db)
	h := NewWalletHandler(repo)
	userID := seedWalletUser(t, db)

	ctx := context.Background()
	keyP := "tx-test-" + userID + "-"
	require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: 5000,
		IdempotencyKey: keyP + "in", Currency: "BTC",
	}))
	require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipOut, AmountSats: -2000,
		IdempotencyKey: keyP + "out", Currency: "BTC",
	}))

	type envelope struct {
		Data interface{} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}

	// All
	w := httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?direction=all", userID))
	require.Equal(t, http.StatusOK, w.Code)
	var all envelope
	require.NoError(t, json.NewDecoder(w.Body).Decode(&all))
	assert.EqualValues(t, 2, all.Meta.Total)

	// Sent (negative)
	w = httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?direction=sent", userID))
	require.Equal(t, http.StatusOK, w.Code)
	var sent envelope
	require.NoError(t, json.NewDecoder(w.Body).Decode(&sent))
	assert.EqualValues(t, 1, sent.Meta.Total)

	// Received (positive)
	w = httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?direction=received", userID))
	require.Equal(t, http.StatusOK, w.Code)
	var rcv envelope
	require.NoError(t, json.NewDecoder(w.Body).Decode(&rcv))
	assert.EqualValues(t, 1, rcv.Meta.Total)
}

func TestWalletHandler_GetTransactions_InvalidDirection(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	h := NewWalletHandler(repository.NewPaymentLedgerRepository(db))
	w := httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?direction=bogus", uuid.New().String()))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletHandler_GetTransactions_InvalidType(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	h := NewWalletHandler(repository.NewPaymentLedgerRepository(db))
	w := httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?type=not_a_type", uuid.New().String()))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWalletHandler_GetTransactions_Pagination(t *testing.T) {
	db := walletLiveDB(t)
	defer db.Close()
	repo := repository.NewPaymentLedgerRepository(db)
	h := NewWalletHandler(repo)
	userID := seedWalletUser(t, db)
	ctx := context.Background()
	keyP := "page-test-" + userID + "-"
	for i := 0; i < 25; i++ {
		require.NoError(t, repo.Record(ctx, &domain.PaymentLedgerEntry{
			UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: int64(100 * (i + 1)),
			IdempotencyKey: keyP + "row" + string(rune('a'+i%26)) + uuid.NewString()[:4],
			Currency:       "BTC",
		}))
	}
	w := httptest.NewRecorder()
	h.GetTransactions(w, authedReq("GET", "/wallet/transactions?count=10&start=0", userID))
	require.Equal(t, http.StatusOK, w.Code)
	var env struct {
		Data []interface{} `json:"data"`
		Meta struct {
			Total int64 `json:"total"`
		} `json:"meta"`
	}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&env))
	assert.EqualValues(t, 25, env.Meta.Total)
	assert.Len(t, env.Data, 10)
}
