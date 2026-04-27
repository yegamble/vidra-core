package domain

import (
	"database/sql"
	"time"
)

// BTCPay invoice statuses as returned by BTCPay Server Greenfield API.
type InvoiceStatus string

const (
	InvoiceStatusNew        InvoiceStatus = "New"
	InvoiceStatusProcessing InvoiceStatus = "Processing"
	InvoiceStatusSettled    InvoiceStatus = "Settled"
	InvoiceStatusInvalid    InvoiceStatus = "Invalid"
	InvoiceStatusExpired    InvoiceStatus = "Expired"
)

// BTCPayInvoice represents a payment invoice stored locally, mirroring BTCPay Server state.
//
// Lightning fields (LightningInvoice, LightningExpiresAt) are populated by the
// invoice-creation flow when the request opted into Lightning via
// payment_method = "lightning" or "both". They are transient on the API
// response only — the canonical LN destination lives in BTCPay Server and is
// re-fetched on demand via GetInvoicePaymentMethods.
type BTCPayInvoice struct {
	ID                 string         `json:"id" db:"id"`
	BTCPayInvoiceID    string         `json:"btcpay_invoice_id" db:"btcpay_invoice_id"`
	UserID             string         `json:"user_id" db:"user_id"`
	AmountSats         int64          `json:"amount_sats" db:"amount_sats"`
	Currency           string         `json:"currency" db:"currency"`
	Status             InvoiceStatus  `json:"status" db:"status"`
	CheckoutLink       string         `json:"checkout_link" db:"btcpay_checkout_link"`
	BitcoinAddress     sql.NullString `json:"bitcoin_address,omitempty" db:"bitcoin_address"`
	LightningInvoice   *string        `json:"lightning_invoice,omitempty" db:"-"`
	LightningExpiresAt *time.Time     `json:"lightning_expires_at,omitempty" db:"-"`
	Metadata           []byte         `json:"metadata,omitempty" db:"metadata"`
	ExpiresAt          time.Time      `json:"expires_at" db:"expires_at"`
	SettledAt          sql.NullTime   `json:"settled_at,omitempty" db:"settled_at"`
	// SystemMessageBroadcastAt is set when this invoice has been used to publish a tip
	// system-message into a live-stream chat. Single-use guard against replay: a second
	// attempt to broadcast with the same invoice is rejected with 409.
	SystemMessageBroadcastAt sql.NullTime `json:"system_message_broadcast_at,omitempty" db:"system_message_broadcast_at"`
	CreatedAt                time.Time    `json:"created_at" db:"created_at"`
	UpdatedAt                time.Time    `json:"updated_at" db:"updated_at"`
}

// BTCPayPayment represents an individual payment received against an invoice.
type BTCPayPayment struct {
	ID              string    `json:"id" db:"id"`
	InvoiceID       string    `json:"invoice_id" db:"invoice_id"`
	BTCPayPaymentID string    `json:"btcpay_payment_id" db:"btcpay_payment_id"`
	AmountSats      int64     `json:"amount_sats" db:"amount_sats"`
	Status          string    `json:"status" db:"status"`
	TransactionID   string    `json:"transaction_id" db:"transaction_id"`
	BlockHeight     int64     `json:"block_height" db:"block_height"`
	CreatedAt       time.Time `json:"created_at" db:"created_at"`
}

// BTCPayWebhookEvent represents an incoming webhook event from BTCPay Server.
type BTCPayWebhookEvent struct {
	Type            string `json:"type"`
	InvoiceID       string `json:"invoiceId"`
	StoreID         string `json:"storeId"`
	OriginalPayload []byte `json:"-"`
}

// BTCPay-specific domain errors.
var (
	ErrInvoiceNotFound        = NewDomainError("INVOICE_NOT_FOUND", "Invoice not found")
	ErrInvoiceExpired         = NewDomainError("INVOICE_EXPIRED", "Invoice has expired")
	ErrInvoiceUnsettled       = NewDomainError("INVOICE_UNSETTLED", "Invoice has not been settled")
	ErrInvoiceAlreadyBroadcast = NewDomainError("INVOICE_ALREADY_BROADCAST", "Invoice has already been used to broadcast a system message")
	ErrInvalidAmount          = NewDomainError("INVALID_AMOUNT", "Invalid payment amount")
	ErrBTCPayUnavailable      = NewDomainError("BTCPAY_UNAVAILABLE", "BTCPay Server is unavailable")
)
