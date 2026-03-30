package shared

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
)

func TestWriteJSON(t *testing.T) {
	w := httptest.NewRecorder()
	data := map[string]string{"foo": "bar"}
	WriteJSON(w, http.StatusOK, data)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", w.Header().Get("Content-Type"))
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success true, got false")
	}

	respData := resp.Data.(map[string]interface{})
	if respData["foo"] != "bar" {
		t.Errorf("expected data foo=bar, got %v", respData["foo"])
	}
}

func TestWriteError(t *testing.T) {
	w := httptest.NewRecorder()
	err := errors.New("something went wrong")
	WriteError(w, http.StatusInternalServerError, err)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", w.Code)
	}

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Success {
		t.Errorf("expected success false, got true")
	}

	if resp.Error.Message != "something went wrong" {
		t.Errorf("expected error message 'something went wrong', got '%s'", resp.Error.Message)
	}
}

func TestWriteError_DomainError(t *testing.T) {
	w := httptest.NewRecorder()
	domainErr := domain.DomainError{
		Code:    "TEST_ERROR",
		Message: "test message",
		Details: "test details",
	}
	WriteError(w, http.StatusBadRequest, domainErr)

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Error.Code != "TEST_ERROR" {
		t.Errorf("expected error code 'TEST_ERROR', got '%s'", resp.Error.Code)
	}
	if resp.Error.Details != "test details" {
		t.Errorf("expected error details 'test details', got '%s'", resp.Error.Details)
	}
}

func TestWriteJSONWithMeta(t *testing.T) {
	w := httptest.NewRecorder()
	data := []string{"a", "b"}
	meta := &Meta{Total: 10, Limit: 2, Offset: 0}
	WriteJSONWithMeta(w, http.StatusOK, data, meta)

	var resp Response
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if resp.Meta == nil {
		t.Fatal("expected meta to be present")
	}
	if resp.Meta.Total != 10 {
		t.Errorf("expected total 10, got %d", resp.Meta.Total)
	}
}

func TestWriteJSON_EncodingError(t *testing.T) {
	w := httptest.NewRecorder()
	// A channel cannot be marshaled to JSON, triggering an error
	data := make(chan int)

	// This should not crash even if the error is ignored or logged
	WriteJSON(w, http.StatusOK, data)

	// Headers were already sent
	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}
