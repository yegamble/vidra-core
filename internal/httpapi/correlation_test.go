package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// getWithHeaders issues a GET to path with optional request headers and returns
// the recorder, so correlation tests can inspect response headers.
func getWithHeaders(srv *Server, path string, headers map[string]string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func TestCorrelationIDEchoesInboundHeader(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := getWithHeaders(srv, "/healthz", map[string]string{correlationHeader: "abc-123_XYZ.9"})
	if got := rec.Header().Get(correlationHeader); got != "abc-123_XYZ.9" {
		t.Errorf("%s = %q, want the echoed inbound value", correlationHeader, got)
	}
}

func TestCorrelationIDMintedWhenAbsent(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := getWithHeaders(srv, "/healthz", nil)
	cid := rec.Header().Get(correlationHeader)
	if cid == "" {
		t.Fatal("a correlation id should be minted when the client sends none")
	}
	// Minted from the server request id, so the two match.
	if rid := rec.Header().Get(echo.HeaderXRequestID); rid != "" && cid != rid {
		t.Errorf("minted correlation id %q should match request id %q", cid, rid)
	}
}

func TestCorrelationIDSanitisesUnsafeInput(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	// CR/LF + spaces + slashes must never survive into the response header.
	rec := getWithHeaders(srv, "/healthz", map[string]string{correlationHeader: "evil\r\nSet-Cookie: x=1 /../a b"})
	got := rec.Header().Get(correlationHeader)
	if strings.ContainsAny(got, "\r\n /") {
		t.Errorf("sanitised id %q still contains unsafe characters", got)
	}
	if got == "" {
		t.Error("sanitising should keep the safe characters, not blank the id")
	}
}

func TestCorrelationIDFallsBackWhenFullyUnsafe(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	// A header of only unsafe chars sanitises to empty, so we mint instead.
	rec := getWithHeaders(srv, "/healthz", map[string]string{correlationHeader: "\r\n\t   "})
	if got := rec.Header().Get(correlationHeader); got == "" {
		t.Error("an all-unsafe inbound id should fall back to a minted id, not empty")
	}
}

func TestCorrelationIDAppearsInRequestLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	srv := New(testConfig(), nil, nil, WithLogger(logger))

	getWithHeaders(srv, "/healthz", map[string]string{correlationHeader: "corr-42"})

	var found bool
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatalf("unmarshal log line %q: %v", line, err)
		}
		if rec["msg"] == "request" && rec["correlation_id"] == "corr-42" {
			found = true
		}
	}
	if !found {
		t.Errorf("request log line should carry correlation_id=corr-42; logs:\n%s", buf.String())
	}
}

func TestRequestLogOmitsAllQueryNamesAndValues(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	srv := New(testConfig(), nil, nil, WithLogger(logger))

	getWithHeaders(srv, "/healthz?code=oauth-code-secret&state=oauth-state-secret&q=private-search", nil)
	logged := buf.String()
	for _, forbidden := range []string{
		"oauth-code-secret", "oauth-state-secret", "private-search", "code=", "state=", "q=",
	} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("request query leaked through %q in logs:\n%s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, `"path":"/healthz"`) {
		t.Fatalf("request log should retain the route template:\n%s", logged)
	}
}
