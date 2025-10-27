package social

import (
	"athena/internal/httpapi/shared"
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
