package setup

import (
	"net/http"
	"net/http/httptest"
	"strings"
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
			// Verify page renders its own content, not welcome content
			assert.Contains(t, w.Body.String(), "Database Configuration")
			assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
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
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Services Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestWizardHandlerEmail(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/email", nil)
	w := httptest.NewRecorder()

	wizard.HandleEmail(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Email")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Email Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestHandleTestEmail_InvalidJSON(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestEmail_EmptyEmail(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader(`{"email":""}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Valid email address required")
}

func TestHandleTestEmail_InvalidEmail(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader(`{"email":"notanemail"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Valid email address required")
}

func TestHandleTestEmail_RateLimit(t *testing.T) {
	wizard := NewWizard()

	for i := 0; i < 3; i++ {
		req := httptest.NewRequest(http.MethodPost, "/setup/test-email",
			strings.NewReader(`{"email":"test@example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		wizard.HandleTestEmail(w, req)
	}

	req := httptest.NewRequest(http.MethodPost, "/setup/test-email",
		strings.NewReader(`{"email":"test@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.168.1.1:54321"
	w := httptest.NewRecorder()
	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusTooManyRequests, w.Code)
}

func TestWizardHandlerNetworking(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/networking", nil)
	w := httptest.NewRecorder()

	wizard.HandleNetworking(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Networking")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Networking Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestWizardHandlerStorage(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/storage", nil)
	w := httptest.NewRecorder()

	wizard.HandleStorage(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Storage")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Storage Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestWizardHandlerSecurity(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/security", nil)
	w := httptest.NewRecorder()

	wizard.HandleSecurity(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Security")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Security Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestWizardHandlerReview(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/review", nil)
	w := httptest.NewRecorder()

	wizard.HandleReview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Review")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Review Configuration")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
}

func TestWizardHandlerComplete(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/complete", nil)
	w := httptest.NewRecorder()

	wizard.HandleComplete(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Setup Complete")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Your Athena instance is configured and ready to use")
	assert.NotContains(t, w.Body.String(), "Welcome to Athena Setup")
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

func TestWelcomePageHasQuickInstallLink(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/welcome", nil)
	w := httptest.NewRecorder()

	wizard.HandleWelcome(w, req)

	body := w.Body.String()
	assert.Contains(t, body, "/setup/quickinstall", "Welcome page should contain link to /setup/quickinstall")
	assert.Contains(t, body, "Quick Install", "Welcome page should contain 'Quick Install' text")
}

func TestWizardHandlerQuickInstall(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "GET quick install page returns 200",
			method:         http.MethodGet,
			expectedStatus: http.StatusOK,
			expectedBody:   "Quick Install (Docker)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			req := httptest.NewRequest(tt.method, "/setup/quickinstall", nil)
			w := httptest.NewRecorder()

			wizard.HandleQuickInstall(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)
			if tt.expectedBody != "" {
				assert.Contains(t, w.Body.String(), tt.expectedBody)
			}
		})
	}
}
