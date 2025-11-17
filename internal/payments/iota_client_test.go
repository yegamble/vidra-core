package payments

import (
	"context"
	"errors"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockIOTANodeClient mocks the IOTA node HTTP client
type MockIOTANodeClient struct {
	mock.Mock
}

func (m *MockIOTANodeClient) GetNodeInfo(ctx context.Context) (*NodeInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*NodeInfo), args.Error(1)
}

func (m *MockIOTANodeClient) GetAddressBalance(ctx context.Context, address string) (int64, error) {
	args := m.Called(ctx, address)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockIOTANodeClient) GetTransactionStatus(ctx context.Context, txHash string) (*TransactionStatus, error) {
	args := m.Called(ctx, txHash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*TransactionStatus), args.Error(1)
}

func (m *MockIOTANodeClient) SubmitTransaction(ctx context.Context, tx *SignedTransaction) (string, error) {
	args := m.Called(ctx, tx)
	return args.String(0), args.Error(1)
}

// TestIOTAClient_GenerateSeed tests secure seed generation
func TestIOTAClient_GenerateSeed(t *testing.T) {
	tests := []struct {
		name    string
		wantErr bool
	}{
		{
			name:    "successful seed generation",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			seed, err := client.GenerateSeed()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			// Verify seed properties
			assert.NotEmpty(t, seed)
			assert.Len(t, seed, 64) // 256-bit seed as hex string
			// Verify it's hex
			assert.Regexp(t, "^[a-f0-9]{64}$", seed)
		})
	}
}

// TestIOTAClient_GenerateSeed_UniqueSeeds tests that seeds are unique
func TestIOTAClient_GenerateSeed_UniqueSeeds(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	seeds := make(map[string]bool)
	for i := 0; i < 100; i++ {
		seed, err := client.GenerateSeed()
		require.NoError(t, err)
		assert.False(t, seeds[seed], "Generated duplicate seed")
		seeds[seed] = true
	}
}

// TestIOTAClient_DeriveAddress tests address derivation from seed
func TestIOTAClient_DeriveAddress(t *testing.T) {
	tests := []struct {
		name    string
		seed    string
		index   uint32
		wantErr bool
		errType error
	}{
		{
			name:    "valid seed and index",
			seed:    repeatString("a", 64), // Valid 64-char hex seed
			index:   0,
			wantErr: false,
		},
		{
			name:    "different index",
			seed:    repeatString("b", 64),
			index:   1,
			wantErr: false,
		},
		{
			name:    "invalid seed length",
			seed:    "abc",
			index:   0,
			wantErr: true,
			errType: domain.ErrInvalidSeed,
		},
		{
			name:    "invalid seed characters",
			seed:    repeatString("g", 64), // 'g' is not hex
			index:   0,
			wantErr: true,
			errType: domain.ErrInvalidSeed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			address, err := client.DeriveAddress(tt.seed, tt.index)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)

			// Verify address format (IOTA Bech32 format)
			assert.NotEmpty(t, address)
			assert.True(t, len(address) > 10)
			assert.True(t, address[:5] == "iota1", "Address should start with iota1")
		})
	}
}

// TestIOTAClient_DeriveAddress_Deterministic tests that derivation is deterministic
func TestIOTAClient_DeriveAddress_Deterministic(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	seed := repeatString("a", 64)
	index := uint32(0)

	// Generate address twice
	addr1, err := client.DeriveAddress(seed, index)
	require.NoError(t, err)

	addr2, err := client.DeriveAddress(seed, index)
	require.NoError(t, err)

	// Should be identical
	assert.Equal(t, addr1, addr2)
}

// TestIOTAClient_DeriveAddress_DifferentIndexes tests different indexes produce different addresses
func TestIOTAClient_DeriveAddress_DifferentIndexes(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	seed := repeatString("a", 64)

	addr0, err := client.DeriveAddress(seed, 0)
	require.NoError(t, err)

	addr1, err := client.DeriveAddress(seed, 1)
	require.NoError(t, err)

	// Should be different
	assert.NotEqual(t, addr0, addr1)
}

// TestIOTAClient_ValidateAddress tests address validation
func TestIOTAClient_ValidateAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{
			name:    "valid bech32 address",
			address: "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
			want:    true,
		},
		{
			name:    "empty address",
			address: "",
			want:    false,
		},
		{
			name:    "wrong prefix",
			address: "btc1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
			want:    false,
		},
		{
			name:    "too short",
			address: "iota1abc",
			want:    false,
		},
		{
			name:    "invalid characters",
			address: "iota1@#$%^&*()_+",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			valid := client.ValidateAddress(tt.address)
			assert.Equal(t, tt.want, valid)
		})
	}
}

