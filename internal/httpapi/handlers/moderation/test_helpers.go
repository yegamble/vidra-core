package moderation

import (
	"context"

	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// NewInstanceHandlers creates instance handlers for tests (stub)
func NewInstanceHandlers(deps ...interface{}) interface{} {
	// This is a stub for test compatibility
	return nil
}

// withUserID adds a user ID to the context (test helper)
func withUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, middleware.UserIDKey, id)
}
