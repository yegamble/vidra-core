package moderation

import (
	"context"
	"net/http"

	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// InstanceHandlersStub is a stub for instance handlers in tests
type InstanceHandlersStub struct{}

// GetInstanceConfig is a stub handler
func (h *InstanceHandlersStub) GetInstanceConfig(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"key": "test", "value": "test"})
}

// UpdateInstanceConfig is a stub handler
func (h *InstanceHandlersStub) UpdateInstanceConfig(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true})
}

// ListInstanceConfigs is a stub handler
func (h *InstanceHandlersStub) ListInstanceConfigs(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, []map[string]interface{}{{"key": "test", "value": "test"}})
}

// GetInstanceAbout is a stub handler
func (h *InstanceHandlersStub) GetInstanceAbout(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"name": "test"})
}

// OEmbed is a stub handler
func (h *InstanceHandlersStub) OEmbed(w http.ResponseWriter, r *http.Request) {
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"type": "video"})
}

// NewInstanceHandlers creates instance handlers for tests (stub)
func NewInstanceHandlers(deps ...interface{}) *InstanceHandlersStub {
	// This is a stub for test compatibility
	return &InstanceHandlersStub{}
}

// withUserID adds a user ID to the context (test helper)
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}
