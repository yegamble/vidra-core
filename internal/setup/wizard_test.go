package setup

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWizardHandlerWelcome(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET welcome page returns 200",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectedBody:   "Welcome to Athena Setup",
		},
		{
			name:           "POST not allowed on welcome",
			method:         http.MethodPost,
			expectedStatus: http.StatusMethodNotAllowed,
			expectedBody:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			req := httptest.NewRequest(tt.method, "/setup/welcome", nil)
			w := httptest.NewRecorder()

			wizard.HandleWelcome(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}

func TestWizardHandlerDatabase(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
	}{
		{
			name:           "GET database page returns 200",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			req := httptest.NewRequest(tt.method, "/setup/database", nil)
			w := httptest.NewRecorder()

			wizard.HandleDatabase(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			assert.Contains(t, w.Body.String(), "Database")
		})
	}
}

func TestWizardHandlerServices(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/services", nil)
	w := httptest.NewRecorder()

	wizard.HandleServices(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Services")
}

func TestWizardHandlerNetworking(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/networking", nil)
	w := httptest.NewRecorder()

	wizard.HandleNetworking(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Networking")
}

func TestWizardHandlerStorage(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/storage", nil)
	w := httptest.NewRecorder()

	wizard.HandleStorage(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Storage")
}

func TestWizardHandlerSecurity(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/security", nil)
	w := httptest.NewRecorder()

	wizard.HandleSecurity(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Security")
}

func TestWizardHandlerReview(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/review", nil)
	w := httptest.NewRecorder()

	wizard.HandleReview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Review")
}

func TestWizardHandlerComplete(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/complete", nil)
	w := httptest.NewRecorder()

	wizard.HandleComplete(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Setup Complete")
}

func TestGenerateJWTSecret(t *testing.T) {
	tests := []struct {
		name       string
		wantMinLen int
	}{
		{
			name:       "generates secret of at least 32 chars",
			wantMinLen: 32,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := GenerateJWTSecret()
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(secret), tt.wantMinLen)
		})
	}
}

func TestNewWizardNginxDefaults(t *testing.T) {
	wizard := NewWizard()

	assert.Equal(t, true, wizard.config.NginxEnabled, "NginxEnabled should default to true")
	assert.Equal(t, "localhost", wizard.config.NginxDomain, "NginxDomain should default to localhost")
	assert.Equal(t, 80, wizard.config.NginxPort, "NginxPort should default to 80")
	assert.Equal(t, "http", wizard.config.NginxProtocol, "NginxProtocol should default to http")
}
