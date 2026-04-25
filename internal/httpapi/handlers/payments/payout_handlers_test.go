package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	ucpayments "vidra-core/internal/usecase/payments"

	_ "github.com/lib/pq"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func payoutHandlerLiveDB(t *testing.T) *sqlx.DB {
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

func seedHandlerUser(t *testing.T, db *sqlx.DB, role string) string {
	t.Helper()
	id := uuid.New().String()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', $4)`,
		id, "phh-"+id[:8], id[:8]+"@test.local", role)
	if err != nil {
		t.Skipf("users table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id) })
	return id
}

func newPayoutHandlers(t *testing.T, db *sqlx.DB) (*PayoutHandler, *AdminPayoutHandler, *repository.PaymentLedgerRepository) {
	ledger := repository.NewPaymentLedgerRepository(db)
	payouts := repository.NewPayoutRepository(db)
	svc := ucpayments.NewPayoutService(payouts, ledger, nil, nil)
	return NewPayoutHandler(svc), NewAdminPayoutHandler(svc), ledger
}

func authReq(method, path, userID string, body interface{}) *http.Request {
	var buf *bytes.Buffer
	if body != nil {
		b, _ := json.Marshal(body)
		buf = bytes.NewBuffer(b)
	} else {
		buf = bytes.NewBuffer(nil)
	}
	r := httptest.NewRequest(method, path, buf)
	r.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, userID)
	return r.WithContext(ctx)
}

func TestPayoutHandler_Request_HappyPath(t *testing.T) {
	db := payoutHandlerLiveDB(t); defer db.Close()
	creator := seedHandlerUser(t, db, "user")
	h, _, ledger := newPayoutHandlers(t, db)

	require.NoError(t, ledger.Record(context.Background(), &domain.PaymentLedgerEntry{
		UserID: creator, EntryType: domain.LedgerEntryTipIn, AmountSats: 100_000,
		IdempotencyKey: "ph-tip-" + creator, Currency: "BTC",
	}))

	w := httptest.NewRecorder()
	h.Request(w, authReq("POST", "/payments/payouts", creator, map[string]interface{}{
		"amount_sats":      30_000,
		"destination":      "bcrt1qexample",
		"destination_type": "on_chain",
	}))
	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestPayoutHandler_Request_InsufficientReturns409(t *testing.T) {
	db := payoutHandlerLiveDB(t); defer db.Close()
	creator := seedHandlerUser(t, db, "user")
	h, _, _ := newPayoutHandlers(t, db)

	w := httptest.NewRecorder()
	h.Request(w, authReq("POST", "/payments/payouts", creator, map[string]interface{}{
		"amount_sats":      10_000,
		"destination":      "bcrt1qexample",
		"destination_type": "on_chain",
	}))
	assert.Equal(t, http.StatusConflict, w.Code)
}

func TestPayoutHandler_Request_BadDestReturns400(t *testing.T) {
	db := payoutHandlerLiveDB(t); defer db.Close()
	creator := seedHandlerUser(t, db, "user")
	h, _, _ := newPayoutHandlers(t, db)

	w := httptest.NewRecorder()
	h.Request(w, authReq("POST", "/payments/payouts", creator, map[string]interface{}{
		"amount_sats":      10_000,
		"destination":      "garbage",
		"destination_type": "on_chain",
	}))
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
