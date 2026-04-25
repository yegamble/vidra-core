package payments

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/payments"
	"vidra-core/internal/repository"

	"github.com/google/uuid"
)

// BTCPayService implements the business logic for BTCPay Server payment operations.
type BTCPayService struct {
	repo          *repository.BTCPayRepository
	client        *payments.BTCPayClient
	webhookSecret string
	ledger        *LedgerService // optional — nil when ledger feature disabled
}

// NewBTCPayService creates a new BTCPay service.
func NewBTCPayService(repo *repository.BTCPayRepository, client *payments.BTCPayClient, webhookSecret string) *BTCPayService {
	return &BTCPayService{
		repo:          repo,
		client:        client,
		webhookSecret: webhookSecret,
	}
}

// SetLedgerService attaches the optional ledger service. When set, settled-invoice
// webhooks write tip_in/tip_out ledger entries. Called once at startup in app.go.
func (s *BTCPayService) SetLedgerService(l *LedgerService) {
	s.ledger = l
}

// CreateInvoice creates a new payment invoice via BTCPay Server and stores it locally.
func (s *BTCPayService) CreateInvoice(ctx context.Context, userID string, amountSats int64, currency string, metadata map[string]interface{}) (*domain.BTCPayInvoice, error) {
	if amountSats <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	if currency == "" {
		currency = "BTC"
	}

	// Convert sats to BTC for the API (1 BTC = 100,000,000 sats)
	amountBTC := float64(amountSats) / 100_000_000

	resp, err := s.client.CreateInvoice(ctx, &payments.CreateInvoiceRequest{
		Amount:   amountBTC,
		Currency: currency,
		Metadata: metadata,
		Checkout: &payments.InvoiceCheckout{
			ExpirationMinutes: 15,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating BTCPay invoice: %w", err)
	}

	now := time.Now()
	invoice := &domain.BTCPayInvoice{
		ID:              uuid.New().String(),
		BTCPayInvoiceID: resp.ID,
		UserID:          userID,
		AmountSats:      amountSats,
		Currency:        currency,
		Status:          domain.InvoiceStatusNew,
		CheckoutLink:    resp.CheckoutLink,
		ExpiresAt:       time.UnixMilli(resp.ExpirationTime),
		CreatedAt:       now,
		UpdatedAt:       now,
	}

	if err := s.repo.CreateInvoice(ctx, invoice); err != nil {
		return nil, fmt.Errorf("storing invoice: %w", err)
	}

	return invoice, nil
}

// GetInvoice retrieves an invoice by its internal ID.
func (s *BTCPayService) GetInvoice(ctx context.Context, invoiceID string) (*domain.BTCPayInvoice, error) {
	return s.repo.GetInvoiceByID(ctx, invoiceID)
}

// GetPaymentsByUser returns paginated invoices for a user.
func (s *BTCPayService) GetPaymentsByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.BTCPayInvoice, error) {
	if limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	return s.repo.GetInvoicesByUser(ctx, userID, limit, offset)
}

// ProcessWebhook processes an incoming webhook event from BTCPay Server.
// It is idempotent — processing the same event twice has no effect.
func (s *BTCPayService) ProcessWebhook(ctx context.Context, event *domain.BTCPayWebhookEvent) error {
	if event.InvoiceID == "" {
		return fmt.Errorf("webhook event missing invoice ID")
	}

	// Map BTCPay webhook event types to invoice statuses
	var newStatus domain.InvoiceStatus
	switch event.Type {
	case "InvoiceProcessing":
		newStatus = domain.InvoiceStatusProcessing
	case "InvoiceSettled":
		newStatus = domain.InvoiceStatusSettled
	case "InvoiceInvalid":
		newStatus = domain.InvoiceStatusInvalid
	case "InvoiceExpired":
		newStatus = domain.InvoiceStatusExpired
	default:
		slog.Info(fmt.Sprintf("Ignoring unknown BTCPay webhook event type: %s", event.Type))
		return nil
	}

	if err := s.repo.UpdateInvoiceStatus(ctx, event.InvoiceID, newStatus); err != nil {
		return fmt.Errorf("updating invoice status: %w", err)
	}

	slog.Info(fmt.Sprintf("BTCPay invoice %s updated to %s", event.InvoiceID, newStatus))

	// On settlement, write ledger entries (tip_out / tip_in) via the ledger service.
	// Idempotent via UNIQUE idempotency_key — webhook retries are safe.
	if newStatus == domain.InvoiceStatusSettled && s.ledger != nil {
		invoice, err := s.repo.GetInvoiceByBTCPayID(ctx, event.InvoiceID)
		if err != nil {
			slog.Error("ledger write: fetch invoice failed", "btcpay_id", event.InvoiceID, "err", err)
			return nil // don't fail the webhook — status update already succeeded
		}
		if lerr := s.ledger.RecordInvoiceSettlement(ctx, invoice); lerr != nil {
			slog.Error("ledger write: settlement recording failed", "invoice_id", invoice.ID, "err", lerr)
		}
	}

	return nil
}

// ValidateWebhookSignature verifies the HMAC-SHA256 signature of a webhook payload.
func (s *BTCPayService) ValidateWebhookSignature(payload []byte, signature string) bool {
	if s.webhookSecret == "" {
		return true // No secret configured — accept all webhooks (dev mode)
	}

	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(payload)
	expectedSig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	return hmac.Equal([]byte(expectedSig), []byte(signature))
}

// SyncInvoiceStatus fetches the latest status from BTCPay Server and updates locally.
func (s *BTCPayService) SyncInvoiceStatus(ctx context.Context, btcpayInvoiceID string) (*domain.BTCPayInvoice, error) {
	resp, err := s.client.GetInvoice(ctx, btcpayInvoiceID)
	if err != nil {
		return nil, fmt.Errorf("fetching invoice from BTCPay: %w", err)
	}

	newStatus := domain.InvoiceStatus(resp.Status)
	if err := s.repo.UpdateInvoiceStatus(ctx, btcpayInvoiceID, newStatus); err != nil {
		return nil, fmt.Errorf("updating local invoice status: %w", err)
	}

	// Fetch updated local record
	invoice, err := s.repo.GetInvoiceByBTCPayID(ctx, btcpayInvoiceID)
	if err != nil {
		return nil, err
	}

	// Parse amount from response
	if resp.Amount != "" {
		if amountBTC, err := strconv.ParseFloat(resp.Amount, 64); err == nil {
			invoice.AmountSats = int64(amountBTC * 100_000_000)
		}
	}

	return invoice, nil
}
