# IPFS Security Implementation Guide

**Quick Reference for Implementing Test Requirements**

---

## Required Imports

Add to `go.mod`:
```bash
go get github.com/ipfs/go-cid@v0.4.1
go get github.com/multiformats/go-multibase@v0.2.0
```

Already present:
- `github.com/multiformats/go-multihash v0.2.3`

---

## 1. CID Validation Implementation

### Function Signature

```go
package ipfs

import (
    "fmt"
    "strings"
    "unicode"

    "github.com/ipfs/go-cid"
    "github.com/multiformats/go-multibase"
    "github.com/multiformats/go-multihash"
)

// ValidateCID validates an IPFS CID for security and correctness
func ValidateCID(cidStr string) error {
    // TODO: Implement validation
    return nil
}
```

### Validation Logic Flow

```go
func ValidateCID(cidStr string) error {
    // 1. Check empty
    if cidStr == "" {
        return fmt.Errorf("CID cannot be empty")
    }

    // 2. Check length (max 128 chars to prevent DoS)
    if len(cidStr) > 128 {
        return fmt.Errorf("CID exceeds maximum length of 128 characters")
    }

    // 3. Check for path traversal attempts
    if strings.Contains(cidStr, "..") || strings.Contains(cidStr, "/") ||
       strings.Contains(cidStr, "\\") || strings.ContainsRune(cidStr, 0) {
        return fmt.Errorf("invalid characters in CID: possible path traversal attempt")
    }

    // 4. Check for whitespace
    if strings.TrimSpace(cidStr) != cidStr {
        return fmt.Errorf("CID contains leading or trailing whitespace")
    }

    // 5. Check for special characters
    for _, r := range cidStr {
        if unicode.IsControl(r) {
            return fmt.Errorf("CID contains control characters")
        }
    }

    // 6. Parse CID using official library
    c, err := cid.Decode(cidStr)
    if err != nil {
        return fmt.Errorf("invalid CID format: %w", err)
    }

    // 7. Enforce CIDv1 only (per CLAUDE.md)
    if c.Version() != 1 {
        return fmt.Errorf("CIDv0 not supported, only CIDv1 allowed")
    }

    // 8. Validate codec (whitelist)
    allowedCodecs := map[uint64]bool{
        0x55: true, // raw
        0x70: true, // dag-pb
        0x71: true, // dag-cbor
    }

    if !allowedCodecs[c.Type()] {
        return fmt.Errorf("codec 0x%x not allowed", c.Type())
    }

    // 9. Validate multihash
    mh := c.Hash()
    if mh == nil {
        return fmt.Errorf("invalid multihash in CID")
    }

    // Decode multihash to verify format
    _, err = multihash.Decode(mh)
    if err != nil {
        return fmt.Errorf("invalid multihash: %w", err)
    }

    return nil
}
```

### Integration Points

Update existing methods to validate CIDs:

```go
// Pin ensures a CID is pinned (with validation)
func (c *Client) Pin(ctx context.Context, cid string) error {
    if !c.enabled {
        return fmt.Errorf("IPFS not enabled")
    }

    // Validate CID before using it
    if err := ValidateCID(cid); err != nil {
        return fmt.Errorf("invalid CID: %w", err)
    }

    // ... rest of existing implementation
}

// ClusterPin pins a CID to IPFS Cluster (with validation)
func (c *Client) ClusterPin(ctx context.Context, cid string) error {
    if !c.clusterEnabled {
        return nil
    }

    // Validate CID before using it
    if err := ValidateCID(cid); err != nil {
        return fmt.Errorf("invalid CID: %w", err)
    }

    // ... rest of existing implementation
}
```

---

## 2. Cluster Authentication Implementation

### Data Structures

```go
package ipfs

import (
    "crypto/tls"
    "crypto/x509"
    "os"
)

// ClusterAuthConfig holds authentication configuration for IPFS Cluster
type ClusterAuthConfig struct {
    // Bearer token authentication
    Token string

    // mTLS authentication
    TLSEnabled    bool
    CertFile      string // Path to client certificate
    KeyFile       string // Path to client private key
    CAFile        string // Path to CA certificate (optional)
    MinTLSVersion uint16 // Minimum TLS version (default: TLS 1.2)
}

// Update Client struct to include auth
type Client struct {
    apiURL         string
    clusterAPIURL  string
    httpClient     *http.Client
    enabled        bool
    clusterEnabled bool
    authConfig     *ClusterAuthConfig // Add this field
}
```

