package port

import (
	"context"

	"vidra-core/internal/domain"
)

// BTCPayService defines the interface for Bitcoin payment operations via BTCPay Server.
type BTCPayService interface {
	CreateInvoice(ctx context.Context, userID string, amountSats int64, currency string, metadata map[string]interface{}) (*domain.BTCPayInvoice, error)
	GetInvoice(ctx context.Context, invoiceID string) (*domain.BTCPayInvoice, error)
	GetPaymentsByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.BTCPayInvoice, error)
	ProcessWebhook(ctx context.Context, event *domain.BTCPayWebhookEvent) error
}
