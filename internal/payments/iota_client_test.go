package payments

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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

func TestIOTAClient_GenerateKeypair(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	privKey, pubKey, err := client.GenerateKeypair()

	require.NoError(t, err)
	assert.Len(t, privKey, 32, "Ed25519 seed/private key should be 32 bytes")
	assert.Len(t, pubKey, 32, "Ed25519 public key should be 32 bytes")
	assert.NotEmpty(t, privKey)
	assert.NotEmpty(t, pubKey)
}

func TestIOTAClient_GenerateKeypair_Unique(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		privKey, _, err := client.GenerateKeypair()
		require.NoError(t, err)
		keyHex := hex.EncodeToString(privKey)
		assert.False(t, seen[keyHex], "Generated duplicate keypair at iteration %d", i)
		seen[keyHex] = true
	}
}

func TestIOTAClient_DeriveAddress(t *testing.T) {
	tests := []struct {
		name      string
		publicKey []byte
		wantErr   bool
		errType   error
	}{
		{
			name:      "valid 32-byte public key",
			publicKey: make([]byte, 32),
			wantErr:   false,
		},
		{
			name: "non-zero public key",
			publicKey: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
				0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
				0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20},
			wantErr: false,
		},
		{
			name:      "empty public key",
			publicKey: []byte{},
			wantErr:   true,
			errType:   domain.ErrInvalidAddress,
		},
		{
			name:      "nil public key",
			publicKey: nil,
			wantErr:   true,
			errType:   domain.ErrInvalidAddress,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := NewIOTAClient("https://api.testnet.iota.org")

			address, err := client.DeriveAddress(tt.publicKey)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errType != nil {
					assert.ErrorIs(t, err, tt.errType)
				}
				return
			}
			require.NoError(t, err)

			assert.NotEmpty(t, address)
			assert.Equal(t, 66, len(address), "Address should be 66 chars (0x + 64 hex)")
			assert.True(t, strings.HasPrefix(address, "0x"), "Address should start with 0x")
			_, hexErr := hex.DecodeString(address[2:])
			assert.NoError(t, hexErr, "Address hex portion should be valid")
		})
	}
}

func TestIOTAClient_DeriveAddress_Deterministic(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	pubKey := make([]byte, 32)
	pubKey[0] = 0xab

	addr1, err := client.DeriveAddress(pubKey)
	require.NoError(t, err)

	addr2, err := client.DeriveAddress(pubKey)
	require.NoError(t, err)

	assert.Equal(t, addr1, addr2, "Same public key should always derive the same address")
}

func TestIOTAClient_DeriveAddress_DifferentKeys(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	pubKey1 := make([]byte, 32)
	pubKey2 := make([]byte, 32)
	pubKey2[0] = 0xff

	addr1, err := client.DeriveAddress(pubKey1)
	require.NoError(t, err)

	addr2, err := client.DeriveAddress(pubKey2)
	require.NoError(t, err)

	assert.NotEqual(t, addr1, addr2, "Different public keys should produce different addresses")
}

