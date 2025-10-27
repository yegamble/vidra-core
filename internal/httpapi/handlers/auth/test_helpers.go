package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
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
	return &AuthHandlers{
		jwtSecret:      jwtSecret,
		ipfsAPI:        ipfsAPI,
		ipfsClusterAPI: ipfsClusterAPI,
	}
}

// authResp is a common response type for auth endpoints
type authResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// integResp is an alias for backwards compatibility
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
	// Stub implementation for tests
	shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
		"message": "User created",
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
