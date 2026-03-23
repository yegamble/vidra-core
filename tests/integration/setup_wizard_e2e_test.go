package integration

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/setup"
)

// newWizardHandler creates a chi router with a given Wizard instance,
// mirroring the setup in internal/setup/server.go.
func newWizardHandler(w *setup.Wizard) http.Handler {
	r := chi.NewRouter()
	r.Get("/setup/welcome", w.HandleWelcome)
	r.Get("/setup/quickinstall", w.HandleQuickInstall)
	r.Post("/setup/quickinstall", w.HandleQuickInstall)
	r.Get("/setup/database", w.HandleDatabase)
	r.Post("/setup/database", w.HandleDatabase)
	r.Get("/setup/services", w.HandleServices)
	r.Post("/setup/services", w.HandleServices)
	r.Get("/setup/email", w.HandleEmail)
	r.Post("/setup/email", w.HandleEmail)
	r.Post("/setup/test-email", w.HandleTestEmail)
	r.Post("/setup/test-database", w.HandleTestDatabase)
	r.Post("/setup/test-redis", w.HandleTestRedis)
	r.Post("/setup/test-ipfs", w.HandleTestIPFS)
	r.Post("/setup/test-iota", w.HandleTestIOTA)
	r.Get("/setup/networking", w.HandleNetworking)
	r.Post("/setup/networking", w.HandleNetworking)
	r.Get("/setup/storage", w.HandleStorage)
	r.Post("/setup/storage", w.HandleStorage)
	r.Get("/setup/security", w.HandleSecurity)
	r.Post("/setup/security", w.HandleSecurity)
	r.Get("/setup/review", w.HandleReview)
	r.Post("/setup/review", w.HandleReview)
	r.Get("/setup/complete", w.HandleComplete)
	return r
}