func TestIOTAClient_ValidateAddress(t *testing.T) {
	tests := []struct {
		name    string
		address string
		want    bool
	}{
		{
			name:    "valid hex address with 0x prefix",
			address: "0x" + repeatString("a", 64),
			want:    true,
		},
		{
			name:    "valid hex address mixed case",
			address: "0x" + repeatString("f", 64),
			want:    true,
		},
		{
			name:    "empty address",
			address: "",
			want:    false,
		},
		{
			name:    "old bech32 iota1 format",
			address: "iota1qpg7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9pq7xkj9",
			want:    false,
		},
		{
			name:    "too short",
			address: "0x1234abcd",
			want:    false,
		},
		{
			name:    "correct length but invalid hex chars",
			address: "0x" + repeatString("g", 64),
			want:    false,
		},
		{
			name:    "no 0x prefix",
			address: repeatString("a", 64),
			want:    false,
		},
		{
			name:    "too long",
			address: "0x" + repeatString("a", 65),
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
			address:     "0x" + repeatString("a", 64),
			mockBalance: 1000000,
			mockError:   nil,
			wantErr:     false,
		},
		{
			name:        "zero balance",
			address:     "0x" + repeatString("b", 64),
			mockBalance: 0,
			mockError:   nil,
			wantErr:     false,
		},
		{
			name:        "network error",
			address:     "0x" + repeatString("c", 64),
			mockBalance: 0,
			mockError:   errors.New("connection timeout"),
			wantErr:     true,
			errType:     domain.ErrIOTANodeUnavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNodeClient := new(MockIOTANodeClient)
			client := NewIOTAClientWithMock(mockNodeClient)
			ctx := context.Background()

			mockNodeClient.On("GetAddressBalance", ctx, tt.address).Return(tt.mockBalance, tt.mockError)

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

func TestIOTAClient_BuildTransaction_NotImplemented(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	_, err := client.BuildTransaction("from", "to", 1000000)

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestIOTAClient_SignTransaction_NotImplemented(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	_, err := client.SignTransaction("privkey", &UnsignedTransaction{})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

func TestIOTAClient_SubmitTransaction_NotImplemented(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")
	ctx := context.Background()

	_, err := client.SubmitTransaction(ctx, &SignedTransaction{
		FromAddress: "from",
		ToAddress:   "to",
		Amount:      1000000,
		Signature:   []byte("sig"),
	})

	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotImplemented)
}

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

			for i, status := range tt.mockStatuses {
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

func TestIOTAClient_ContextTimeout(t *testing.T) {
	mockNodeClient := new(MockIOTANodeClient)
	client := NewIOTAClientWithMock(mockNodeClient)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	testAddr := "0x" + repeatString("a", 64)
	mockNodeClient.On("GetAddressBalance", ctx, mock.Anything).
		Return(int64(0), context.Canceled)

	_, err := client.GetBalance(ctx, testAddr)
	assert.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestIOTAClient_DeriveAddress_Blake2b(t *testing.T) {
	client := NewIOTAClient("https://api.testnet.iota.org")

	pubKey := make([]byte, 32)
	pubKey[0] = 0x42

	addr, err := client.DeriveAddress(pubKey)
	require.NoError(t, err)

	copyAddr := "0x" + hex.EncodeToString(pubKey)
	assert.NotEqual(t, copyAddr, addr, "Address must be Blake2b hash, not a direct copy of the public key")
	assert.Equal(t, 66, len(addr))
	assert.True(t, strings.HasPrefix(addr, "0x"))
}

func TestIOTAClient_JSONRPC_GetBalance_CallsServer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "iotax_getBalance", req["method"])

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"coinType":        "0x2::iota::IOTA",
				"coinObjectCount": 1,
				"totalBalance":    "1500000",
				"lockedBalance":   map[string]interface{}{},
			},
		})
	}))
	defer server.Close()

	client := NewIOTAClient(server.URL)
	balance, err := client.GetBalance(context.Background(), "0x"+repeatString("a", 64))

	require.NoError(t, err)
	assert.True(t, called, "Expected HTTP call to server")
	assert.Equal(t, int64(1500000), balance)
}

func TestIOTAClient_JSONRPC_GetNodeInfo_CallsServer(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "iota_getLatestCheckpointSequenceNumber", req["method"])

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result":  "99999",
		})
	}))
	defer server.Close()

	client := NewIOTAClient(server.URL)
	info, err := client.GetNodeInfo(context.Background())

	require.NoError(t, err)
	assert.True(t, called, "Expected HTTP call to server")
	assert.True(t, info.IsHealthy)
}

func TestIOTAClient_JSONRPC_GetTransactionStatus_CallsServer(t *testing.T) {
	txDigest := "Bx7mFpVYhSFpFMGGVeVRVTqDjDFNPQmB6YEjCX2HuUV"
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		var req map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&req)
		assert.Equal(t, "iota_getTransactionBlock", req["method"])

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"digest":     txDigest,
				"checkpoint": "12345",
			},
		})
	}))
	defer server.Close()

	client := NewIOTAClient(server.URL)
	status, err := client.GetTransactionStatus(context.Background(), txDigest)

	require.NoError(t, err)
	assert.True(t, called, "Expected HTTP call to server")
	assert.Equal(t, txDigest, status.TxHash)
	assert.True(t, status.IsConfirmed)
	assert.Equal(t, "12345", status.BlockID)
}

func TestIOTAClient_JSONRPC_RetryOnServerError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"result": map[string]interface{}{
				"coinType":     "0x2::iota::IOTA",
				"totalBalance": "500000",
			},
		})
	}))
	defer server.Close()

	client := NewIOTAClient(server.URL)
	balance, err := client.GetBalance(context.Background(), "0x"+repeatString("a", 64))

	require.NoError(t, err, "Should succeed after retries")
	assert.Equal(t, int64(500000), balance)
	assert.GreaterOrEqual(t, callCount, 3, "Should have retried at least 3 times")
}

func TestIOTAClient_JSONRPC_NoRetryOnRPCError(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      1,
			"error": map[string]interface{}{
				"code":    -32602,
				"message": "Invalid params",
			},
		})
	}))
	defer server.Close()

	client := NewIOTAClient(server.URL)
	_, err := client.GetBalance(context.Background(), "0x"+repeatString("a", 64))

	require.Error(t, err)
	assert.Equal(t, 1, callCount, "RPC errors should not be retried")
}

func repeatString(s string, n int) string {
	result := ""
	for i := 0; i < n; i++ {
		result += s
	}
	return result
}
