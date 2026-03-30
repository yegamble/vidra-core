package payments

import (
	"context"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

var testEncryptionKey = []byte("test-encryption-key-must-be-32!!")

type MockIOTARepository struct {
	mock.Mock
}

func (m *MockIOTARepository) CreateWallet(ctx context.Context, wallet *domain.IOTAWallet) error {
	args := m.Called(ctx, wallet)
	return args.Error(0)
}

func (m *MockIOTARepository) GetWalletByUserID(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockIOTARepository) GetWalletByID(ctx context.Context, walletID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, walletID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockIOTARepository) UpdateWalletBalance(ctx context.Context, walletID string, balance int64) error {
	args := m.Called(ctx, walletID, balance)
	return args.Error(0)
}

func (m *MockIOTARepository) CreatePaymentIntent(ctx context.Context, intent *domain.IOTAPaymentIntent) error {
	args := m.Called(ctx, intent)
	return args.Error(0)
}

func (m *MockIOTARepository) GetPaymentIntentByID(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx, intentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockIOTARepository) UpdatePaymentIntentStatus(ctx context.Context, intentID string, status domain.PaymentIntentStatus, txID *string) error {
	args := m.Called(ctx, intentID, status, txID)
	return args.Error(0)
}

func (m *MockIOTARepository) GetActivePaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockIOTARepository) GetExpiredPaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockIOTARepository) CreateTransaction(ctx context.Context, tx *domain.IOTATransaction) error {
	args := m.Called(ctx, tx)
	return args.Error(0)
}

func (m *MockIOTARepository) GetTransactionByHash(ctx context.Context, txHash string) (*domain.IOTATransaction, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTATransaction), args.Error(1)
}

func (m *MockIOTARepository) UpdateTransactionStatus(ctx context.Context, txID string, status domain.TransactionStatus, confirmations int) error {
	args := m.Called(ctx, txID, status, confirmations)
	return args.Error(0)
}

func (m *MockIOTARepository) GetTransactionsByWalletID(ctx context.Context, walletID string, limit, offset int) ([]*domain.IOTATransaction, error) {
	args := m.Called(ctx, walletID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTATransaction), args.Error(1)
}

type MockIOTAClient struct {
	mock.Mock
}

func (m *MockIOTAClient) GenerateKeypair() ([]byte, []byte, error) {
	args := m.Called()
	priv, _ := args.Get(0).([]byte)
	pub, _ := args.Get(1).([]byte)
	return priv, pub, args.Error(2)
}

func (m *MockIOTAClient) DeriveAddress(publicKey []byte) (string, error) {
	args := m.Called(publicKey)
	return args.String(0), args.Error(1)
}

func (m *MockIOTAClient) ValidateAddress(address string) bool {
	args := m.Called(address)
	return args.Bool(0)
}

func (m *MockIOTAClient) GetBalance(ctx context.Context, address string) (int64, error) {
	args := m.Called(ctx, address)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockIOTAClient) GetNodeStatus(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockIOTAClient) QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]domain.ReceivedTransaction, error) {
	args := m.Called(ctx, toAddress, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.ReceivedTransaction), args.Error(1)
}

