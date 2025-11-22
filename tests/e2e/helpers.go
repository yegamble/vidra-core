package e2e

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Config holds E2E test configuration
type Config struct {
	BaseURL    string
	AdminEmail string
	AdminPass  string
}

// DefaultConfig returns the default E2E test configuration
func DefaultConfig() *Config {
	baseURL := os.Getenv("E2E_BASE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:18080"
	}

	return &Config{
		BaseURL:    baseURL,
		AdminEmail: "admin@example.com",
		AdminPass:  "admin123",
	}
}

// TestClient wraps HTTP client with auth and helper methods
type TestClient struct {
	BaseURL    string
	HTTPClient *http.Client
	Token      string
	UserID     string
}

// NewTestClient creates a new test client
func NewTestClient(baseURL string) *TestClient {
	return &TestClient{
		BaseURL: baseURL,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SetAuthToken sets the authentication token for the client
func (c *TestClient) SetAuthToken(token string) {
	c.Token = token
}

// DoRequest performs an HTTP request with authentication if token is set
func (c *TestClient) DoRequest(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return c.HTTPClient.Do(req)
}

// RegisterUser registers a new user and returns the access token
func (c *TestClient) RegisterUser(t *testing.T, username, email, password string) (userID, token string) {
	payload := map[string]interface{}{
		"username": username,
		"email":    email,
		"password": password,
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := c.Post("/auth/register", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusCreated, resp.StatusCode, "User registration failed")

	// Parse response envelope
	var envelope struct {
		Data struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	c.Token = envelope.Data.AccessToken
	c.UserID = envelope.Data.User.ID

	return envelope.Data.User.ID, envelope.Data.AccessToken
}

// Login authenticates a user and returns the access token
func (c *TestClient) Login(t *testing.T, username, password string) (userID, token string) {
	payload := map[string]interface{}{
		"username": username,
		"password": password,
	}

	body, err := json.Marshal(payload)
	require.NoError(t, err)

	resp, err := c.Post("/auth/login", "application/json", bytes.NewReader(body))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode, "User login failed")

	// Parse response envelope
	var envelope struct {
		Data struct {
			User struct {
				ID       string `json:"id"`
				Username string `json:"username"`
			} `json:"user"`
			AccessToken string `json:"access_token"`
		} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	c.Token = envelope.Data.AccessToken
	c.UserID = envelope.Data.User.ID

	return envelope.Data.User.ID, envelope.Data.AccessToken
}

// UploadVideo uploads a test video file
func (c *TestClient) UploadVideo(t *testing.T, videoPath, title, description string) (videoID string) {
	// Create multipart form
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	// Add video file
	file, err := os.Open(videoPath)
	require.NoError(t, err)
	defer func() { _ = file.Close() }()

	part, err := writer.CreateFormFile("video", filepath.Base(videoPath))
	require.NoError(t, err)

	_, err = io.Copy(part, file)
	require.NoError(t, err)

	// Add metadata
	_ = writer.WriteField("title", title)
	_ = writer.WriteField("description", description)
	_ = writer.WriteField("privacy", "public")

	err = writer.Close()
	require.NoError(t, err)

	// Send request
	req, err := http.NewRequest("POST", c.BaseURL+"/api/v1/videos", &buf)
	require.NoError(t, err)

	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusCreated, resp.StatusCode, "Video upload failed")

	// Parse response envelope
	var envelope struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	return envelope.Data.ID
}

// GetVideo retrieves video details
func (c *TestClient) GetVideo(t *testing.T, videoID string) map[string]interface{} {
	resp, err := c.Get(fmt.Sprintf("/api/v1/videos/%s", videoID))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Get video failed")

	// Parse response envelope
	var envelope struct {
		Data map[string]interface{} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	return envelope.Data
}

// ListVideos retrieves a list of videos
func (c *TestClient) ListVideos(t *testing.T) []map[string]interface{} {
	resp, err := c.Get("/api/v1/videos")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode, "List videos failed")

	// Parse response envelope
	var envelope struct {
		Data []map[string]interface{} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	return envelope.Data
}

// SearchVideos searches for videos
func (c *TestClient) SearchVideos(t *testing.T, query string) []map[string]interface{} {
	// URL encode the query parameter to handle spaces and special characters
	encodedQuery := url.QueryEscape(query)
	searchURL := fmt.Sprintf("/api/v1/videos/search?q=%s", encodedQuery)

	resp, err := c.Get(searchURL)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Search videos failed")

	// Parse response envelope
	var envelope struct {
		Data []map[string]interface{} `json:"data"`
	}

	err = json.NewDecoder(resp.Body).Decode(&envelope)
	require.NoError(t, err)

	return envelope.Data
}

// DeleteVideo deletes a video
func (c *TestClient) DeleteVideo(t *testing.T, videoID string) {
	req, err := http.NewRequest("DELETE", c.BaseURL+fmt.Sprintf("/api/v1/videos/%s", videoID), nil)
	require.NoError(t, err)

	req.Header.Set("Authorization", "Bearer "+c.Token)

	resp, err := c.HTTPClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusNoContent, resp.StatusCode, "Delete video failed")
}

// Get performs an authenticated GET request
func (c *TestClient) Get(path string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return c.HTTPClient.Do(req)
}

// Post performs an authenticated POST request
func (c *TestClient) Post(path, contentType string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest("POST", c.BaseURL+path, body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", contentType)

	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return c.HTTPClient.Do(req)
}

// WaitForService waits for a service to become available
func WaitForService(ctx context.Context, url string, timeout time.Duration) error {
	client := &http.Client{Timeout: 5 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}

		resp, err := client.Do(req)
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			return nil
		}

		if resp != nil {
			_ = resp.Body.Close()
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
			// Retry
		}
	}

	return fmt.Errorf("service at %s did not become available within %v", url, timeout)
}

// HealthCheck performs a health check on the API
func HealthCheck(t *testing.T, baseURL string) {
	resp, err := http.Get(baseURL + "/health")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	require.Equal(t, http.StatusOK, resp.StatusCode, "Health check failed")
}

// GenerateUniqueUsername generates a unique username for E2E testing
// It uses the test name, GitHub run ID, and microsecond timestamp for uniqueness
// The username is kept under 50 characters (database constraint: VARCHAR(50))
func GenerateUniqueUsername(t *testing.T) string {
	// Use E2E_RUN_ID from GitHub Actions for better uniqueness across runs
	runID := os.Getenv("E2E_RUN_ID")
	if runID == "" {
		runID = fmt.Sprintf("%d", time.Now().Unix())
	}
	timestamp := time.Now().UnixNano() % 1000000                 // 6 digits microsecond precision
	testHash := fmt.Sprintf("%x", md5.Sum([]byte(t.Name())))[:6] // 6-char hash
	username := fmt.Sprintf("e2e_%s_%s_%d", testHash, runID, timestamp)
	if len(username) > 50 {
		username = username[:50] // Truncate if too long
	}
	return username
}

