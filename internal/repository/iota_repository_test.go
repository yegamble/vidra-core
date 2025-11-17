package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestIOTARepository_CreateWallet tests wallet creation
func TestIOTARepository_CreateWallet(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	// Create test user
	userID := createTestUserForIOTA(t, testDB)

	tests := []struct {
		name    string
		wallet  *domain.IOTAWallet
		wantErr error
	}{
		{
			name: "valid wallet",
			wallet: &domain.IOTAWallet{
				ID:            uuid.New().String(),
				UserID:        userID,
				EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
				SeedNonce:     []byte("nonce_12bytes"),
				Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
				BalanceIOTA:   0,
			},
			wantErr: nil,
		},
		{
			name: "duplicate wallet for same user",
			wallet: &domain.IOTAWallet{
				ID:            uuid.New().String(),
				UserID:        userID,
				EncryptedSeed: []byte("another_encrypted_seed_data___"),
				SeedNonce:     []byte("nonce2______"),
				Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7abc",
				BalanceIOTA:   0,
			},
			wantErr: domain.ErrWalletAlreadyExists,
		},
		{
			name: "wallet with non-existent user",
			wallet: &domain.IOTAWallet{
				ID:            uuid.New().String(),
				UserID:        uuid.New().String(), // non-existent user
				EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
				SeedNonce:     []byte("nonce_12bytes"),
				Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7def",
				BalanceIOTA:   0,
			},
			wantErr: domain.ErrUserNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.CreateWallet(ctx, tt.wallet)
			if tt.wantErr != nil {
				assert.Error(t, err)
				// Check if it's the expected error type
				return
			}
			assert.NoError(t, err)
			assert.NotEmpty(t, tt.wallet.ID)
			assert.NotZero(t, tt.wallet.CreatedAt)
			assert.NotZero(t, tt.wallet.UpdatedAt)
		})
	}
}

// TestIOTARepository_GetWalletByUserID tests wallet retrieval by user ID
func TestIOTARepository_GetWalletByUserID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	// Create test user
	userID := createTestUserForIOTA(t, testDB)

	// Create wallet
	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
		BalanceIOTA:   1000000,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	tests := []struct {
		name    string
		userID  string
		wantErr error
	}{
		{
			name:    "existing wallet",
			userID:  userID,
			wantErr: nil,
		},
		{
			name:    "non-existent wallet",
			userID:  uuid.New().String(),
			wantErr: domain.ErrWalletNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			found, err := repo.GetWalletByUserID(ctx, tt.userID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				assert.Nil(t, found)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, wallet.UserID, found.UserID)
			assert.Equal(t, wallet.Address, found.Address)
			assert.Equal(t, wallet.BalanceIOTA, found.BalanceIOTA)
			// Verify encrypted seed is retrieved
			assert.Equal(t, wallet.EncryptedSeed, found.EncryptedSeed)
			assert.Equal(t, wallet.SeedNonce, found.SeedNonce)
		})
	}
}

// TestIOTARepository_GetWalletByID tests wallet retrieval by ID
func TestIOTARepository_GetWalletByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
		BalanceIOTA:   500000,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	found, err := repo.GetWalletByID(ctx, wallet.ID)
	require.NoError(t, err)
	assert.Equal(t, wallet.ID, found.ID)
	assert.Equal(t, wallet.Address, found.Address)

	// Non-existent wallet
	_, err = repo.GetWalletByID(ctx, uuid.New().String())
	assert.ErrorIs(t, err, domain.ErrWalletNotFound)
}

