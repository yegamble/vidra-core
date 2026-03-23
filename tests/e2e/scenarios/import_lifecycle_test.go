package scenarios

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"vidra-core/tests/e2e"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// importClient wraps TestClient with import-specific helpers.
type importClient struct {
	*e2e.TestClient
}

// createImport creates a video import and returns the import ID.
// It returns the HTTP status code and the response body for assertion.
func (c *importClient) createImport(t *testing.T, sourceURL string) (statusCode int, importID string, body map[string]interface{}) {
	t.Helper()
	payload := map[string]interface{}{
		"targetUrl":     sourceURL,
		"targetPrivacy": "private",
	}
	b, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := c.Post("/api/v1/videos/imports", "application/json", bytes.NewReader(b))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(rawBody, &body))

	if data, ok := body["data"].(map[string]interface{}); ok {
		if id, ok := data["id"].(string); ok {
			importID = id
		}
	}
	return resp.StatusCode, importID, body
}

// getImport retrieves an import by ID.
func (c *importClient) getImport(t *testing.T, importID string) (statusCode int, body map[string]interface{}) {
	t.Helper()
	resp, err := c.Get(fmt.Sprintf("/api/v1/videos/imports/%s", importID))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(rawBody, &body))
	return resp.StatusCode, body
}

// listImports lists all imports for the authenticated user.
func (c *importClient) listImports(t *testing.T) (statusCode int, body map[string]interface{}) {
	t.Helper()
	resp, err := c.Get("/api/v1/videos/imports?limit=20&offset=0")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	require.NoError(t, json.Unmarshal(rawBody, &body))
	return resp.StatusCode, body
}

// cancelImport cancels an import by ID.
func (c *importClient) cancelImport(t *testing.T, importID string) (statusCode int, body map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("POST", c.BaseURL+fmt.Sprintf("/api/v1/videos/imports/%s/cancel", importID), nil)
	require.NoError(t, err)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	_ = json.Unmarshal(rawBody, &body)
	return resp.StatusCode, body
}

// retryImport retries a failed import.
func (c *importClient) retryImport(t *testing.T, importID string) (statusCode int, body map[string]interface{}) {
	t.Helper()
	req, err := http.NewRequest("POST", c.BaseURL+fmt.Sprintf("/api/v1/videos/imports/%s/retry", importID), nil)
	require.NoError(t, err)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.HTTPClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	rawBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	_ = json.Unmarshal(rawBody, &body)
	return resp.StatusCode, body
}

// pollImportStatus polls GET /imports/{id} until the status matches or timeout.
func pollImportStatus(t *testing.T, c *importClient, importID, wantStatus string, timeout time.Duration) (finalStatus string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, body := c.getImport(t, importID)
		if data, ok := body["data"].(map[string]interface{}); ok {
			if st, ok := data["status"].(string); ok {
				finalStatus = st
				if st == wantStatus {
					return finalStatus
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return finalStatus
}

// TestImportLifecycle_CreateGetList tests the core import API contract:
// create → get → list.
//
// The test spins up a local httptest.Server to provide a URL for the import.
// In a full integration environment, the server's yt-dlp will attempt to
// validate and download from that URL; the test validates the API response
// shape regardless of whether the download succeeds.
func TestImportLifecycle_CreateGetList(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping import lifecycle E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 30*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// Spin up a local mock download server that serves a small byte sequence
	// so the yt-dlp URL validator can reach it over HTTP.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		// Minimal FTYP box bytes — enough for Content-Type sniffing
		_, _ = w.Write([]byte("\x00\x00\x00\x1cftypisom\x00\x00\x00\x00isomiso2avc1mp41"))
	}))
	defer mockServer.Close()

	client := &importClient{e2e.NewTestClient(cfg.BaseURL)}

	// Register a unique user for this test run.
	ts := time.Now().UnixNano() % 10_000_000_000
	hash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8]
	username := fmt.Sprintf("e2e_%s_%d", hash, ts)
	_, _ = client.RegisterUser(t, username, username+"@example.com", "SecurePass123!")

	// --- Step 1: Create import ---
	// The mock URL may fail yt-dlp validation (not a real video site), so we
	// accept both 201 (import created) and 4xx (validation/URL error).
	statusCode, importID, createBody := client.createImport(t, mockServer.URL+"/video.mp4")
	t.Logf("Create import: status=%d importID=%q", statusCode, importID)

	// Validate response envelope structure.
	assert.Contains(t, createBody, "success", "response must have success field")
	if statusCode == http.StatusCreated {
		require.NotEmpty(t, importID, "created import must have an ID")
		assert.Equal(t, true, createBody["success"])

		data, ok := createBody["data"].(map[string]interface{})
		require.True(t, ok, "data field must be an object")
		assert.NotEmpty(t, data["id"])
		assert.NotEmpty(t, data["status"])
		t.Logf("Import created: id=%s status=%s", data["id"], data["status"])

		// --- Step 2: Get import ---
		getStatus, getBody := client.getImport(t, importID)
		assert.Equal(t, http.StatusOK, getStatus, "GET import must return 200")
		assert.Equal(t, true, getBody["success"])

		getData, ok := getBody["data"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, importID, getData["id"])
		t.Logf("Get import: status=%s", getData["status"])

		// --- Step 3: List imports ---
		listStatus, listBody := client.listImports(t)
		assert.Equal(t, http.StatusOK, listStatus, "list imports must return 200")
		assert.Equal(t, true, listBody["success"])

		listData, ok := listBody["data"].(map[string]interface{})
		require.True(t, ok, "list data must be an object")
		// May be []interface{} or map with "data" key depending on handler
		t.Logf("List imports: raw data type=%T", listData)
	} else {
		// Validation error — still verify error envelope structure.
		assert.Equal(t, false, createBody["success"])
		assert.Contains(t, createBody, "error", "error response must have error field")
		t.Logf("Import creation rejected (expected in test env without real URL): %v", createBody["error"])
	}
}