// noRedirectClient returns an http.Client that doesn't follow redirects.
// This lets us inspect the redirect Location without following it.
func noRedirectClient() *http.Client {
	return &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// doForm submits a url.Values form to the given URL via POST, including the CSRF token.
func doForm(t *testing.T, client *http.Client, rawURL string, form url.Values, csrfToken string) *http.Response {
	t.Helper()
	if csrfToken != "" {
		form.Set("_csrf_token", csrfToken)
	}
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	require.NoError(t, err)
	return resp
}

// TestSetupWizardFullFlow tests the complete wizard flow: welcome → complete.
// Uses Docker mode (PostgresMode=docker) to avoid real DB connections.
// Does NOT require any Docker services — runs entirely in-process via httptest.
func TestSetupWizardFullFlow(t *testing.T) {
	tmpDir := t.TempDir()

	wizard := setup.NewWizard()
	wizard.OutputDir = tmpDir
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	ts := httptest.NewServer(newWizardHandler(wizard))
	defer ts.Close()

	client := noRedirectClient()
	csrf := wizard.GetCSRFToken()

	// Step 1: GET /setup/welcome
	resp, err := client.Get(ts.URL + "/setup/welcome")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "welcome page should return 200")

	// Step 2: GET /setup/database
	resp2, err := client.Get(ts.URL + "/setup/database")
	require.NoError(t, err)
	resp2.Body.Close()
	assert.Equal(t, http.StatusOK, resp2.StatusCode, "database page should return 200")

	// Step 3: POST /setup/database — Docker mode
	dbForm := url.Values{
		"POSTGRES_MODE": {"docker"},
	}
	resp3 := doForm(t, client, ts.URL+"/setup/database", dbForm, csrf)
	resp3.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp3.StatusCode, "database POST should redirect")
	assert.Equal(t, "/setup/services", resp3.Header.Get("Location"), "database POST should redirect to services")

	// Step 4: GET /setup/services
	resp4, err := client.Get(ts.URL + "/setup/services")
	require.NoError(t, err)
	resp4.Body.Close()
	assert.Equal(t, http.StatusOK, resp4.StatusCode, "services page should return 200")

	// Step 5: POST /setup/services — enable IPFS + IOTA in Docker mode
	svcForm := url.Values{
		"REDIS_MODE":   {"docker"},
		"ENABLE_IPFS":  {"true"},
		"IPFS_MODE":    {"docker"},
		"ENABLE_IOTA":  {"true"},
		"IOTA_MODE":    {"docker"},
		"IOTA_NETWORK": {"testnet"},
	}
	resp5 := doForm(t, client, ts.URL+"/setup/services", svcForm, csrf)
	resp5.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp5.StatusCode, "services POST should redirect")
	assert.Equal(t, "/setup/email", resp5.Header.Get("Location"))

	// Step 6: GET /setup/email
	resp6, err := client.Get(ts.URL + "/setup/email")
	require.NoError(t, err)
	resp6.Body.Close()
	assert.Equal(t, http.StatusOK, resp6.StatusCode, "email page should return 200")

	// Step 7: POST /setup/email — Docker mode (Mailpit)
	emailForm := url.Values{
		"SMTP_MODE":         {"docker"},
		"SMTP_FROM_ADDRESS": {"noreply@localhost"},
		"SMTP_FROM_NAME":    {"Athena"},
	}
	resp7 := doForm(t, client, ts.URL+"/setup/email", emailForm, csrf)
	resp7.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp7.StatusCode, "email POST should redirect")
	assert.Equal(t, "/setup/networking", resp7.Header.Get("Location"))

	// Step 8: POST /setup/networking — HTTP, localhost:80
	netForm := url.Values{
		"NGINX_DOMAIN":   {"localhost"},
		"NGINX_PROTOCOL": {"http"},
		"NGINX_PORT":     {"80"},
	}
	resp8 := doForm(t, client, ts.URL+"/setup/networking", netForm, csrf)
	resp8.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp8.StatusCode, "networking POST should redirect")
	assert.Equal(t, "/setup/storage", resp8.Header.Get("Location"))

	// Step 9: POST /setup/storage
	storageForm := url.Values{
		"STORAGE_PATH":      {"./data/storage"},
		"BACKUP_ENABLED":    {"true"},
		"BACKUP_TARGET":     {"local"},
		"BACKUP_SCHEDULE":   {"0 2 * * *"},
		"BACKUP_RETENTION":  {"7"},
		"BACKUP_LOCAL_PATH": {"./backups"},
	}
	resp9 := doForm(t, client, ts.URL+"/setup/storage", storageForm, csrf)
	resp9.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp9.StatusCode, "storage POST should redirect")
	assert.Equal(t, "/setup/security", resp9.Header.Get("Location"))

	// Step 10: POST /setup/security — set admin credentials
	secForm := url.Values{
		"ADMIN_USERNAME": {"admin"},
		"ADMIN_EMAIL":    {"admin@example.com"},
		"ADMIN_PASSWORD": {"S3cur3P@ssw0rd!"},
	}
	resp10 := doForm(t, client, ts.URL+"/setup/security", secForm, csrf)
	resp10.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp10.StatusCode, "security POST should redirect")
	assert.Equal(t, "/setup/review", resp10.Header.Get("Location"))

	// Step 11: GET /setup/review
	resp11, err := client.Get(ts.URL + "/setup/review")
	require.NoError(t, err)
	resp11.Body.Close()
	assert.Equal(t, http.StatusOK, resp11.StatusCode, "review page should return 200")

	// Step 12: POST /setup/review — generates .env and docker-compose.override.yml
	reviewForm := url.Values{
		"POSTGRES_MODE": {"docker"},
	}
	resp12 := doForm(t, client, ts.URL+"/setup/review", reviewForm, csrf)
	resp12.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp12.StatusCode, "review POST should redirect: %s", resp12.Header.Get("Location"))
	assert.Equal(t, "/setup/complete", resp12.Header.Get("Location"))

	// Step 13: GET /setup/complete
	resp13, err := client.Get(ts.URL + "/setup/complete")
	require.NoError(t, err)
	resp13.Body.Close()
	assert.Equal(t, http.StatusOK, resp13.StatusCode, "complete page should return 200")

	// Verify .env was written to OutputDir
	envPath := filepath.Join(tmpDir, ".env")
	_, err = os.Stat(envPath)
	require.NoError(t, err, ".env should exist in OutputDir")

	// Verify docker-compose.override.yml was written
	composePath := filepath.Join(tmpDir, "docker-compose.override.yml")
	_, err = os.Stat(composePath)
	require.NoError(t, err, "docker-compose.override.yml should exist in OutputDir")

	// Verify .env contains expected values
	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envStr := string(envContent)
	assert.Contains(t, envStr, "SETUP_COMPLETED=true", ".env should mark setup complete")
	assert.Contains(t, envStr, "ADMIN_USERNAME=admin", ".env should contain admin username")
	assert.Contains(t, envStr, "ADMIN_EMAIL=admin@example.com", ".env should contain admin email")
}

