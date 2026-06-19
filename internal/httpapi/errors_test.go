package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

func TestErrorEnvelopeNotFound(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/does-not-exist", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "not_found" {
		t.Errorf("code = %q, want not_found", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("message is empty")
	}
	if body.Error.RequestID == "" {
		t.Error("request_id is empty; RequestID middleware should populate it")
	}
}

// TestErrorEnvelopeHidesInternalDetail asserts a 5xx never leaks the underlying
// error message to the client.
func TestErrorEnvelopeHidesInternalDetail(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	srv.echo.GET("/boom", func(c echo.Context) error {
		return echo.NewHTTPError(http.StatusInternalServerError, "leaky secret detail")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/boom", nil)
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "leaky secret detail") {
		t.Errorf("response leaked internal detail: %s", rec.Body.String())
	}
	var body ErrorResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.Error.Code != "internal_error" {
		t.Errorf("code = %q, want internal_error", body.Error.Code)
	}
}

func TestCodeForStatus(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:          "bad_request",
		http.StatusUnauthorized:        "unauthorized",
		http.StatusForbidden:           "forbidden",
		http.StatusNotFound:            "not_found",
		http.StatusConflict:            "conflict",
		http.StatusTooManyRequests:     "rate_limited",
		http.StatusInternalServerError: "internal_error",
		http.StatusBadGateway:          "server_error",
		http.StatusTeapot:              "client_error",
	}
	for status, want := range cases {
		if got := codeForStatus(status); got != want {
			t.Errorf("codeForStatus(%d) = %q, want %q", status, got, want)
		}
	}
}
