package payments

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

	_ "github.com/lib/pq"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAdminLister struct{ ids []string }

func (f *fakeAdminLister) ListAdminIDs(ctx context.Context) ([]string, error) { return f.ids, nil }

func payoutLiveDB(t *testing.T) *sqlx.DB {
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

func seedUserPO(t *testing.T, db *sqlx.DB) string {
	t.Helper()
	id := uuid.New().String()
	ctx := context.Background()
	_, err := db.ExecContext(ctx,
		`INSERT INTO users (id, username, email, password_hash, role)
		 VALUES ($1, $2, $3, 'x', 'user')`,
		id, "po-"+id[:8], id[:8]+"@test.local")
	if err != nil {
		t.Skipf("users table: %v", err)
	}
	t.Cleanup(func() { _, _ = db.ExecContext(ctx, "DELETE FROM users WHERE id = $1", id) })
	return id
}

func newPayoutService(t *testing.T, db *sqlx.DB) (*PayoutService, *fakeNotifier, *repository.PaymentLedgerRepository) {
	ledgerRepo := repository.NewPaymentLedgerRepository(db)
	payoutRepo := repository.NewPayoutRepository(db)
	notif := &fakeNotifier{}
	admins := &fakeAdminLister{ids: []string{}}
	svc := NewPayoutService(payoutRepo, ledgerRepo, notif, admins)
	return svc, notif, ledgerRepo
}

func seedBalance(t *testing.T, repo *repository.PaymentLedgerRepository, userID string, sats int64, key string) {
	t.Helper()
	require.NoError(t, repo.Record(context.Background(), &domain.PaymentLedgerEntry{
		UserID: userID, EntryType: domain.LedgerEntryTipIn, AmountSats: sats,
		IdempotencyKey: key, Currency: "BTC",
	}))
}

func TestPayoutService_RequestPayout_HappyPath(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)

	seedBalance(t, ledger, userID, 100_000, "po-happy-"+userID)
	p, err := svc.RequestPayout(context.Background(), userID, domain.PayoutRequest{
		AmountSats: 50_000, Destination: "bcrt1qexample", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)
	assert.Equal(t, domain.PayoutStatusPending, p.Status)
	assert.NotEmpty(t, p.ID)

	bal, _ := ledger.GetAvailableBalance(context.Background(), nil, userID)
	assert.Equal(t, int64(50_000), bal, "reservation reduces available balance")

	pending, _ := ledger.GetPendingPayoutSats(context.Background(), userID)
	assert.Equal(t, int64(50_000), pending, "pending sats surface the reservation")
}

func TestPayoutService_RequestPayout_InsufficientBalance(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 1_000, "po-insuf-"+userID)

	_, err := svc.RequestPayout(context.Background(), userID, domain.PayoutRequest{
		AmountSats: 5_000, Destination: "bcrt1qexample", DestinationType: domain.PayoutDestOnChain,
	})
	assert.ErrorIs(t, err, domain.ErrInsufficientBalance)
}

func TestPayoutService_RequestPayout_DestValidation(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	svc, _, _ := newPayoutService(t, db)
	cases := []struct {
		name string
		req  domain.PayoutRequest
	}{
		{"empty dest", domain.PayoutRequest{AmountSats: 1000, DestinationType: domain.PayoutDestOnChain}},
		{"garbage prefix", domain.PayoutRequest{AmountSats: 1000, Destination: "xyz123", DestinationType: domain.PayoutDestOnChain}},
		{"bolt11 prefix wrong", domain.PayoutRequest{AmountSats: 1000, Destination: "lightning:invalid", DestinationType: domain.PayoutDestLightning}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.RequestPayout(context.Background(), uuid.New().String(), c.req)
			assert.ErrorIs(t, err, domain.ErrPayoutInvalidDest)
		})
	}
}

func TestPayoutService_RequestPayout_AmountTooSmall(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	svc, _, _ := newPayoutService(t, db)
	_, err := svc.RequestPayout(context.Background(), uuid.New().String(), domain.PayoutRequest{
		AmountSats: 500, Destination: "bcrt1q", DestinationType: domain.PayoutDestOnChain,
	})
	assert.ErrorIs(t, err, domain.ErrPayoutAmountTooSmall)
}

