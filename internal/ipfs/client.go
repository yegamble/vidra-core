package ipfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Client provides IPFS operations for uploading and pinning content
type Client struct {
	apiURL         string
	clusterAPIURL  string
	httpClient     *http.Client
	clusterClient  *http.Client // Separate client for cluster (may have different TLS config)
	clusterAuth    *ClusterAuthConfig
	enabled        bool
	clusterEnabled bool
}

// ipfsAddResponse matches the JSON response from IPFS add endpoint
type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// NewClient creates a new IPFS client without authentication
func NewClient(apiURL, clusterAPIURL string, timeout time.Duration) *Client {
	return &Client{
		apiURL:         apiURL,
		clusterAPIURL:  clusterAPIURL,
		httpClient:     &http.Client{Timeout: timeout},
		clusterClient:  &http.Client{Timeout: timeout},
		clusterAuth:    nil,
		enabled:        apiURL != "",
		clusterEnabled: clusterAPIURL != "",
	}
}

// NewClientWithAuth creates a new IPFS client with cluster authentication
func NewClientWithAuth(apiURL, clusterAPIURL string, timeout time.Duration, auth *ClusterAuthConfig) *Client {
	// If clusterAPIURL is empty, use apiURL for cluster operations (common in simple setups)
	effectiveClusterURL := clusterAPIURL
	if effectiveClusterURL == "" && apiURL != "" {
		effectiveClusterURL = apiURL
	}

	client := &Client{
		apiURL:         apiURL,
		clusterAPIURL:  effectiveClusterURL,
		httpClient:     &http.Client{Timeout: timeout},
		clusterAuth:    auth,
		enabled:        apiURL != "",
		clusterEnabled: effectiveClusterURL != "",
	}

	// Create authenticated cluster client if auth is provided
	if auth != nil {
		// SECURITY: Enforce HTTPS when Bearer token authentication is used
		if auth.Token != "" && strings.HasPrefix(effectiveClusterURL, "http://") {
			// This is a critical security issue - fail immediately
			// Bearer tokens MUST NOT be transmitted over unencrypted HTTP
			client.clusterClient = &http.Client{Timeout: timeout}
			client.clusterAuth = nil
			client.clusterEnabled = false
			// In production, this would log a critical security error
			return client
		}

		if err := auth.Validate(); err != nil {
			// Log validation error but continue with default client
			// In production, this would use the logger
			client.clusterClient = &http.Client{Timeout: timeout}
		} else {
			authClient, err := auth.GetHTTPClient()
			if err != nil {
				// Fallback to default client
				client.clusterClient = &http.Client{Timeout: timeout}
			} else {
				authClient.Timeout = timeout
				client.clusterClient = authClient
			}
		}
	} else {
		client.clusterClient = &http.Client{Timeout: timeout}
	}

	return client
}

// NewClientWithAuthFromEnv creates a new IPFS client with authentication loaded from environment
func NewClientWithAuthFromEnv(apiURL, clusterAPIURL string, timeout time.Duration) *Client {
	auth := NewClusterAuthConfig()

	// If no auth configured in environment, use regular constructor
	// but still apply the cluster URL fallback logic
	if auth.Token == "" && !auth.TLSEnabled {
		// Use NewClientWithAuth with nil auth to get the URL fallback behavior
		return NewClientWithAuth(apiURL, clusterAPIURL, timeout, nil)
	}

	return NewClientWithAuth(apiURL, clusterAPIURL, timeout, auth)
}

// IsEnabled returns whether IPFS operations are enabled
func (c *Client) IsEnabled() bool {
	return c.enabled
}

// AddFile uploads a single file to IPFS and returns its CID
func (c *Client) AddFile(ctx context.Context, filePath string) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("IPFS not enabled")
	}

	file, err := os.Open(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to open file: %w", err)
	}
	defer func() { _ = file.Close() }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(fw, file); err != nil {
		return "", fmt.Errorf("failed to copy file content: %w", err)
	}
	_ = mw.Close()

	// Use CIDv1 with raw leaves for better compatibility and performance
	reqURL := c.apiURL + "/api/v0/add?pin=true&cid-version=1&raw-leaves=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload to IPFS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("IPFS add failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	cid, err := parseIPFSAddResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to parse IPFS response: %w", err)
	}

	return cid, nil
}

// AddDirectory uploads an entire directory to IPFS and returns the root CID
// This is useful for HLS variants which contain multiple files (playlist + segments)
func (c *Client) AddDirectory(ctx context.Context, dirPath string) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("IPFS not enabled")
	}

	// Verify directory exists
	info, err := os.Stat(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", dirPath)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	// Walk the directory and add all files
	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		// Get relative path for IPFS
		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

		// Create form file with directory structure
		fw, err := mw.CreateFormFile("file", relPath)
		if err != nil {
			return err
		}

		if _, err := io.Copy(fw, file); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		return "", fmt.Errorf("failed to walk directory: %w", err)
	}
	_ = mw.Close()

	// Use wrap-with-directory to create a directory CID
	reqURL := c.apiURL + "/api/v0/add?pin=true&cid-version=1&raw-leaves=true&wrap-with-directory=true&recursive=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, &body)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to upload to IPFS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("IPFS add failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	// When wrap-with-directory is used, the last entry is the directory itself
	cid, err := parseIPFSAddResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to parse IPFS response: %w", err)
	}

	return cid, nil
}

