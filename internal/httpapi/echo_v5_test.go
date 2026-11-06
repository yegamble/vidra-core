package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v5"
)

// Echo v5's router sentinels are not *echo.HTTPError; a 405 must still get the
// typed envelope rather than falling through to a scrubbed 500.
func TestErrorEnvelopeMethodNotAllowed(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodDelete, "/healthz", nil))

	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != codeForStatus(http.StatusMethodNotAllowed) {
		t.Errorf("code = %q, want %q", body.Error.Code, codeForStatus(http.StatusMethodNotAllowed))
	}
}

// The request log line must carry the status the error handler actually sent,
// not the status the returned error implies.
func TestRequestLogRecordsFinalStatus(t *testing.T) {
	var buf bytes.Buffer
	srv := New(testConfig(), nil, nil, WithLogger(slog.New(slog.NewJSONHandler(&buf, nil))))
	srv.echo.GET("/quota", func(*echo.Context) error { return &QuotaExceededError{} })

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/quota", nil))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", rec.Code)
	}
	for _, line := range bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n")) {
		var m map[string]any
		if json.Unmarshal(line, &m) == nil && m["msg"] == "request" {
			if m["status"] != float64(http.StatusUnprocessableEntity) {
				t.Fatalf("logged status = %v, want 422", m["status"])
			}
			return
		}
	}
	t.Fatalf("no request log line; logs:\n%s", buf.String())
}

func TestServerAppliesSocketTimeouts(t *testing.T) {
	cfg := testConfig()
	srv := New(cfg, nil, nil)
	hs := srv.httpSrv
	if hs.Handler != srv.echo {
		t.Fatal("http.Server does not serve the echo router")
	}
	if hs.ReadHeaderTimeout != cfg.HTTPReadTimeout || hs.ReadTimeout != cfg.HTTPReadTimeout ||
		hs.WriteTimeout != cfg.HTTPWriteTimeout || hs.IdleTimeout != idleTimeout {
		t.Fatalf("timeouts = header %v read %v write %v idle %v", hs.ReadHeaderTimeout, hs.ReadTimeout, hs.WriteTimeout, hs.IdleTimeout)
	}
}

// The body-limit 413 is a status-only sentinel in v5; the spool must pass it
// through instead of reporting a generic failure.
func TestStatusErrorFindsEchoSentinels(t *testing.T) {
	body := "--b\r\nContent-Disposition: form-data; name=\"note\"\r\n\r\n" + strings.Repeat("x", 4096) + "\r\n--b--\r\n"
	mr := multipart.NewReader(&limitedReadCloser{r: io.NopCloser(strings.NewReader(body)), limit: 64}, "b")
	if _, err := nextArchivePart(mr); echo.StatusCode(err) != http.StatusRequestEntityTooLarge {
		t.Fatalf("nextArchivePart over the body limit = %v, want the 413", err)
	}
	he := echo.NewHTTPError(http.StatusServiceUnavailable, "disk full")
	if got := spoolReadError(fmt.Errorf("x: %w", he)); !errors.Is(got, he) {
		t.Fatalf("spoolReadError = %v, want the HTTPError itself", got)
	}
	if statusError(errors.New("plain")) != nil {
		t.Fatal("statusError matched an error with no status")
	}
}
