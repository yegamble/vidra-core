package ipfs_streaming

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"

	"github.com/stretchr/testify/assert"
)

func newDisabledService() *Service {
	cfg := &config.Config{
		EnableIPFSStreaming: false,
	}
	return NewService(cfg)
}

func newEnabledService() *Service {
	cfg := &config.Config{
		EnableIPFSStreaming:            true,
		IPFSGatewayURLs:                []string{"http://localhost:8080"},
		IPFSStreamingTimeout:           5 * time.Second,
		IPFSStreamingMaxRetries:        2,
		IPFSGatewayHealthCheckInterval: 60 * time.Second,
		IPFSStreamingPreferLocal:       true,
		IPFSStreamingFallbackToLocal:   true,
		IPFSStreamingBufferSize:        32768,
	}
	return NewService(cfg)
}

// --- Tests for NewService ---

func TestNewService_Disabled(t *testing.T) {
	svc := newDisabledService()
	assert.NotNil(t, svc)
	assert.False(t, svc.IsEnabled())
	assert.Nil(t, svc.gatewayClient)
}

func TestNewService_Enabled(t *testing.T) {
	svc := newEnabledService()
	defer svc.Close()
	assert.NotNil(t, svc)
	assert.True(t, svc.IsEnabled())
	assert.NotNil(t, svc.gatewayClient)
}

// --- Tests for IsEnabled ---

func TestIsEnabled(t *testing.T) {
	disabled := newDisabledService()
	assert.False(t, disabled.IsEnabled())

	enabled := newEnabledService()
	defer enabled.Close()
	assert.True(t, enabled.IsEnabled())
}

// --- Tests for GetMetrics ---

func TestGetMetrics_Initial(t *testing.T) {
	svc := newDisabledService()

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(0), metrics.IPFSRequests)
	assert.Equal(t, int64(0), metrics.IPFSSuccesses)
	assert.Equal(t, int64(0), metrics.IPFSFailures)
	assert.Equal(t, int64(0), metrics.LocalRequests)
	assert.Equal(t, int64(0), metrics.LocalSuccesses)
	assert.Equal(t, int64(0), metrics.LocalFailures)
	assert.Equal(t, int64(0), metrics.CacheHits)
	assert.Equal(t, int64(0), metrics.CacheMisses)
}

// --- Tests for GetGatewayHealth ---

func TestGetGatewayHealth_Disabled(t *testing.T) {
	svc := newDisabledService()

	health := svc.GetGatewayHealth()
	assert.NotNil(t, health)
	assert.Empty(t, health)
}

func TestGetGatewayHealth_Enabled(t *testing.T) {
	svc := newEnabledService()
	defer svc.Close()

	health := svc.GetGatewayHealth()
	assert.NotNil(t, health)
}

// --- Tests for Close ---

func TestClose_Disabled(t *testing.T) {
	svc := newDisabledService()
	// Should not panic
	svc.Close()
}

func TestClose_Enabled(t *testing.T) {
	svc := newEnabledService()
	// Should cleanly close
	svc.Close()
}

// --- Tests for StreamFile ---

func TestStreamFile_Disabled_ServeLocal(t *testing.T) {
	svc := newDisabledService()

	// Create a temp file to serve
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("video content"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, tmpFile, "")
	assert.NoError(t, err)
	assert.Equal(t, "local", w.Header().Get("X-Content-Source"))

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.LocalRequests)
	assert.Equal(t, int64(1), metrics.LocalSuccesses)
}

func TestStreamFile_Disabled_LocalFileNotFound(t *testing.T) {
	svc := newDisabledService()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, "/nonexistent/file.mp4", "")
	assert.Error(t, err)

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.LocalRequests)
	assert.Equal(t, int64(1), metrics.LocalFailures)
}

func TestStreamFile_Enabled_PreferLocal_FileExists(t *testing.T) {
	svc := newEnabledService()
	defer svc.Close()

	// Create a temp file
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("video content"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, tmpFile, "QmTest")
	assert.NoError(t, err)
	assert.Equal(t, "local", w.Header().Get("X-Content-Source"))
}

func TestStreamFile_Enabled_PreferLocal_NoLocalNoCID(t *testing.T) {
	svc := newEnabledService()
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, "/nonexistent/file.mp4", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "both local and IPFS streaming failed")
}
