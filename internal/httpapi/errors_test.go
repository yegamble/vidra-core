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

// TestHandleReservedRendersOneConflictForBothKinds pins SC1's HTTP shape: 409
// with the stable code `handle_reserved`, and — the part that is a decision
// rather than a detail — a message that does not say which kind of actor holds
// the name.
//
// Saying so would make each form an oracle for the other namespace: an anonymous
// registration form that reports "that name belongs to a channel" enumerates
// channels, and a channel-creation form that reports "that belongs to an
// account" enumerates accounts. Neither caller can act on the distinction
// anyway — the remedy is the same word.
func TestHandleReservedRendersOneConflictForBothKinds(t *testing.T) {
	srv := New(testConfig(), nil, nil)
	e := srv.Handler()
	srv.echo.GET("/test-account-side", func(echo.Context) error { return &HandleReservedError{} })
	srv.echo.GET("/test-channel-side", func(echo.Context) error { return &HandleReservedError{} })

	var bodies []ErrorResponse
	for _, path := range []string{"/test-account-side", "/test-channel-side"} {
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s status = %d, want 409; body=%s", path, rec.Code, rec.Body.String())
		}
		var body ErrorResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s unmarshal: %v", path, err)
		}
		if body.Error.Code != "handle_reserved" {
			t.Errorf("%s code = %q, want handle_reserved", path, body.Error.Code)
		}
		for _, leak := range []string{"channel", "account", "username"} {
			if strings.Contains(strings.ToLower(body.Error.Message), leak+" named") {
				t.Errorf("%s message names the holding kind: %q", path, body.Error.Message)
			}
		}
		bodies = append(bodies, body)
	}
	if bodies[0].Error.Message != bodies[1].Error.Message {
		t.Errorf("the two directions must answer identically:\n  %q\n  %q",
			bodies[0].Error.Message, bodies[1].Error.Message)
	}
}
