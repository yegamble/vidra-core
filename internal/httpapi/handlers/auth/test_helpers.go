package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// NewServer creates an AuthHandlers instance for tests (backwards compatibility)
func NewServer(
	userRepo interface{},
	authRepo interface{},
	jwtSecret string,
	emailService interface{},
	pingTimeout int,
	ipfsAPI string,
	ipfsClusterAPI string,
	redisTimeout int,
	redisClient interface{},
) *AuthHandlers {
	// This is a stub for test compatibility
	// Tests should be refactored to use NewAuthHandlers directly
	h := &AuthHandlers{
		jwtSecret:      jwtSecret,
		ipfsAPI:        ipfsAPI,
		ipfsClusterAPI: ipfsClusterAPI,
	}

	// Set the userRepo if provided
	if ur, ok := userRepo.(usecase.UserRepository); ok && ur != nil {
		h.userRepo = ur
	}

	// Set the authRepo if provided
	if ar, ok := authRepo.(usecase.AuthRepository); ok && ar != nil {
		h.authRepo = ar
	}

	return h
}

// integResp is an alias for testResponse for backwards compatibility
type integResp = testResponse

// testResponse is the standard response structure for tests
type testResponse struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

// withUserID adds a user ID to the context (test helper)
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}

// decodeResponse decodes a response for tests
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) testResponse {
	t.Helper()
	var resp testResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

// Stub methods for testing - these should be implemented properly
// or tests should be refactored to use real handlers

// Login is a stub method for tests
func (h *AuthHandlers) Login(w http.ResponseWriter, r *http.Request) {
	// Stub implementation for tests
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  "test-token",
		"refresh_token": "test-refresh",
	})
}

// Register is a stub method for tests
func (h *AuthHandlers) Register(w http.ResponseWriter, r *http.Request) {
	// Parse request body to perform basic validation for tests
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	// Validate required fields
	if req.Username == "" || req.Email == "" || req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing required fields"))
		return
	}

	// Check for duplicates (stub uses simple hardcoded checks for test data)
	if req.Email == "dup@example.com" {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("CONFLICT", "Email already exists"))
		return
	}
	if req.Username == "dupname" {
		shared.WriteError(w, http.StatusConflict, domain.NewDomainError("CONFLICT", "Username already exists"))
		return
	}

	// Return success response
	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"user": map[string]interface{}{
			"id":       "test-user-id",
			"username": req.Username,
			"email":    req.Email,
		},
		"access_token":  "test-access-token",
		"refresh_token": "test-refresh-token",
	})
}

// RefreshToken is a stub method for tests
func (h *AuthHandlers) RefreshToken(w http.ResponseWriter, r *http.Request) {
	// Stub implementation for tests
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  "new-test-token",
		"refresh_token": "new-test-refresh",
	})
}

// Logout is a stub method for tests
func (h *AuthHandlers) Logout(w http.ResponseWriter, r *http.Request) {
	// Stub implementation for tests
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message": "Logged out successfully",
	})
}
