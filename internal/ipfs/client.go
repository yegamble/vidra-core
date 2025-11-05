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
	apiURL        string
	clusterAPIURL string
	httpClient    *http.Client
	enabled       bool
	clusterEnabled bool
}

// ipfsAddResponse matches the JSON response from IPFS add endpoint
type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

// NewClient creates a new IPFS client
func NewClient(apiURL, clusterAPIURL string, timeout time.Duration) *Client {
	return &Client{
		apiURL:         apiURL,
		clusterAPIURL:  clusterAPIURL,
		httpClient:     &http.Client{Timeout: timeout},
		enabled:        apiURL != "",
		clusterEnabled: clusterAPIURL != "",
	}
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
	defer file.Close()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", filepath.Base(filePath))
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(fw, file); err != nil {
		return "", fmt.Errorf("failed to copy file content: %w", err)
	}
	mw.Close()

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
	defer resp.Body.Close()

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
		defer file.Close()

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
	mw.Close()

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
	defer resp.Body.Close()

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

	reqURL := c.apiURL + "/api/v0/pin/add?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to pin: %w", err)
	}
	defer resp.Body.Close()

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

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Best-effort, log but don't fail
		return nil
	}
	defer resp.Body.Close()

	// Cluster returns various status codes on success
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	// Try alternative endpoint format
	reqURL = c.clusterAPIURL + "/pins/add?arg=" + url.QueryEscape(cid)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil
	}

	resp2, err := c.httpClient.Do(req2)
	if err != nil {
		return nil
	}
	defer resp2.Body.Close()

	return nil
}

// AddAndPin uploads a file and pins it (convenience method)
func (c *Client) AddAndPin(ctx context.Context, filePath string) (string, error) {
	cid, err := c.AddFile(ctx, filePath)
	if err != nil {
		return "", err
	}

	// Pin is typically redundant when pin=true in add, but ensures it
	if err := c.Pin(ctx, cid); err != nil {
		// Log but don't fail - it was already pinned during add
		// In production, you'd use a logger here
	}

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
	if err := c.Pin(ctx, cid); err != nil {
		// Log but don't fail
	}

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