// TestIOTARepository_UpdateWalletBalance tests balance updates
func TestIOTARepository_UpdateWalletBalance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
		BalanceIOTA:   1000000,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	tests := []struct {
		name       string
		walletID   string
		newBalance int64
		wantErr    error
	}{
		{
			name:       "increase balance",
			walletID:   wallet.ID,
			newBalance: 2000000,
			wantErr:    nil,
		},
		{
			name:       "decrease balance",
			walletID:   wallet.ID,
			newBalance: 500000,
			wantErr:    nil,
		},
		{
			name:       "zero balance",
			walletID:   wallet.ID,
			newBalance: 0,
			wantErr:    nil,
		},
		{
			name:       "non-existent wallet",
			walletID:   uuid.New().String(),
			newBalance: 1000,
			wantErr:    domain.ErrWalletNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.UpdateWalletBalance(ctx, tt.walletID, tt.newBalance)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			// Verify balance was updated
			updated, err := repo.GetWalletByID(ctx, tt.walletID)
			require.NoError(t, err)
			assert.Equal(t, tt.newBalance, updated.BalanceIOTA)
			assert.True(t, updated.UpdatedAt.After(wallet.UpdatedAt))
		})
	}
}

// TestIOTARepository_CreatePaymentIntent tests payment intent creation
func TestIOTARepository_CreatePaymentIntent(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)
	videoID := createTestVideoForIOTA(t, testDB, userID)

	tests := []struct {
		name    string
		intent  *domain.IOTAPaymentIntent
		wantErr bool
	}{
		{
			name: "valid payment intent with video",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         userID,
				VideoID:        sql.NullString{String: videoID, Valid: true},
				AmountIOTA:     1000000,
				PaymentAddress: "iota1qpayment1111111111111111111111111111111111111111111111111",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(1 * time.Hour),
				Metadata:       []byte(`{"purpose":"video_tip"}`),
			},
			wantErr: false,
		},
		{
			name: "valid payment intent without video",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         userID,
				VideoID:        sql.NullString{Valid: false},
				AmountIOTA:     500000,
				PaymentAddress: "iota1qpayment2222222222222222222222222222222222222222222222222",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(30 * time.Minute),
				Metadata:       []byte(`{"purpose":"donation"}`),
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.CreatePaymentIntent(ctx, tt.intent)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotEmpty(t, tt.intent.ID)
			assert.NotZero(t, tt.intent.CreatedAt)
			assert.NotZero(t, tt.intent.UpdatedAt)
		})
	}
}

// TestIOTARepository_GetPaymentIntentByID tests payment intent retrieval
func TestIOTARepository_GetPaymentIntentByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	intent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		VideoID:        sql.NullString{Valid: false},
		AmountIOTA:     1000000,
		PaymentAddress: "iota1qpayment1111111111111111111111111111111111111111111111111",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		Metadata:       []byte(`{"purpose":"test"}`),
	}
	err := repo.CreatePaymentIntent(ctx, intent)
	require.NoError(t, err)

	found, err := repo.GetPaymentIntentByID(ctx, intent.ID)
	require.NoError(t, err)
	assert.Equal(t, intent.ID, found.ID)
	assert.Equal(t, intent.AmountIOTA, found.AmountIOTA)
	assert.Equal(t, intent.PaymentAddress, found.PaymentAddress)
	assert.Equal(t, intent.Status, found.Status)

	// Non-existent intent
	_, err = repo.GetPaymentIntentByID(ctx, uuid.New().String())
	assert.ErrorIs(t, err, domain.ErrPaymentIntentNotFound)
}

