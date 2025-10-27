package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/httpapi/handlers/messaging"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	ucn "athena/internal/usecase/notification"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// AuthServerStub is a stub auth server for tests
type AuthServerStub struct{}

// Login is a stub login handler
func (s *AuthServerStub) Login(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"access_token":  "test-token",
		"refresh_token": "test-refresh",
	})
}

// NewServer creates a test server (stub for compatibility)
func NewServer(deps ...interface{}) *AuthServerStub {
	// This is a stub for test compatibility
	// Tests should be refactored to use proper initialization
	return &AuthServerStub{}
}

// NewNotificationHandlers creates notification handlers for tests
func NewNotificationHandlers(notificationService ucn.Service) *messaging.NotificationHandlers {
	return messaging.NewNotificationHandlers(notificationService)
}

// integResp is a wrapper for API responses
//
//nolint:unused // used in test files
type integResp struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

// authResp is a common response type for auth endpoints
//
//nolint:unused // used in test files
type authResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

// testResponse is the standard response structure for tests
//
//nolint:unused // used in test files
type testResponse struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

// withUserID adds a user ID to the context (test helper)
//
//nolint:unused // used in test files
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}

// decodeResponse decodes a response for tests
//
//nolint:unused // used in test files
func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder) testResponse {
	t.Helper()
	var resp testResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}
