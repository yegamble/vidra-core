package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
)

func TestBodyLimitRejectsOversized(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPBodyLimit = "16" // 16 bytes
	srv := New(cfg, nil, nil)
	srv.echo.POST("/sink", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sink", strings.NewReader(strings.Repeat("x", 100)))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "request_entity_too_large" {
		t.Errorf("code = %q, want request_entity_too_large", body.Error.Code)
	}
}

func TestBodyLimitAllowsUnderLimit(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPBodyLimit = "1K"
	srv := New(cfg, nil, nil)
	srv.echo.POST("/sink", func(c echo.Context) error { return c.NoContent(http.StatusOK) })

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/sink", strings.NewReader("small body"))
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

// TestRequestDeadline asserts that a handler honouring its request context sees
// the configured deadline fire and that the resulting timeout is rendered as a
// 503 request_timeout envelope.
func TestRequestDeadline(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPRequestTimeout = 20 * time.Millisecond
	srv := New(cfg, nil, nil)
	srv.echo.GET("/slow", func(c echo.Context) error {
		ctx := c.Request().Context()
		<-ctx.Done()
		return ctx.Err()
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/slow", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "request_timeout" {
		t.Errorf("code = %q, want request_timeout", body.Error.Code)
	}
}

// TestRequestDeadlinePropagates confirms a fast handler is unaffected and the
// deadline is actually present on the request context.
func TestRequestDeadlinePropagates(t *testing.T) {
	cfg := testConfig()
	cfg.HTTPRequestTimeout = 5 * time.Second
	srv := New(cfg, nil, nil)
	srv.echo.GET("/fast", func(c echo.Context) error {
		if _, ok := c.Request().Context().Deadline(); !ok {
			t.Error("expected a deadline on the request context")
		}
		return c.NoContent(http.StatusOK)
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/fast", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
