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

type Client struct {
	apiURL         string
	clusterAPIURL  string
	httpClient     *http.Client
	clusterClient  *http.Client
	clusterAuth    *ClusterAuthConfig
	enabled        bool
	clusterEnabled bool
}

type ipfsAddResponse struct {
	Name string `json:"Name"`
	Hash string `json:"Hash"`
	Size string `json:"Size"`
}

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

func NewClientWithAuth(apiURL, clusterAPIURL string, timeout time.Duration, auth *ClusterAuthConfig) *Client {
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

	if auth != nil {
		if auth.Token != "" && strings.HasPrefix(effectiveClusterURL, "http://") {
			client.clusterClient = &http.Client{Timeout: timeout}
			client.clusterAuth = nil
			client.clusterEnabled = false
			return client
		}

		if err := auth.Validate(); err != nil {
			client.clusterClient = &http.Client{Timeout: timeout}
		} else {
			authClient, err := auth.GetHTTPClient()
			if err != nil {
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

func NewClientWithAuthFromEnv(apiURL, clusterAPIURL string, timeout time.Duration) *Client {
	auth := NewClusterAuthConfig()

	if auth.Token == "" && !auth.TLSEnabled {
		return NewClientWithAuth(apiURL, clusterAPIURL, timeout, nil)
	}

	return NewClientWithAuth(apiURL, clusterAPIURL, timeout, auth)
}

func (c *Client) IsEnabled() bool {
	return c.enabled
}

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

func (c *Client) AddDirectory(ctx context.Context, dirPath string) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("IPFS not enabled")
	}

	info, err := os.Stat(dirPath)
	if err != nil {
		return "", fmt.Errorf("failed to stat directory: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", dirPath)
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)

	err = filepath.Walk(dirPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(dirPath, path)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()

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

	cid, err := parseIPFSAddResponse(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to parse IPFS response: %w", err)
	}

	return cid, nil
}

func (c *Client) Pin(ctx context.Context, cid string) error {
	if !c.enabled {
		return fmt.Errorf("IPFS not enabled")
	}

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

func (c *Client) ClusterPin(ctx context.Context, cid string) error {
	if !c.clusterEnabled {
		return nil
	}

	if err := ValidateCID(cid); err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return fmt.Errorf("failed to apply auth: %w", err)
		}
	}

	client := c.clusterClient
	if client == nil {
		client = c.httpClient
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	reqURL = c.clusterAPIURL + "/pins/add?arg=" + url.QueryEscape(cid)
	req2, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return nil
	}

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

func (c *Client) ClusterUnpin(ctx context.Context, cid string) error {
	if !c.clusterEnabled {
		return nil
	}

	if err := ValidateCID(cid); err != nil {
		return fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return fmt.Errorf("failed to apply auth: %w", err)
		}
	}

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

type ClusterStatusResponse struct {
	Status  string                 `json:"status"`
	PeerMap map[string]interface{} `json:"peer_map"`
}

func (c *Client) ClusterStatus(ctx context.Context, cid string) (*ClusterStatusResponse, error) {
	if !c.clusterEnabled {
		return nil, fmt.Errorf("cluster not enabled")
	}

	if err := ValidateCID(cid); err != nil {
		return nil, fmt.Errorf("invalid CID: %w", err)
	}

	reqURL := c.clusterAPIURL + "/pins/" + cid
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	if c.clusterAuth != nil {
		if err := c.clusterAuth.ApplyToRequest(req); err != nil {
			return nil, fmt.Errorf("failed to apply auth: %w", err)
		}
	}

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

func (c *Client) UpdateAuthToken(newToken string) {
	if c.clusterAuth != nil {
		c.clusterAuth.UpdateToken(newToken)
	}
}

func (c *Client) AddAndPin(ctx context.Context, filePath string) (string, error) {
	cid, err := c.AddFile(ctx, filePath)
	if err != nil {
		return "", err
	}

	_ = c.Pin(ctx, cid)

	_ = c.ClusterPin(ctx, cid)

	return cid, nil
}

func (c *Client) AddDirectoryAndPin(ctx context.Context, dirPath string) (string, error) {
	cid, err := c.AddDirectory(ctx, dirPath)
	if err != nil {
		return "", err
	}

	_ = c.Pin(ctx, cid)

	_ = c.ClusterPin(ctx, cid)

	return cid, nil
}

func (c *Client) Cat(ctx context.Context, cid string) (io.ReadCloser, error) {
	if !c.enabled {
		return nil, fmt.Errorf("IPFS not enabled")
	}

	reqURL := c.apiURL + "/api/v0/cat?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to cat from IPFS: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("IPFS cat failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return resp.Body, nil
}

// ObjectStatResult holds the result of an IPFS object/stat call.
type ObjectStatResult struct {
	Hash           string `json:"Hash"`
	NumLinks       int    `json:"NumLinks"`
	BlockSize      int64  `json:"BlockSize"`
	LinksSize      int64  `json:"LinksSize"`
	DataSize       int64  `json:"DataSize"`
	CumulativeSize int64  `json:"CumulativeSize"`
}

// ObjectStat retrieves metadata about an IPFS object via the object/stat API.
func (c *Client) ObjectStat(ctx context.Context, cid string) (*ObjectStatResult, error) {
	if !c.enabled {
		return nil, fmt.Errorf("IPFS not enabled")
	}

	reqURL := c.apiURL + "/api/v0/object/stat?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to stat IPFS object: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, fmt.Errorf("IPFS object/stat failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var result ObjectStatResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode object/stat response: %w", err)
	}
	return &result, nil
}

func (c *Client) Unpin(ctx context.Context, cid string) error {
	if !c.enabled {
		return fmt.Errorf("IPFS not enabled")
	}

	reqURL := c.apiURL + "/api/v0/pin/rm?arg=" + url.QueryEscape(cid)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to unpin from IPFS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		body := string(bodyBytes)
		if strings.Contains(body, "not pinned") || strings.Contains(body, "pinned indirectly") {
			return nil
		}
		return fmt.Errorf("IPFS unpin failed with status %d: %s", resp.StatusCode, body)
	}

	return nil
}

func (c *Client) IsPinned(ctx context.Context, cid string) (bool, error) {
	if !c.enabled {
		return false, fmt.Errorf("IPFS not enabled")
	}

	reqURL := c.apiURL + "/api/v0/pin/ls?arg=" + url.QueryEscape(cid) + "&type=recursive"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
	if err != nil {
		return false, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to check pin status: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, nil
	}

	return true, nil
}

func (c *Client) AddReader(ctx context.Context, name string, reader io.Reader) (string, error) {
	if !c.enabled {
		return "", fmt.Errorf("IPFS not enabled")
	}

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", name)
	if err != nil {
		return "", fmt.Errorf("failed to create form file: %w", err)
	}

	if _, err := io.Copy(fw, reader); err != nil {
		return "", fmt.Errorf("failed to copy content: %w", err)
	}
	_ = mw.Close()

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
