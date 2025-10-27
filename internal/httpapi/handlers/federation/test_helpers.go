package federation

import (
	"athena/internal/httpapi/shared"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// stringPtr returns a pointer to a string
//
//nolint:unused // used in test files
func stringPtr(s string) *string {
	return &s
}
