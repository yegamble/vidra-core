package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type MockIOTAPaymentRepository struct {
	mock.Mock
}

func (m *MockIOTAPaymentRepository) GetActivePaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockIOTAPaymentRepository) UpdatePaymentIntentStatus(ctx context.Context, intentID string, status domain.PaymentIntentStatus, txID *string) error {
	args := m.Called(ctx, intentID, status, txID)
	return args.Error(0)
}

func (m *MockIOTAPaymentRepository) CreateTransaction(ctx context.Context, tx *domain.IOTATransaction) error {
	args := m.Called(ctx, tx)
	return args.Error(0)
}

func (m *MockIOTAPaymentRepository) GetTransactionByHash(ctx context.Context, txHash string) (*domain.IOTATransaction, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTATransaction), args.Error(1)
}

func (m *MockIOTAPaymentRepository) UpdateTransactionStatus(ctx context.Context, txID string, status domain.TransactionStatus, confirmations int) error {
	args := m.Called(ctx, txID, status, confirmations)
	return args.Error(0)
}

func (m *MockIOTAPaymentRepository) GetExpiredPaymentIntents(ctx context.Context) ([]*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockIOTAPaymentRepository) GetWalletByID(ctx context.Context, walletID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, walletID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockIOTAPaymentRepository) GetWalletByUserID(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockIOTAPaymentRepository) UpdateWalletBalance(ctx context.Context, walletID string, balance int64) error {
	args := m.Called(ctx, walletID, balance)
	return args.Error(0)
}

type MockIOTAPaymentClient struct {
	mock.Mock
}

func (m *MockIOTAPaymentClient) GetBalance(ctx context.Context, address string) (int64, error) {
	args := m.Called(ctx, address)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockIOTAPaymentClient) QueryTransactionBlocks(ctx context.Context, toAddress string, limit int) ([]domain.ReceivedTransaction, error) {
	args := m.Called(ctx, toAddress, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.ReceivedTransaction), args.Error(1)
}

func (m *MockIOTAPaymentClient) GetTransactionStatus(ctx context.Context, txHash string) (*TransactionStatus, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*TransactionStatus), args.Error(1)
}

type TransactionStatus struct {
	TxHash        string
	Confirmations int
	IsConfirmed   bool
}

func TestNewIOTAPaymentWorker(t *testing.T) {
	mockRepo := new(MockIOTAPaymentRepository)
	mockClient := new(MockIOTAPaymentClient)

	worker := NewIOTAPaymentWorker(mockRepo, mockClient)

	assert.NotNil(t, worker)
	assert.NotNil(t, worker.done)
}

