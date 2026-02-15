package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSetupServer_HealthEndpoint(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["status"] != "setup_required" {
		t.Errorf("Expected status 'setup_required', got '%s'", response["status"])
	}
}

func TestSetupServer_NotFoundReturnsSetupRequired(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("Expected status 503, got %d", w.Code)
	}

	var response map[string]string
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["error"] != "setup_required" {
		t.Errorf("Expected error 'setup_required', got '%s'", response["error"])
	}

	if response["message"] == "" {
		t.Error("Expected non-empty message")
	}
}

func TestNewServer_DefaultPort(t *testing.T) {
	server := NewServer("")
	if server.Port != "8080" {
		t.Errorf("Expected default port 8080, got %s", server.Port)
	}
}

func TestNewServer_CustomPort(t *testing.T) {
	server := NewServer("9090")
	if server.Port != "9090" {
		t.Errorf("Expected port 9090, got %s", server.Port)
	}
}