func TestPayoutService_ApproveAndMarkExecuted(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	adminID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 100_000, "po-app-"+userID)

	ctx := context.Background()
	p, err := svc.RequestPayout(ctx, userID, domain.PayoutRequest{
		AmountSats: 30_000, Destination: "bcrt1qexample", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)

	require.NoError(t, svc.ApprovePayout(ctx, p.ID, adminID))

	// Second approval = 409
	err = svc.ApprovePayout(ctx, p.ID, adminID)
	assert.ErrorIs(t, err, domain.ErrPayoutInvalidStatus)

	// MarkExecuted requires txid
	require.NoError(t, svc.MarkExecuted(ctx, p.ID, "txid-abc-123"))
	bal, _ := ledger.GetAvailableBalance(ctx, nil, userID)
	assert.Equal(t, int64(70_000), bal, "completed payout keeps reservation permanent")
}

func TestPayoutService_RejectPending_RestoresBalance(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 50_000, "po-rej-"+userID)

	ctx := context.Background()
	p, err := svc.RequestPayout(ctx, userID, domain.PayoutRequest{
		AmountSats: 20_000, Destination: "bcrt1qexample", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)

	require.NoError(t, svc.RejectPayout(ctx, p.ID, "duplicate"))
	bal, _ := ledger.GetAvailableBalance(ctx, nil, userID)
	assert.Equal(t, int64(50_000), bal, "rejection compensates the reservation")
}

func TestPayoutService_CancelByOwner(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	other := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 50_000, "po-can-"+userID)

	ctx := context.Background()
	p, err := svc.RequestPayout(ctx, userID, domain.PayoutRequest{
		AmountSats: 10_000, Destination: "bcrt1qexample", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)

	// Other user cannot cancel
	err = svc.CancelPayout(ctx, p.ID, other)
	assert.ErrorIs(t, err, domain.ErrPayoutForbidden)

	// Owner cancel succeeds + restores balance.
	require.NoError(t, svc.CancelPayout(ctx, p.ID, userID))
	bal, _ := ledger.GetAvailableBalance(ctx, nil, userID)
	assert.Equal(t, int64(50_000), bal, "cancel compensates the reservation")
}

func TestPayoutService_RejectIdempotent(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 50_000, "po-rej-idem-"+userID)

	ctx := context.Background()
	p, err := svc.RequestPayout(ctx, userID, domain.PayoutRequest{
		AmountSats: 10_000, Destination: "bcrt1q", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)

	require.NoError(t, svc.RejectPayout(ctx, p.ID, "first"))
	// Second reject: row is no longer in pending/approved → ErrPayoutInvalidStatus.
	err = svc.RejectPayout(ctx, p.ID, "second")
	assert.ErrorIs(t, err, domain.ErrPayoutInvalidStatus)

	// Balance still restored exactly once.
	bal, _ := ledger.GetAvailableBalance(ctx, nil, userID)
	assert.Equal(t, int64(50_000), bal, "double reject must not double-credit")
}

func TestPayoutService_ConcurrentApproveAndCancel_OneWins(t *testing.T) {
	db := payoutLiveDB(t); defer db.Close()
	userID := seedUserPO(t, db)
	adminID := seedUserPO(t, db)
	svc, _, ledger := newPayoutService(t, db)
	seedBalance(t, ledger, userID, 50_000, "po-race-"+userID)

	ctx := context.Background()
	p, err := svc.RequestPayout(ctx, userID, domain.PayoutRequest{
		AmountSats: 5_000, Destination: "bcrt1q", DestinationType: domain.PayoutDestOnChain,
	})
	require.NoError(t, err)

	// Fire approve + cancel concurrently. Conditional UPDATE ensures one wins.
	var wg sync.WaitGroup
	var approveErr, cancelErr error
	wg.Add(2)
	go func() { defer wg.Done(); approveErr = svc.ApprovePayout(ctx, p.ID, adminID) }()
	go func() { defer wg.Done(); cancelErr = svc.CancelPayout(ctx, p.ID, userID) }()
	wg.Wait()

	successes := 0
	for _, e := range []error{approveErr, cancelErr} {
		if e == nil {
			successes++
		} else {
			assert.True(t, errors.Is(e, domain.ErrPayoutInvalidStatus))
		}
	}
	assert.Equal(t, 1, successes, "exactly one of approve/cancel must win")
}
