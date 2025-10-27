package video

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/httpapi/shared"
)

// Response is an alias for shared.Response for tests
type Response = shared.Response

// ErrorInfo is an alias for shared.ErrorInfo for tests
type ErrorInfo = shared.ErrorInfo

// Meta is an alias for shared.Meta for tests
type Meta = shared.Meta

// ipfsAddResponse represents a single line in IPFS add NDJSON output
//
//nolint:unused // used in test files
type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// parseIPFSAddResponse parses the final CID from an ipfs add NDJSON stream.
//
//nolint:unused // used in test files
func parseIPFSAddResponse(r io.Reader) (string, error) {
	var last ipfsAddResponse
	// Use a scanner to read line-delimited JSON objects
	sc := bufio.NewScanner(r)
	// Increase buffer for large JSON lines
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 10*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var cur ipfsAddResponse
		if err := json.Unmarshal([]byte(line), &cur); err != nil {
			return "", err
		}
		if cur.Hash != "" {
			last = cur
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	if last.Hash == "" {
		return "", fmt.Errorf("missing CID in IPFS response")
	}
	return last.Hash, nil
}

// withChiURLParam adds a URL parameter to the request context for testing
//
//nolint:unused // used in test files
func withChiURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}