// TestSetupWizardQuickInstallFlow tests the Quick Install single-form path.
func TestSetupWizardQuickInstallFlow(t *testing.T) {
	tmpDir := t.TempDir()

	wizard := setup.NewWizard()
	wizard.OutputDir = tmpDir
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	ts := httptest.NewServer(newWizardHandler(wizard))
	defer ts.Close()

	client := noRedirectClient()
	csrf := wizard.GetCSRFToken()

	// GET quick install page
	resp, err := client.Get(ts.URL + "/setup/quickinstall")
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode, "quickinstall GET should return 200")

	// POST quick install form
	form := url.Values{
		"ADMIN_USERNAME":         {"admin"},
		"ADMIN_EMAIL":            {"admin@example.com"},
		"ADMIN_PASSWORD":         {"SecurePass123!"},
		"ADMIN_PASSWORD_CONFIRM": {"SecurePass123!"},
		"NGINX_DOMAIN":           {"localhost"},
	}
	resp2 := doForm(t, client, ts.URL+"/setup/quickinstall", form, csrf)
	resp2.Body.Close()
	assert.Equal(t, http.StatusSeeOther, resp2.StatusCode, "quickinstall POST should redirect")
	assert.Equal(t, "/setup/complete", resp2.Header.Get("Location"))

	// Verify .env written to OutputDir
	envPath := filepath.Join(tmpDir, ".env")
	_, err = os.Stat(envPath)
	require.NoError(t, err, ".env should exist in OutputDir after quick install")

	// Verify .env content
	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)
	envStr := string(envContent)
	assert.Contains(t, envStr, "SETUP_COMPLETED=true", ".env should mark setup complete")
	assert.Contains(t, envStr, "ADMIN_USERNAME=admin")
}

// TestSetupWizardInvalidFormSubmissions tests that invalid form submissions
// return errors (not redirects).
func TestSetupWizardInvalidFormSubmissions(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		form       url.Values
		wantStatus int
	}{
		{
			name:       "quickinstall missing admin username",
			path:       "/setup/quickinstall",
			form:       url.Values{"ADMIN_EMAIL": {"admin@example.com"}, "ADMIN_PASSWORD": {"pass123!"}, "ADMIN_PASSWORD_CONFIRM": {"pass123!"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "quickinstall password mismatch",
			path:       "/setup/quickinstall",
			form:       url.Values{"ADMIN_USERNAME": {"admin"}, "ADMIN_EMAIL": {"admin@example.com"}, "ADMIN_PASSWORD": {"pass123!"}, "ADMIN_PASSWORD_CONFIRM": {"different!"}},
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "quickinstall password too short",
			path:       "/setup/quickinstall",
			form:       url.Values{"ADMIN_USERNAME": {"admin"}, "ADMIN_EMAIL": {"admin@example.com"}, "ADMIN_PASSWORD": {"short"}, "ADMIN_PASSWORD_CONFIRM": {"short"}},
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			wizard := setup.NewWizard()
			wizard.OutputDir = tmpDir
			ts := httptest.NewServer(newWizardHandler(wizard))
			defer ts.Close()

			client := noRedirectClient()
			resp := doForm(t, client, ts.URL+tt.path, tt.form, wizard.GetCSRFToken())
			resp.Body.Close()
			assert.Equal(t, tt.wantStatus, resp.StatusCode)
		})
	}
}

// TestSetupWizardEnvFileContents verifies the generated .env file has correct
// values for the selected configuration.
func TestSetupWizardEnvFileContents(t *testing.T) {
	tmpDir := t.TempDir()

	wizard := setup.NewWizard()
	wizard.OutputDir = tmpDir
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	ts := httptest.NewServer(newWizardHandler(wizard))
	defer ts.Close()

	client := noRedirectClient()
	csrf := wizard.GetCSRFToken()

	// Set up admin credentials through security form first
	secForm := url.Values{
		"ADMIN_USERNAME": {"testadmin"},
		"ADMIN_EMAIL":    {"testadmin@example.com"},
		"ADMIN_PASSWORD": {"TestAdmin123!"},
	}
	resp := doForm(t, client, ts.URL+"/setup/security", secForm, csrf)
	resp.Body.Close()

	// Submit review to generate .env
	reviewForm := url.Values{
		"POSTGRES_MODE": {"docker"},
	}
	resp2 := doForm(t, client, ts.URL+"/setup/review", reviewForm, csrf)
	resp2.Body.Close()
	require.Equal(t, http.StatusSeeOther, resp2.StatusCode)

	// Read and verify .env content
	envPath := filepath.Join(tmpDir, ".env")
	envContent, err := os.ReadFile(envPath)
	require.NoError(t, err)

	// Parse key=value pairs
	envVars := parseEnvFile(string(envContent))

	// Verify expected values
	assert.Equal(t, "true", envVars["SETUP_COMPLETED"])
	assert.Equal(t, "testadmin", envVars["ADMIN_USERNAME"])
	assert.Equal(t, "testadmin@example.com", envVars["ADMIN_EMAIL"])
	// JWT_SECRET should be generated and non-empty
	assert.NotEmpty(t, envVars["JWT_SECRET"])
	// In Docker mode, POSTGRES_MODE=docker is set (no DATABASE_URL — handled by Docker Compose)
	assert.Equal(t, "docker", envVars["POSTGRES_MODE"])
}

// parseEnvFile parses a .env file into a map of key=value pairs.
func parseEnvFile(content string) map[string]string {
	vars := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			vars[parts[0]] = parts[1]
		}
	}
	return vars
}