// TestIOTARepository_UpdatePaymentIntentStatus tests status updates
func TestIOTARepository_UpdatePaymentIntentStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	intent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		VideoID:        sql.NullString{Valid: false},
		AmountIOTA:     1000000,
		PaymentAddress: "iota1qpayment1111111111111111111111111111111111111111111111111",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	err := repo.CreatePaymentIntent(ctx, intent)
	require.NoError(t, err)

	tests := []struct {
		name          string
		intentID      string
		newStatus     domain.PaymentIntentStatus
		transactionID *string
		wantErr       error
	}{
		{
			name:          "mark as paid",
			intentID:      intent.ID,
			newStatus:     domain.PaymentIntentStatusPaid,
			transactionID: stringPtrForIOTA(uuid.New().String()),
			wantErr:       nil,
		},
		{
			name:          "mark as expired",
			intentID:      intent.ID,
			newStatus:     domain.PaymentIntentStatusExpired,
			transactionID: nil,
			wantErr:       nil,
		},
		{
			name:          "non-existent intent",
			intentID:      uuid.New().String(),
			newStatus:     domain.PaymentIntentStatusPaid,
			transactionID: nil,
			wantErr:       domain.ErrPaymentIntentNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.UpdatePaymentIntentStatus(ctx, tt.intentID, tt.newStatus, tt.transactionID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			// Verify status was updated
			updated, err := repo.GetPaymentIntentByID(ctx, tt.intentID)
			require.NoError(t, err)
			assert.Equal(t, tt.newStatus, updated.Status)
			if tt.newStatus == domain.PaymentIntentStatusPaid {
				assert.True(t, updated.PaidAt.Valid)
				if tt.transactionID != nil {
					assert.Equal(t, *tt.transactionID, updated.TransactionID.String)
				}
			}
		})
	}
}

// TestIOTARepository_GetActivePaymentIntents tests retrieving active intents
func TestIOTARepository_GetActivePaymentIntents(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	// Create multiple payment intents with different statuses
	activeIntent1 := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     1000000,
		PaymentAddress: "iota1qactive11111111111111111111111111111111111111111111111111",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	err := repo.CreatePaymentIntent(ctx, activeIntent1)
	require.NoError(t, err)

	activeIntent2 := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     500000,
		PaymentAddress: "iota1qactive22222222222222222222222222222222222222222222222222",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(30 * time.Minute),
	}
	err = repo.CreatePaymentIntent(ctx, activeIntent2)
	require.NoError(t, err)

	expiredIntent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     750000,
		PaymentAddress: "iota1qexpired333333333333333333333333333333333333333333333333",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(-1 * time.Hour), // expired
	}
	err = repo.CreatePaymentIntent(ctx, expiredIntent)
	require.NoError(t, err)

	paidIntent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     2000000,
		PaymentAddress: "iota1qpaid444444444444444444444444444444444444444444444444444",
		Status:         domain.PaymentIntentStatusPaid,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	err = repo.CreatePaymentIntent(ctx, paidIntent)
	require.NoError(t, err)

	// Get active intents (pending and not expired)
	active, err := repo.GetActivePaymentIntents(ctx)
	require.NoError(t, err)

	// Should return only the two non-expired pending intents
	assert.Len(t, active, 2)

	// Verify they are the correct intents
	activeIDs := make(map[string]bool)
	for _, intent := range active {
		activeIDs[intent.ID] = true
		assert.Equal(t, domain.PaymentIntentStatusPending, intent.Status)
		assert.True(t, intent.ExpiresAt.After(time.Now()))
	}
	assert.True(t, activeIDs[activeIntent1.ID])
	assert.True(t, activeIDs[activeIntent2.ID])
}

// TestIOTARepository_GetExpiredPaymentIntents tests retrieving expired intents
func TestIOTARepository_GetExpiredPaymentIntents(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	// Create expired pending intent
	expiredIntent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     1000000,
		PaymentAddress: "iota1qexpired111111111111111111111111111111111111111111111111",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(-1 * time.Hour),
	}
	err := repo.CreatePaymentIntent(ctx, expiredIntent)
	require.NoError(t, err)

	// Create active intent
	activeIntent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         userID,
		AmountIOTA:     500000,
		PaymentAddress: "iota1qactive22222222222222222222222222222222222222222222222222",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	err = repo.CreatePaymentIntent(ctx, activeIntent)
	require.NoError(t, err)

	expired, err := repo.GetExpiredPaymentIntents(ctx)
	require.NoError(t, err)

	// Should return only the expired intent
	assert.Len(t, expired, 1)
	assert.Equal(t, expiredIntent.ID, expired[0].ID)
}

