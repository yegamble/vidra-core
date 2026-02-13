package domain

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestPaymentIntentStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   PaymentIntentStatus
		expected string
	}{
		{"Pending", PaymentIntentStatusPending, "pending"},
		{"Paid", PaymentIntentStatusPaid, "paid"},
		{"Expired", PaymentIntentStatusExpired, "expired"},
		{"Refunded", PaymentIntentStatusRefunded, "refunded"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, PaymentIntentStatus(tt.expected), tt.status)
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestTransactionTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		txType   TransactionType
		expected string
	}{
		{"Deposit", TransactionTypeDeposit, "deposit"},
		{"Withdrawal", TransactionTypeWithdrawal, "withdrawal"},
		{"Payment", TransactionTypePayment, "payment"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, TransactionType(tt.expected), tt.txType)
			assert.Equal(t, tt.expected, string(tt.txType))
		})
	}
}

func TestTransactionStatusConstants(t *testing.T) {
	tests := []struct {
		name     string
		status   TransactionStatus
		expected string
	}{
		{"Pending", TransactionStatusPending, "pending"},
		{"Confirmed", TransactionStatusConfirmed, "confirmed"},
		{"Failed", TransactionStatusFailed, "failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, TransactionStatus(tt.expected), tt.status)
			assert.Equal(t, tt.expected, string(tt.status))
		})
	}
}

func TestPaymentDomainErrors(t *testing.T) {
	tests := []struct {
		name         string
		err          DomainError
		expectedCode string
		expectedMsg  string
	}{
		{"ErrWalletNotFound", ErrWalletNotFound, "WALLET_NOT_FOUND", "Wallet not found"},
		{"ErrWalletAlreadyExists", ErrWalletAlreadyExists, "WALLET_ALREADY_EXISTS", "Wallet already exists for this user"},
		{"ErrPaymentIntentNotFound", ErrPaymentIntentNotFound, "PAYMENT_INTENT_NOT_FOUND", "Payment intent not found"},
		{"ErrPaymentIntentExpired", ErrPaymentIntentExpired, "PAYMENT_INTENT_EXPIRED", "Payment intent has expired"},
		{"ErrInvalidAmount", ErrInvalidAmount, "INVALID_AMOUNT", "Invalid payment amount"},
		{"ErrInsufficientBalance", ErrInsufficientBalance, "INSUFFICIENT_BALANCE", "Insufficient wallet balance"},
		{"ErrTransactionNotFound", ErrTransactionNotFound, "TRANSACTION_NOT_FOUND", "Transaction not found"},
		{"ErrInvalidAddress", ErrInvalidAddress, "INVALID_ADDRESS", "Invalid IOTA address"},
		{"ErrPaymentAlreadyPaid", ErrPaymentAlreadyPaid, "PAYMENT_ALREADY_PAID", "Payment intent already paid"},
		{"ErrInvalidSeed", ErrInvalidSeed, "INVALID_SEED", "Invalid wallet seed"},
		{"ErrEncryptionFailed", ErrEncryptionFailed, "ENCRYPTION_FAILED", "Failed to encrypt wallet seed"},
		{"ErrDecryptionFailed", ErrDecryptionFailed, "DECRYPTION_FAILED", "Failed to decrypt wallet seed"},
		{"ErrIOTANodeUnavailable", ErrIOTANodeUnavailable, "IOTA_NODE_UNAVAILABLE", "IOTA node is unavailable"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectedCode, tt.err.Code)
			assert.Equal(t, tt.expectedMsg, tt.err.Message)
			assert.NotEmpty(t, tt.err.Error())
		})
	}
}

func TestPaymentDomainErrorsCount(t *testing.T) {
	// Verify we have all 15 payment domain errors accounted for
	errors := []DomainError{
		ErrWalletNotFound,
		ErrWalletAlreadyExists,
		ErrPaymentIntentNotFound,
		ErrPaymentIntentExpired,
		ErrInvalidAmount,
		ErrInsufficientBalance,
		ErrTransactionNotFound,
		ErrInvalidAddress,
		ErrPaymentAlreadyPaid,
		ErrInvalidSeed,
		ErrEncryptionFailed,
		ErrDecryptionFailed,
		ErrIOTANodeUnavailable,
		ErrTransactionBroadcast,
		ErrRateLimitExceeded,
	}

	assert.Len(t, errors, 15)

	// Verify all have non-empty codes
	for _, err := range errors {
		assert.NotEmpty(t, err.Code, "Every DomainError must have a non-empty Code")
		assert.NotEmpty(t, err.Message, "Every DomainError must have a non-empty Message")
	}
}

func TestPaymentDomainErrorsAreDistinct(t *testing.T) {
	errors := []DomainError{
		ErrWalletNotFound,
		ErrWalletAlreadyExists,
		ErrPaymentIntentNotFound,
		ErrPaymentIntentExpired,
		ErrInvalidAmount,
		ErrInsufficientBalance,
		ErrTransactionNotFound,
		ErrInvalidAddress,
		ErrPaymentAlreadyPaid,
		ErrInvalidSeed,
		ErrEncryptionFailed,
		ErrDecryptionFailed,
		ErrIOTANodeUnavailable,
		ErrTransactionBroadcast,
		ErrRateLimitExceeded,
	}

	codes := make(map[string]bool)
	for _, err := range errors {
		assert.False(t, codes[err.Code], "Duplicate error code found: %s", err.Code)
		codes[err.Code] = true
	}
}

func TestIOTAWalletJSONSeedHidden(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	wallet := IOTAWallet{
		ID:            "wallet-123",
		UserID:        "user-456",
		EncryptedSeed: []byte("secret-seed-data"),
		SeedNonce:     []byte("nonce-data"),
		Address:       "iota1qp2test...",
		BalanceIOTA:   1000000,
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	data, err := json.Marshal(wallet)
	assert.NoError(t, err)

	// Verify that EncryptedSeed and SeedNonce are NOT in JSON output (json:"-" tag)
	var raw map[string]interface{}
	err = json.Unmarshal(data, &raw)
	assert.NoError(t, err)

	assert.Nil(t, raw["encrypted_seed"], "EncryptedSeed should not appear in JSON")
	assert.Nil(t, raw["seed_nonce"], "SeedNonce should not appear in JSON")
	assert.Equal(t, "wallet-123", raw["id"])
	assert.Equal(t, "iota1qp2test...", raw["address"])
}

func TestTransactionBroadcastError(t *testing.T) {
	assert.Equal(t, "TRANSACTION_BROADCAST_FAILED", ErrTransactionBroadcast.Code)
	assert.Equal(t, "Failed to broadcast transaction", ErrTransactionBroadcast.Message)
}

func TestRateLimitExceededError(t *testing.T) {
	assert.Equal(t, "RATE_LIMIT_EXCEEDED", ErrRateLimitExceeded.Code)
	assert.Equal(t, "Rate limit exceeded for wallet operations", ErrRateLimitExceeded.Message)
}
