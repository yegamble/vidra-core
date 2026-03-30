package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPaymentService_GetWallet(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := new(MockIOTARepository)
		svc := NewPaymentService(repo, nil, testEncryptionKey)
		ctx := context.Background()
		userID := uuid.New().String()

		wallet := &domain.IOTAWallet{
			ID:     uuid.New().String(),
			UserID: userID,
		}
		repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)

		result, err := svc.GetWallet(ctx, userID)

		require.NoError(t, err)
		assert.Equal(t, userID, result.UserID)
		repo.AssertExpectations(t)
	})

	t.Run("not found", func(t *testing.T) {
		repo := new(MockIOTARepository)
		svc := NewPaymentService(repo, nil, testEncryptionKey)
		ctx := context.Background()
		userID := uuid.New().String()

		repo.On("GetWalletByUserID", ctx, userID).Return(nil, domain.ErrWalletNotFound)

		result, err := svc.GetWallet(ctx, userID)

		require.Nil(t, result)
		require.ErrorIs(t, err, domain.ErrWalletNotFound)
		repo.AssertExpectations(t)
	})
}

func TestPaymentService_CreatePaymentIntent_SuccessPath(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()
	videoID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:      uuid.New().String(),
		UserID:  userID,
		Address: "iota1qwallet-addr",
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	repo.On("CreatePaymentIntent", ctx, mock.AnythingOfType("*domain.IOTAPaymentIntent")).Return(nil)

	intent, err := svc.CreatePaymentIntent(ctx, userID, &videoID, 500000)

	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.Equal(t, userID, intent.UserID)
	assert.Equal(t, int64(500000), intent.AmountIOTA)
	assert.Equal(t, domain.PaymentIntentStatusPending, intent.Status)
	assert.True(t, intent.ExpiresAt.After(time.Now()))
	assert.True(t, intent.VideoID.Valid)
	assert.Equal(t, videoID, intent.VideoID.String)
	repo.AssertExpectations(t)
}

func TestPaymentService_CreatePaymentIntent_NoVideoID(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:      uuid.New().String(),
		UserID:  userID,
		Address: "iota1qwallet-addr",
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	repo.On("CreatePaymentIntent", ctx, mock.AnythingOfType("*domain.IOTAPaymentIntent")).Return(nil)

	intent, err := svc.CreatePaymentIntent(ctx, userID, nil, 100000)

	require.NoError(t, err)
	require.NotNil(t, intent)
	assert.False(t, intent.VideoID.Valid)
	repo.AssertExpectations(t)
}

func TestPaymentService_CreatePaymentIntent_RepoCreateError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:      uuid.New().String(),
		UserID:  userID,
		Address: "iota1qwallet-addr",
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	repo.On("CreatePaymentIntent", ctx, mock.AnythingOfType("*domain.IOTAPaymentIntent")).
		Return(errors.New("db error"))

	intent, err := svc.CreatePaymentIntent(ctx, userID, nil, 100000)

	require.Nil(t, intent)
	require.ErrorContains(t, err, "failed to create payment intent")
	repo.AssertExpectations(t)
}

func TestPaymentService_GetWalletBalance_BalanceChanged(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()
	walletID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:          walletID,
		UserID:      userID,
		Address:     "iota1qaddr",
		BalanceIOTA: 1000,
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	client.On("GetBalance", ctx, "iota1qaddr").Return(int64(2000), nil)
	repo.On("UpdateWalletBalance", ctx, walletID, int64(2000)).Return(nil)

	balance, err := svc.GetWalletBalance(ctx, userID)

	require.NoError(t, err)
	assert.Equal(t, int64(2000), balance)
	repo.AssertExpectations(t)
	client.AssertExpectations(t)
}

func TestPaymentService_GetWalletBalance_UpdateError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()
	walletID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:          walletID,
		UserID:      userID,
		Address:     "iota1qaddr",
		BalanceIOTA: 1000,
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	client.On("GetBalance", ctx, "iota1qaddr").Return(int64(2000), nil)
	repo.On("UpdateWalletBalance", ctx, walletID, int64(2000)).Return(errors.New("db err"))

	balance, err := svc.GetWalletBalance(ctx, userID)

	require.NoError(t, err)
	assert.Equal(t, int64(2000), balance)
}

