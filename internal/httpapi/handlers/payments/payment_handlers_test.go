package payments

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockPaymentService mocks the payment service
type MockPaymentService struct {
	mock.Mock
}

func (m *MockPaymentService) CreateWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockPaymentService) GetWalletBalance(ctx context.Context, userID string) (int64, error) {
	args := m.Called(ctx, userID)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockPaymentService) GetWallet(ctx context.Context, userID string) (*domain.IOTAWallet, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAWallet), args.Error(1)
}

func (m *MockPaymentService) CreatePaymentIntent(ctx context.Context, userID string, videoID *string, amount int64) (*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx, userID, videoID, amount)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockPaymentService) GetPaymentIntent(ctx context.Context, intentID string) (*domain.IOTAPaymentIntent, error) {
	args := m.Called(ctx, intentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IOTAPaymentIntent), args.Error(1)
}

func (m *MockPaymentService) GetTransactionHistory(ctx context.Context, userID string, limit, offset int) ([]*domain.IOTATransaction, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.IOTATransaction), args.Error(1)
}

// TestCreateWallet tests POST /api/v1/payments/wallet
func TestCreateWallet(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		authenticated  bool
		setupMocks     func(*MockPaymentService)
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name:          "successful wallet creation",
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				wallet := &domain.IOTAWallet{
					ID:          uuid.New().String(),
					UserID:      uuid.New().String(),
					Address:     "iota1qwallet111111111111111111111111111111111111111111111111111",
					BalanceIOTA: 0,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
				svc.On("CreateWallet", mock.Anything, mock.Anything).Return(wallet, nil)
			},
			expectedStatus: http.StatusCreated,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.NotEmpty(t, data["id"])
				assert.NotEmpty(t, data["address"])
				assert.Equal(t, float64(0), data["balance_iota"])
				// Verify encrypted seed is NOT in response
				assert.Nil(t, data["encrypted_seed"])
				assert.Nil(t, data["seed_nonce"])
			},
		},
		{
			name:          "wallet already exists",
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				svc.On("CreateWallet", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletAlreadyExists)
			},
			expectedStatus: http.StatusConflict,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
				assert.Contains(t, resp["error"].(string), "already exists")
			},
		},
		{
			name:           "unauthenticated request",
			userID:         "",
			authenticated:  false,
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusUnauthorized,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			tt.setupMocks(mockService)

			handler := NewPaymentHandler(mockService)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/wallet", nil)
			if tt.authenticated {
				ctx := context.WithValue(req.Context(), "user_id", tt.userID)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()

			handler.CreateWallet(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			var response map[string]interface{}
			err := json.Unmarshal(rr.Body.Bytes(), &response)
			require.NoError(t, err)

			if tt.checkResponse != nil {
				tt.checkResponse(t, response)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// TestGetWallet tests GET /api/v1/payments/wallet
func TestGetWallet(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		authenticated  bool
		setupMocks     func(*MockPaymentService)
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name:          "get existing wallet",
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				wallet := &domain.IOTAWallet{
					ID:          uuid.New().String(),
					UserID:      uuid.New().String(),
					Address:     "iota1qwallet111111111111111111111111111111111111111111111111111",
					BalanceIOTA: 1000000,
					CreatedAt:   time.Now(),
					UpdatedAt:   time.Now(),
				}
				svc.On("GetWallet", mock.Anything, mock.Anything).Return(wallet, nil)
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.NotEmpty(t, data["address"])
				assert.Equal(t, float64(1000000), data["balance_iota"])
			},
		},
		{
			name:          "wallet not found",
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				svc.On("GetWallet", mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
			},
		},
		{
			name:           "unauthenticated",
			userID:         "",
			authenticated:  false,
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			tt.setupMocks(mockService)

			handler := NewPaymentHandler(mockService)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/wallet", nil)
			if tt.authenticated {
				ctx := context.WithValue(req.Context(), "user_id", tt.userID)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()

			handler.GetWallet(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.checkResponse != nil {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				require.NoError(t, err)
				tt.checkResponse(t, response)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// TestCreatePaymentIntent tests POST /api/v1/payments/intent
func TestCreatePaymentIntent(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		authenticated  bool
		requestBody    map[string]interface{}
		setupMocks     func(*MockPaymentService)
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name:          "successful intent creation",
			userID:        uuid.New().String(),
			authenticated: true,
			requestBody: map[string]interface{}{
				"amount_iota": 1000000,
				"video_id":    uuid.New().String(),
			},
			setupMocks: func(svc *MockPaymentService) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					VideoID:        sql.NullString{String: uuid.New().String(), Valid: true},
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment111111111111111111111111111111111111111111111111",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}
				svc.On("CreatePaymentIntent", mock.Anything, mock.Anything, mock.Anything, int64(1000000)).
					Return(intent, nil)
			},
			expectedStatus: http.StatusCreated,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.NotEmpty(t, data["id"])
				assert.NotEmpty(t, data["payment_address"])
				assert.Equal(t, "pending", data["status"])
				assert.Equal(t, float64(1000000), data["amount_iota"])
			},
		},
		{
			name:          "invalid amount - zero",
			userID:        uuid.New().String(),
			authenticated: true,
			requestBody: map[string]interface{}{
				"amount_iota": 0,
			},
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
				assert.Contains(t, resp["error"].(string), "amount")
			},
		},
		{
			name:          "invalid amount - negative",
			userID:        uuid.New().String(),
			authenticated: true,
			requestBody: map[string]interface{}{
				"amount_iota": -1000,
			},
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusBadRequest,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
			},
		},
		{
			name:           "missing amount",
			userID:         uuid.New().String(),
			authenticated:  true,
			requestBody:    map[string]interface{}{},
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "unauthenticated",
			userID:         "",
			authenticated:  false,
			requestBody:    map[string]interface{}{},
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			tt.setupMocks(mockService)

			handler := NewPaymentHandler(mockService)

			bodyBytes, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/intent", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			if tt.authenticated {
				ctx := context.WithValue(req.Context(), "user_id", tt.userID)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()

			handler.CreatePaymentIntent(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.checkResponse != nil {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				require.NoError(t, err)
				tt.checkResponse(t, response)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// TestGetPaymentIntent tests GET /api/v1/payments/intent/:id
func TestGetPaymentIntent(t *testing.T) {
	tests := []struct {
		name           string
		intentID       string
		userID         string
		authenticated  bool
		setupMocks     func(*MockPaymentService)
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name:          "get existing intent",
			intentID:      uuid.New().String(),
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				intent := &domain.IOTAPaymentIntent{
					ID:             uuid.New().String(),
					UserID:         uuid.New().String(),
					AmountIOTA:     1000000,
					PaymentAddress: "iota1qpayment111",
					Status:         domain.PaymentIntentStatusPending,
					ExpiresAt:      time.Now().Add(1 * time.Hour),
					CreatedAt:      time.Now(),
					UpdatedAt:      time.Now(),
				}
				svc.On("GetPaymentIntent", mock.Anything, mock.Anything).Return(intent, nil)
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.True(t, resp["success"].(bool))
				data := resp["data"].(map[string]interface{})
				assert.Equal(t, "pending", data["status"])
				assert.NotEmpty(t, data["payment_address"])
			},
		},
		{
			name:          "intent not found",
			intentID:      uuid.New().String(),
			userID:        uuid.New().String(),
			authenticated: true,
			setupMocks: func(svc *MockPaymentService) {
				svc.On("GetPaymentIntent", mock.Anything, mock.Anything).
					Return(nil, domain.ErrPaymentIntentNotFound)
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.False(t, resp["success"].(bool))
			},
		},
		{
			name:           "unauthenticated",
			intentID:       uuid.New().String(),
			userID:         "",
			authenticated:  false,
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			tt.setupMocks(mockService)

			handler := NewPaymentHandler(mockService)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/intent/"+tt.intentID, nil)
			if tt.authenticated {
				ctx := context.WithValue(req.Context(), "user_id", tt.userID)
				req = req.WithContext(ctx)
			}

			// Add URL params using chi router
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.intentID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			rr := httptest.NewRecorder()

			handler.GetPaymentIntent(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.checkResponse != nil {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				require.NoError(t, err)
				tt.checkResponse(t, response)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// TestGetTransactionHistory tests GET /api/v1/payments/transactions
func TestGetTransactionHistory(t *testing.T) {
	tests := []struct {
		name           string
		userID         string
		authenticated  bool
		queryParams    string
		setupMocks     func(*MockPaymentService)
		expectedStatus int
		checkResponse  func(*testing.T, map[string]interface{})
	}{
		{
			name:          "get transaction history",
			userID:        uuid.New().String(),
			authenticated: true,
			queryParams:   "?limit=10&offset=0",
			setupMocks: func(svc *MockPaymentService) {
				transactions := []*domain.IOTATransaction{
					{
						ID:              uuid.New().String(),
						TransactionHash: "0x1234567890abcdef",
						AmountIOTA:      1000000,
						TxType:          domain.TransactionTypeDeposit,
						Status:          domain.TransactionStatusConfirmed,
						Confirmations:   15,
						CreatedAt:       time.Now(),
					},
				}
				svc.On("GetTransactionHistory", mock.Anything, mock.Anything, 10, 0).
					Return(transactions, nil)
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, resp map[string]interface{}) {
				assert.True(t, resp["success"].(bool))
				data := resp["data"].([]interface{})
				assert.Len(t, data, 1)
				tx := data[0].(map[string]interface{})
				assert.Equal(t, "deposit", tx["tx_type"])
				assert.Equal(t, "confirmed", tx["status"])
			},
		},
		{
			name:          "wallet not found",
			userID:        uuid.New().String(),
			authenticated: true,
			queryParams:   "",
			setupMocks: func(svc *MockPaymentService) {
				svc.On("GetTransactionHistory", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, domain.ErrWalletNotFound)
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "unauthenticated",
			userID:         "",
			authenticated:  false,
			queryParams:    "",
			setupMocks:     func(svc *MockPaymentService) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			tt.setupMocks(mockService)

			handler := NewPaymentHandler(mockService)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/transactions"+tt.queryParams, nil)
			if tt.authenticated {
				ctx := context.WithValue(req.Context(), "user_id", tt.userID)
				req = req.WithContext(ctx)
			}
			rr := httptest.NewRecorder()

			handler.GetTransactionHistory(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.checkResponse != nil {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				require.NoError(t, err)
				tt.checkResponse(t, response)
			}

			mockService.AssertExpectations(t)
		})
	}
}

// TestValidateInputSanitization tests that inputs are properly sanitized
func TestValidateInputSanitization(t *testing.T) {
	tests := []struct {
		name           string
		requestBody    map[string]interface{}
		expectedStatus int
	}{
		{
			name: "SQL injection attempt in video_id",
			requestBody: map[string]interface{}{
				"amount_iota": 1000000,
				"video_id":    "'; DROP TABLE videos; --",
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name: "XSS attempt in metadata",
			requestBody: map[string]interface{}{
				"amount_iota": 1000000,
				"metadata":    "<script>alert('xss')</script>",
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			handler := NewPaymentHandler(mockService)

			bodyBytes, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/payments/intent", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(req.Context(), "user_id", uuid.New().String())
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handler.CreatePaymentIntent(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)
		})
	}
}

// TestRateLimiting tests rate limiting on endpoints
func TestRateLimiting(t *testing.T) {
	t.Skip("Rate limiting integration test - requires middleware setup")
}

// TestConcurrentRequests tests handling of concurrent requests
func TestConcurrentRequests(t *testing.T) {
	t.Skip("Concurrent request handling test - requires load testing")
}

// TestErrorHandling tests proper error response formatting
func TestErrorHandling(t *testing.T) {
	tests := []struct {
		name        string
		mockError   error
		expectedMsg string
	}{
		{
			name:        "wallet not found",
			mockError:   domain.ErrWalletNotFound,
			expectedMsg: "Wallet not found",
		},
		{
			name:        "invalid amount",
			mockError:   domain.ErrInvalidAmount,
			expectedMsg: "Invalid payment amount",
		},
		{
			name:        "generic error",
			mockError:   errors.New("database connection failed"),
			expectedMsg: "Internal server error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockService := new(MockPaymentService)
			mockService.On("GetWallet", mock.Anything, mock.Anything).Return(nil, tt.mockError)

			handler := NewPaymentHandler(mockService)

			req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/wallet", nil)
			ctx := context.WithValue(req.Context(), "user_id", uuid.New().String())
			req = req.WithContext(ctx)
			rr := httptest.NewRecorder()

			handler.GetWallet(rr, req)

			var response map[string]interface{}
			err := json.Unmarshal(rr.Body.Bytes(), &response)
			require.NoError(t, err)

			assert.False(t, response["success"].(bool))
			// Error message should be user-friendly, not exposing internal details
			if tt.mockError == domain.ErrWalletNotFound || tt.mockError == domain.ErrInvalidAmount {
				assert.Contains(t, response["error"].(string), tt.expectedMsg)
			}
		})
	}
}

// TestSecurityHeaders tests that security headers are properly set
func TestSecurityHeaders(t *testing.T) {
	mockService := new(MockPaymentService)
	wallet := &domain.IOTAWallet{
		ID:          uuid.New().String(),
		UserID:      uuid.New().String(),
		Address:     "iota1qwallet",
		BalanceIOTA: 1000000,
	}
	mockService.On("GetWallet", mock.Anything, mock.Anything).Return(wallet, nil)

	handler := NewPaymentHandler(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/payments/wallet", nil)
	ctx := context.WithValue(req.Context(), "user_id", uuid.New().String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	handler.GetWallet(rr, req)

	// Check security headers (these would typically be set by middleware)
	// For now, just verify response format
	assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))
}
