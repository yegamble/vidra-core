package ipfs

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestClusterAuth_HTTPSEnforcement_BearerTokenOverHTTP tests that bearer tokens are blocked over HTTP
func TestClusterAuth_HTTPSEnforcement_BearerTokenOverHTTP(t *testing.T) {
	// Create an HTTP (not HTTPS) test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Request should never reach server when HTTP is used with bearer token")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Verify the test server is using HTTP
	require.True(t, strings.HasPrefix(server.URL, "http://"), "Test server should use HTTP")

	// Create client with bearer token authentication over HTTP
	auth := &ClusterAuthConfig{
		Token: "secret-token-should-not-be-sent",
	}

	client := NewClientWithAuth(server.URL, server.URL, 5*time.Second, auth)

	// Client should be created but cluster should be disabled for security
	require.NotNil(t, client)
	assert.False(t, client.clusterEnabled, "Cluster should be disabled when bearer token is used over HTTP")
	assert.Nil(t, client.clusterAuth, "Cluster auth should be nil when HTTP is detected")
}

// TestClusterAuth_HTTPSEnforcement_ValidateSecureTransport tests the security validation method
func TestClusterAuth_HTTPSEnforcement_ValidateSecureTransport(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		clusterURL  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "bearer token over HTTPS - allowed",
			token:      "secret-token",
			clusterURL: "https://cluster.example.com:9094",
			wantErr:    false,
		},
		{
			name:        "bearer token over HTTP - forbidden",
			token:       "secret-token",
			clusterURL:  "http://cluster.example.com:9094",
			wantErr:     true,
			errContains: "CRITICAL SECURITY ERROR",
		},
		{
			name:       "no token over HTTP - allowed (unauthenticated)",
			token:      "",
			clusterURL: "http://localhost:9094",
			wantErr:    false,
		},
		{
			name:       "no token over HTTPS - allowed",
			token:      "",
			clusterURL: "https://cluster.example.com:9094",
			wantErr:    false,
		},
		{
			name:        "bearer token over HTTP localhost - still forbidden",
			token:       "dev-token",
			clusterURL:  "http://localhost:9094",
			wantErr:     true,
			errContains: "Bearer token authentication over HTTP is forbidden",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &ClusterAuthConfig{
				Token: tt.token,
			}

			err := auth.ValidateSecureTransport(tt.clusterURL)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestClusterAuth_HTTPSEnforcement_mTLSOverHTTP tests mTLS behavior over HTTP
func TestClusterAuth_HTTPSEnforcement_mTLSOverHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// mTLS without bearer token over HTTP should work (mTLS implies encryption)
	tmpDir := t.TempDir()
	// Note: These are dummy files for testing - real certs would fail to load
	auth := &ClusterAuthConfig{
		TLSEnabled: true,
		CertFile:   tmpDir + "/cert.pem",
		KeyFile:    tmpDir + "/key.pem",
		Token:      "", // No bearer token
	}

	// Should not trigger HTTP enforcement since no bearer token
	err := auth.ValidateSecureTransport(server.URL)
	assert.NoError(t, err, "mTLS without bearer token should not trigger HTTP check")
}

// TestClusterAuth_HTTPSEnforcement_BothAuthMethodsOverHTTP tests combined auth over HTTP
func TestClusterAuth_HTTPSEnforcement_BothAuthMethodsOverHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("Request should not be sent when bearer token is used over HTTP")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmpDir := t.TempDir()
	auth := &ClusterAuthConfig{
		TLSEnabled: true,
		CertFile:   tmpDir + "/cert.pem",
		KeyFile:    tmpDir + "/key.pem",
		Token:      "secret-token", // Bearer token present
	}

	// Bearer token over HTTP should fail regardless of mTLS
	err := auth.ValidateSecureTransport(server.URL)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Bearer token authentication over HTTP is forbidden")
}

// TestClusterAuth_HTTPSEnforcement_RealWorldScenarios tests production scenarios
func TestClusterAuth_HTTPSEnforcement_RealWorldScenarios(t *testing.T) {
	tests := []struct {
		name        string
		clusterURL  string
		token       string
		shouldBlock bool
		description string
	}{
		{
			name:        "production HTTPS cluster with token",
			clusterURL:  "https://ipfs-cluster.production.com:9094",
			token:       "prod-secret-token",
			shouldBlock: false,
			description: "Legitimate production setup - should work",
		},
		{
			name:        "development HTTP cluster with token",
			clusterURL:  "http://localhost:9094",
			token:       "dev-token",
			shouldBlock: true,
			description: "Even in development, bearer tokens over HTTP are forbidden",
		},
		{
			name:        "development HTTP cluster without token",
			clusterURL:  "http://localhost:9094",
			token:       "",
			shouldBlock: false,
			description: "Development without auth is allowed but not recommended",
		},
		{
			name:        "internal network HTTP with token",
			clusterURL:  "http://192.168.1.100:9094",
			token:       "internal-token",
			shouldBlock: true,
			description: "Internal networks still require HTTPS for bearer tokens",
		},
		{
			name:        "kubernetes service HTTP with token",
			clusterURL:  "http://ipfs-cluster-service:9094",
			token:       "k8s-secret-token",
			shouldBlock: true,
			description: "K8s internal services should use TLS/mTLS, not bearer over HTTP",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := &ClusterAuthConfig{
				Token: tt.token,
			}

			err := auth.ValidateSecureTransport(tt.clusterURL)
			if tt.shouldBlock {
				assert.Error(t, err, tt.description)
			} else {
				assert.NoError(t, err, tt.description)
			}
		})
	}
}