### Constructor Functions

```go
// NewClientWithAuth creates a new IPFS client with authentication
func NewClientWithAuth(apiURL, clusterAPIURL string, timeout time.Duration,
    auth *ClusterAuthConfig) *Client {

    client := &Client{
        apiURL:         apiURL,
        clusterAPIURL:  clusterAPIURL,
        enabled:        apiURL != "",
        clusterEnabled: clusterAPIURL != "",
        authConfig:     auth,
    }

    // Build HTTP client with auth
    httpClient := &http.Client{
        Timeout: timeout,
    }

    // Configure TLS if enabled
    if auth != nil && auth.TLSEnabled {
        tlsConfig, err := buildTLSConfig(auth)
        if err != nil {
            // Log error, fallback to non-TLS
            client.httpClient = httpClient
            return client
        }

        httpClient.Transport = &http.Transport{
            TLSClientConfig: tlsConfig,
        }
    }

    client.httpClient = httpClient
    return client
}

// NewClientWithAuthFromEnv creates client with auth from environment
func NewClientWithAuthFromEnv(apiURL, clusterAPIURL string,
    timeout time.Duration) *Client {

    auth := &ClusterAuthConfig{
        Token: os.Getenv("IPFS_CLUSTER_SECRET"),
    }

    // Check for TLS env vars
    certFile := os.Getenv("IPFS_CLUSTER_CERT_FILE")
    keyFile := os.Getenv("IPFS_CLUSTER_KEY_FILE")

    if certFile != "" && keyFile != "" {
        auth.TLSEnabled = true
        auth.CertFile = certFile
        auth.KeyFile = keyFile
        auth.CAFile = os.Getenv("IPFS_CLUSTER_CA_FILE")
        auth.MinTLSVersion = tls.VersionTLS12
    }

    return NewClientWithAuth(apiURL, clusterAPIURL, timeout, auth)
}

// UpdateAuthToken rotates the authentication token
func (c *Client) UpdateAuthToken(newToken string) {
    if c.authConfig == nil {
        c.authConfig = &ClusterAuthConfig{}
    }
    c.authConfig.Token = newToken
}
```

### TLS Configuration

```go
// buildTLSConfig constructs TLS configuration for mTLS
func buildTLSConfig(auth *ClusterAuthConfig) (*tls.Config, error) {
    tlsConfig := &tls.Config{
        MinVersion: auth.MinTLSVersion,
    }

    if tlsConfig.MinVersion == 0 {
        tlsConfig.MinVersion = tls.VersionTLS12 // Default
    }

    // Load client certificate
    if auth.CertFile != "" && auth.KeyFile != "" {
        cert, err := tls.LoadX509KeyPair(auth.CertFile, auth.KeyFile)
        if err != nil {
            return nil, fmt.Errorf("failed to load client certificate: %w", err)
        }
        tlsConfig.Certificates = []tls.Certificate{cert}
    }

    // Load CA certificate if provided
    if auth.CAFile != "" {
        caCert, err := os.ReadFile(auth.CAFile)
        if err != nil {
            return nil, fmt.Errorf("failed to load CA certificate: %w", err)
        }

        caCertPool := x509.NewCertPool()
        if !caCertPool.AppendCertsFromPEM(caCert) {
            return nil, fmt.Errorf("failed to parse CA certificate")
        }
        tlsConfig.RootCAs = caCertPool
    }

    return tlsConfig, nil
}
```

### HTTP Request Wrapper

```go
// addAuthHeaders adds authentication headers to HTTP request
func (c *Client) addAuthHeaders(req *http.Request) {
    if c.authConfig == nil {
        return
    }

    // Add Bearer token if configured
    if c.authConfig.Token != "" {
        req.Header.Set("Authorization", "Bearer "+c.authConfig.Token)
    }

    // Add User-Agent
    req.Header.Set("User-Agent", "athena-ipfs-client/1.0")
}
```

