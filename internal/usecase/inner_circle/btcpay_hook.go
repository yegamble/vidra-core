package inner_circle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

// MembershipUpserter is the subset of the membership repository used by the
// BTCPay settlement hook. Defined as an interface so the hook can be tested
// without a real DB.
type MembershipUpserter interface {
	UpsertActiveByBTCPay(ctx context.Context, userID, channelID uuid.UUID, tierID string, btcpayInvoiceID uuid.UUID) (uuid.UUID, error)
}

// SubscriptionLedgerWriter writes the `subscription_in` ledger entry on
// successful Inner Circle activation. The interface is local to this package
// to avoid pulling in the payments/ledger graph; the concrete impl is the
// payment_ledger repository.
type SubscriptionLedgerWriter interface {
	RecordSubscriptionIn(ctx context.Context, params SubscriptionLedgerEntry) error
}

// SubscriptionLedgerEntry is the shape the BTCPay/Polar hooks pass to the
// ledger writer. Idempotency_key is required and stable per source event.
type SubscriptionLedgerEntry struct {
	UserID         string
	ChannelID      string
	AmountSats     int64
	Currency       string
	InvoiceID      string
	IdempotencyKey string
	Metadata       map[string]interface{}
}

// BTCPaySettlementHook handles a settled BTCPay invoice whose metadata.type is
// "inner_circle". For other invoice types it is a no-op.
//
// The hook is idempotent: the underlying UpsertActiveByBTCPay path either
// inserts a new active row or extends `expires_at` on the existing row by 30
// days. Webhook retries from BTCPay therefore stack-extend the same membership
// rather than producing duplicates — desired behavior for renewal invoices.
//
// The hook also writes a `subscription_in` ledger entry keyed on
// `ic-sub-{btcpay_invoice_id}` so the transaction history surfaces the
// activation. Idempotency_key prevents double-writes on webhook retry.
type BTCPaySettlementHook struct {
	memberships MembershipUpserter
	ledger      SubscriptionLedgerWriter // optional — nil-safe
}

// NewBTCPaySettlementHook builds the hook.
func NewBTCPaySettlementHook(memberships MembershipUpserter, ledger SubscriptionLedgerWriter) *BTCPaySettlementHook {
	return &BTCPaySettlementHook{memberships: memberships, ledger: ledger}
}

// Handle is invoked by BTCPayService.AddSettlementHook on every settled invoice.
func (h *BTCPaySettlementHook) Handle(ctx context.Context, invoice *domain.BTCPayInvoice) error {
	if h == nil || h.memberships == nil || invoice == nil || len(invoice.Metadata) == 0 {
		return nil
	}
	var meta map[string]interface{}
	if err := json.Unmarshal(invoice.Metadata, &meta); err != nil {
		return fmt.Errorf("inner_circle hook: parse metadata: %w", err)
	}
	if meta["type"] != "inner_circle" {
		return nil // not for us
	}

	channelIDStr, _ := meta["channel_id"].(string)
	tierID, _ := meta["tier_id"].(string)
	userIDStr, _ := meta["user_id"].(string)
	if channelIDStr == "" || tierID == "" || userIDStr == "" {
		return errors.New("inner_circle hook: metadata missing channel_id/tier_id/user_id")
	}
	if !ValidTierID(tierID) {
		return fmt.Errorf("inner_circle hook: invalid tier %q", tierID)
	}

	channelID, err := uuid.Parse(channelIDStr)
	if err != nil {
		return fmt.Errorf("inner_circle hook: bad channel_id: %w", err)
	}
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return fmt.Errorf("inner_circle hook: bad user_id: %w", err)
	}
	invoiceUUID, err := uuid.Parse(invoice.ID)
	if err != nil {
		return fmt.Errorf("inner_circle hook: bad invoice id: %w", err)
	}

	membershipID, err := h.memberships.UpsertActiveByBTCPay(ctx, userID, channelID, tierID, invoiceUUID)
	if err != nil {
		return fmt.Errorf("inner_circle hook: upsert membership: %w", err)
	}
	slog.Info("inner_circle_btcpay_settled",
		"membership_id", membershipID.String(),
		"channel_id", channelID.String(),
		"user_id", userID.String(),
		"tier_id", tierID,
		"invoice_id", invoice.ID,
	)
	// Write the subscription_in ledger entry. Idempotency_key is stable per
	// invoice ID — webhook retries don't double-credit. Best-effort: a ledger
	// write failure logs but does not roll back the membership activation
	// (the activation is the user-visible signal; ledger is accounting).
	if h.ledger != nil {
		ledgerErr := h.ledger.RecordSubscriptionIn(ctx, SubscriptionLedgerEntry{
			UserID:         userID.String(),
			ChannelID:      channelID.String(),
			AmountSats:     invoice.AmountSats,
			Currency:       invoice.Currency,
			InvoiceID:      invoice.ID,
			IdempotencyKey: "ic-sub-" + invoice.ID,
			Metadata: map[string]interface{}{
				"type":       "inner_circle",
				"rail":       "btcpay",
				"channel_id": channelID.String(),
				"tier_id":    tierID,
			},
		})
		if ledgerErr != nil {
			slog.Warn("inner_circle ledger write failed", "invoice_id", invoice.ID, "err", ledgerErr)
		}
	}
	return nil
}
