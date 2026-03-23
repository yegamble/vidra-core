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

// --- Helper: create service wired to a real httptest gateway ---

func newIPFSFirstServiceWithGateway(gatewayURL string) *Service {
	cfg := &config.Config{
		EnableIPFSStreaming:            true,
		IPFSGatewayURLs:                []string{gatewayURL},
		IPFSStreamingTimeout:           5 * time.Second,
		IPFSStreamingMaxRetries:        2,
		IPFSGatewayHealthCheckInterval: 0, // no background health checks in tests
		IPFSStreamingPreferLocal:       false,
		IPFSStreamingFallbackToLocal:   true,
		IPFSStreamingBufferSize:        32768,
	}
	return NewService(cfg)
}

func newIPFSFirstNoFallbackServiceWithGateway(gatewayURL string) *Service {
	cfg := &config.Config{
		EnableIPFSStreaming:            true,
		IPFSGatewayURLs:                []string{gatewayURL},
		IPFSStreamingTimeout:           5 * time.Second,
		IPFSStreamingMaxRetries:        2,
		IPFSGatewayHealthCheckInterval: 0,
		IPFSStreamingPreferLocal:       false,
		IPFSStreamingFallbackToLocal:   false,
		IPFSStreamingBufferSize:        32768,
	}
	return NewService(cfg)
}

// ==================== StreamFile IPFS-first mode tests ====================

func TestStreamFile_IPFSFirst_SuccessWithCID(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ipfs video data"))
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, "/nonexistent/file.mp4", "QmTestCID")
	assert.NoError(t, err)
	assert.Equal(t, "ipfs", w.Header().Get("X-Content-Source"))
	assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
	assert.Contains(t, w.Body.String(), "ipfs video data")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.IPFSRequests)
	assert.Equal(t, int64(1), metrics.IPFSSuccesses)
}

func TestStreamFile_IPFSFirst_IPFSFailsFallbackToLocal(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	// Create local file for fallback
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("local fallback data"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, tmpFile, "QmTestCID")
	assert.NoError(t, err)
	assert.Equal(t, "local", w.Header().Get("X-Content-Source"))

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.IPFSRequests)
	assert.Equal(t, int64(1), metrics.IPFSFailures)
	assert.Equal(t, int64(1), metrics.LocalRequests)
	assert.Equal(t, int64(1), metrics.LocalSuccesses)
}

func TestStreamFile_IPFSFirst_IPFSFailsFallbackDisabled(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()

	svc := newIPFSFirstNoFallbackServiceWithGateway(gateway.URL)
	defer svc.Close()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("local data"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, tmpFile, "QmTestCID")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "IPFS streaming failed and fallback disabled or unavailable")
}

func TestStreamFile_IPFSFirst_EmptyCID_FallbackToLocal(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call IPFS gateway when CID is empty")
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("local data"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	// Empty CID, fallback enabled, local file exists
	err := svc.StreamFile(context.Background(), w, r, tmpFile, "")
	assert.NoError(t, err)
	assert.Equal(t, "local", w.Header().Get("X-Content-Source"))
}

func TestStreamFile_IPFSFirst_EmptyCID_NoLocal_FallbackDisabled(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("should not call IPFS gateway when CID is empty")
	}))
	defer gateway.Close()

	svc := newIPFSFirstNoFallbackServiceWithGateway(gateway.URL)
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.StreamFile(context.Background(), w, r, "/nonexistent/file.mp4", "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "IPFS streaming failed and fallback disabled or unavailable")
}

// ==================== serveFromIPFS tests via real HTTP ====================

func TestServeFromIPFS_RangeRequest(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("partial"))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("full"))
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("Range", "bytes=0-1023")

	err := svc.serveFromIPFS(context.Background(), w, r, "QmTestCID")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, w.Code)
	assert.Equal(t, "partial", w.Body.String())
	assert.Equal(t, "ipfs", w.Header().Get("X-Content-Source"))
}

func TestServeFromIPFS_NoRangeRequest(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get("Range"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("full content"))
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.serveFromIPFS(context.Background(), w, r, "QmTestCID")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "full content", w.Body.String())
}

func TestServeFromIPFS_GatewayError(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.serveFromIPFS(context.Background(), w, r, "QmTestCID")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch from IPFS")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.IPFSRequests)
	assert.Equal(t, int64(1), metrics.IPFSFailures)
}

// ==================== serveFromLocal tests ====================

func TestServeFromLocal_Success(t *testing.T) {
	svc := newDisabledService()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "video.mp4")
	_ = os.WriteFile(tmpFile, []byte("video content here"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.serveFromLocal(w, r, tmpFile)
	assert.NoError(t, err)
	assert.Equal(t, "local", w.Header().Get("X-Content-Source"))
	assert.Equal(t, "bytes", w.Header().Get("Accept-Ranges"))
	assert.Contains(t, w.Body.String(), "video content here")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.LocalRequests)
	assert.Equal(t, int64(1), metrics.LocalSuccesses)
}

func TestServeFromLocal_FileNotFound(t *testing.T) {
	svc := newDisabledService()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.serveFromLocal(w, r, "/nonexistent/video.mp4")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open local file")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.LocalRequests)
	assert.Equal(t, int64(1), metrics.LocalFailures)
}

func TestServeFromLocal_RangeRequest(t *testing.T) {
	svc := newDisabledService()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "video.mp4")
	content := "0123456789abcdef"
	_ = os.WriteFile(tmpFile, []byte(content), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	r.Header.Set("Range", "bytes=0-3")

	err := svc.serveFromLocal(w, r, tmpFile)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, w.Code)
	assert.Equal(t, "0123", w.Body.String())
}

// ==================== GetMetrics after operations ====================

func TestGetMetrics_AfterLocalOperations(t *testing.T) {
	svc := newDisabledService()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("data"), 0600)

	// Two successful local serves
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/stream", nil)
		_ = svc.StreamFile(context.Background(), w, r, tmpFile, "")
	}

	// One failed local serve
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	_ = svc.StreamFile(context.Background(), w, r, "/nonexistent/file.mp4", "")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(3), metrics.LocalRequests)
	assert.Equal(t, int64(2), metrics.LocalSuccesses)
	assert.Equal(t, int64(1), metrics.LocalFailures)
}

func TestGetMetrics_AfterIPFSOperations(t *testing.T) {
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data"))
	}))
	defer gateway.Close()

	svc := newIPFSFirstServiceWithGateway(gateway.URL)
	defer svc.Close()

	// One successful IPFS serve
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)
	_ = svc.StreamFile(context.Background(), w, r, "/nonexistent", "QmCID1")

	metrics := svc.GetMetrics()
	assert.Equal(t, int64(1), metrics.IPFSRequests)
	assert.Equal(t, int64(1), metrics.IPFSSuccesses)
	assert.Equal(t, int64(0), metrics.IPFSFailures)
}

// ==================== tryServeFromLocal tests ====================

func TestTryServeFromLocal_FileExists(t *testing.T) {
	svc := newDisabledService()

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.mp4")
	_ = os.WriteFile(tmpFile, []byte("data"), 0600)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.tryServeFromLocal(w, r, tmpFile)
	assert.NoError(t, err)
}

func TestTryServeFromLocal_FileNotExists(t *testing.T) {
	svc := newDisabledService()

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/stream", nil)

	err := svc.tryServeFromLocal(w, r, "/nonexistent/file.mp4")
	assert.Error(t, err)
}