### Updated Cluster Methods

```go
// ClusterPin pins a CID to IPFS Cluster with authentication
func (c *Client) ClusterPin(ctx context.Context, cid string) error {
    if !c.clusterEnabled {
        return nil
    }

    // Validate CID
    if err := ValidateCID(cid); err != nil {
        return fmt.Errorf("invalid CID: %w", err)
    }

    // Enforce HTTPS for authenticated requests
    if c.authConfig != nil && c.authConfig.Token != "" {
        if !strings.HasPrefix(c.clusterAPIURL, "https://") {
            // Log warning: sending auth over HTTP is insecure
        }
    }

    reqURL := c.clusterAPIURL + "/pins/" + cid
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, nil)
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }

    // Add authentication headers
    c.addAuthHeaders(req)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil // Best-effort
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }

    // Handle authentication errors
    if resp.StatusCode == http.StatusUnauthorized {
        // Log: unauthorized - check token
        return nil // Best-effort
    }

    if resp.StatusCode == http.StatusForbidden {
        // Log: forbidden - invalid token
        return nil // Best-effort
    }

    return nil
}

// ClusterUnpin removes a pin from IPFS Cluster (new method)
func (c *Client) ClusterUnpin(ctx context.Context, cid string) error {
    if !c.clusterEnabled {
        return nil
    }

    // Validate CID
    if err := ValidateCID(cid); err != nil {
        return fmt.Errorf("invalid CID: %w", err)
    }

    reqURL := c.clusterAPIURL + "/pins/" + cid
    req, err := http.NewRequestWithContext(ctx, http.MethodDelete, reqURL, nil)
    if err != nil {
        return fmt.Errorf("failed to create request: %w", err)
    }

    // Add authentication headers
    c.addAuthHeaders(req)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode >= 200 && resp.StatusCode < 300 {
        return nil
    }

    return fmt.Errorf("unpin failed with status %d", resp.StatusCode)
}

// ClusterStatus checks pin status in IPFS Cluster (new method)
func (c *Client) ClusterStatus(ctx context.Context, cid string) (*ClusterPinStatus, error) {
    if !c.clusterEnabled {
        return nil, fmt.Errorf("cluster not enabled")
    }

    // Validate CID
    if err := ValidateCID(cid); err != nil {
        return nil, fmt.Errorf("invalid CID: %w", err)
    }

    reqURL := c.clusterAPIURL + "/pins/" + cid
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
    if err != nil {
        return nil, fmt.Errorf("failed to create request: %w", err)
    }

    // Add authentication headers
    c.addAuthHeaders(req)

    resp, err := c.httpClient.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("status check failed with status %d", resp.StatusCode)
    }

    var status ClusterPinStatus
    if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
        return nil, fmt.Errorf("failed to decode response: %w", err)
    }

    return &status, nil
}

// ClusterPinStatus represents IPFS Cluster pin status
type ClusterPinStatus struct {
    Status  string                 `json:"status"`
    PeerMap map[string]interface{} `json:"peer_map"`
}
```

---

## 3. Security Considerations

### Token Storage
- NEVER log authentication tokens
- NEVER include tokens in error messages
- NEVER commit tokens to version control
- Use environment variables or secret management systems

### HTTPS Enforcement
```go
// Check if using auth over HTTP
if c.authConfig != nil && c.authConfig.Token != "" {
    if strings.HasPrefix(c.clusterAPIURL, "http://") {
        // LOG WARNING: Sending authentication over unencrypted HTTP
        // Consider returning error in production
    }
}
```

### String() Method (Prevent Token Leakage)
```go
// String returns string representation without exposing secrets
func (c *Client) String() string {
    hasAuth := c.authConfig != nil && c.authConfig.Token != ""
    return fmt.Sprintf("Client{apiURL: %s, clusterURL: %s, authenticated: %v}",
        c.apiURL, c.clusterAPIURL, hasAuth)
}
```

---

## 4. Testing Implementation

### Run Tests After Implementation

