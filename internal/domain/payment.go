package domain

import (
	"database/sql"
	"time"
)

// IOTAWallet represents a user's IOTA wallet
type IOTAWallet struct {
	ID            string    `json:"id" db:"id"`
	UserID        string    `json:"user_id" db:"user_id"`
	EncryptedSeed []byte    `json:"-" db:"encrypted_seed"` // Never expose in JSON
	SeedNonce     []byte    `json:"-" db:"seed_nonce"`     // Never expose in JSON
	Address       string    `json:"address" db:"address"`
	BalanceIOTA   int64     `json:"balance_iota" db:"balance_iota"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// PaymentIntentStatus represents the status of a payment intent
type PaymentIntentStatus string

const (
	PaymentIntentStatusPending  PaymentIntentStatus = "pending"
	PaymentIntentStatusPaid     PaymentIntentStatus = "paid"
	PaymentIntentStatusExpired  PaymentIntentStatus = "expired"
	PaymentIntentStatusRefunded PaymentIntentStatus = "refunded"
)

// IOTAPaymentIntent represents a payment request
type IOTAPaymentIntent struct {
	ID             string              `json:"id" db:"id"`
	UserID         string              `json:"user_id" db:"user_id"`
	VideoID        sql.NullString      `json:"video_id,omitempty" db:"video_id"`
	AmountIOTA     int64               `json:"amount_iota" db:"amount_iota"`
	PaymentAddress string              `json:"payment_address" db:"payment_address"`
	Status         PaymentIntentStatus `json:"status" db:"status"`
	ExpiresAt      time.Time           `json:"expires_at" db:"expires_at"`
	PaidAt         sql.NullTime        `json:"paid_at,omitempty" db:"paid_at"`
	TransactionID  sql.NullString      `json:"transaction_id,omitempty" db:"transaction_id"`
	Metadata       []byte              `json:"metadata,omitempty" db:"metadata"` // JSONB
	CreatedAt      time.Time           `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time           `json:"updated_at" db:"updated_at"`
}

// TransactionType represents the type of IOTA transaction
type TransactionType string

const (
	TransactionTypeDeposit    TransactionType = "deposit"
	TransactionTypeWithdrawal TransactionType = "withdrawal"
	TransactionTypePayment    TransactionType = "payment"
)

// TransactionStatus represents the status of a transaction
type TransactionStatus string

const (
	TransactionStatusPending   TransactionStatus = "pending"
	TransactionStatusConfirmed TransactionStatus = "confirmed"
	TransactionStatusFailed    TransactionStatus = "failed"
)

// IOTATransaction represents a blockchain transaction
type IOTATransaction struct {
	ID              string            `json:"id" db:"id"`
	WalletID        sql.NullString    `json:"wallet_id,omitempty" db:"wallet_id"`
	TransactionHash string            `json:"transaction_hash" db:"transaction_hash"`
	AmountIOTA      int64             `json:"amount_iota" db:"amount_iota"`
	TxType          TransactionType   `json:"tx_type" db:"tx_type"`
	Status          TransactionStatus `json:"status" db:"status"`
	Confirmations   int               `json:"confirmations" db:"confirmations"`
	FromAddress     sql.NullString    `json:"from_address,omitempty" db:"from_address"`
	ToAddress       sql.NullString    `json:"to_address,omitempty" db:"to_address"`
	Metadata        []byte            `json:"metadata,omitempty" db:"metadata"` // JSONB
	ConfirmedAt     sql.NullTime      `json:"confirmed_at,omitempty" db:"confirmed_at"`
	CreatedAt       time.Time         `json:"created_at" db:"created_at"`
}

// Payment errors using DomainError
var (
	ErrWalletNotFound        = NewDomainError("WALLET_NOT_FOUND", "Wallet not found")
	ErrWalletAlreadyExists   = NewDomainError("WALLET_ALREADY_EXISTS", "Wallet already exists for this user")
	ErrPaymentIntentNotFound = NewDomainError("PAYMENT_INTENT_NOT_FOUND", "Payment intent not found")
	ErrPaymentIntentExpired  = NewDomainError("PAYMENT_INTENT_EXPIRED", "Payment intent has expired")
	ErrInvalidAmount         = NewDomainError("INVALID_AMOUNT", "Invalid payment amount")
	ErrInsufficientBalance   = NewDomainError("INSUFFICIENT_BALANCE", "Insufficient wallet balance")
	ErrTransactionNotFound   = NewDomainError("TRANSACTION_NOT_FOUND", "Transaction not found")
	ErrInvalidAddress        = NewDomainError("INVALID_ADDRESS", "Invalid IOTA address")
	ErrPaymentAlreadyPaid    = NewDomainError("PAYMENT_ALREADY_PAID", "Payment intent already paid")
	ErrInvalidSeed           = NewDomainError("INVALID_SEED", "Invalid wallet seed")
	ErrEncryptionFailed      = NewDomainError("ENCRYPTION_FAILED", "Failed to encrypt wallet seed")
	ErrDecryptionFailed      = NewDomainError("DECRYPTION_FAILED", "Failed to decrypt wallet seed")
	ErrIOTANodeUnavailable   = NewDomainError("IOTA_NODE_UNAVAILABLE", "IOTA node is unavailable")
	ErrTransactionBroadcast  = NewDomainError("TRANSACTION_BROADCAST_FAILED", "Failed to broadcast transaction")
	ErrRateLimitExceeded     = NewDomainError("RATE_LIMIT_EXCEEDED", "Rate limit exceeded for wallet operations")
)