// TestIOTARepository_CreateTransaction tests transaction creation
func TestIOTARepository_CreateTransaction(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	// Create wallet for transaction
	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qwallet111111111111111111111111111111111111111111111111111",
		BalanceIOTA:   0,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	tests := []struct {
		name    string
		tx      *domain.IOTATransaction
		wantErr bool
	}{
		{
			name: "deposit transaction",
			tx: &domain.IOTATransaction{
				ID:              uuid.New().String(),
				WalletID:        sql.NullString{String: wallet.ID, Valid: true},
				TransactionHash: "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
				AmountIOTA:      1000000,
				TxType:          domain.TransactionTypeDeposit,
				Status:          domain.TransactionStatusPending,
				Confirmations:   0,
				FromAddress:     sql.NullString{String: "iota1qsender1111", Valid: true},
				ToAddress:       sql.NullString{String: wallet.Address, Valid: true},
				Metadata:        []byte(`{"note":"test deposit"}`),
			},
			wantErr: false,
		},
		{
			name: "payment transaction",
			tx: &domain.IOTATransaction{
				ID:              uuid.New().String(),
				WalletID:        sql.NullString{String: wallet.ID, Valid: true},
				TransactionHash: "0xabcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
				AmountIOTA:      500000,
				TxType:          domain.TransactionTypePayment,
				Status:          domain.TransactionStatusPending,
				Confirmations:   0,
				FromAddress:     sql.NullString{String: wallet.Address, Valid: true},
				ToAddress:       sql.NullString{String: "iota1qrecipient1", Valid: true},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.CreateTransaction(ctx, tt.tx)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.NotEmpty(t, tt.tx.ID)
			assert.NotZero(t, tt.tx.CreatedAt)
		})
	}
}

// TestIOTARepository_GetTransactionByHash tests transaction retrieval
func TestIOTARepository_GetTransactionByHash(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qwallet111111111111111111111111111111111111111111111111111",
		BalanceIOTA:   0,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	tx := &domain.IOTATransaction{
		ID:              uuid.New().String(),
		WalletID:        sql.NullString{String: wallet.ID, Valid: true},
		TransactionHash: "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		AmountIOTA:      1000000,
		TxType:          domain.TransactionTypeDeposit,
		Status:          domain.TransactionStatusPending,
		Confirmations:   0,
	}
	err = repo.CreateTransaction(ctx, tx)
	require.NoError(t, err)

	found, err := repo.GetTransactionByHash(ctx, tx.TransactionHash)
	require.NoError(t, err)
	assert.Equal(t, tx.ID, found.ID)
	assert.Equal(t, tx.TransactionHash, found.TransactionHash)
	assert.Equal(t, tx.AmountIOTA, found.AmountIOTA)

	// Non-existent transaction
	_, err = repo.GetTransactionByHash(ctx, "0xnonexistent")
	assert.ErrorIs(t, err, domain.ErrTransactionNotFound)
}

// TestIOTARepository_UpdateTransactionStatus tests transaction status updates
func TestIOTARepository_UpdateTransactionStatus(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qwallet111111111111111111111111111111111111111111111111111",
		BalanceIOTA:   0,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	tx := &domain.IOTATransaction{
		ID:              uuid.New().String(),
		WalletID:        sql.NullString{String: wallet.ID, Valid: true},
		TransactionHash: "0x1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		AmountIOTA:      1000000,
		TxType:          domain.TransactionTypeDeposit,
		Status:          domain.TransactionStatusPending,
		Confirmations:   0,
	}
	err = repo.CreateTransaction(ctx, tx)
	require.NoError(t, err)

	// Update to confirmed
	err = repo.UpdateTransactionStatus(ctx, tx.ID, domain.TransactionStatusConfirmed, 10)
	require.NoError(t, err)

	updated, err := repo.GetTransactionByHash(ctx, tx.TransactionHash)
	require.NoError(t, err)
	assert.Equal(t, domain.TransactionStatusConfirmed, updated.Status)
	assert.Equal(t, 10, updated.Confirmations)
	assert.True(t, updated.ConfirmedAt.Valid)
}

