package channel

import (
	"context"
	"encoding/json"
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

// NewServer creates a test server (stub for compatibility)
func NewServer(deps ...interface{}) interface{} {
	// This is a stub for test compatibility
	// Tests should be refactored to use proper initialization
	return nil
}

// NewNotificationHandlers creates notification handlers for tests
func NewNotificationHandlers(deps ...interface{}) interface{} {
	// This is a stub for test compatibility
	return nil
}

// integResp is a wrapper for API responses
type integResp struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
}

// authResp is a common response type for auth endpoints
type authResp struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

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
