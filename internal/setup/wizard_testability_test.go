package setup

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"athena/internal/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestWizardHasURLValidatorField verifies the Wizard struct has an injectable URLValidator field.
func TestWizardHasURLValidatorField(t *testing.T) {
	wizard := NewWizard()
	// Default validator should be non-nil
	assert.NotNil(t, wizard.URLValidator, "NewWizard() should initialize URLValidator to a non-nil default")
}

// TestWizardHasOutputDirField verifies the Wizard struct has an OutputDir field.
func TestWizardHasOutputDirField(t *testing.T) {
	wizard := NewWizard()
	// Default OutputDir should be empty string (current dir)
	assert.Equal(t, "", wizard.OutputDir, "NewWizard() should initialize OutputDir to empty string")
}

// TestWizardDefaultURLValidatorBlocksPrivateIPs verifies the default Wizard blocks connections to private IPs.
func TestWizardDefaultURLValidatorBlocksPrivateIPs(t *testing.T) {
	wizard := NewWizard()

	body := `{"url":"http://127.0.0.1:9999"}`
	req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIPFS(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, false, resp["success"], "default validator should block private IPs")
}

// TestWizardInjectableURLValidatorAllowsPrivateIPs verifies that overriding URLValidator allows private IPs.
func TestWizardInjectableURLValidatorAllowsPrivateIPs(t *testing.T) {
	// Start a test IPFS-like server on localhost
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"Version":"test-ipfs"}`))
	}))
	defer ts.Close()

	wizard := NewWizard()
	wizard.URLValidator = security.NewURLValidatorAllowPrivate()

	body, _ := json.Marshal(map[string]string{"url": ts.URL})
	req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIPFS(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"], "allow-private validator should allow localhost connections")
}

// TestWizardIOTAInjectableValidator verifies the IOTA handler also uses the injectable validator.
func TestWizardIOTAInjectableValidator(t *testing.T) {
	// Start a test IOTA-like JSON-RPC server on localhost
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":"35834a8a"}`))
	}))
	defer ts.Close()

	wizard := NewWizard()
	wizard.URLValidator = security.NewURLValidatorAllowPrivate()

	body, _ := json.Marshal(map[string]string{"url": ts.URL})
	req := httptest.NewRequest(http.MethodPost, "/setup/test-iota", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	wizard.HandleTestIOTA(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(w.Body).Decode(&resp))
	assert.Equal(t, true, resp["success"], "allow-private validator should allow localhost IOTA connections")
}

// TestWizardOutputDirIsUsedForFileWrites verifies that OutputDir is used for .env and compose override.
func TestWizardOutputDirIsUsedForFileWrites(t *testing.T) {
	tmpDir := t.TempDir()

	wizard := NewWizard()
	wizard.OutputDir = tmpDir
	wizard.config.AdminPassword = "testpassword123"
	wizard.config.PostgresMode = "docker" // Skip DB creation and admin user in review
	wizard.config.NginxProtocol = "http"
	wizard.config.NginxDomain = "localhost"
	wizard.config.NginxPort = 80

	// Create nginx conf subdirectory in temp dir (required by GenerateNginxConfig)
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	// POST to review - triggers file writes
	form := "POSTGRES_MODE=docker"
	req := httptest.NewRequest(http.MethodPost, "/setup/review", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleReview(w, req)

	// Should redirect to /setup/complete on success
	assert.Equal(t, http.StatusSeeOther, w.Code, "review POST should redirect: %s", w.Body.String())
	assert.Equal(t, "/setup/complete", w.Header().Get("Location"))

	// Verify .env was written to OutputDir, not current directory
	envPath := filepath.Join(tmpDir, ".env")
	_, err := os.Stat(envPath)
	assert.NoError(t, err, ".env should exist in OutputDir (%s)", tmpDir)

	// Verify docker-compose.override.yml was written to OutputDir
	composePath := filepath.Join(tmpDir, "docker-compose.override.yml")
	_, err = os.Stat(composePath)
	assert.NoError(t, err, "docker-compose.override.yml should exist in OutputDir (%s)", tmpDir)

	// Verify files were NOT written to current directory
	if _, err := os.Stat(".env"); err == nil {
		t.Error(".env should NOT be in the current directory")
		os.Remove(".env") // cleanup
	}
	if _, err := os.Stat("docker-compose.override.yml"); err == nil {
		t.Error("docker-compose.override.yml should NOT be in the current directory")
		os.Remove("docker-compose.override.yml") // cleanup
	}
}

// TestWizardOutputDirUsedByQuickInstall verifies QuickInstall also respects OutputDir.
func TestWizardOutputDirUsedByQuickInstall(t *testing.T) {
	tmpDir := t.TempDir()

	wizard := NewWizard()
	wizard.OutputDir = tmpDir

	// Create nginx conf subdirectory in temp dir
	require.NoError(t, os.MkdirAll(filepath.Join(tmpDir, "nginx", "conf"), 0755))

	form := "ADMIN_USERNAME=admin&ADMIN_EMAIL=admin%40example.com&ADMIN_PASSWORD=password123&ADMIN_PASSWORD_CONFIRM=password123&NGINX_DOMAIN=localhost"
	req := httptest.NewRequest(http.MethodPost, "/setup/quickinstall", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	w := httptest.NewRecorder()
	wizard.HandleQuickInstall(w, req)

	assert.Equal(t, http.StatusSeeOther, w.Code, "quick install should redirect: %s", w.Body.String())

	// Verify .env was written to OutputDir
	envPath := filepath.Join(tmpDir, ".env")
	_, err := os.Stat(envPath)
	assert.NoError(t, err, ".env should exist in OutputDir for QuickInstall")

	// Verify not in current directory
	if _, err := os.Stat(".env"); err == nil {
		t.Error(".env should NOT be in the current directory")
		os.Remove(".env")
	}
}
