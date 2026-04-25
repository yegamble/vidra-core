package domain

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLedgerEntryType_IsValid(t *testing.T) {
	valid := []LedgerEntryType{
		LedgerEntryTipIn, LedgerEntryTipOut, LedgerEntryPayoutRequested,
		LedgerEntryPayoutCompleted, LedgerEntryPayoutRejected,
		LedgerEntryPayoutCancelled, LedgerEntrySubscriptionIn,
	}
	for _, et := range valid {
		assert.True(t, et.IsValid(), string(et)+" should be valid")
	}
	assert.False(t, LedgerEntryType("").IsValid())
	assert.False(t, LedgerEntryType("payout_approved").IsValid(),
		"payout_approved is intentionally NOT a ledger entry type per spec-review F04")
	assert.False(t, LedgerEntryType("bogus").IsValid())
}

func TestInvoiceLedgerIdempotencyKey(t *testing.T) {
	got := InvoiceLedgerIdempotencyKey("abc-123", LedgerEntryTipIn)
	assert.Equal(t, "invoice-abc-123-tip_in", got)

	got = InvoiceLedgerIdempotencyKey("abc-123", LedgerEntryTipOut)
	assert.Equal(t, "invoice-abc-123-tip_out", got)
}

func TestPayoutIdempotencyKeys_AllFourDistinct(t *testing.T) {
	pid := "payout-uuid-1"
	keys := []string{
		PayoutRequestIdempotencyKey(pid),
		PayoutRejectedIdempotencyKey(pid),
		PayoutCancelledIdempotencyKey(pid),
		PayoutCompletedIdempotencyKey(pid),
	}
	seen := map[string]bool{}
	for _, k := range keys {
		assert.False(t, seen[k], "duplicate key: "+k)
		seen[k] = true
		assert.Contains(t, k, pid, "key should embed payout id")
	}
	assert.Len(t, seen, 4, "must have four distinct keys for payout lifecycle")
}

func TestLedgerEntryIdempotencyKeys_NeverCollide(t *testing.T) {
	// An invoice-keyed tip_in and a payout-keyed requested must never produce
	// the same idempotency string — verify the prefixes enforce this.
	assert.NotEqual(t,
		InvoiceLedgerIdempotencyKey("1", LedgerEntryTipIn),
		PayoutRequestIdempotencyKey("1"),
	)
}

func TestLedgerDirection_Values(t *testing.T) {
	assert.Equal(t, LedgerDirection("all"), LedgerDirectionAll)
	assert.Equal(t, LedgerDirection("sent"), LedgerDirectionSent)
	assert.Equal(t, LedgerDirection("received"), LedgerDirectionReceived)
}
