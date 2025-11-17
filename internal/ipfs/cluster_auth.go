package ipfs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
)

// ClusterAuthConfig holds authentication configuration for IPFS Cluster
type ClusterAuthConfig struct {
	// Token for Bearer authentication
	Token string

	// TLS configuration
	TLSEnabled    bool
	CertFile      string
	KeyFile       string
	CACertFile    string
	MinTLSVersion uint16

	// Internal state
	mu sync.RWMutex
}

// NewClusterAuthConfig creates a new ClusterAuthConfig from environment variables
func NewClusterAuthConfig() *ClusterAuthConfig {
	token := os.Getenv("IPFS_CLUSTER_SECRET")
	certFile := os.Getenv("IPFS_CLUSTER_CLIENT_CERT")
	keyFile := os.Getenv("IPFS_CLUSTER_CLIENT_KEY")
	caCertFile := os.Getenv("IPFS_CLUSTER_CA_CERT")

	config := &ClusterAuthConfig{
		Token:         token,
		CertFile:      certFile,
		KeyFile:       keyFile,
		CACertFile:    caCertFile,
		TLSEnabled:    certFile != "" && keyFile != "",
		MinTLSVersion: tls.VersionTLS12,
	}

	return config
}

// Validate checks if the authentication configuration is valid
func (c *ClusterAuthConfig) Validate() error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	hasMTLSAuth := c.TLSEnabled && c.CertFile != "" && c.KeyFile != ""

	// At least one auth method should be configured
	// Note: Both can be empty for unauthenticated mode (development only)

	// Validate TLS files exist if TLS is enabled
	if hasMTLSAuth {
		if _, err := os.Stat(c.CertFile); err != nil {
			return fmt.Errorf("client certificate not found: %w", err)
		}
		if _, err := os.Stat(c.KeyFile); err != nil {
			return fmt.Errorf("client key not found: %w", err)
		}
		if c.CACertFile != "" {
			if _, err := os.Stat(c.CACertFile); err != nil {
				return fmt.Errorf("CA certificate not found: %w", err)
			}
		}
	}

	return nil
}

// ValidateSecureTransport verifies that authentication is not used over insecure HTTP
// This is a critical security check that MUST be performed before using bearer tokens
func (c *ClusterAuthConfig) ValidateSecureTransport(clusterURL string) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// If bearer token is configured, HTTPS is required
	if c.Token != "" && strings.HasPrefix(clusterURL, "http://") {
		return fmt.Errorf("CRITICAL SECURITY ERROR: Bearer token authentication over HTTP is forbidden - use HTTPS")
	}

	return nil
}

// ApplyToRequest adds authentication headers to an HTTP request
func (c *ClusterAuthConfig) ApplyToRequest(req *http.Request) error {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Add Bearer token if configured
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return nil
}

// GetHTTPClient creates an HTTP client with appropriate TLS configuration
func (c *ClusterAuthConfig) GetHTTPClient() (*http.Client, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	// Create TLS config
	tlsConfig := &tls.Config{
		MinVersion: c.MinTLSVersion,
	}

	// Load client certificates if mTLS is enabled
	if c.TLSEnabled && c.CertFile != "" && c.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}

		// Load CA certificate if provided
		if c.CACertFile != "" {
			caCert, err := os.ReadFile(c.CACertFile)
			if err != nil {
				return nil, fmt.Errorf("failed to read CA certificate: %w", err)
			}

			caCertPool := x509.NewCertPool()
			if !caCertPool.AppendCertsFromPEM(caCert) {
				return nil, fmt.Errorf("failed to parse CA certificate")
			}
			tlsConfig.RootCAs = caCertPool
		}
	}

	transport := &http.Transport{
		TLSClientConfig: tlsConfig,
	}

	return &http.Client{
		Transport: transport,
	}, nil
}

// UpdateToken atomically updates the bearer token (for token rotation)
func (c *ClusterAuthConfig) UpdateToken(newToken string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Token = newToken
}

// GetToken safely retrieves the current token
func (c *ClusterAuthConfig) GetToken() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Token
}

// String returns a safe string representation that doesn't expose secrets
func (c *ClusterAuthConfig) String() string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	parts := []string{"ClusterAuthConfig{"}

	if c.Token != "" {
		parts = append(parts, "Token: [REDACTED]")
	}

	if c.TLSEnabled {
		parts = append(parts, fmt.Sprintf("TLS: enabled, CertFile: %s", c.CertFile))
	}

	parts = append(parts, "}")
	return strings.Join(parts, " ")
}
