package payments

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

	_ "github.com/lib/pq"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- Fakes ----

type fakeChannelLookup struct {
	owners map[string]string
	err    error
}

func (f *fakeChannelLookup) ResolveOwner(ctx context.Context, channelID string) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	return f.owners[channelID], nil
}

type fakeNotifier struct {
	calls []struct {
		userID string
		nType  domain.NotificationType
		title  string
	}
	emitErr error
}

func (f *fakeNotifier) Emit(ctx context.Context, userID string, nType domain.NotificationType, title, message string, data map[string]interface{}) error {
	f.calls = append(f.calls, struct {
		userID string
		nType  domain.NotificationType
		title  string
	}{userID, nType, title})
	return f.emitErr
}

// ---- Live-DB integration tests (skip when DATABASE_URL not reachable) ----

func liveDB(t *testing.T) *sqlx.DB {
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

func seedUser(t *testing.T, db *sqlx.DB, prefix string) string {
	t.Helper()
	id := uuid.New().String()
	_, err := db.ExecContext(context.Background(),
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', 'user')`,
		id, prefix+"-"+id[:8], id[:8]+"@test.local")
	if err != nil {
		t.Skipf("users table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(context.Background(), "DELETE FROM users WHERE id = $1", id) })
	return id
}

func seedChannel(t *testing.T, db *sqlx.DB, accountID, channelID string) {
	t.Helper()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO channels (id, account_id, handle, display_name)
		 VALUES ($1, $2, $3, $4)`,
		channelID, accountID, "ch-"+channelID[:8], "Channel "+channelID[:8])
	if err != nil {
		t.Skipf("channels table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM channels WHERE id = $1", channelID) })
}

func TestLedgerService_RecordInvoiceSettlement_TipWithRecipient(t *testing.T) {
	db := liveDB(t)
	defer db.Close()

	ctx := context.Background()
	payerID := seedUser(t, db, "payer")
	recipientID := seedUser(t, db, "recipient")
	channelID := uuid.New().String()
	seedChannel(t, db, recipientID, channelID)

	ledgerRepo := repository.NewPaymentLedgerRepository(db)
	invoiceRepo := repository.NewBTCPayRepository(db)
	lookup := &fakeChannelLookup{owners: map[string]string{channelID: recipientID}}
	notif := &fakeNotifier{}
	svc := NewLedgerService(ledgerRepo, invoiceRepo, lookup, notif)

	metaBytes, _ := json.Marshal(map[string]interface{}{
		"type":       "tip",
		"channel_id": channelID,
		"amount_usd": 5,
	})
	invoice := &domain.BTCPayInvoice{
		ID:              uuid.New().String(),
		BTCPayInvoiceID: "btcpay-test-" + uuid.NewString()[:8],
		UserID:          payerID,
		AmountSats:      7250,
		Currency:        "BTC",
		Status:          domain.InvoiceStatusSettled,
		Metadata:        metaBytes,
	}
	// Seed the invoice row (Record requires FK to exist).
	require.NoError(t, invoiceRepo.CreateInvoice(ctx, invoice))
	t.Cleanup(func() {
		_, _ = db.ExecContext(ctx, "DELETE FROM btcpay_invoices WHERE id = $1", invoice.ID)
	})

	require.NoError(t, svc.RecordInvoiceSettlement(ctx, invoice))

	payerBal, err := ledgerRepo.GetAvailableBalance(ctx, nil, payerID)
	require.NoError(t, err)
	assert.Equal(t, int64(-7250), payerBal, "payer balance reflects tip_out")

	recipBal, err := ledgerRepo.GetAvailableBalance(ctx, nil, recipientID)
	require.NoError(t, err)
	assert.Equal(t, int64(7250), recipBal, "recipient balance reflects tip_in")

	// Replay the webhook — idempotent.
	require.NoError(t, svc.RecordInvoiceSettlement(ctx, invoice))
	payerBalAfter, _ := ledgerRepo.GetAvailableBalance(ctx, nil, payerID)
	recipBalAfter, _ := ledgerRepo.GetAvailableBalance(ctx, nil, recipientID)
	assert.Equal(t, payerBal, payerBalAfter, "replay must not double-debit payer")
	assert.Equal(t, recipBal, recipBalAfter, "replay must not double-credit recipient")

	// Notification emitted once on first run; second run re-emits (best-effort;
	// consumer-side dedup is out of scope for ledger service).
	require.NotEmpty(t, notif.calls)
	assert.Equal(t, domain.NotificationTipReceived, notif.calls[0].nType)
	assert.Equal(t, recipientID, notif.calls[0].userID)
}

func TestLedgerService_RecordInvoiceSettlement_NoChannel(t *testing.T) {
	db := liveDB(t)
	defer db.Close()
	ctx := context.Background()
	payerID := seedUser(t, db, "payer")

	ledgerRepo := repository.NewPaymentLedgerRepository(db)
	invoiceRepo := repository.NewBTCPayRepository(db)
	svc := NewLedgerService(ledgerRepo, invoiceRepo, &fakeChannelLookup{owners: map[string]string{}}, &fakeNotifier{})

	metaBytes, _ := json.Marshal(map[string]interface{}{"type": "tip"})
	invoice := &domain.BTCPayInvoice{
		ID:              uuid.New().String(),
		BTCPayInvoiceID: "btcpay-nochan-" + uuid.NewString()[:8],
		UserID:          payerID,
		AmountSats:      1000, Currency: "BTC", Status: domain.InvoiceStatusSettled,
		Metadata: metaBytes,
	}
	require.NoError(t, invoiceRepo.CreateInvoice(ctx, invoice))
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM btcpay_invoices WHERE id = $1", invoice.ID) })

	require.NoError(t, svc.RecordInvoiceSettlement(ctx, invoice))

	payerBal, _ := ledgerRepo.GetAvailableBalance(ctx, nil, payerID)
	assert.Equal(t, int64(-1000), payerBal, "tip_out still recorded when channel unresolvable")
}

func TestLedgerService_RecordInvoiceSettlement_NotSettled(t *testing.T) {
	svc := NewLedgerService(nil, nil, nil, nil)
	err := svc.RecordInvoiceSettlement(context.Background(), &domain.BTCPayInvoice{
		Status: domain.InvoiceStatusNew,
	})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "expected Settled")
}

func TestLedgerService_RecordInvoiceSettlement_NonTip(t *testing.T) {
	// Non-tip settled invoice (future: inner circle) — no-op for tip_* ledger entries.
	svc := NewLedgerService(nil, nil, nil, nil)
	meta, _ := json.Marshal(map[string]interface{}{"type": "inner_circle"})
	err := svc.RecordInvoiceSettlement(context.Background(), &domain.BTCPayInvoice{
		Status:   domain.InvoiceStatusSettled,
		Metadata: meta,
	})
	assert.NoError(t, err, "non-tip settled invoice should be a no-op, not an error")
}

func TestLedgerService_RecordInvoiceSettlement_NotifierErrorDoesNotFail(t *testing.T) {
	db := liveDB(t)
	defer db.Close()
	ctx := context.Background()
	payerID := seedUser(t, db, "payer")
	recipientID := seedUser(t, db, "recipient")
	channelID := uuid.New().String()
	seedChannel(t, db, recipientID, channelID)

	ledgerRepo := repository.NewPaymentLedgerRepository(db)
	invoiceRepo := repository.NewBTCPayRepository(db)
	lookup := &fakeChannelLookup{owners: map[string]string{channelID: recipientID}}
	notif := &fakeNotifier{emitErr: errors.New("notify broken")}
	svc := NewLedgerService(ledgerRepo, invoiceRepo, lookup, notif)

	metaBytes, _ := json.Marshal(map[string]interface{}{
		"type": "tip", "channel_id": channelID,
	})
	invoice := &domain.BTCPayInvoice{
		ID:              uuid.New().String(),
		BTCPayInvoiceID: "btcpay-notifErr-" + uuid.NewString()[:8],
		UserID:          payerID,
		AmountSats:      500, Currency: "BTC", Status: domain.InvoiceStatusSettled,
		Metadata: metaBytes,
	}
	require.NoError(t, invoiceRepo.CreateInvoice(ctx, invoice))
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM btcpay_invoices WHERE id = $1", invoice.ID) })

	err := svc.RecordInvoiceSettlement(ctx, invoice)
	require.NoError(t, err, "notifier failure must not fail the ledger write")

	bal, _ := ledgerRepo.GetAvailableBalance(ctx, nil, recipientID)
	assert.Equal(t, int64(500), bal, "recipient ledger write succeeds even when notifier errors")
}