func TestIOTAPaymentWorker_CheckPaymentIntent(t *testing.T) {
	tests := []struct {
		name       string
		intent     *domain.IOTAPaymentIntent
		setupMocks func(*MockIOTAPaymentRepository, *MockIOTAPaymentClient)
		wantErr    bool
	}{
		{
			name: "payment detected - exact amount",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         uuid.New().String(),
				AmountIOTA:     1000000,
				PaymentAddress: "iota1qpayment111",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(1 * time.Hour),
				CreatedAt:      time.Now().Add(-5 * time.Minute),
			},
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				txs := []domain.ReceivedTransaction{
					{Digest: "worker-tx-1", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1000000},
				}
				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment111", 50).Return(txs, nil)
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(&domain.IOTAWallet{ID: "wallet-1"}, nil)
				repo.On("CreateTransaction", mock.Anything, mock.MatchedBy(func(tx *domain.IOTATransaction) bool {
					assert.Equal(t, int64(1000000), tx.AmountIOTA)
					assert.Equal(t, domain.TransactionTypePayment, tx.TxType)
					return true
				})).Return(nil)
				repo.On("UpdatePaymentIntentStatus", mock.Anything, mock.Anything,
					domain.PaymentIntentStatusPaid, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "payment detected - overpayment",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         uuid.New().String(),
				AmountIOTA:     1000000,
				PaymentAddress: "iota1qpayment222",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(1 * time.Hour),
				CreatedAt:      time.Now().Add(-5 * time.Minute),
			},
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				txs := []domain.ReceivedTransaction{
					{Digest: "worker-tx-2", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1500000},
				}
				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment222", 50).Return(txs, nil)
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(&domain.IOTAWallet{ID: "wallet-1"}, nil)
				repo.On("CreateTransaction", mock.Anything, mock.Anything).Return(nil)
				repo.On("UpdatePaymentIntentStatus", mock.Anything, mock.Anything,
					domain.PaymentIntentStatusPaid, mock.Anything).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "partial payment - not enough",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         uuid.New().String(),
				AmountIOTA:     1000000,
				PaymentAddress: "iota1qpayment333",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(1 * time.Hour),
				CreatedAt:      time.Now().Add(-5 * time.Minute),
			},
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				txs := []domain.ReceivedTransaction{
					{Digest: "worker-tx-3", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 500000},
				}
				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment333", 50).Return(txs, nil)
			},
			wantErr: false,
		},
		{
			name: "network error querying transactions",
			intent: &domain.IOTAPaymentIntent{
				ID:             uuid.New().String(),
				UserID:         uuid.New().String(),
				AmountIOTA:     1000000,
				PaymentAddress: "iota1qpayment444",
				Status:         domain.PaymentIntentStatusPending,
				ExpiresAt:      time.Now().Add(1 * time.Hour),
				CreatedAt:      time.Now().Add(-5 * time.Minute),
			},
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment444", 50).
					Return(nil, errors.New("connection timeout"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTAPaymentRepository)
			mockClient := new(MockIOTAPaymentClient)
			tt.setupMocks(mockRepo, mockClient)

			w := &IOTAPaymentWorker{
				repo:   mockRepo,
				client: mockClient,
				done:   make(chan bool),
			}
			ctx := context.Background()

			err := w.checkPaymentIntent(ctx, tt.intent)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestIOTAPaymentWorker_CheckPaymentIntent_NoMatchingTransactions(t *testing.T) {
	// Verify that transactions whose timestamps predate (intent.CreatedAt - 5s) are not counted.
	// If only old transactions exist, totalAmount stays 0 and no payment is recorded.
	mockRepo := new(MockIOTAPaymentRepository)
	mockClient := new(MockIOTAPaymentClient)

	createdAt := time.Now()
	intent := &domain.IOTAPaymentIntent{
		ID:             uuid.New().String(),
		UserID:         uuid.New().String(),
		AmountIOTA:     1000000,
		PaymentAddress: "iota1qpayment_old",
		Status:         domain.PaymentIntentStatusPending,
		ExpiresAt:      createdAt.Add(1 * time.Hour),
		CreatedAt:      createdAt,
	}

	// Transaction timestamp is 30 seconds before intent creation — outside the 5s buffer window.
	oldTimestampMs := createdAt.Add(-30 * time.Second).UnixMilli()
	txs := []domain.ReceivedTransaction{
		{Digest: "old-tx-1", TimestampMs: oldTimestampMs, AmountIOTA: 9999999},
	}
	mockClient.On("QueryTransactionBlocks", mock.Anything, intent.PaymentAddress, 50).Return(txs, nil)

	w := &IOTAPaymentWorker{
		repo:   mockRepo,
		client: mockClient,
		done:   make(chan bool),
	}
	ctx := context.Background()

	err := w.checkPaymentIntent(ctx, intent)
	assert.NoError(t, err)

	// CreateTransaction and UpdatePaymentIntentStatus must NOT be called.
	mockRepo.AssertNotCalled(t, "CreateTransaction")
	mockRepo.AssertNotCalled(t, "UpdatePaymentIntentStatus")
	mockRepo.AssertExpectations(t)
	mockClient.AssertExpectations(t)
}

func TestIOTAPaymentWorker_ProcessPayments(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockIOTAPaymentRepository, *MockIOTAPaymentClient)
		wantErr    bool
	}{
		{
			name: "process multiple intents",
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				createdAt := time.Now().Add(-5 * time.Minute)
				intents := []*domain.IOTAPaymentIntent{
					{
						ID:             uuid.New().String(),
						UserID:         uuid.New().String(),
						AmountIOTA:     1000000,
						PaymentAddress: "iota1qpayment111",
						Status:         domain.PaymentIntentStatusPending,
						ExpiresAt:      time.Now().Add(1 * time.Hour),
						CreatedAt:      createdAt,
					},
					{
						ID:             uuid.New().String(),
						UserID:         uuid.New().String(),
						AmountIOTA:     500000,
						PaymentAddress: "iota1qpayment222",
						Status:         domain.PaymentIntentStatusPending,
						ExpiresAt:      time.Now().Add(1 * time.Hour),
						CreatedAt:      createdAt,
					},
				}

				repo.On("GetActivePaymentIntents", mock.Anything).Return(intents, nil)

				txs1 := []domain.ReceivedTransaction{
					{Digest: "proc-tx-1", TimestampMs: time.Now().UnixMilli(), AmountIOTA: 1000000},
				}
				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment111", 50).Return(txs1, nil)
				repo.On("GetWalletByUserID", mock.Anything, mock.Anything).Return(&domain.IOTAWallet{ID: "wallet-1"}, nil).Once()
				repo.On("CreateTransaction", mock.Anything, mock.Anything).Return(nil).Once()
				repo.On("UpdatePaymentIntentStatus", mock.Anything, intents[0].ID,
					domain.PaymentIntentStatusPaid, mock.Anything).Return(nil)

				client.On("QueryTransactionBlocks", mock.Anything, "iota1qpayment222", 50).
					Return([]domain.ReceivedTransaction{}, nil)

				repo.On("GetExpiredPaymentIntents", mock.Anything).Return([]*domain.IOTAPaymentIntent{}, nil)
			},
			wantErr: false,
		},
		{
			name: "no active intents",
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				repo.On("GetActivePaymentIntents", mock.Anything).
					Return([]*domain.IOTAPaymentIntent{}, nil)
				repo.On("GetExpiredPaymentIntents", mock.Anything).
					Return([]*domain.IOTAPaymentIntent{}, nil)
			},
			wantErr: false,
		},
		{
			name: "error fetching intents",
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				repo.On("GetActivePaymentIntents", mock.Anything).
					Return(nil, errors.New("database error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTAPaymentRepository)
			mockClient := new(MockIOTAPaymentClient)
			tt.setupMocks(mockRepo, mockClient)

			w := &IOTAPaymentWorker{
				repo:   mockRepo,
				client: mockClient,
				done:   make(chan bool),
			}
			ctx := context.Background()

			err := w.processPayments(ctx)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestIOTAPaymentWorker_ExpireOldIntents(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockIOTAPaymentRepository)
		wantErr    bool
	}{
		{
			name: "expire multiple intents",
			setupMocks: func(repo *MockIOTAPaymentRepository) {
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
			setupMocks: func(repo *MockIOTAPaymentRepository) {
				repo.On("GetExpiredPaymentIntents", mock.Anything).
					Return([]*domain.IOTAPaymentIntent{}, nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTAPaymentRepository)
			mockClient := new(MockIOTAPaymentClient)
			tt.setupMocks(mockRepo)

			w := &IOTAPaymentWorker{
				repo:   mockRepo,
				client: mockClient,
				done:   make(chan bool),
			}
			ctx := context.Background()

			err := w.expireOldIntents(ctx)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRepo.AssertExpectations(t)
		})
	}
}

func TestIOTAPaymentWorker_ErrorHandling(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockIOTAPaymentRepository, *MockIOTAPaymentClient)
		wantErr    bool
	}{
		{
			name: "retry on network error",
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment111",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
				}

				repo.On("GetActivePaymentIntents", mock.Anything).Return([]*domain.IOTAPaymentIntent{intent}, nil)

				client.On("QueryTransactionBlocks", mock.Anything, intent.PaymentAddress, 50).
					Return(nil, errors.New("network timeout")).Once()

				repo.On("GetExpiredPaymentIntents", mock.Anything).Return([]*domain.IOTAPaymentIntent{}, nil)
			},
			wantErr: false,
		},
		{
			name: "handle database error gracefully",
			setupMocks: func(repo *MockIOTAPaymentRepository, client *MockIOTAPaymentClient) {
				repo.On("GetActivePaymentIntents", mock.Anything).
					Return(nil, errors.New("database connection lost"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRepo := new(MockIOTAPaymentRepository)
			mockClient := new(MockIOTAPaymentClient)
			tt.setupMocks(mockRepo, mockClient)

			w := &IOTAPaymentWorker{
				repo:   mockRepo,
				client: mockClient,
				done:   make(chan bool),
			}
			ctx := context.Background()

			err := w.processPayments(ctx)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			mockRepo.AssertExpectations(t)
			mockClient.AssertExpectations(t)
		})
	}
}