func TestPaymentService_GetWalletBalance_NetworkError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	wallet := &domain.IOTAWallet{
		ID:      uuid.New().String(),
		UserID:  userID,
		Address: "iota1qaddr",
	}
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	client.On("GetBalance", ctx, "iota1qaddr").Return(int64(0), errors.New("network err"))

	_, err := svc.GetWalletBalance(ctx, userID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get balance from network")
}

func TestPaymentService_DetectPayment_IntentNotFound(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()

	repo.On("GetPaymentIntentByID", ctx, "intent-missing").Return(nil, domain.ErrPaymentIntentNotFound)

	err := svc.DetectPayment(ctx, "intent-missing")

	require.ErrorIs(t, err, domain.ErrPaymentIntentNotFound)
}

func TestPaymentService_DetectPayment_QueryError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	intentID := uuid.New().String()

	intent := &domain.IOTAPaymentIntent{
		ID:             intentID,
		UserID:         uuid.New().String(),
		AmountIOTA:     1000,
		PaymentAddress: "iota1qpay",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	repo.On("GetPaymentIntentByID", ctx, intentID).Return(intent, nil)
	client.On("QueryTransactionBlocks", ctx, "iota1qpay", 50).Return(nil, errors.New("network down"))

	err := svc.DetectPayment(ctx, intentID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to query transaction blocks")
}

func TestPaymentService_DetectPayment_CreateTransactionError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	intentID := uuid.New().String()
	userID := uuid.New().String()

	createdAt := time.Now().Add(-5 * time.Minute)
	intent := &domain.IOTAPaymentIntent{
		ID:             intentID,
		UserID:         userID,
		AmountIOTA:     1000,
		PaymentAddress: "iota1qpay",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		CreatedAt:      createdAt,
	}
	txs := []domain.ReceivedTransaction{
		{Digest: "tx-digest-err", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1000},
	}
	repo.On("GetPaymentIntentByID", ctx, intentID).Return(intent, nil)
	client.On("QueryTransactionBlocks", ctx, "iota1qpay", 50).Return(txs, nil)
	repo.On("GetWalletByUserID", ctx, userID).Return(nil, domain.ErrWalletNotFound)
	repo.On("CreateTransaction", ctx, mock.AnythingOfType("*domain.IOTATransaction")).
		Return(errors.New("db error"))

	err := svc.DetectPayment(ctx, intentID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create transaction")
}

func TestPaymentService_DetectPayment_UpdateStatusError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	intentID := uuid.New().String()
	userID := uuid.New().String()
	walletID := uuid.New().String()

	createdAt := time.Now().Add(-5 * time.Minute)
	intent := &domain.IOTAPaymentIntent{
		ID:             intentID,
		UserID:         userID,
		AmountIOTA:     1000,
		PaymentAddress: "iota1qpay",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		CreatedAt:      createdAt,
	}
	wallet := &domain.IOTAWallet{
		ID:     walletID,
		UserID: userID,
	}
	txs := []domain.ReceivedTransaction{
		{Digest: "tx-digest-update-err", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 2000},
	}
	repo.On("GetPaymentIntentByID", ctx, intentID).Return(intent, nil)
	client.On("QueryTransactionBlocks", ctx, "iota1qpay", 50).Return(txs, nil)
	repo.On("GetWalletByUserID", ctx, userID).Return(wallet, nil)
	repo.On("CreateTransaction", ctx, mock.AnythingOfType("*domain.IOTATransaction")).Return(nil)
	repo.On("UpdatePaymentIntentStatus", ctx, intentID, domain.PaymentIntentStatusPaid, mock.Anything).
		Return(errors.New("update err"))

	err := svc.DetectPayment(ctx, intentID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update payment intent")
}

func TestPaymentService_ExpirePaymentIntents_GetError(t *testing.T) {
	repo := new(MockIOTARepository)
	svc := NewPaymentService(repo, nil, testEncryptionKey)
	ctx := context.Background()

	repo.On("GetExpiredPaymentIntents", ctx).Return(nil, errors.New("db down"))

	err := svc.ExpirePaymentIntents(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get expired intents")
}

func TestPaymentService_ExpirePaymentIntents_UpdateError(t *testing.T) {
	repo := new(MockIOTARepository)
	svc := NewPaymentService(repo, nil, testEncryptionKey)
	ctx := context.Background()

	intentID := uuid.New().String()
	repo.On("GetExpiredPaymentIntents", ctx).Return([]*domain.IOTAPaymentIntent{
		{ID: intentID, Status: domain.PaymentIntentStatusPending},
	}, nil)
	repo.On("UpdatePaymentIntentStatus", ctx, intentID, domain.PaymentIntentStatusExpired, (*string)(nil)).
		Return(errors.New("update err"))

	err := svc.ExpirePaymentIntents(ctx)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to expire intent")
}

func TestPaymentService_CreateWallet_DeriveAddressError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	repo.On("GetWalletByUserID", ctx, userID).Return(nil, domain.ErrWalletNotFound)
	client.On("GenerateKeypair").Return(make([]byte, 32), make([]byte, 32), nil)
	client.On("DeriveAddress", mock.Anything).Return("", errors.New("derive err"))

	_, err := svc.CreateWallet(ctx, userID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to derive address")
}

func TestPaymentService_CreateWallet_RepoError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	repo.On("GetWalletByUserID", ctx, userID).Return(nil, domain.ErrWalletNotFound)
	client.On("GenerateKeypair").Return(make([]byte, 32), make([]byte, 32), nil)
	client.On("DeriveAddress", mock.Anything).Return("0x"+repeatString("a", 64), nil)
	repo.On("CreateWallet", ctx, mock.Anything).Return(errors.New("insert err"))

	_, err := svc.CreateWallet(ctx, userID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create wallet")
}

func TestPaymentService_CreateWallet_CheckExistingError(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	userID := uuid.New().String()

	repo.On("GetWalletByUserID", ctx, userID).Return(nil, errors.New("db down"))

	_, err := svc.CreateWallet(ctx, userID)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check existing wallet")
}

func TestPaymentService_DetectPayment_NoMatchingTransactions(t *testing.T) {
	repo := new(MockIOTARepository)
	client := new(MockIOTAClient)
	svc := NewPaymentService(repo, client, testEncryptionKey)
	ctx := context.Background()
	intentID := uuid.New().String()

	// Intent created just now; transaction happened 30 seconds ago (before intent + outside 5s buffer)
	createdAt := time.Now()
	intent := &domain.IOTAPaymentIntent{
		ID:             intentID,
		UserID:         uuid.New().String(),
		AmountIOTA:     1000,
		PaymentAddress: "iota1qpay-old",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		CreatedAt:      createdAt,
	}
	// Transaction timestamp is 30s before intent creation — outside the 5s buffer, should be filtered
	oldTxMs := createdAt.Add(-30 * time.Second).UnixMilli()
	txs := []domain.ReceivedTransaction{
		{Digest: "old-tx-digest", TimestampMs: oldTxMs, AmountIOTA: 5000},
	}
	repo.On("GetPaymentIntentByID", ctx, intentID).Return(intent, nil)
	client.On("QueryTransactionBlocks", ctx, "iota1qpay-old", 50).Return(txs, nil)

	err := svc.DetectPayment(ctx, intentID)

	require.NoError(t, err) // no error, just not paid yet
	// Verify no payment was recorded
	repo.AssertNotCalled(t, "CreateTransaction")
	repo.AssertNotCalled(t, "UpdatePaymentIntentStatus")
}

func TestPaymentService_DecryptPrivateKey_WrongNonce(t *testing.T) {
	svc := NewPaymentService(nil, nil, testEncryptionKey)

	encrypted, _, err := svc.EncryptPrivateKey("test-seed-value")
	require.NoError(t, err)

	wrongNonce := make([]byte, 12)
	_, err = svc.DecryptPrivateKey(encrypted, wrongNonce)
	require.Error(t, err)
}
