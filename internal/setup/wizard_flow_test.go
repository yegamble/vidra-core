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
	form.Set("POSTGRES_HOST", "external.host")
	form.Set("POSTGRES_PORT", "5432")
	form.Set("POSTGRES_USER", "user")
	form.Set("POSTGRES_PASSWORD", "pass")
	form.Set("POSTGRES_DB", "athena")
	form.Set("POSTGRES_SSLMODE", "disable")
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
	assert.Equal(t, "external.host", wizard.config.PostgresHost)
	assert.Equal(t, 5432, wizard.config.PostgresPort)
	assert.Equal(t, "user", wizard.config.PostgresUser)
	assert.Equal(t, "pass", wizard.config.PostgresPassword)
	assert.Equal(t, "athena", wizard.config.PostgresDB)
	assert.Equal(t, "disable", wizard.config.PostgresSSLMode)
	// DatabaseURL is constructed from individual fields
	assert.Contains(t, wizard.config.DatabaseURL, "postgres://user:pass@external.host:5432/athena")
	assert.Contains(t, wizard.config.DatabaseURL, "sslmode=disable")
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
	assert.Contains(t, envContent, "POSTGRES_HOST=external.host")
	assert.Contains(t, envContent, "POSTGRES_PORT=5432")
	assert.Contains(t, envContent, "POSTGRES_USER=user")
	assert.Contains(t, envContent, "POSTGRES_PASSWORD=pass")
	assert.Contains(t, envContent, "POSTGRES_DB=athena")
	assert.Contains(t, envContent, "POSTGRES_SSLMODE=disable")
	assert.Contains(t, envContent, "REDIS_MODE=external")
	assert.Contains(t, envContent, "REDIS_URL=redis://external.host:6379/0")
}

func TestWizardInvalidDatabaseURL(t *testing.T) {
	wizard := NewWizard()

	// Test with empty POSTGRES_HOST (required field validation)
	form := url.Values{}
	form.Set("POSTGRES_MODE", "external")
	form.Set("POSTGRES_HOST", "") // Empty host should fail
	form.Set("POSTGRES_PORT", "5432")
	form.Set("POSTGRES_USER", "user")
	form.Set("POSTGRES_PASSWORD", "pass")
	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "PostgreSQL host is required")
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
	form.Set("POSTGRES_HOST", "host1")
	form.Set("POSTGRES_PORT", "5432")
	form.Set("POSTGRES_USER", "user1")
	form.Set("POSTGRES_PASSWORD", "pass")
	form.Set("POSTGRES_DB", "db1")
	form.Set("POSTGRES_SSLMODE", "disable")
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

// TestProcessDatabaseFormIndividualFields tests processDatabaseForm with individual PostgreSQL fields
func TestProcessDatabaseFormIndividualFields(t *testing.T) {
	wizard := NewWizard()

	form := url.Values{}
	form.Set("POSTGRES_MODE", "external")
	form.Set("POSTGRES_HOST", "testhost")
	form.Set("POSTGRES_PORT", "5433")
	form.Set("POSTGRES_USER", "testuser")
	form.Set("POSTGRES_PASSWORD", "testpass123")
	form.Set("POSTGRES_DB", "testdb")
	form.Set("POSTGRES_SSLMODE", "require")

	req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)

	require.Equal(t, http.StatusSeeOther, w.Code)

	wizard.mu.Lock()
	assert.Equal(t, "external", wizard.config.PostgresMode)
	assert.Equal(t, "testhost", wizard.config.PostgresHost)
	assert.Equal(t, 5433, wizard.config.PostgresPort)
	assert.Equal(t, "testuser", wizard.config.PostgresUser)
	assert.Equal(t, "testpass123", wizard.config.PostgresPassword)
	assert.Equal(t, "testdb", wizard.config.PostgresDB)
	assert.Equal(t, "require", wizard.config.PostgresSSLMode)
	// Verify DatabaseURL was constructed correctly
	assert.Contains(t, wizard.config.DatabaseURL, "postgres://testuser:testpass123@testhost:5433/testdb")
	assert.Contains(t, wizard.config.DatabaseURL, "sslmode=require")
	wizard.mu.Unlock()
}

