package setup

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWizardFullFlowDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	wizard := NewWizard()

	form := url.Values{}
	form.Set("POSTGRES_MODE", "docker")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/services", w.Header().Get("Location"))

	form = url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IPFS", "false")
	req = httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleServices(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/email", w.Header().Get("Location"))

	form = url.Values{}
	form.Set("SMTP_MODE", "docker")
	form.Set("SMTP_FROM_ADDRESS", "noreply@localhost")
	form.Set("SMTP_FROM_NAME", "Athena")
	req = httptest.NewRequest(http.MethodPost, "/setup/email", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleEmail(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/networking", w.Header().Get("Location"))

	form = url.Values{}
	form.Set("NGINX_DOMAIN", "localhost")
	form.Set("NGINX_PORT", "80")
	form.Set("NGINX_PROTOCOL", "http")
	req = httptest.NewRequest(http.MethodPost, "/setup/networking", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleNetworking(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/storage", w.Header().Get("Location"))

	form = url.Values{}
	form.Set("STORAGE_PATH", "./storage")
	form.Set("BACKUP_ENABLED", "false")
	req = httptest.NewRequest(http.MethodPost, "/setup/storage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleStorage(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/security", w.Header().Get("Location"))

	form = url.Values{}
	form.Set("ADMIN_USERNAME", "admin")
	form.Set("ADMIN_EMAIL", "admin@example.com")
	form.Set("ADMIN_PASSWORD", "securepassword123")
	req = httptest.NewRequest(http.MethodPost, "/setup/security", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleSecurity(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/review", w.Header().Get("Location"))

	wizard.config.DatabaseURL = "postgres://test:test@localhost/test" // Mock DB URL

	origEnvPath := envPath
	err := WriteEnvFile(origEnvPath, wizard.config)
	require.NoError(t, err)

	envData, err := os.ReadFile(origEnvPath)
	require.NoError(t, err)
	envContent := string(envData)

	assert.Contains(t, envContent, "SETUP_COMPLETED=true")
	assert.Contains(t, envContent, "POSTGRES_MODE=docker")
	assert.Contains(t, envContent, "REDIS_MODE=docker")
	assert.Contains(t, envContent, "ENABLE_IPFS=false")
}

func TestWizardFullFlowExternal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	wizard := NewWizard()

	form := url.Values{}
	form.Set("POSTGRES_MODE", "external")
	form.Set("DATABASE_URL", "postgres://user:pass@external.host:5432/athena?sslmode=disable")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)

	form = url.Values{}
	form.Set("REDIS_MODE", "external")
	form.Set("REDIS_URL", "redis://external.host:6379/0")
	form.Set("ENABLE_IPFS", "false")
	req = httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleServices(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.Equal(t, "external", wizard.config.PostgresMode)
	assert.Equal(t, "postgres://user:pass@external.host:5432/athena?sslmode=disable", wizard.config.DatabaseURL)
	assert.Equal(t, "external", wizard.config.RedisMode)
	assert.Equal(t, "redis://external.host:6379/0", wizard.config.RedisURL)
	wizard.mu.Unlock()

	form = url.Values{}
	form.Set("SMTP_MODE", "external")
	form.Set("SMTP_HOST", "smtp.example.com")
	form.Set("SMTP_PORT", "587")
	form.Set("SMTP_USERNAME", "noreply")
	form.Set("SMTP_PASSWORD", "secret")
	form.Set("SMTP_FROM_ADDRESS", "noreply@example.com")
	form.Set("SMTP_FROM_NAME", "Athena Videos")
	req = httptest.NewRequest(http.MethodPost, "/setup/email", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleEmail(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)

	form = url.Values{}
	form.Set("NGINX_DOMAIN", "videos.example.com")
	form.Set("NGINX_PORT", "443")
	form.Set("NGINX_PROTOCOL", "https")
	form.Set("NGINX_TLS_MODE", "letsencrypt")
	form.Set("NGINX_LETSENCRYPT_EMAIL", "admin@example.com")
	req = httptest.NewRequest(http.MethodPost, "/setup/networking", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleNetworking(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/storage", w.Header().Get("Location"))

	wizard.mu.Lock()
	assert.Equal(t, "videos.example.com", wizard.config.NginxDomain)
	assert.Equal(t, 443, wizard.config.NginxPort)
	assert.Equal(t, "https", wizard.config.NginxProtocol)
	assert.Equal(t, "letsencrypt", wizard.config.NginxTLSMode)
	assert.Equal(t, "admin@example.com", wizard.config.NginxEmail)
	wizard.mu.Unlock()

	form = url.Values{}
	form.Set("STORAGE_PATH", "./storage")
	req = httptest.NewRequest(http.MethodPost, "/setup/storage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleStorage(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)

	form = url.Values{}
	form.Set("ADMIN_USERNAME", "admin")
	form.Set("ADMIN_EMAIL", "admin@example.com")
	form.Set("ADMIN_PASSWORD", "securepassword123")
	req = httptest.NewRequest(http.MethodPost, "/setup/security", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleSecurity(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)

	err := WriteEnvFile(envPath, wizard.config)
	require.NoError(t, err)

	envData, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envContent := string(envData)

	assert.Contains(t, envContent, "POSTGRES_MODE=external")
	assert.Contains(t, envContent, "DATABASE_URL=postgres://user:pass@external.host:5432/athena")
	assert.Contains(t, envContent, "REDIS_MODE=external")
	assert.Contains(t, envContent, "REDIS_URL=redis://external.host:6379/0")
}

func TestWizardInvalidDatabaseURL(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("POSTGRES_MODE", "external")
	form.Set("DATABASE_URL", "mysql://user:pass@localhost:3306/athena")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid database URL")
}

func TestWizardInvalidJWTSecret(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("JWT_SECRET_CUSTOM", "short")
	form.Set("ADMIN_USERNAME", "admin")
	form.Set("ADMIN_EMAIL", "admin@example.com")
	req := httptest.NewRequest(http.MethodPost, "/setup/security", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleSecurity(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Invalid JWT secret")
}

func TestWizardMissingAdminPassword(t *testing.T) {
	wizard := NewWizard()
	wizard.config.DatabaseURL = "postgres://test:test@localhost/test"

	form := url.Values{}
	req := httptest.NewRequest(http.MethodPost, "/setup/review", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleReview(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "Admin password is required")
}

func TestWizardEnvFileGeneration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	wizard := NewWizard()

	wizard.config.PostgresMode = "docker"
	wizard.config.RedisMode = "docker"
	wizard.config.EnableIPFS = false
	wizard.config.StoragePath = "./storage"
	wizard.config.JWTSecret = "test-secret-at-least-32-characters-long"
	wizard.config.AdminUsername = "admin"
	wizard.config.AdminEmail = "admin@example.com"

	err := WriteEnvFile(envPath, wizard.config)
	require.NoError(t, err)

	envData, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envContent := string(envData)

	assert.Contains(t, envContent, "SETUP_COMPLETED=true")
	assert.Contains(t, envContent, "JWT_SECRET=test-secret-at-least-32-characters-long")
	assert.Contains(t, envContent, "POSTGRES_MODE=docker")
	assert.Contains(t, envContent, "REDIS_MODE=docker")
	assert.Contains(t, envContent, "ENABLE_IPFS=false")
}

func TestWizardCustomJWTSecret(t *testing.T) {
	wizard := NewWizard()

	customSecret := "my-custom-jwt-key-with-at-least-32-characters-long"

	form := url.Values{}
	form.Set("JWT_SECRET_CUSTOM", customSecret)
	form.Set("ADMIN_USERNAME", "admin")
	form.Set("ADMIN_EMAIL", "admin@example.com")
	req := httptest.NewRequest(http.MethodPost, "/setup/security", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleSecurity(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.Equal(t, customSecret, wizard.config.JWTSecret)
	wizard.mu.Unlock()
}

func TestWizardIPFSEnabled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IPFS", "true")
	form.Set("IPFS_MODE", "external")
	form.Set("IPFS_API_URL", "http://external.ipfs.host:5001")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.True(t, wizard.config.EnableIPFS)
	assert.Equal(t, "external", wizard.config.IPFSMode)
	assert.Equal(t, "http://external.ipfs.host:5001", wizard.config.IPFSAPIUrl)
	wizard.mu.Unlock()

	wizard.config.PostgresMode = "docker"
	wizard.config.DatabaseURL = "postgres://test:test@localhost/test"
	wizard.config.JWTSecret = "test-secret"

	err := WriteEnvFile(envPath, wizard.config)
	require.NoError(t, err)

	envData, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envContent := string(envData)

	assert.Contains(t, envContent, "ENABLE_IPFS=true")
	assert.Contains(t, envContent, "IPFS_MODE=external")
	assert.Contains(t, envContent, "IPFS_API_URL=http://external.ipfs.host:5001")
}

func TestWizardOptionalServicesDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	envPath := filepath.Join(tempDir, ".env")

	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IPFS", "false")
	form.Set("ENABLE_CLAMAV", "false")
	form.Set("ENABLE_WHISPER", "false")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.config.PostgresMode = "docker"
	wizard.config.DatabaseURL = "postgres://test:test@localhost/test"
	wizard.config.JWTSecret = "test-secret"

	err := WriteEnvFile(envPath, wizard.config)
	require.NoError(t, err)

	envData, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envContent := string(envData)

	assert.Contains(t, envContent, "ENABLE_IPFS=false")
	assert.Contains(t, envContent, "ENABLE_CLAMAV=false")
	assert.Contains(t, envContent, "ENABLE_WHISPER=false")
}

func TestWizardStateIsolation(t *testing.T) {
	wizard1 := NewWizard()
	wizard2 := NewWizard()

	form := url.Values{}
	form.Set("POSTGRES_MODE", "external")
	form.Set("DATABASE_URL", "postgres://user1:pass@host1/db1?sslmode=disable")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard1.HandleDatabase(w, req)

	form = url.Values{}
	form.Set("POSTGRES_MODE", "docker")
	req = httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard2.HandleDatabase(w, req)

	wizard1.mu.Lock()
	wizard1Config := wizard1.config.PostgresMode
	wizard1.mu.Unlock()

	wizard2.mu.Lock()
	wizard2Config := wizard2.config.PostgresMode
	wizard2.mu.Unlock()

	assert.Equal(t, "external", wizard1Config)
	assert.Equal(t, "docker", wizard2Config)
}

func TestWizardIOTADockerMode(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IOTA", "true")
	form.Set("IOTA_MODE", "docker")
	form.Set("IOTA_NETWORK", "testnet")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.True(t, wizard.config.EnableIOTA)
	assert.Equal(t, "docker", wizard.config.IOTAMode)
	assert.Equal(t, "testnet", wizard.config.IOTANetwork)
	wizard.mu.Unlock()
}

func TestWizardIOTAExternalMode(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IOTA", "true")
	form.Set("IOTA_MODE", "external")
	form.Set("IOTA_NODE_URL", "http://iota-node.example.com:14265")
	form.Set("IOTA_NETWORK", "mainnet")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.True(t, wizard.config.EnableIOTA)
	assert.Equal(t, "external", wizard.config.IOTAMode)
	assert.Equal(t, "http://iota-node.example.com:14265", wizard.config.IOTANodeURL)
	assert.Equal(t, "mainnet", wizard.config.IOTANetwork)
	wizard.mu.Unlock()
}

func TestWizardIOTAExternalModeInvalidURL(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IOTA", "true")
	form.Set("IOTA_MODE", "external")
	form.Set("IOTA_NODE_URL", "not-a-url")
	form.Set("IOTA_NETWORK", "testnet")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWizardIOTAInvalidNetwork(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IOTA", "true")
	form.Set("IOTA_MODE", "docker")
	form.Set("IOTA_NETWORK", "devnet")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWizardIOTADisabled(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("REDIS_MODE", "docker")
	req := httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.False(t, wizard.config.EnableIOTA)
	wizard.mu.Unlock()
}

func TestWizardPageRendersOwnContent(t *testing.T) {
	wizard := NewWizard()

	tests := []struct {
		name        string
		url         string
		handler     http.HandlerFunc
		expectedH2  string
		notExpected string
	}{
		{
			name:        "welcome page",
			url:         "/setup/welcome",
			handler:     wizard.HandleWelcome,
			expectedH2:  "Welcome to Athena Setup",
			notExpected: "Database Configuration",
		},
		{
			name:        "database page",
			url:         "/setup/database",
			handler:     wizard.HandleDatabase,
			expectedH2:  "Database Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "services page",
			url:         "/setup/services",
			handler:     wizard.HandleServices,
			expectedH2:  "Services Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "email page",
			url:         "/setup/email",
			handler:     wizard.HandleEmail,
			expectedH2:  "Email Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "networking page",
			url:         "/setup/networking",
			handler:     wizard.HandleNetworking,
			expectedH2:  "Networking Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "storage page",
			url:         "/setup/storage",
			handler:     wizard.HandleStorage,
			expectedH2:  "Storage Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "security page",
			url:         "/setup/security",
			handler:     wizard.HandleSecurity,
			expectedH2:  "Security Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "review page",
			url:         "/setup/review",
			handler:     wizard.HandleReview,
			expectedH2:  "Review Configuration",
			notExpected: "Welcome to Athena Setup",
		},
		{
			name:        "complete page",
			url:         "/setup/complete",
			handler:     wizard.HandleComplete,
			expectedH2:  "Setup Complete",
			notExpected: "Welcome to Athena Setup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()

			tt.handler(w, req)

			assert.Equal(t, http.StatusOK, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedH2, "page should render its own heading")
			assert.NotContains(t, w.Body.String(), tt.notExpected, "page should not render other page content")
		})
	}
}

func TestWizardFullNavigationFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	wizard := NewWizard()

	// Database POST → redirects to services
	form := url.Values{}
	form.Set("POSTGRES_MODE", "docker")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/services", w.Header().Get("Location"))

	// Services POST → redirects to email
	form = url.Values{}
	form.Set("REDIS_MODE", "docker")
	form.Set("ENABLE_IPFS", "false")
	form.Set("ENABLE_CLAMAV", "false")
	form.Set("ENABLE_WHISPER", "false")
	req = httptest.NewRequest(http.MethodPost, "/setup/services", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleServices(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/email", w.Header().Get("Location"))

	// Email POST → redirects to networking
	form = url.Values{}
	form.Set("SMTP_MODE", "docker")
	form.Set("SMTP_FROM_ADDRESS", "noreply@localhost")
	form.Set("SMTP_FROM_NAME", "Athena")
	req = httptest.NewRequest(http.MethodPost, "/setup/email", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleEmail(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/networking", w.Header().Get("Location"))

	// Networking POST → redirects to storage
	form = url.Values{}
	form.Set("NGINX_DOMAIN", "localhost")
	form.Set("NGINX_PORT", "80")
	form.Set("NGINX_PROTOCOL", "http")
	req = httptest.NewRequest(http.MethodPost, "/setup/networking", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleNetworking(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/storage", w.Header().Get("Location"))

	// Storage POST → redirects to security
	form = url.Values{}
	form.Set("STORAGE_PATH", "./storage")
	form.Set("BACKUP_ENABLED", "false")
	req = httptest.NewRequest(http.MethodPost, "/setup/storage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleStorage(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/security", w.Header().Get("Location"))

	// Security POST → redirects to review
	form = url.Values{}
	form.Set("ADMIN_USERNAME", "admin")
	form.Set("ADMIN_EMAIL", "admin@example.com")
	form.Set("ADMIN_PASSWORD", "securepassword123")
	req = httptest.NewRequest(http.MethodPost, "/setup/security", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w = httptest.NewRecorder()
	wizard.HandleSecurity(w, req)
	require.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/review", w.Header().Get("Location"))

	// Verify admin password was saved to config
	wizard.mu.Lock()
	assert.Equal(t, "securepassword123", wizard.config.AdminPassword)
	wizard.mu.Unlock()

	// Review GET → renders review page
	req = httptest.NewRequest(http.MethodGet, "/setup/review", nil)
	w = httptest.NewRecorder()
	wizard.HandleReview(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Review Configuration")
}
