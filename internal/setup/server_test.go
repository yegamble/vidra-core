package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSetupServer_HealthEndpoint(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "setup_required", response["status"])
}

func TestSetupServer_NotFoundReturnsSetupRequired(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/api/videos", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response map[string]string
	require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
	assert.Equal(t, "setup_required", response["error"])
	assert.NotEmpty(t, response["message"])
}

func TestNewServer_DefaultPort(t *testing.T) {
	server := NewServer("")
	assert.Equal(t, "8080", server.Port)
}

func TestNewServer_CustomPort(t *testing.T) {
	server := NewServer("9090")
	assert.Equal(t, "9090", server.Port)
}

func TestSetupServer_RootRedirectsToWelcome(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/welcome", w.Header().Get("Location"))
}

func TestSetupServer_WizardPagesReturnHTML(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	tests := []struct {
		name            string
		path            string
		expectedContent string
	}{
		{"welcome page", "/setup/welcome", "Welcome to Vidra Core Setup"},
		{"database page", "/setup/database", "Database"},
		{"services page", "/setup/services", "Services"},
		{"storage page", "/setup/storage", "Storage"},
		{"security page", "/setup/security", "Security"},
		{"review page", "/setup/review", "Review"},
		{"complete page", "/setup/complete", "Setup Complete"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.True(t, strings.Contains(w.Body.String(), tt.expectedContent),
				"Expected response to contain %q", tt.expectedContent)
		})
	}
}

func TestSetupServer_APIEndpointsReturn503(t *testing.T) {
	server := NewServer("8080")
	handler := server.Handler()

	tests := []struct {
		name string
		path string
	}{
		{"videos endpoint", "/api/v1/videos"},
		{"users endpoint", "/api/v1/users/me"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			w := httptest.NewRecorder()

			handler.ServeHTTP(w, req)

			assert.Equal(t, http.StatusServiceUnavailable, w.Code)

			var response map[string]string
			require.NoError(t, json.NewDecoder(w.Body).Decode(&response))
			assert.Equal(t, "setup_required", response["error"])
		})
	}
}