// TestClusterAuth_HTTPSEnforcement_ClientCreationPrevention tests that client creation fails safely
func TestClusterAuth_HTTPSEnforcement_ClientCreationPrevention(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("No requests should be sent to HTTP server with bearer token")
	}))
	defer httpServer.Close()

	auth := &ClusterAuthConfig{
		Token: "secret-token",
	}

	client := NewClientWithAuth(httpServer.URL, httpServer.URL, 5*time.Second, auth)

	// Client object is created but cluster operations are disabled
	assert.NotNil(t, client)
	assert.False(t, client.clusterEnabled, "Cluster should be disabled")

	// Attempt cluster operations - should be no-op
	ctx := context.Background()
	err := client.ClusterPin(ctx, "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi")

	// Should not error (best-effort) but also should not send request
	assert.NoError(t, err, "ClusterPin should be no-op when cluster is disabled")
}

// TestClusterAuth_HTTPSEnforcement_TokenRotationSafety tests token rotation with HTTP
func TestClusterAuth_HTTPSEnforcement_TokenRotationSafety(t *testing.T) {
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("No requests should be sent")
	}))
	defer httpServer.Close()

	// Start without token
	auth := &ClusterAuthConfig{
		Token: "",
	}

	client := NewClientWithAuth(httpServer.URL, httpServer.URL, 5*time.Second, auth)
	assert.NotNil(t, client)

	// Rotate to add a token - should not enable HTTP bearer auth
	client.UpdateAuthToken("new-secret-token")

	// Even with token rotation, HTTP should remain blocked
	if client.clusterAuth != nil {
		err := client.clusterAuth.ValidateSecureTransport(httpServer.URL)
		if client.clusterAuth.Token != "" {
			assert.Error(t, err, "Token rotation should not enable HTTP bearer auth")
		}
	}
}

// TestClusterAuth_HTTPSEnforcement_ConfigurationFromEnvironment tests env var security
func TestClusterAuth_HTTPSEnforcement_ConfigurationFromEnvironment(t *testing.T) {
	// Set environment variables for HTTP cluster with token
	t.Setenv("IPFS_CLUSTER_SECRET", "env-secret-token")

	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("No requests should be sent to HTTP server")
	}))
	defer httpServer.Close()

	client := NewClientWithAuthFromEnv(httpServer.URL, httpServer.URL, 5*time.Second)

	// Should be created but cluster disabled
	assert.NotNil(t, client)
	assert.False(t, client.clusterEnabled, "Cluster should be disabled when env token used over HTTP")
}

// TestClusterAuth_HTTPSEnforcement_OperationBlocking tests that operations are blocked
func TestClusterAuth_HTTPSEnforcement_OperationBlocking(t *testing.T) {
	requestReceived := false

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestReceived = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	auth := &ClusterAuthConfig{
		Token: "secret-token",
	}

	client := NewClientWithAuth(server.URL, server.URL, 5*time.Second, auth)
	ctx := context.Background()

	// Try all cluster operations
	testCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

	_ = client.ClusterPin(ctx, testCID)
	_ = client.ClusterUnpin(ctx, testCID)
	_, _ = client.ClusterStatus(ctx, testCID)

	// No requests should have been sent
	assert.False(t, requestReceived, "No cluster operations should be sent over HTTP with bearer token")
}

// TestClusterAuth_HTTPSEnforcement_LoggingAndAuditing verifies security logging points
func TestClusterAuth_HTTPSEnforcement_LoggingAndAuditing(t *testing.T) {
	// This test documents where security logging should occur
	// In production, these points would emit security audit logs

	auth := &ClusterAuthConfig{
		Token: "secret-token",
	}

	// SECURITY LOGGING POINT 1: HTTP with bearer token detected
	err := auth.ValidateSecureTransport("http://cluster.local:9094")
	assert.Error(t, err)
	// Production should log: "SECURITY: Blocked bearer token transmission over HTTP"

	// SECURITY LOGGING POINT 2: Client creation with insecure config
	httpURL := "http://insecure-cluster:9094"
	client := NewClientWithAuth(httpURL, httpURL, 5*time.Second, auth)
	assert.False(t, client.clusterEnabled)
	// Production should log: "SECURITY: IPFS cluster disabled due to insecure HTTP configuration"

	// SECURITY LOGGING POINT 3: Attempted operation on disabled cluster
	ctx := context.Background()
	_ = client.ClusterPin(ctx, "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi")
	// Production should log: "SECURITY: Cluster operation blocked - cluster disabled for security"
}