// TestIOTAClient_GetBalance tests balance query
func TestIOTAClient_GetBalance(t *testing.T) {
	tests := []struct {
		name        string
		address     string
		mockBalance int64
		mockError   error
		wantErr     bool
		errType     error
	}{
		{
			name:        "successful balance query",
			address:     "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
			mockBalance: 1000000,
			mockError:   nil,
			wantErr:     false,
		},
		{
			name:        "zero balance",
			address:     "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7abc",
			mockBalance: 0,
			mockError:   nil,
			wantErr:     false,
		},
		{
			name:        "network error",
			address:     "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7def",
			mockBalance: 0,
			mockError:   errors.New("connection timeout"),
			wantErr:     true,
			errType:     domain.ErrIOTANodeUnavailable,
		},
		{
			name:        "invalid address",
			address:     "invalid",
			mockBalance: 0,
			mockError:   nil,
			wantErr:     true,
			errType:     domain.ErrInvalidAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNodeClient := new(MockIOTANodeClient)
			client := NewIOTAClientWithMock(mockNodeClient)
			ctx := context.Background()

			if tt.address != "invalid" {
				mockNodeClient.On("GetAddressBalance", ctx, tt.address).Return(tt.mockBalance, tt.mockError)
			}

			balance, err := client.GetBalance(ctx, tt.address)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mockBalance, balance)

			mockNodeClient.AssertExpectations(t)
		})
	}
}

// TestIOTAClient_BuildTransaction tests transaction building
func TestIOTAClient_BuildTransaction(t *testing.T) {
	tests := []struct {
		name        string
		fromAddress string
		toAddress   string
		amount      int64
		wantErr     bool
		errType     error
	}{
		{
			name:        "valid transaction",
			fromAddress: "iota1qsender111111111111111111111111111111111111111111111111111",
			toAddress:   "iota1qrecipient111111111111111111111111111111111111111111111111",
			amount:      1000000,
			wantErr:     false,
		},
		{
			name:        "zero amount",
			fromAddress: "iota1qsender111111111111111111111111111111111111111111111111111",
			toAddress:   "iota1qrecipient111111111111111111111111111111111111111111111111",
			amount:      0,
			wantErr:     true,
			errType:     domain.ErrInvalidAmount,
		},
		{
			name:        "negative amount",
			fromAddress: "iota1qsender111111111111111111111111111111111111111111111111111",
			toAddress:   "iota1qrecipient111111111111111111111111111111111111111111111111",
			amount:      -1000,
			wantErr:     true,
			errType:     domain.ErrInvalidAmount,
		},
		{
			name:        "invalid from address",
			fromAddress: "invalid",
			toAddress:   "iota1qrecipient111111111111111111111111111111111111111111111111",
			amount:      1000000,
			wantErr:     true,
			errType:     domain.ErrInvalidAddress,
		},
		{
			name:        "invalid to address",
			fromAddress: "iota1qsender111111111111111111111111111111111111111111111111111",
			toAddress:   "invalid",
			amount:      1000000,
			wantErr:     true,
			errType:     domain.ErrInvalidAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			tx, err := client.BuildTransaction(tt.fromAddress, tt.toAddress, tt.amount)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, tx)
		})
	}
}

// TestIOTAClient_SignTransaction tests transaction signing
func TestIOTAClient_SignTransaction(t *testing.T) {
	tests := []struct {
		name    string
		seed    string
		tx      interface{}
		wantErr bool
		errType error
	}{
		{
			name:    "valid signing",
			seed:    repeatString("a", 64),
			tx:      &UnsignedTransaction{},
			wantErr: false,
		},
		{
			name:    "invalid seed",
			seed:    "invalid",
			tx:      &UnsignedTransaction{},
			wantErr: true,
			errType: domain.ErrInvalidSeed,
		},
		{
			name:    "nil transaction",
			seed:    repeatString("a", 64),
			tx:      nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			signed, err := client.SignTransaction(tt.seed, tt.tx)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.NotNil(t, signed)
			assert.NotEmpty(t, signed.Signature)
		})
	}
}

