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
			expectedBody:   "Welcome to Vidra Core Setup",
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
			assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
}

func TestHandleTestEmail_InvalidJSON(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", wizard.csrfToken)
	w := httptest.NewRecorder()

	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleTestEmail_EmptyEmail(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader(`{"email":""}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", wizard.csrfToken)
	w := httptest.NewRecorder()

	wizard.HandleTestEmail(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Valid email address required")
}

func TestHandleTestEmail_InvalidEmail(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodPost, "/setup/test-email", strings.NewReader(`{"email":"notanemail"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", wizard.csrfToken)
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
		req.Header.Set("X-CSRF-Token", wizard.csrfToken)
		req.RemoteAddr = "192.168.1.1:12345"
		w := httptest.NewRecorder()
		wizard.HandleTestEmail(w, req)
	}

	req := httptest.NewRequest(http.MethodPost, "/setup/test-email",
		strings.NewReader(`{"email":"test@example.com"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", wizard.csrfToken)
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
}

func TestWizardHandlerComplete(t *testing.T) {
	wizard := NewWizard()
	req := httptest.NewRequest(http.MethodGet, "/setup/complete", nil)
	w := httptest.NewRecorder()

	wizard.HandleComplete(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Setup Complete")
	// Verify page renders its own content, not welcome content
	assert.Contains(t, w.Body.String(), "Your Vidra Core instance is configured and ready to use")
	assert.NotContains(t, w.Body.String(), "Welcome to Vidra Core Setup")
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

func TestCSRFProtection(t *testing.T) {
	wizard := NewWizard()

	t.Run("POST without CSRF token returns 403", func(t *testing.T) {
		form := strings.NewReader("POSTGRES_MODE=docker")
		req := httptest.NewRequest(http.MethodPost, "/setup/database", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		wizard.HandleDatabase(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "CSRF")
	})

	t.Run("POST with wrong CSRF token returns 403", func(t *testing.T) {
		form := strings.NewReader("POSTGRES_MODE=docker&_csrf_token=wrong-token")
		req := httptest.NewRequest(http.MethodPost, "/setup/database", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		wizard.HandleDatabase(w, req)

		assert.Equal(t, http.StatusForbidden, w.Code)
		assert.Contains(t, w.Body.String(), "CSRF")
	})

	t.Run("POST with valid CSRF token proceeds", func(t *testing.T) {
		form := strings.NewReader("POSTGRES_MODE=docker&_csrf_token=" + wizard.csrfToken)
		req := httptest.NewRequest(http.MethodPost, "/setup/database", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()

		wizard.HandleDatabase(w, req)

		// Should redirect to next step (303), not 403
		assert.Equal(t, http.StatusSeeOther, w.Code)
	})

	t.Run("CSRF token via X-CSRF-Token header works", func(t *testing.T) {
		form := strings.NewReader("POSTGRES_MODE=docker")
		req := httptest.NewRequest(http.MethodPost, "/setup/database", form)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("X-CSRF-Token", wizard.csrfToken)
		w := httptest.NewRecorder()

		wizard.HandleDatabase(w, req)

		assert.Equal(t, http.StatusSeeOther, w.Code)
	})

	t.Run("CSRF token is rendered in GET templates", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/setup/database", nil)
		w := httptest.NewRecorder()

		wizard.HandleDatabase(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Body.String(), wizard.csrfToken)
	})

	t.Run("CSRF protects all form endpoints", func(t *testing.T) {
		endpoints := []struct {
			path    string
			handler func(http.ResponseWriter, *http.Request)
		}{
			{"/setup/services", wizard.HandleServices},
			{"/setup/email", wizard.HandleEmail},
			{"/setup/networking", wizard.HandleNetworking},
			{"/setup/storage", wizard.HandleStorage},
			{"/setup/security", wizard.HandleSecurity},
		}

		for _, ep := range endpoints {
			form := strings.NewReader("_csrf_token=invalid")
			req := httptest.NewRequest(http.MethodPost, ep.path, form)
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			ep.handler(w, req)

			assert.Equal(t, http.StatusForbidden, w.Code, "expected 403 for %s without valid CSRF", ep.path)
		}
	})
}

func TestCSRFTokenGeneration(t *testing.T) {
	w1 := NewWizard()
	w2 := NewWizard()

	require.NotEmpty(t, w1.csrfToken)
	require.NotEmpty(t, w2.csrfToken)
	assert.NotEqual(t, w1.csrfToken, w2.csrfToken, "each wizard instance should get a unique CSRF token")
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