func TestIOTAPaymentWorker_StartStop(t *testing.T) {
	mockRepo := new(MockIOTAPaymentRepository)
	mockClient := new(MockIOTAPaymentClient)

	mockRepo.On("GetActivePaymentIntents", mock.Anything).
		Return([]*domain.IOTAPaymentIntent{}, nil).Maybe()
	mockRepo.On("GetExpiredPaymentIntents", mock.Anything).
		Return([]*domain.IOTAPaymentIntent{}, nil).Maybe()

	worker := NewIOTAPaymentWorker(mockRepo, mockClient)
	ctx := context.Background()

	worker.Start(ctx, 100*time.Millisecond)

	time.Sleep(300 * time.Millisecond)

	worker.Stop()

	time.Sleep(100 * time.Millisecond)
}

func TestIOTAPaymentWorker_ContextCancellation(t *testing.T) {
	mockRepo := new(MockIOTAPaymentRepository)
	mockClient := new(MockIOTAPaymentClient)

	mockRepo.On("GetActivePaymentIntents", mock.Anything).
		Return([]*domain.IOTAPaymentIntent{}, nil).Maybe()
	mockRepo.On("GetExpiredPaymentIntents", mock.Anything).
		Return([]*domain.IOTAPaymentIntent{}, nil).Maybe()

	worker := NewIOTAPaymentWorker(mockRepo, mockClient)
	ctx, cancel := context.WithCancel(context.Background())

	worker.Start(ctx, 100*time.Millisecond)

	cancel()

	time.Sleep(200 * time.Millisecond)
	worker.Stop()
}