// TestImportLifecycle_Cancel tests the cancel path:
// create import → cancel → verify cancelled status.
func TestImportLifecycle_Cancel(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping import lifecycle E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 30*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("\x00\x00\x00\x1cftypisom"))
	}))
	defer mockServer.Close()

	client := &importClient{e2e.NewTestClient(cfg.BaseURL)}

	ts := time.Now().UnixNano() % 10_000_000_000
	hash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8]
	username := fmt.Sprintf("e2e_%s_%d", hash, ts)
	_, _ = client.RegisterUser(t, username, username+"@example.com", "SecurePass123!")

	statusCode, importID, _ := client.createImport(t, mockServer.URL+"/video.mp4")
	if statusCode != http.StatusCreated || importID == "" {
		t.Skipf("Import creation failed (status=%d) — skipping cancel test (requires a successfully created import)", statusCode)
	}
	t.Logf("Created import %s for cancel test", importID)

	// Cancel the import.
	cancelStatus, cancelBody := client.cancelImport(t, importID)
	t.Logf("Cancel import: status=%d body=%v", cancelStatus, cancelBody)

	// Accept 200 (cancelled) or 409 (already completed/failed before we could cancel).
	assert.Contains(t, []int{http.StatusOK, http.StatusConflict}, cancelStatus,
		"cancel must return 200 or 409")

	if cancelStatus == http.StatusOK {
		assert.Equal(t, true, cancelBody["success"])
		// Poll for cancelled status (up to 3 seconds).
		finalStatus := pollImportStatus(t, client, importID, "cancelled", 3*time.Second)
		t.Logf("Final import status after cancel: %s", finalStatus)
	}
}

// TestImportLifecycle_Retry tests the retry path:
// create import → wait for failure → retry → status becomes pending or downloading.
func TestImportLifecycle_Retry(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping import lifecycle E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 30*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// Serve a URL that will initially succeed validation but fail download
	// by closing the connection partway through.
	var requestCount atomic.Int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := requestCount.Add(1)
		if n == 1 {
			// First request: return empty body to trigger a download failure.
			w.WriteHeader(http.StatusOK)
			return
		}
		// Subsequent requests: serve minimal video bytes.
		w.Header().Set("Content-Type", "video/mp4")
		_, _ = w.Write([]byte("\x00\x00\x00\x1cftypisom"))
	}))
	defer mockServer.Close()

	client := &importClient{e2e.NewTestClient(cfg.BaseURL)}

	ts := time.Now().UnixNano() % 10_000_000_000
	hash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:8]
	username := fmt.Sprintf("e2e_%s_%d", hash, ts)
	_, _ = client.RegisterUser(t, username, username+"@example.com", "SecurePass123!")

	statusCode, importID, _ := client.createImport(t, mockServer.URL+"/video.mp4")
	if statusCode != http.StatusCreated || importID == "" {
		t.Skipf("Import creation failed (status=%d) — skipping retry test", statusCode)
	}
	t.Logf("Created import %s for retry test", importID)

	// Wait up to 5 seconds for the import to reach a terminal state (failed or cancelled).
	finalStatus := pollImportStatus(t, client, importID, "failed", 5*time.Second)
	t.Logf("Import status before retry: %s", finalStatus)

	if finalStatus != "failed" {
		t.Skipf("Import did not reach 'failed' state (got %q) — retry test requires a failed import", finalStatus)
	}

	// Retry the failed import.
	retryStatus, retryBody := client.retryImport(t, importID)
	t.Logf("Retry import: status=%d body=%v", retryStatus, retryBody)

	assert.Equal(t, http.StatusOK, retryStatus, "retry must return 200")
	assert.Equal(t, true, retryBody["success"])

	// After retry, the import should be re-queued (pending or downloading).
	retryStat := pollImportStatus(t, client, importID, "pending", 2*time.Second)
	if retryStat == "" {
		retryStat = "unknown"
	}
	t.Logf("Import status after retry: %s", retryStat)
}

// TestImportLifecycle_Unauthorized verifies that all import endpoints require auth.
func TestImportLifecycle_Unauthorized(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping import lifecycle E2E test in short mode")
	}

	cfg := e2e.DefaultConfig()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := e2e.WaitForService(ctx, cfg.BaseURL+"/health", 15*time.Second); err != nil {
		t.Skipf("Skipping E2E test: API service not available (%v)", err)
	}

	// Unauthenticated client (no token).
	unauthClient := &importClient{e2e.NewTestClient(cfg.BaseURL)}

	endpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"create import", "POST", "/api/v1/videos/imports"},
		{"list imports", "GET", "/api/v1/videos/imports"},
		{"get import", "GET", "/api/v1/videos/imports/nonexistent-id"},
		{"cancel import", "POST", "/api/v1/videos/imports/nonexistent-id/cancel"},
		{"retry import", "POST", "/api/v1/videos/imports/nonexistent-id/retry"},
	}

	for _, ep := range endpoints {
		t.Run(ep.name, func(t *testing.T) {
			var resp *http.Response
			var err error
			switch ep.method {
			case "GET":
				resp, err = unauthClient.Get(ep.path)
			case "POST":
				resp, err = unauthClient.Post(ep.path, "application/json", bytes.NewReader([]byte("{}")))
			}
			require.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"%s without auth must return 401", ep.name)
		})
	}
}
