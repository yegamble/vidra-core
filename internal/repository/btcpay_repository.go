package repository

import (
	"context"
	"database/sql"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/jmoiron/sqlx"
)

// BTCPayRepository handles database operations for BTCPay invoices and payments.
type BTCPayRepository struct {
	db *sqlx.DB
}

// NewBTCPayRepository creates a new BTCPay repository.
func NewBTCPayRepository(db *sqlx.DB) *BTCPayRepository {
	return &BTCPayRepository{db: db}
}

// CreateInvoice stores a new invoice record.
func (r *BTCPayRepository) CreateInvoice(ctx context.Context, invoice *domain.BTCPayInvoice) error {
	query := `INSERT INTO btcpay_invoices (id, btcpay_invoice_id, user_id, amount_sats, currency, status, btcpay_checkout_link, bitcoin_address, metadata, expires_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`
	_, err := r.db.ExecContext(ctx, query,
		invoice.ID, invoice.BTCPayInvoiceID, invoice.UserID, invoice.AmountSats,
		invoice.Currency, invoice.Status, invoice.CheckoutLink, invoice.BitcoinAddress,
		invoice.Metadata, invoice.ExpiresAt, invoice.CreatedAt, invoice.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting btcpay invoice: %w", err)
	}
	return nil
}

// GetInvoiceByID retrieves an invoice by its internal UUID.
func (r *BTCPayRepository) GetInvoiceByID(ctx context.Context, id string) (*domain.BTCPayInvoice, error) {
	var invoice domain.BTCPayInvoice
	err := r.db.GetContext(ctx, &invoice, "SELECT * FROM btcpay_invoices WHERE id = $1", id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("getting btcpay invoice: %w", err)
	}
	return &invoice, nil
}

// GetInvoiceByBTCPayID retrieves an invoice by its BTCPay Server ID.
func (r *BTCPayRepository) GetInvoiceByBTCPayID(ctx context.Context, btcpayID string) (*domain.BTCPayInvoice, error) {
	var invoice domain.BTCPayInvoice
	err := r.db.GetContext(ctx, &invoice, "SELECT * FROM btcpay_invoices WHERE btcpay_invoice_id = $1", btcpayID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("getting btcpay invoice by btcpay ID: %w", err)
	}
	return &invoice, nil
}

// GetInvoicesByUser retrieves paginated invoices for a user.
func (r *BTCPayRepository) GetInvoicesByUser(ctx context.Context, userID string, limit, offset int) ([]*domain.BTCPayInvoice, error) {
	var invoices []*domain.BTCPayInvoice
	err := r.db.SelectContext(ctx, &invoices,
		"SELECT * FROM btcpay_invoices WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3",
		userID, limit, offset,
	)
	if err != nil {
		return nil, fmt.Errorf("listing btcpay invoices: %w", err)
	}
	return invoices, nil
}

// MarkInvoiceSystemMessageBroadcast atomically sets the broadcast marker on an invoice. It
// uses a conditional update so the FIRST caller wins; subsequent callers see rows=0 and
// receive ErrInvoiceAlreadyBroadcast (the replay-protection signal). The invoice must already
// be settled — the handler enforces that separately by reading the row first.
func (r *BTCPayRepository) MarkInvoiceSystemMessageBroadcast(ctx context.Context, invoiceID string) error {
	result, err := r.db.ExecContext(ctx,
		`UPDATE btcpay_invoices
		 SET system_message_broadcast_at = NOW(), updated_at = NOW()
		 WHERE id = $1 AND system_message_broadcast_at IS NULL`,
		invoiceID,
	)
	if err != nil {
		return fmt.Errorf("marking invoice system_message_broadcast_at: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		// Either the invoice doesn't exist OR the marker is already set. The caller fetched
		// the invoice before calling so the "already broadcast" interpretation is correct.
		return domain.ErrInvoiceAlreadyBroadcast
	}
	return nil
}

// UpdateInvoiceStatus updates the status of an invoice.
func (r *BTCPayRepository) UpdateInvoiceStatus(ctx context.Context, btcpayID string, status domain.InvoiceStatus) error {
	result, err := r.db.ExecContext(ctx,
		"UPDATE btcpay_invoices SET status = $1, updated_at = NOW() WHERE btcpay_invoice_id = $2",
		status, btcpayID,
	)
	if err != nil {
		return fmt.Errorf("updating btcpay invoice status: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrInvoiceNotFound
	}
	return nil
}

// CreatePayment stores a payment record against an invoice.
func (r *BTCPayRepository) CreatePayment(ctx context.Context, payment *domain.BTCPayPayment) error {
	query := `INSERT INTO btcpay_payments (id, invoice_id, btcpay_payment_id, amount_sats, status, transaction_id, block_height, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.db.ExecContext(ctx, query,
		payment.ID, payment.InvoiceID, payment.BTCPayPaymentID,
		payment.AmountSats, payment.Status, payment.TransactionID,
		payment.BlockHeight, payment.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("inserting btcpay payment: %w", err)
	}
	return nil
}