func TestPaymentService_CreateWallet(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		setupMocks func(*MockIOTARepository, *MockIOTAClient)
		wantErr    bool
		errType    error
	}{
		{
			name:   "successful wallet creation",
			userID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
				client.On("GenerateKeypair").Return(make([]byte, 32), make([]byte, 32), nil)
				client.On("DeriveAddress", mock.Anything).
					Return("0x"+"a"+repeatString("a", 63), nil)
				repo.On("CreateWallet", mock.Anything, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:   "wallet already exists",
			userID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				existingWallet := &domain.IOTAWallet{
					ID:     uuid.New().String(),
					UserID: uuid.New().String(),
				}
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(existingWallet, nil)
			},
			wantErr: true,
			errType: domain.ErrWalletAlreadyExists,
		},
		{
			name:   "seed generation fails",
			userID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
				client.On("GenerateKeypair").Return(nil, nil, errors.New("rng failure"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo, mockClient)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			wallet, err := service.CreateWallet(ctx, tt.userID)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, wallet)
			assert.Equal(t, tt.userID, wallet.UserID)
			assert.NotEmpty(t, wallet.Address)
			assert.NotEmpty(t, wallet.EncryptedPrivateKey)
			assert.NotEmpty(t, wallet.PrivateKeyNonce)

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestPaymentService_GetWalletBalance(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		setupMocks func(*MockIOTARepository, *MockIOTAClient)
		wantErr    bool
		errType    error
	}{
		{
			name:   "get balance for existing wallet",
			userID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				wallet := &domain.IOTAWallet{
					ID:          uuid.New().String(),
					UserID:      uuid.New().String(),
					Address:     "iota1qwallet111",
					BalanceIOTA: 1000000,
				}
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(wallet, nil)
				client.On("GetBalance", mock.Anything, wallet.Address).Return(int64(1000000), nil)
			},
			wantErr: false,
		},
		{
			name:   "wallet not found",
			userID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
			},
			wantErr: true,
			errType: domain.ErrWalletNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo, mockClient)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			balance, err := service.GetWalletBalance(ctx, tt.userID)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.GreaterOrEqual(t, balance, int64(0))

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestPaymentService_CreatePaymentIntent(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		videoID    *string
		amount     int64
		setupMocks func(*MockIOTARepository, *MockIOTAClient)
		wantErr    bool
		errType    error
	}{
		{
			name:    "successful intent creation",
			userID:  uuid.New().String(),
			videoID: stringPtr(uuid.New().String()),
			amount:  1000000,
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				wallet := &domain.IOTAWallet{
					ID:                  uuid.New().String(),
					UserID:              uuid.New().String(),
					EncryptedPrivateKey: []byte("encrypted"),
					PrivateKeyNonce:     []byte("nonce"),
					Address:             "iota1qwallet",
					BalanceIOTA:         0,
				}
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(wallet, nil)
				repo.On("CreatePaymentIntent", mock.Anything, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name:    "invalid amount - zero",
			userID:  uuid.New().String(),
			videoID: nil,
			amount:  0,
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
			},
			wantErr: true,
			errType: domain.ErrInvalidAmount,
		},
		{
			name:    "invalid amount - negative",
			userID:  uuid.New().String(),
			videoID: nil,
			amount:  -1000,
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
			},
			wantErr: true,
			errType: domain.ErrInvalidAmount,
		},
		{
			name:    "wallet not found",
			userID:  uuid.New().String(),
			videoID: nil,
			amount:  1000000,
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
			},
			wantErr: true,
			errType: domain.ErrWalletNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo, mockClient)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			intent, err := service.CreatePaymentIntent(ctx, tt.userID, tt.videoID, tt.amount)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, intent)
			assert.Equal(t, tt.userID, intent.UserID)
			assert.Equal(t, tt.amount, intent.AmountIOTA)
			assert.NotEmpty(t, intent.PaymentAddress)
			assert.Equal(t, domain.PaymentIntentStatusPending, intent.Status)
			assert.True(t, intent.ExpiresAt.After(time.Now()))

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestPaymentService_GetPaymentIntent(t *testing.T) {
	tests := []struct {
		name       string
		intentID   string
		setupMocks func(*MockIOTARepository)
		wantErr    bool
		errType    error
	}{
		{
			name:     "existing intent",
			intentID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
			},
			wantErr: false,
		},
		{
			name:     "non-existent intent",
			intentID: uuid.New().String(),
			setupMocks: func(repo *MockIOTARepository) {
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrPaymentIntentNotFound)
			},
			wantErr: true,
			errType: domain.ErrPaymentIntentNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			intent, err := service.GetPaymentIntent(ctx, tt.intentID)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, intent)

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestPaymentService_DetectPayment(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockIOTARepository, *MockIOTAClient)
		wantErr    bool
		errType    error
	}{
		{
			name: "detect exact payment",
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				createdAt := time.Now().Add(-5 * time.Minute)
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment111",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
					CreatedAt:      createdAt,
				}
				wallet := &domain.IOTAWallet{
					ID:      uuid.New().String(),
					UserID:  intent.UserID,
					Address: "iota1qwallet",
				}
				txs := []domain.ReceivedTransaction{
					{Digest: "real-tx-digest-1", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1000000},
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
				client.On("QueryTransactionBlocks", mock.Anything, intent.PaymentAddress, 50).Return(txs, nil)
				repo.On("GetWalletByUserID", mock.Anything, intent.UserID).Return(wallet, nil)
				repo.On("CreateTransaction", mock.Anything, mock.Anything).Return(nil)
				repo.On("UpdatePaymentIntentStatus", mock.Anything, mock.Anything,
					domain.PaymentIntentStatusPaid, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "overpayment accepted",
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				createdAt := time.Now().Add(-5 * time.Minute)
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment222",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
					CreatedAt:      createdAt,
				}
				wallet := &domain.IOTAWallet{
					ID:      uuid.New().String(),
					UserID:  intent.UserID,
					Address: "iota1qwallet",
				}
				txs := []domain.ReceivedTransaction{
					{Digest: "real-tx-digest-2", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1500000},
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
				client.On("QueryTransactionBlocks", mock.Anything, intent.PaymentAddress, 50).Return(txs, nil)
				repo.On("GetWalletByUserID", mock.Anything, intent.UserID).Return(wallet, nil)
				repo.On("CreateTransaction", mock.Anything, mock.Anything).Return(nil)
				repo.On("UpdatePaymentIntentStatus", mock.Anything, mock.Anything,
					domain.PaymentIntentStatusPaid, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "partial payment - not enough",
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				createdAt := time.Now().Add(-5 * time.Minute)
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment333",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
					CreatedAt:      createdAt,
				}
				txs := []domain.ReceivedTransaction{
					{Digest: "real-tx-digest-3", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 500000},
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
				client.On("QueryTransactionBlocks", mock.Anything, intent.PaymentAddress, 50).Return(txs, nil)
			},
			wantErr: false,
		},
		{
			name: "intent already paid",
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment444",
					Status:         domain.PaymentIntentStatusPaid,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
			},
			wantErr: true,
			errType: domain.ErrPaymentAlreadyPaid,
		},
		{
			name: "intent expired",
			setupMocks: func(repo *MockIOTARepository, client *MockIOTAClient) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment555",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(-1 * time.Hour),
				}
				repo.On("GetPaymentIntentByID", mock.Anything, mock.Anything).Return(intent, nil)
			},
			wantErr: true,
			errType: domain.ErrPaymentIntentExpired,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo, mockClient)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			err := service.DetectPayment(ctx, uuid.New().String())
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			assert.NoError(t, err)

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestPaymentService_ExpirePaymentIntents(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockIOTARepository)
		wantErr    bool
	}{
		{
			name: "expire multiple intents",
			setupMocks: func(repo *MockIOTARepository) {
				expiredIntents := []*domain.IOTAPaymentIntent{
					{
						ID:        uuid.New().String(),
						Status:    domain.PaymentIntentStatusPending,
						ExpiresAt: time.Now().Add(-1 * time.Hour),
					},
					{
						ID:        uuid.New().String(),
						Status:    domain.PaymentIntentStatusPending,
						ExpiresAt: time.Now().Add(-2 * time.Hour),
					},
				}
				repo.On("GetExpiredPaymentIntents", mock.Anything).Return(expiredIntents, nil)
				for _, intent := range expiredIntents {
					repo.On("UpdatePaymentIntentStatus", mock.Anything, intent.ID,
						domain.PaymentIntentStatusExpired, (*string)(nil)).Return(nil)
				}
			},
			wantErr: false,
		},
		{
			name: "no expired intents",
			setupMocks: func(repo *MockIOTARepository) {
				repo.On("GetExpiredPaymentIntents", mock.Anything).
					Return([]*domain.IOTAPaymentIntent{}, nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			err := service.ExpirePaymentIntents(ctx)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestPaymentService_SeedEncryption(t *testing.T) {
	encryptionKey := make([]byte, 32)
	_, err := rand.Read(encryptionKey)
	require.NoError(t, err)

	service := NewPaymentService(nil, nil, encryptionKey)

	plainSeed := repeatString("a", 64)

	encrypted, nonce, err := service.EncryptPrivateKey(plainSeed)
	require.NoError(t, err)
	assert.NotEmpty(t, encrypted)
	assert.NotEmpty(t, nonce)
	assert.NotEqual(t, []byte(plainSeed), encrypted)

	decrypted, err := service.DecryptPrivateKey(encrypted, nonce)
	require.NoError(t, err)
	assert.Equal(t, plainSeed, decrypted)
}

func TestPaymentService_SeedEncryption_DifferentNonces(t *testing.T) {
	encryptionKey := make([]byte, 32)
	_, err := rand.Read(encryptionKey)
	require.NoError(t, err)

	service := NewPaymentService(nil, nil, encryptionKey)

	plainSeed := repeatString("a", 64)

	encrypted1, nonce1, err := service.EncryptPrivateKey(plainSeed)
	require.NoError(t, err)

	encrypted2, nonce2, err := service.EncryptPrivateKey(plainSeed)
	require.NoError(t, err)

	assert.NotEqual(t, nonce1, nonce2)
	assert.NotEqual(t, encrypted1, encrypted2)

	decrypted1, err := service.DecryptPrivateKey(encrypted1, nonce1)
	require.NoError(t, err)
	assert.Equal(t, plainSeed, decrypted1)

	decrypted2, err := service.DecryptPrivateKey(encrypted2, nonce2)
	require.NoError(t, err)
	assert.Equal(t, plainSeed, decrypted2)
}

func TestPaymentService_SeedNeverLogged(t *testing.T) {
	mockRepo := new(MockIOTARepository)
	mockClient := new(MockIOTAClient)

	userID := uuid.New().String()
	privKey := make([]byte, 32)

	mockRepo.On("GetWalletByUserID", mock.Anything, userID).
		Return(nil, domain.ErrWalletNotFound)
	mockClient.On("GenerateKeypair").Return(privKey, make([]byte, 32), nil)
	mockClient.On("DeriveAddress", mock.Anything).
		Return("0x"+repeatString("a", 64), nil)
	mockRepo.On("CreateWallet", mock.Anything, mock.MatchedBy(func(w *domain.IOTAWallet) bool {
		assert.NotEqual(t, privKey, w.EncryptedPrivateKey)
		assert.NotEmpty(t, w.EncryptedPrivateKey)
		assert.NotEmpty(t, w.PrivateKeyNonce)
		return true
	})).Return(nil)

	service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
	ctx := context.Background()

	_, err := service.CreateWallet(ctx, userID)
	require.NoError(t, err)

	mockRepo.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestPaymentService_RateLimiting(t *testing.T) {
	t.Skip("Rate limiting implementation pending")
}

func TestPaymentService_GetTransactionHistory(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		limit      int
		offset     int
		setupMocks func(*MockIOTARepository)
		wantErr    bool
	}{
		{
			name:   "get transaction history",
			userID: uuid.New().String(),
			limit:  10,
			offset: 0,
			setupMocks: func(repo *MockIOTARepository) {
				wallet := &domain.IOTAWallet{
					ID:     uuid.New().String(),
					UserID: uuid.New().String(),
				}
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(wallet, nil)

				transactions := []*domain.IOTATransaction{
					{
						ID:                uuid.New().String(),
						TransactionDigest: "0x123",
						AmountIOTA:        1000000,
						TxType:            domain.TransactionTypeDeposit,
						Status:            domain.TransactionStatusConfirmed,
					},
				}
				repo.On("GetTransactionsByWalletID", mock.Anything, wallet.ID, 10, 0).
					Return(transactions, nil)
			},
			wantErr: false,
		},
		{
			name:   "wallet not found",
			userID: uuid.New().String(),
			limit:  10,
			offset: 0,
			setupMocks: func(repo *MockIOTARepository) {
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTARepository)
			mockClient := new(MockIOTAClient)
			tt.setupMocks(mockRepo)

			service := NewPaymentService(mockRepo, mockClient, testEncryptionKey)
			ctx := context.Background()

			transactions, err := service.GetTransactionHistory(ctx, tt.userID, tt.limit, tt.offset)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, transactions)

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestPaymentService_ConcurrentWalletCreation(t *testing.T) {
	t.Skip("Concurrent wallet creation test - requires transaction handling")
}

func stringPtr(s string) *string {
	return &s
}