// TestIOTAClient_SubmitTransaction tests transaction submission
func TestIOTAClient_SubmitTransaction(t *testing.T) {
	tests := []struct {
		name       string
		tx         *SignedTransaction
		mockTxHash string
		mockError  error
		wantErr    bool
		errType    error
	}{
		{
			name: "successful submission",
			tx: &SignedTransaction{
				FromAddress: "iota1qsender",
				ToAddress:   "iota1qrecipient",
				Amount:      1000000,
				Signature:   []byte("signature"),
			},
			mockTxHash: "0x1234567890abcdef",
			mockError:  nil,
			wantErr:    false,
		},
		{
			name: "network error",
			tx: &SignedTransaction{
				FromAddress: "iota1qsender",
				ToAddress:   "iota1qrecipient",
				Amount:      1000000,
				Signature:   []byte("signature"),
			},
			mockTxHash: "",
			mockError:  errors.New("network timeout"),
			wantErr:    true,
			errType:    domain.ErrTransactionBroadcast,
		},
		{
			name:    "nil transaction",
			tx:      nil,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNodeClient := new(MockIOTANodeClient)
			client := NewIOTAClientWithMock(mockNodeClient)
			ctx := context.Background()

			if tt.tx != nil {
				mockNodeClient.On("SubmitTransaction", ctx, tt.tx).Return(tt.mockTxHash, tt.mockError)
			}

			txHash, err := client.SubmitTransaction(ctx, tt.tx)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mockTxHash, txHash)

			if tt.tx != nil {
				mockNodeClient.AssertExpectations(t)
			}
		})
	}
}

// TestIOTAClient_GetTransactionStatus tests transaction status queries
func TestIOTAClient_GetTransactionStatus(t *testing.T) {
	tests := []struct {
		name       string
		txHash     string
		mockStatus *TransactionStatus
		mockError  error
		wantErr    bool
	}{
		{
			name:   "confirmed transaction",
			txHash: "0x1234567890abcdef",
			mockStatus: &TransactionStatus{
				TxHash:        "0x1234567890abcdef",
				Confirmations: 15,
				IsConfirmed:   true,
				BlockID:       "block123",
			},
			mockError: nil,
			wantErr:   false,
		},
		{
			name:   "pending transaction",
			txHash: "0xabcdef1234567890",
			mockStatus: &TransactionStatus{
				TxHash:        "0xabcdef1234567890",
				Confirmations: 3,
				IsConfirmed:   false,
				BlockID:       "",
			},
			mockError: nil,
			wantErr:   false,
		},
		{
			name:       "transaction not found",
			txHash:     "0xnonexistent",
			mockStatus: nil,
			mockError:  errors.New("transaction not found"),
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNodeClient := new(MockIOTANodeClient)
			client := NewIOTAClientWithMock(mockNodeClient)
			ctx := context.Background()

			mockNodeClient.On("GetTransactionStatus", ctx, tt.txHash).Return(tt.mockStatus, tt.mockError)

			status, err := client.GetTransactionStatus(ctx, tt.txHash)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.mockStatus, status)

			mockNodeClient.AssertExpectations(t)
		})
	}
}

// TestIOTAClient_WaitForConfirmation tests waiting for transaction confirmation
func TestIOTAClient_WaitForConfirmation(t *testing.T) {
	tests := []struct {
		name             string
		txHash           string
		requiredConfirms int
		mockStatuses     []*TransactionStatus
		wantErr          bool
		expectedConfirms int
	}{
		{
			name:             "quick confirmation",
			txHash:           "0x1234567890abcdef",
			requiredConfirms: 10,
			mockStatuses: []*TransactionStatus{
				{Confirmations: 5, IsConfirmed: false},
				{Confirmations: 10, IsConfirmed: true},
			},
			wantErr:          false,
			expectedConfirms: 10,
		},
		{
			name:             "timeout before confirmation",
			txHash:           "0xabcdef1234567890",
			requiredConfirms: 10,
			mockStatuses: []*TransactionStatus{
				{Confirmations: 2, IsConfirmed: false},
				{Confirmations: 4, IsConfirmed: false},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNodeClient := new(MockIOTANodeClient)
			client := NewIOTAClientWithMock(mockNodeClient)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			// Mock multiple status calls
			for i, status := range tt.mockStatuses {
				// For timeout scenarios, the last status should be returnable indefinitely
				if tt.wantErr && i == len(tt.mockStatuses)-1 {
					mockNodeClient.On("GetTransactionStatus", mock.Anything, tt.txHash).
						Return(status, nil).Maybe()
				} else {
					mockNodeClient.On("GetTransactionStatus", mock.Anything, tt.txHash).
						Return(status, nil).Once()
				}
			}

			confirmations, err := client.WaitForConfirmation(ctx, tt.txHash, tt.requiredConfirms, 500*time.Millisecond)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.expectedConfirms, confirmations)
		})
	}
}

// TestIOTAClient_ContextTimeout tests that operations respect context timeouts
func TestIOTAClient_ContextTimeout(t *testing.T) {
	mockNodeClient := new(MockIOTANodeClient)
	client := NewIOTAClientWithMock(mockNodeClient)

	// Create a context that's already cancelled
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	mockNodeClient.On("GetAddressBalance", ctx, mock.Anything).
		Return(int64(0), context.Canceled)

	_, err := client.GetBalance(ctx, "iota1qtest")
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

// Helper to repeat string
func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
