package channel

import (
	"net/http"

	"athena/internal/httpapi/handlers/messaging"
	"athena/internal/httpapi/shared"
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