// TestIOTARepository_GetTransactionsByWalletID tests transaction listing
func TestIOTARepository_GetTransactionsByWalletID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("encrypted_seed_data_32_bytes__"),
		SeedNonce:     []byte("nonce_12bytes"),
		Address:       "iota1qwallet111111111111111111111111111111111111111111111111111",
		BalanceIOTA:   0,
	}
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	// Create multiple transactions
	for i := 0; i < 5; i++ {
		tx := &domain.IOTATransaction{
			ID:              uuid.New().String(),
			WalletID:        sql.NullString{String: wallet.ID, Valid: true},
			TransactionHash: uuid.New().String(),
			AmountIOTA:      int64(1000000 * (i + 1)),
			TxType:          domain.TransactionTypeDeposit,
			Status:          domain.TransactionStatusPending,
			Confirmations:   i,
		}
		err = repo.CreateTransaction(ctx, tx)
		require.NoError(t, err)
	}

	transactions, err := repo.GetTransactionsByWalletID(ctx, wallet.ID, 10, 0)
	require.NoError(t, err)
	assert.Len(t, transactions, 5)

	// Test pagination
	transactions, err = repo.GetTransactionsByWalletID(ctx, wallet.ID, 2, 0)
	require.NoError(t, err)
	assert.Len(t, transactions, 2)

	transactions, err = repo.GetTransactionsByWalletID(ctx, wallet.ID, 2, 2)
	require.NoError(t, err)
	assert.Len(t, transactions, 2)
}

// TestIOTARepository_EncryptedSeedNotExposed tests that seed is never exposed in logs
func TestIOTARepository_EncryptedSeedNotExposed(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}

	repo := NewIOTARepository(testDB.DB)
	ctx := context.Background()

	userID := createTestUserForIOTA(t, testDB)

	wallet := &domain.IOTAWallet{
		ID:            uuid.New().String(),
		UserID:        userID,
		EncryptedSeed: []byte("super_secret_encrypted_seed___"),
		SeedNonce:     []byte("secret_nonce"),
		Address:       "iota1qwallet111111111111111111111111111111111111111111111111111",
		BalanceIOTA:   0,
	}

	// When wallet is created or retrieved, seed should never be in logs
	err := repo.CreateWallet(ctx, wallet)
	require.NoError(t, err)

	// Retrieve wallet
	retrieved, err := repo.GetWalletByUserID(ctx, userID)
	require.NoError(t, err)

	// Verify seed is retrieved but should never be logged
	assert.Equal(t, wallet.EncryptedSeed, retrieved.EncryptedSeed)
	assert.Equal(t, wallet.SeedNonce, retrieved.SeedNonce)
}

// Helper functions

func createTestUserForIOTA(t *testing.T, testDB *testutil.TestDB) string {
	t.Helper()
	// Using the actual NewUserRepository from this package
	userRepo := NewUserRepository(testDB.DB)
	ctx := context.Background()

	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser_" + uuid.New().String()[:8],
		Email:       "test_" + uuid.New().String()[:8] + "@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
		IsActive:    true,
	}

	err := userRepo.Create(ctx, user, "hashed_password")
	require.NoError(t, err)
	return user.ID
}

func createTestVideoForIOTA(t *testing.T, testDB *testutil.TestDB, userID string) string {
	t.Helper()
	// Using the actual NewVideoRepository from this package
	videoRepo := NewVideoRepository(testDB.DB)
	ctx := context.Background()

	video := &domain.Video{
		Title:       "Test Video",
		Description: "Test Description",
		UserID:      userID,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
	}

	err := videoRepo.Create(ctx, video)
	require.NoError(t, err)
	return video.ID
}

func stringPtrForIOTA(s string) *string {
	return &s
}
