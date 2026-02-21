// Package integration contains integration tests that require live Docker services.
// Run with: TEST_INTEGRATION=true go test ./tests/integration/... -run TestServiceConnections -v -timeout 120s
// Requires: docker compose --profile test-integration up -d
package integration

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/security"
	"athena/internal/setup"
)

// skipIfNoIntegration skips the test unless TEST_INTEGRATION=true is set.
func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("TEST_INTEGRATION") != "true" {
		t.Skip("Skipping integration test: set TEST_INTEGRATION=true and start Docker services with 'docker compose --profile test-integration up -d'")
	}
}

// newIntegrationWizard creates a Wizard configured for integration testing.
// URLValidator is set to AllowPrivate to allow connections to Docker container IPs.
// Each call returns a fresh wizard to avoid rate limit interference between tests.
func newIntegrationWizard() *setup.Wizard {
	w := setup.NewWizard()
	w.URLValidator = security.NewURLValidatorAllowPrivate()
	return w
}

// testConnectionJSON posts JSON body to the wizard handler and returns the decoded response.
func testConnectionJSON(t *testing.T, handler http.Handler, path string, body interface{}) map[string]interface{} {
	t.Helper()
	bodyJSON, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(string(bodyJSON)))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "127.0.0.1:12345"

	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	var resp map[string]interface{}
	require.NoError(t, json.NewDecoder(rw.Body).Decode(&resp))
	return resp
}

// TestServiceConnections_PostgreSQL tests the database test connection handler
// against a live PostgreSQL instance running on localhost:15432.
func TestServiceConnections_PostgreSQL(t *testing.T) {
	skipIfNoIntegration(t)

	wizard := newIntegrationWizard()
	handler := newWizardHandler(wizard)

	t.Run("success with valid credentials", func(t *testing.T) {
		resp := testConnectionJSON(t, handler, "/setup/test-database", map[string]interface{}{
			"host":     "localhost",
			"port":     15432,
			"user":     "integration_user",
			"password": "integration_password",
			"database": "athena_integration",
			"sslmode":  "disable",
		})
		assert.Equal(t, true, resp["success"], "expected success, got: %v", resp)
	})

	t.Run("failure with wrong password", func(t *testing.T) {
		// Need a fresh wizard for rate limit purposes
		wizard2 := newIntegrationWizard()
		handler2 := newWizardHandler(wizard2)
		resp := testConnectionJSON(t, handler2, "/setup/test-database", map[string]interface{}{
			"host":     "localhost",
			"port":     15432,
			"user":     "integration_user",
			"password": "wrong_password",
			"database": "athena_integration",
			"sslmode":  "disable",
		})
		assert.Equal(t, false, resp["success"], "expected failure with wrong password")
	})

	t.Run("failure with unreachable host", func(t *testing.T) {
		wizard3 := newIntegrationWizard()
		handler3 := newWizardHandler(wizard3)
		resp := testConnectionJSON(t, handler3, "/setup/test-database", map[string]interface{}{
			"host":     "localhost",
			"port":     19999, // nothing listening here
			"user":     "user",
			"password": "pass",
			"database": "db",
			"sslmode":  "disable",
		})
		assert.Equal(t, false, resp["success"], "expected failure with unreachable host")
	})
}

// TestServiceConnections_Redis tests the Redis test connection handler
// against a live Redis instance running on localhost:16379.
func TestServiceConnections_Redis(t *testing.T) {
	skipIfNoIntegration(t)

	wizard := newIntegrationWizard()
	handler := newWizardHandler(wizard)

	t.Run("success with valid Redis", func(t *testing.T) {
		resp := testConnectionJSON(t, handler, "/setup/test-redis", map[string]interface{}{
			"host": "localhost",
			"port": 16379,
		})
		assert.Equal(t, true, resp["success"], "expected Redis PING success, got: %v", resp)
	})

	t.Run("failure with unreachable port", func(t *testing.T) {
		wizard2 := newIntegrationWizard()
		handler2 := newWizardHandler(wizard2)
		resp := testConnectionJSON(t, handler2, "/setup/test-redis", map[string]interface{}{
			"host": "localhost",
			"port": 19998, // nothing listening
		})
		assert.Equal(t, false, resp["success"], "expected failure with unreachable Redis")
	})
}

// TestServiceConnections_Redis_PasswordBug documents a known bug:
// HandleTestRedis accepts a 'password' field but never sends AUTH before PING.
// This test verifies the bug is present (passes even with a "wrong" password
// against an unauthenticated Redis, since AUTH is never sent).
// Bug to fix: The handler should send AUTH <password>\r\n before PING when password != "".
func TestServiceConnections_Redis_PasswordBug(t *testing.T) {
	skipIfNoIntegration(t)

	// Our test Redis has no password, so sending any password field should still succeed
	// (because the handler ignores the password field and just does PING).
	// If the bug is fixed, a password-protected Redis would require AUTH first.
	wizard := newIntegrationWizard()
	handler := newWizardHandler(wizard)

	resp := testConnectionJSON(t, handler, "/setup/test-redis", map[string]interface{}{
		"host":     "localhost",
		"port":     16379,
		"password": "anypassword", // This field is parsed but never used for AUTH
	})
	// BUG: This succeeds even though 'anypassword' is ignored.
	// A correctly implemented handler would reject this against a password-protected Redis.
	assert.Equal(t, true, resp["success"], "BUG: Redis password field is parsed but AUTH command is never sent")
	t.Log("KNOWN BUG documented: HandleTestRedis parses 'password' field but never sends AUTH command")
}