// TestDatabaseHTMLHasIndividualFields verifies database.html template contains individual PostgreSQL input fields
func TestDatabaseHTMLHasIndividualFields(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodGet, "/setup/database", nil)
	w := httptest.NewRecorder()
	wizard.HandleDatabase(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Check for individual PostgreSQL field inputs
	assert.Contains(t, body, "POSTGRES_HOST", "database.html should have POSTGRES_HOST input")
	assert.Contains(t, body, "POSTGRES_PORT", "database.html should have POSTGRES_PORT input")
	assert.Contains(t, body, "POSTGRES_USER", "database.html should have POSTGRES_USER input")
	assert.Contains(t, body, "POSTGRES_PASSWORD", "database.html should have POSTGRES_PASSWORD input")
	assert.Contains(t, body, "POSTGRES_DB", "database.html should have POSTGRES_DB input")
	assert.Contains(t, body, "POSTGRES_SSLMODE", "database.html should have POSTGRES_SSLMODE select")

	// Verify testConnection() JavaScript function exists
	assert.Contains(t, body, "function testConnection()", "database.html should have testConnection() JavaScript function")
	assert.Contains(t, body, "/setup/test-database", "database.html should POST to /setup/test-database endpoint")
}

// TestReviewPageMasksPassword verifies review.html template masks the PostgreSQL password
func TestReviewPageMasksPassword(t *testing.T) {
	wizard := NewWizard()

	// Set up external PostgreSQL config
	wizard.config.PostgresMode = "external"
	wizard.config.PostgresHost = "external.host"
	wizard.config.PostgresPort = 5432
	wizard.config.PostgresUser = "myuser"
	wizard.config.PostgresPassword = "secretpassword123"
	wizard.config.PostgresDB = "mydb"
	wizard.config.PostgresSSLMode = "require"
	wizard.config.DatabaseURL = "postgres://myuser:secretpassword123@external.host:5432/mydb?sslmode=require"

	req := httptest.NewRequest(http.MethodGet, "/setup/review", nil)
	w := httptest.NewRecorder()
	wizard.HandleReview(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Verify individual fields are shown (except password)
	assert.Contains(t, body, "external.host", "review page should show PostgreSQL host")
	assert.Contains(t, body, "5432", "review page should show PostgreSQL port")
	assert.Contains(t, body, "myuser", "review page should show PostgreSQL user")
	assert.Contains(t, body, "mydb", "review page should show PostgreSQL database")
	assert.Contains(t, body, "require", "review page should show PostgreSQL SSL mode")

	// Verify password is masked (should show bullet points, not the actual password)
	assert.Contains(t, body, "••••", "review page should mask password with bullet points")
	assert.NotContains(t, body, "secretpassword123", "review page should NOT show the actual password in plain text")
}

// TestProcessDatabaseFormPasswordEncoding verifies special characters in password are properly URL-encoded
func TestProcessDatabaseFormPasswordEncoding(t *testing.T) {
	tests := []struct {
		name     string
		password string
	}{
		{name: "at sign", password: "p@ssword"},
		{name: "colon", password: "p:ssword"},
		{name: "slash", password: "p/ssword"},
		{name: "hash", password: "p#ssword"},
		{name: "space", password: "p ssword"},
		{name: "complex", password: "p@ss/w#rd 123:!"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()

			form := url.Values{}
			form.Set("POSTGRES_MODE", "external")
			form.Set("POSTGRES_HOST", "testhost")
			form.Set("POSTGRES_PORT", "5432")
			form.Set("POSTGRES_USER", "testuser")
			form.Set("POSTGRES_PASSWORD", tt.password)
			form.Set("POSTGRES_DB", "testdb")
			form.Set("POSTGRES_SSLMODE", "disable")

			req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			wizard.HandleDatabase(w, req)

			require.Equal(t, http.StatusSeeOther, w.Code)

			wizard.mu.Lock()
			// Parse the constructed URL and verify the password round-trips correctly
			parsed, err := url.Parse(wizard.config.DatabaseURL)
			require.NoError(t, err, "DatabaseURL should be a valid URL")
			pw, ok := parsed.User.Password()
			require.True(t, ok, "URL should have a password")
			assert.Equal(t, tt.password, pw, "password should round-trip through URL encoding")
			wizard.mu.Unlock()
		})
	}
}

