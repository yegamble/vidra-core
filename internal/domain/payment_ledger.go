package domain

import (
	"database/sql"
	"fmt"
	"time"
)

// LedgerEntryType mirrors the Postgres ledger_entry_type enum declared in
// migration 094. Every money-movement event records one entry.
type LedgerEntryType string

const (
	LedgerEntryTipIn            LedgerEntryType = "tip_in"
	LedgerEntryTipOut           LedgerEntryType = "tip_out"
	LedgerEntryPayoutRequested  LedgerEntryType = "payout_requested"
	LedgerEntryPayoutCompleted  LedgerEntryType = "payout_completed"
	LedgerEntryPayoutRejected   LedgerEntryType = "payout_rejected"
	LedgerEntryPayoutCancelled  LedgerEntryType = "payout_cancelled"
	LedgerEntrySubscriptionIn   LedgerEntryType = "subscription_in"
)

// IsValid reports whether the entry type is a known canonical constant.
func (t LedgerEntryType) IsValid() bool {
	switch t {
	case LedgerEntryTipIn, LedgerEntryTipOut, LedgerEntryPayoutRequested,
		LedgerEntryPayoutCompleted, LedgerEntryPayoutRejected,
		LedgerEntryPayoutCancelled, LedgerEntrySubscriptionIn:
		return true
	}
	return false
}

// PaymentLedgerEntry represents one row in the payment_ledger table.
type PaymentLedgerEntry struct {
	ID                 string          `db:"id" json:"id"`
	UserID             string          `db:"user_id" json:"user_id"`
	CounterpartyUserID sql.NullString  `db:"counterparty_user_id" json:"counterparty_user_id,omitempty"`
	ChannelID          sql.NullString  `db:"channel_id" json:"channel_id,omitempty"`
	EntryType          LedgerEntryType `db:"entry_type" json:"entry_type"`
	AmountSats         int64           `db:"amount_sats" json:"amount_sats"`
	Currency           string          `db:"currency" json:"currency"`
	InvoiceID          sql.NullString  `db:"invoice_id" json:"invoice_id,omitempty"`
	PayoutID           sql.NullString  `db:"payout_id" json:"payout_id,omitempty"`
	Metadata           []byte          `db:"metadata" json:"metadata,omitempty"`
	IdempotencyKey     string          `db:"idempotency_key" json:"-"`
	CreatedAt          time.Time       `db:"created_at" json:"created_at"`
}

// LedgerListFilter captures query params for GET /wallet/transactions.
type LedgerListFilter struct {
	UserID     string
	Direction  LedgerDirection
	EntryType  LedgerEntryType // zero-value = any
	Limit      int
	Offset     int
	StartDate  *time.Time
	EndDate    *time.Time
}

// LedgerDirection narrows results to sent (negative amount), received (positive),
// or all entries for a user.
type LedgerDirection string

const (
	LedgerDirectionAll      LedgerDirection = "all"
	LedgerDirectionSent     LedgerDirection = "sent"
	LedgerDirectionReceived LedgerDirection = "received"
)

// HydratedLedgerEntry wraps a PaymentLedgerEntry with hydrated counterparty
// and channel names (joined at query time — not cached on the row).
type HydratedLedgerEntry struct {
	PaymentLedgerEntry
	CounterpartyName string `db:"counterparty_name" json:"counterparty_name,omitempty"`
	ChannelName      string `db:"channel_name" json:"channel_name,omitempty"`
}

// WalletBalance is the response shape for GET /wallet/balance.
// See Task 3 Key Decisions (plan) for the invariant:
//   AvailableSats = SUM(amount_sats) WHERE user_id = <user>
//   PendingPayoutSats = ABS(SUM(amount_sats)) for payout_requested entries
//                       whose payout is still pending OR approved (informational,
//                       NOT subtracted again from AvailableSats).
type WalletBalance struct {
	AvailableSats     int64     `json:"available_sats"`
	PendingPayoutSats int64     `json:"pending_payout_sats"`
	Currency          string    `json:"currency"`
	AsOf              time.Time `json:"as_of"`
}

// IdempotencyKey builds a canonical key for webhook-triggered ledger writes.
// Invoice-based keys use `invoice-<invoice-uuid>-<entry_type>`; payout-lifecycle
// keys use `payout-<payout-uuid>-<suffix>` (see payout_* builders below).
func InvoiceLedgerIdempotencyKey(invoiceID string, entryType LedgerEntryType) string {
	return fmt.Sprintf("invoice-%s-%s", invoiceID, entryType)
}

// Payout-lifecycle idempotency key builders — one per state transition.
// All four keys share the payout UUID prefix for enumerability in audits.
func PayoutRequestIdempotencyKey(payoutID string) string {
	return fmt.Sprintf("payout-%s-requested", payoutID)
}
func PayoutRejectedIdempotencyKey(payoutID string) string {
	return fmt.Sprintf("payout-%s-rejected", payoutID)
}
func PayoutCancelledIdempotencyKey(payoutID string) string {
	return fmt.Sprintf("payout-%s-cancelled", payoutID)
}
func PayoutCompletedIdempotencyKey(payoutID string) string {
	return fmt.Sprintf("payout-%s-completed", payoutID)
}

// Ledger-layer domain errors.
var (
	ErrLedgerEntryInvalid      = NewDomainError("LEDGER_ENTRY_INVALID", "Invalid ledger entry")
	ErrInsufficientBalance     = NewDomainError("INSUFFICIENT_BALANCE", "Insufficient available balance")
	ErrLedgerDuplicate         = NewDomainError("LEDGER_DUPLICATE", "Ledger entry already exists for this idempotency key")
)
