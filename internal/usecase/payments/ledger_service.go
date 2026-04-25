package payments

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
)

// LedgerService is the business-logic layer for payment_ledger writes driven by
// invoice settlement webhooks and (later, Task 4) payout lifecycle transitions.
// Payout-specific lifecycle is in PayoutService; this service handles the
// invoice-side of the ledger (tip_in / tip_out).
type LedgerService struct {
	ledgerRepo  *repository.PaymentLedgerRepository
	invoiceRepo *repository.BTCPayRepository
	channelRepo ChannelLookup
	notifier    NotificationEmitter
}

// ChannelLookup is the subset of channel-repo behaviour the ledger service
// needs — resolve a channel UUID to its account_id (owner user UUID).
type ChannelLookup interface {
	ResolveOwner(ctx context.Context, channelID string) (ownerUserID string, err error)
}

// NotificationEmitter is the minimal interface LedgerService uses to push a
// notification (e.g. tip_received) without importing the full notification
// service graph.
type NotificationEmitter interface {
	Emit(ctx context.Context, userID string, nType domain.NotificationType, title, message string, data map[string]interface{}) error
}

// NewLedgerService wires the ledger service for the invoice-settlement path.
func NewLedgerService(
	ledgerRepo *repository.PaymentLedgerRepository,
	invoiceRepo *repository.BTCPayRepository,
	channelRepo ChannelLookup,
	notifier NotificationEmitter,
) *LedgerService {
	return &LedgerService{
		ledgerRepo:  ledgerRepo,
		invoiceRepo: invoiceRepo,
		channelRepo: channelRepo,
		notifier:    notifier,
	}
}

// RecordInvoiceSettlement writes the tip_out (payer) and, when a channel
// recipient is resolvable, tip_in (channel owner) ledger entries for a
// just-settled invoice. Idempotent under webhook retries via the UNIQUE
// idempotency_key index. Emits a tip_received notification on successful tip_in.
//
// Fails soft: notification-emit errors are logged but do not roll back the
// ledger writes (best-effort delivery; stop-hook requirement is correctness of
// ledger, not delivery guarantees of the transient notification).
func (s *LedgerService) RecordInvoiceSettlement(ctx context.Context, invoice *domain.BTCPayInvoice) error {
	if invoice == nil {
		return fmt.Errorf("RecordInvoiceSettlement: nil invoice")
	}
	if invoice.Status != domain.InvoiceStatusSettled {
		return fmt.Errorf("RecordInvoiceSettlement: expected Settled, got %s", invoice.Status)
	}

	// Parse metadata to determine if this is a tip (and resolve recipient channel).
	meta, _ := parseMetadata(invoice.Metadata)
	isTip := meta["type"] == "tip"

	// tip_out — always record for the payer as long as it's a tip. Non-tip
	// invoices (e.g. inner circle, once they land) get their own entry types.
	if !isTip {
		return nil
	}

	payerEntry := &domain.PaymentLedgerEntry{
		UserID:         invoice.UserID,
		EntryType:      domain.LedgerEntryTipOut,
		AmountSats:     -invoice.AmountSats,
		Currency:       invoice.Currency,
		InvoiceID:      nullString(invoice.ID),
		Metadata:       invoice.Metadata,
		IdempotencyKey: domain.InvoiceLedgerIdempotencyKey(invoice.ID, domain.LedgerEntryTipOut),
	}
	// Resolve channel → recipient owner (optional).
	channelIDRaw, _ := meta["channel_id"].(string)
	var recipientUserID string
	if channelIDRaw != "" && s.channelRepo != nil {
		owner, err := s.channelRepo.ResolveOwner(ctx, channelIDRaw)
		if err == nil && owner != "" {
			recipientUserID = owner
			payerEntry.CounterpartyUserID = nullString(owner)
			payerEntry.ChannelID = nullString(channelIDRaw)
		}
	}

	if err := s.ledgerRepo.Record(ctx, payerEntry); err != nil && !errors.Is(err, domain.ErrLedgerDuplicate) {
		return fmt.Errorf("recording tip_out: %w", err)
	}

	// tip_in for the recipient (only if resolvable).
	if recipientUserID != "" {
		recipientEntry := &domain.PaymentLedgerEntry{
			UserID:             recipientUserID,
			CounterpartyUserID: nullString(invoice.UserID),
			ChannelID:          nullString(channelIDRaw),
			EntryType:          domain.LedgerEntryTipIn,
			AmountSats:         invoice.AmountSats,
			Currency:           invoice.Currency,
			InvoiceID:          nullString(invoice.ID),
			Metadata:           invoice.Metadata,
			IdempotencyKey:     domain.InvoiceLedgerIdempotencyKey(invoice.ID, domain.LedgerEntryTipIn),
		}
		if err := s.ledgerRepo.Record(ctx, recipientEntry); err != nil && !errors.Is(err, domain.ErrLedgerDuplicate) {
			return fmt.Errorf("recording tip_in: %w", err)
		}

		// Best-effort notification — don't fail the webhook if notify fails.
		if s.notifier != nil {
			title := "You received a tip"
			message := fmt.Sprintf("%d sats tip received", invoice.AmountSats)
			if err := s.notifier.Emit(ctx, recipientUserID, domain.NotificationTipReceived, title, message, map[string]interface{}{
				"invoice_id":  invoice.ID,
				"channel_id":  channelIDRaw,
				"amount_sats": invoice.AmountSats,
			}); err != nil {
				slog.Warn("tip_received notification emit failed", "err", err, "recipient", recipientUserID)
			}
		}
	}

	return nil
}

func parseMetadata(raw []byte) (map[string]interface{}, error) {
	if len(raw) == 0 {
		return map[string]interface{}{}, nil
	}
	var m map[string]interface{}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	return m, nil
}

// nullString produces a sql.NullString from a possibly-empty string.
func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}