// TestServicesPageTestConnectionButtons verifies services.html renders test connection buttons
func TestServicesPageTestConnectionButtons(t *testing.T) {
	wizard := NewWizard()

	req := httptest.NewRequest(http.MethodGet, "/setup/services", nil)
	w := httptest.NewRecorder()
	wizard.HandleServices(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	body := w.Body.String()

	// Verify test connection JavaScript functions exist
	assert.Contains(t, body, "testRedis()", "services.html should have testRedis() function")
	assert.Contains(t, body, "testIPFS()", "services.html should have testIPFS() function")
	assert.Contains(t, body, "testIOTA()", "services.html should have testIOTA() function")

	// Verify test connection endpoints are referenced
	assert.Contains(t, body, "/setup/test-redis", "services.html should reference /setup/test-redis endpoint")
	assert.Contains(t, body, "/setup/test-ipfs", "services.html should reference /setup/test-ipfs endpoint")
	assert.Contains(t, body, "/setup/test-iota", "services.html should reference /setup/test-iota endpoint")
}

// TestDatabaseFormShellMetacharValidation verifies shell metacharacters are rejected
func TestDatabaseFormShellMetacharValidation(t *testing.T) {
	tests := []struct {
		name  string
		field string
		value string
	}{
		{name: "host with semicolon", field: "POSTGRES_HOST", value: "host;rm -rf /"},
		{name: "host with pipe", field: "POSTGRES_HOST", value: "host|cat /etc/passwd"},
		{name: "user with dollar", field: "POSTGRES_USER", value: "user$HOME"},
		{name: "user with backtick", field: "POSTGRES_USER", value: "user`whoami`"},
		{name: "db with ampersand", field: "POSTGRES_DB", value: "db&echo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()

			form := url.Values{}
			form.Set("POSTGRES_MODE", "external")
			form.Set("POSTGRES_HOST", "validhost")
			form.Set("POSTGRES_PORT", "5432")
			form.Set("POSTGRES_USER", "validuser")
			form.Set("POSTGRES_PASSWORD", "validpass")
			form.Set("POSTGRES_DB", "validdb")
			form.Set("POSTGRES_SSLMODE", "disable")
			// Override the targeted field with the malicious value
			form.Set(tt.field, tt.value)

			req := httptest.NewRequest(http.MethodPost, "/setup/database", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()
			wizard.HandleDatabase(w, req)

			assert.Equal(t, http.StatusBadRequest, w.Code, "should reject shell metacharacters in %s", tt.field)
		})
	}
}

// TestQuickInstallFormValidation tests processQuickInstallForm validation
func TestQuickInstallFormValidation(t *testing.T) {
	tests := []struct {
		name          string
		formValues    url.Values
		expectedCode  int
		expectedError string
	}{
		{
			name: "empty admin password",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {""},
				"ADMIN_PASSWORD_CONFIRM": {""},
				"NGINX_DOMAIN":           {"localhost"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin password is required",
		},
		{
			name: "short admin password",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"short"},
				"ADMIN_PASSWORD_CONFIRM": {"short"},
				"NGINX_DOMAIN":           {"localhost"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin password must be at least 8 characters",
		},
		{
			name: "mismatched passwords",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password456"},
				"NGINX_DOMAIN":           {"localhost"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Passwords do not match",
		},
		{
			name: "invalid domain",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {"invalid..domain"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Invalid domain",
		},
		{
			name: "empty domain",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {""},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Domain is required",
		},
		{
			name: "missing admin username",
			formValues: url.Values{
				"ADMIN_USERNAME":         {""},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {"localhost"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin username is required",
		},
		{
			name: "missing admin email",
			formValues: url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {""},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {"localhost"},
			},
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin email is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()

			req := httptest.NewRequest(http.MethodPost, "/setup/quickinstall", strings.NewReader(tt.formValues.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			wizard.HandleQuickInstall(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			if tt.expectedError != "" {
				assert.Contains(t, w.Body.String(), tt.expectedError)
			}
		})
	}
}

func TestQuickInstallUsernameValidation(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		expectedCode  int
		expectedError string
	}{
		{
			name:          "shell metacharacters in username",
			username:      "admin;rm -rf",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin username contains invalid characters",
		},
		{
			name:          "pipe in username",
			username:      "admin|cat",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin username contains invalid characters",
		},
		{
			name:          "dollar sign in username",
			username:      "admin$HOME",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin username contains invalid characters",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			formValues := url.Values{
				"ADMIN_USERNAME":         {tt.username},
				"ADMIN_EMAIL":            {"admin@example.com"},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {"localhost"},
			}
			req := httptest.NewRequest(http.MethodPost, "/setup/quickinstall", strings.NewReader(formValues.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			wizard.HandleQuickInstall(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedError)
		})
	}
}

func TestQuickInstallEmailValidation(t *testing.T) {
	tests := []struct {
		name          string
		email         string
		expectedCode  int
		expectedError string
	}{
		{
			name:          "email without at sign",
			email:         "notanemail",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin email must be a valid email address",
		},
		{
			name:          "email with just text",
			email:         "admin",
			expectedCode:  http.StatusBadRequest,
			expectedError: "Admin email must be a valid email address",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wizard := NewWizard()
			formValues := url.Values{
				"ADMIN_USERNAME":         {"admin"},
				"ADMIN_EMAIL":            {tt.email},
				"ADMIN_PASSWORD":         {"password123"},
				"ADMIN_PASSWORD_CONFIRM": {"password123"},
				"NGINX_DOMAIN":           {"localhost"},
			}
			req := httptest.NewRequest(http.MethodPost, "/setup/quickinstall", strings.NewReader(formValues.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			wizard.HandleQuickInstall(w, req)

			assert.Equal(t, tt.expectedCode, w.Code)
			assert.Contains(t, w.Body.String(), tt.expectedError)
		})
	}
}

func TestQuickInstallDockerModeWritesEnv(t *testing.T) {
	wizard := NewWizard()

	tmpDir := t.TempDir()
	wizard.OutputDir = tmpDir
	// Create nginx conf dir required by GenerateNginxConfig
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	formValues := url.Values{
		"ADMIN_USERNAME":         {"admin"},
		"ADMIN_EMAIL":            {"admin@example.com"},
		"ADMIN_PASSWORD":         {"password123"},
		"ADMIN_PASSWORD_CONFIRM": {"password123"},
		"NGINX_DOMAIN":           {"localhost"},
	}
	req := httptest.NewRequest(http.MethodPost, "/setup/quickinstall", strings.NewReader(formValues.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()

	wizard.HandleQuickInstall(w, req)

	// In Docker mode, should redirect successfully (no database needed)
	assert.Equal(t, http.StatusSeeOther, w.Code)
	assert.Equal(t, "/setup/complete", w.Header().Get("Location"))

	// Verify .env file was written with admin credentials and Docker defaults
	envContent, err := os.ReadFile(filepath.Join(tmpDir, ".env"))
	require.NoError(t, err)
	content := string(envContent)
	assert.Contains(t, content, "POSTGRES_MODE=docker")
	assert.Contains(t, content, "REDIS_MODE=docker")
	assert.Contains(t, content, "NGINX_DOMAIN=localhost")
	assert.Contains(t, content, "ADMIN_USERNAME=admin")
	assert.Contains(t, content, "ADMIN_EMAIL=admin@example.com")
	assert.Contains(t, content, "ADMIN_PASSWORD=password123")
	assert.Contains(t, content, "SETUP_COMPLETED=true")
}