// TestServiceConnections_IPFS tests the IPFS test connection handler
// against a live IPFS instance running on localhost:15001.
func TestServiceConnections_IPFS(t *testing.T) {
	skipIfNoIntegration(t)

	wizard := newIntegrationWizard()
	handler := newWizardHandler(wizard)

	t.Run("success with valid IPFS", func(t *testing.T) {
		resp := testConnectionJSON(t, handler, "/setup/test-ipfs", map[string]interface{}{
			"url": "http://localhost:15001",
		})
		assert.Equal(t, true, resp["success"], "expected IPFS connection success, got: %v", resp)
	})

	t.Run("failure with invalid URL", func(t *testing.T) {
		wizard2 := newIntegrationWizard()
		handler2 := newWizardHandler(wizard2)
		resp := testConnectionJSON(t, handler2, "/setup/test-ipfs", map[string]interface{}{
			"url": "http://localhost:19997", // nothing listening
		})
		assert.Equal(t, false, resp["success"], "expected failure with unreachable IPFS")
	})
}

// TestServiceConnections_IOTA tests the IOTA test connection handler
// against our lightweight mock IOTA RPC server running on localhost:19500.
func TestServiceConnections_IOTA(t *testing.T) {
	skipIfNoIntegration(t)

	wizard := newIntegrationWizard()
	handler := newWizardHandler(wizard)

	t.Run("success with mock IOTA node", func(t *testing.T) {
		resp := testConnectionJSON(t, handler, "/setup/test-iota", map[string]interface{}{
			"url": "http://localhost:19500",
		})
		assert.Equal(t, true, resp["success"], "expected IOTA connection success, got: %v", resp)
	})

	t.Run("failure with unreachable IOTA node", func(t *testing.T) {
		wizard2 := newIntegrationWizard()
		handler2 := newWizardHandler(wizard2)
		resp := testConnectionJSON(t, handler2, "/setup/test-iota", map[string]interface{}{
			"url": "http://localhost:19996", // nothing listening
		})
		assert.Equal(t, false, resp["success"], "expected failure with unreachable IOTA node")
	})
}

// TestServiceConnections_Email tests the email test handler
// against Mailpit running on localhost:19401 (SMTP) and 19400 (web UI).
func TestServiceConnections_Email(t *testing.T) {
	skipIfNoIntegration(t)

	tmpDir := t.TempDir()

	wizard := newIntegrationWizard()
	wizard.OutputDir = tmpDir

	handler := newWizardHandler(wizard)
	client := noRedirectClient()
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// First set SMTP config via the email form
	emailForm := fmt.Sprintf("SMTP_MODE=external&SMTP_HOST=localhost&SMTP_PORT=19401&SMTP_FROM_ADDRESS=noreply%%40test.local&SMTP_FROM_NAME=Test")
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/setup/email", strings.NewReader(emailForm))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	// Now test sending an email
	testEmailReq, _ := http.NewRequest(http.MethodPost, ts.URL+"/setup/test-email",
		strings.NewReader(`{"email":"recipient@test.local"}`))
	testEmailReq.Header.Set("Content-Type", "application/json")
	testEmailReq.RemoteAddr = "127.0.0.1:54321"

	testEmailResp, err := client.Do(testEmailReq)
	require.NoError(t, err)
	defer testEmailResp.Body.Close()

	var result map[string]interface{}
	require.NoError(t, json.NewDecoder(testEmailResp.Body).Decode(&result))
	assert.Equal(t, true, result["success"], "expected email send success via Mailpit, got: %v", result)

	// Verify the email arrived via Mailpit API
	mailpitResp, err := http.Get("http://localhost:19400/api/v1/messages")
	if err != nil {
		t.Logf("Could not verify via Mailpit API: %v", err)
		return
	}
	defer mailpitResp.Body.Close()

	body, _ := io.ReadAll(mailpitResp.Body)
	t.Logf("Mailpit messages: %s", string(body))

	var messages map[string]interface{}
	if err := json.Unmarshal(body, &messages); err == nil {
		total, _ := messages["total"].(float64)
		assert.Greater(t, total, float64(0), "expected at least 1 message in Mailpit")
	}
}

// TestServiceConnections_RateLimiting verifies the rate limiter blocks
// more than 3 requests per 5 minutes from the same IP.
func TestServiceConnections_RateLimiting(t *testing.T) {
	// This test is safe to run without Docker — it tests the rate limiter logic
	wizard := setup.NewWizard()
	wizard.URLValidator = security.NewURLValidatorAllowPrivate()
	handler := newWizardHandler(wizard)

	// Make 4 requests with the same IP
	for i := 0; i < 4; i++ {
		req := httptest.NewRequest(http.MethodPost, "/setup/test-ipfs",
			strings.NewReader(`{"url":"http://localhost:9999"}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "192.0.2.1:12345" // consistent IP

		rw := httptest.NewRecorder()
		handler.ServeHTTP(rw, req)

		if i < 3 {
			// First 3 requests should not be rate-limited (may fail for other reasons)
			assert.NotEqual(t, http.StatusTooManyRequests, rw.Code,
				"request %d should not be rate limited", i+1)
		} else {
			// 4th request should be rate limited
			assert.Equal(t, http.StatusTooManyRequests, rw.Code,
				"4th request should be rate limited")
		}
	}
}