// Pin ensures a CID is pinned on the local IPFS node (idempotent)
func (c *Client) Pin(ctx context.Context, cid string) error {
	if !c.enabled {
		return fmt.Errorf("IPFS not enabled")
	}

	// Validate CID before sending to IPFS
	if err := ValidateCID(cid); err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.apiURL + "/api/v0/pin/add?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to pin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("IPFS pin failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ClusterPin pins a CID to IPFS Cluster (best-effort, won't fail if cluster unavailable)
func (c *Client) ClusterPin(ctx context.Context, cid string) error {
	if !c.clusterEnabled {
		// Not an error - cluster is optional
		return nil
	}

	// Validate CID before sending to cluster
	if err := ValidateCID(cid); err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Apply authentication if configured
	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return fmt.Errorf("failed to apply auth: %w", err)
		}
	}

	// Use authenticated cluster client
	client := c.clusterClient
	if client == nil {
		client = c.httpClient
	}

	resp, err := client.Do(req)
	if err != nil {
		// Return context errors immediately (not best-effort)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// Best-effort for other errors
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	// Cluster returns various status codes on success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Try alternative endpoint format
	reqURL = c.clusterAPIURL + "/pins/add?arg=" + url.QueryEscape(cid)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

	// Apply authentication to second request too
	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req2); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return nil
		}
	}

	resp2, err := client.Do(req2)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	defer func() { _ = resp2.Body.Close() }()

	return nil
}

// ClusterUnpin unpins a CID from IPFS Cluster
func (c *Client) ClusterUnpin(ctx context.Context, cid string) error {
	if !c.clusterEnabled {
		// Not an error - cluster is optional
		return nil
	}

	// Validate CID before sending to cluster
	if err := ValidateCID(cid); err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Apply authentication if configured
	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return fmt.Errorf("failed to apply auth: %w", err)
		}
	}

	// Use authenticated cluster client
	client := c.clusterClient
	if client == nil {
		client = c.httpClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to unpin: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("cluster unpin failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return nil
}

// ClusterStatusResponse represents the status response from IPFS Cluster
type ClusterStatusResponse struct {
	Status  string                 `json:"status"`
	PeerMap map[string]interface{} `json:"peer_map"`
}

// ClusterStatus retrieves the pin status for a CID from IPFS Cluster
func (c *Client) ClusterStatus(ctx context.Context, cid string) (*ClusterStatusResponse, error) {
	if !c.clusterEnabled {
		return nil, fmt.Errorf("cluster not enabled")
	}

	// Validate CID before sending to cluster
	if err := ValidateCID(cid); err != nil {
		return nil, fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Apply authentication if configured
	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return nil, fmt.Errorf("failed to apply auth: %w", err)
		}
	}

	// Use authenticated cluster client
	client := c.clusterClient
	if client == nil {
		client = c.httpClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("cluster status failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var status ClusterStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		return nil, fmt.Errorf("failed to decode status response: %w", err)
	}

	return &status, nil
}

// UpdateAuthToken updates the cluster authentication token (for token rotation)
func (c *Client) UpdateAuthToken(newToken string) {
	if c.clusterAuth != nil {
		c.clusterAuth.UpdateToken(newToken)
	}
}

// AddAndPin uploads a file and pins it (convenience method)
func (c *Client) AddAndPin(ctx context.Context, filePath string) (string, error) {
	cid, err := c.AddFile(ctx, filePath)
	if err != nil {
		return "", err
	}

	// Pin is typically redundant when pin=true in add, but ensures it
	// Ignore error as it was already pinned during add
	_ = c.Pin(ctx, cid)

	// Best-effort cluster pin
	_ = c.ClusterPin(ctx, cid)

	return cid, nil
}

// AddDirectoryAndPin uploads a directory and pins it (convenience method)
func (c *Client) AddDirectoryAndPin(ctx context.Context, dirPath string) (string, error) {
	cid, err := c.AddDirectory(ctx, dirPath)
	if err != nil {
		return "", err
	}

	// Pin is typically redundant when pin=true in add
	// Ignore error as it was already pinned during add
	_ = c.Pin(ctx, cid)

	// Best-effort cluster pin
	_ = c.ClusterPin(ctx, cid)

	return cid, nil
}

// parseIPFSAddResponse parses the final CID from IPFS add NDJSON stream
func parseIPFSAddResponse(r io.Reader) (string, error) {
	var last ipfsAddResponse
	sc := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	sc.Buffer(buf, 10*1024*1024)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var cur ipfsAddResponse
		if err := json.Unmarshal([]byte(line), &cur); err != nil {
			return "", err
		}
		if cur.Hash != "" {
			last = cur
		}
	}

	if err := sc.Err(); err != nil {
		return "", err
	}

	if last.Hash == "" {
		return "", fmt.Errorf("missing CID in IPFS response")
	}

	return last.Hash, nil
}
