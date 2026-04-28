package inner_circle

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type fakeUpserter struct {
	called    bool
	gotUser   uuid.UUID
	gotChan   uuid.UUID
	gotTier   string
	gotInvoice uuid.UUID
	err       error
}

func (f *fakeUpserter) UpsertActiveByBTCPay(_ context.Context, userID, channelID uuid.UUID, tierID string, invoiceID uuid.UUID) (uuid.UUID, error) {
	f.called = true
	f.gotUser = userID
	f.gotChan = channelID
	f.gotTier = tierID
	f.gotInvoice = invoiceID
	return uuid.New(), f.err
}

func makeInvoice(t *testing.T, meta map[string]interface{}) *domain.BTCPayInvoice {
	t.Helper()
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return &domain.BTCPayInvoice{
		ID:       uuid.New().String(),
		Metadata: b,
	}
}

func TestHandle_NonInnerCircleType_NoOp(t *testing.T) {
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	inv := makeInvoice(t, map[string]interface{}{"type": "tip"})
	if err := h.Handle(context.Background(), inv); err != nil {
		t.Fatalf("err = %v", err)
	}
	if up.called {
		t.Fatalf("non-inner-circle invoice should not call upserter")
	}
}

func TestHandle_NoMetadata_NoOp(t *testing.T) {
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	inv := &domain.BTCPayInvoice{ID: uuid.New().String()}
	if err := h.Handle(context.Background(), inv); err != nil {
		t.Fatalf("err = %v", err)
	}
	if up.called {
		t.Fatalf("invoice without metadata should not invoke upserter")
	}
}

func TestHandle_InnerCircleHappyPath(t *testing.T) {
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	channelID := uuid.New()
	userID := uuid.New()
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": channelID.String(),
		"tier_id":    "vip",
		"user_id":    userID.String(),
	})
	if err := h.Handle(context.Background(), inv); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !up.called {
		t.Fatalf("expected upserter to be called")
	}
	if up.gotUser != userID {
		t.Fatalf("user_id = %s, want %s", up.gotUser, userID)
	}
	if up.gotChan != channelID {
		t.Fatalf("channel_id = %s, want %s", up.gotChan, channelID)
	}
	if up.gotTier != "vip" {
		t.Fatalf("tier_id = %q, want vip", up.gotTier)
	}
}

func TestHandle_InvalidTier(t *testing.T) {
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": uuid.New().String(),
		"tier_id":    "diamond",
		"user_id":    uuid.New().String(),
	})
	if err := h.Handle(context.Background(), inv); err == nil {
		t.Fatalf("expected error for invalid tier")
	}
	if up.called {
		t.Fatalf("upserter must not be called for invalid tier")
	}
}

func TestHandle_MissingFields(t *testing.T) {
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	inv := makeInvoice(t, map[string]interface{}{
		"type":    "inner_circle",
		"tier_id": "vip",
	})
	if err := h.Handle(context.Background(), inv); err == nil {
		t.Fatalf("expected error when metadata is missing fields")
	}
	if up.called {
		t.Fatalf("upserter must not be called when metadata incomplete")
	}
}

func TestHandle_UpserterError_Propagates(t *testing.T) {
	up := &fakeUpserter{err: errors.New("db blip")}
	h := NewBTCPaySettlementHook(up, nil)
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": uuid.New().String(),
		"tier_id":    "vip",
		"user_id":    uuid.New().String(),
	})
	if err := h.Handle(context.Background(), inv); err == nil {
		t.Fatalf("expected error from upserter to propagate")
	}
}

func TestHandle_RenewalIdempotent(t *testing.T) {
	// The repository implementation stack-extends expires_at on conflict — the
	// hook itself just delegates. Double-call should pass through cleanly.
	up := &fakeUpserter{}
	h := NewBTCPaySettlementHook(up, nil)
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": uuid.New().String(),
		"tier_id":    "supporter",
		"user_id":    uuid.New().String(),
	})
	for i := 0; i < 2; i++ {
		if err := h.Handle(context.Background(), inv); err != nil {
			t.Fatalf("iter %d: err = %v", i, err)
		}
	}
}

type fakeLedgerWriter struct {
	called   bool
	captured SubscriptionLedgerEntry
	err      error
}

func (f *fakeLedgerWriter) RecordSubscriptionIn(_ context.Context, e SubscriptionLedgerEntry) error {
	f.called = true
	f.captured = e
	return f.err
}

func TestHandle_WritesSubscriptionInLedger_OnSuccess(t *testing.T) {
	up := &fakeUpserter{}
	lw := &fakeLedgerWriter{}
	h := NewBTCPaySettlementHook(up, lw)
	channelID := uuid.New()
	userID := uuid.New()
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": channelID.String(),
		"tier_id":    "vip",
		"user_id":    userID.String(),
	})
	inv.AmountSats = 22750
	inv.Currency = "BTC"

	if err := h.Handle(context.Background(), inv); err != nil {
		t.Fatalf("err = %v", err)
	}
	if !lw.called {
		t.Fatalf("ledger writer should be invoked on inner_circle settle")
	}
	if lw.captured.IdempotencyKey != "ic-sub-"+inv.ID {
		t.Fatalf("idempotency_key = %q, want ic-sub-%s", lw.captured.IdempotencyKey, inv.ID)
	}
	if lw.captured.AmountSats != 22750 {
		t.Fatalf("amount_sats = %d, want 22750", lw.captured.AmountSats)
	}
	if lw.captured.UserID != userID.String() {
		t.Fatalf("user_id = %q, want %s", lw.captured.UserID, userID)
	}
	if lw.captured.ChannelID != channelID.String() {
		t.Fatalf("channel_id mismatch")
	}
}

func TestHandle_LedgerFailure_DoesNotBubble(t *testing.T) {
	up := &fakeUpserter{}
	lw := &fakeLedgerWriter{err: errors.New("ledger db down")}
	h := NewBTCPaySettlementHook(up, lw)
	inv := makeInvoice(t, map[string]interface{}{
		"type":       "inner_circle",
		"channel_id": uuid.New().String(),
		"tier_id":    "supporter",
		"user_id":    uuid.New().String(),
	})
	if err := h.Handle(context.Background(), inv); err != nil {
		t.Fatalf("ledger failure must not propagate; got %v", err)
	}
	if !up.called {
		t.Fatalf("membership activation must succeed even if ledger fails")
	}
}

// Ensure *fakeUpserter satisfies the interface — guards against drift.
var _ MembershipUpserter = (*fakeUpserter)(nil)
var _ SubscriptionLedgerWriter = (*fakeLedgerWriter)(nil)