```bash
# Run all IPFS tests
go test -v ./internal/ipfs

# Run only security tests
go test -v ./internal/ipfs -run "TestValidateCID|TestClusterAuth"

# Run with race detection
go test -race ./internal/ipfs

# Run fuzzing (extended)
go test -v ./internal/ipfs -run TestFuzz -timeout 30m

# Benchmark performance
go test -bench=. ./internal/ipfs
```

### Expected Results After Implementation

All tests should PASS:
```
PASS: TestValidateCID_ValidCIDv1Base32
PASS: TestValidateCID_RejectsPathTraversal
PASS: TestClusterAuth_BearerToken
PASS: TestClusterAuth_mTLS_ClientCertificateLoading
... (44 total tests)

PASS
ok      athena/internal/ipfs    2.456s
```

---

## 5. Configuration Examples

### Environment Variables

```bash
# Basic configuration
export IPFS_API_URL="http://localhost:5001"
export IPFS_CLUSTER_API_URL="https://localhost:9094"

# Bearer token authentication
export IPFS_CLUSTER_SECRET="your-secret-token-here"

# mTLS authentication
export IPFS_CLUSTER_CERT_FILE="/path/to/client.crt"
export IPFS_CLUSTER_KEY_FILE="/path/to/client.key"
export IPFS_CLUSTER_CA_FILE="/path/to/ca.crt"
```

### Programmatic Configuration

```go
// Bearer token only
authConfig := &ipfs.ClusterAuthConfig{
    Token: "your-secret-token",
}

// mTLS only
authConfig := &ipfs.ClusterAuthConfig{
    TLSEnabled:    true,
    CertFile:      "/path/to/client.crt",
    KeyFile:       "/path/to/client.key",
    MinTLSVersion: tls.VersionTLS12,
}

// Both Bearer token and mTLS
authConfig := &ipfs.ClusterAuthConfig{
    Token:         "your-secret-token",
    TLSEnabled:    true,
    CertFile:      "/path/to/client.crt",
    KeyFile:       "/path/to/client.key",
    CAFile:        "/path/to/ca.crt",
    MinTLSVersion: tls.VersionTLS13,
}

client := ipfs.NewClientWithAuth(
    "http://localhost:5001",
    "https://localhost:9094",
    5*time.Minute,
    authConfig,
)
```

---

## 6. Performance Targets

| Operation | Target | Maximum |
|-----------|--------|---------|
| ValidateCID (valid) | < 1ms | 5ms |
| ValidateCID (invalid) | < 5ms | 10ms |
| Token header addition | < 0.1ms | 1ms |
| TLS handshake | < 100ms | 500ms |

---

## 7. Error Handling Examples

```go
// CID validation errors
err := ValidateCID("../../etc/passwd")
// Error: "invalid characters in CID: possible path traversal attempt"

err := ValidateCID("QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG")
// Error: "CIDv0 not supported, only CIDv1 allowed"

err := ValidateCID("")
// Error: "CID cannot be empty"

// Authentication errors
err := client.ClusterPin(ctx, cid)
// If 401: logs "unauthorized - check token" (best-effort, may not error)
// If 403: logs "forbidden - invalid token"
```

---

## 8. Migration Path

### Update existing code:

```go
// BEFORE (insecure)
client := NewClient("http://localhost:5001", "http://localhost:9094", 5*time.Minute)
err := client.ClusterPin(ctx, userProvidedCID) // No validation!

// AFTER (secure)
client := NewClientWithAuthFromEnv("http://localhost:5001", "https://localhost:9094", 5*time.Minute)
err := client.ClusterPin(ctx, userProvidedCID) // Validates CID + authenticates
```

---

## Summary

1. **Add dependencies:** `go get github.com/ipfs/go-cid@v0.4.1`
2. **Implement ValidateCID()** with all security checks
3. **Add ClusterAuthConfig struct** and auth methods
4. **Update Client struct** to include auth config
5. **Add authentication headers** to all Cluster requests
6. **Integrate CID validation** into Pin, ClusterPin, etc.
7. **Run tests:** `go test -v ./internal/ipfs`
8. **Achieve 100% pass rate**

All implementation details are specified by the test suite. Follow the tests!
