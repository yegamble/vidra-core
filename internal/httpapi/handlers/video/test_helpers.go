package video

import (
	"io"
	"net/http"

	"athena/internal/httpapi/shared"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// parseIPFSAddResponse parses the IPFS add response (stub for tests)
func parseIPFSAddResponse(body io.Reader) (map[string]interface{}, error) {
	// This is a stub for test compatibility
	return nil, nil
}

// StreamVideoHandler is a stub handler for tests
func StreamVideoHandler(deps ...interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Stub implementation
	}
}

// HLSHandler is a stub handler for tests
func HLSHandler(deps ...interface{}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Stub implementation
	}
}

// StreamVideo is a stub function for tests
func StreamVideo(w http.ResponseWriter, r *http.Request, videoPath string, videoID string) {
	// Stub implementation
}
