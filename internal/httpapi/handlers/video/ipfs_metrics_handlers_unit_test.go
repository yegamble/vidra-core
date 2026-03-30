package video

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	ucipfs "vidra-core/internal/usecase/ipfs_streaming"
)

func TestIPFSMetricsHandlers_UnitBranches(t *testing.T) {
	t.Run("disabled service returns 503", func(t *testing.T) {
		handler := NewIPFSMetricsHandlers(nil)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/ipfs/metrics", nil)
		rr := httptest.NewRecorder()
		handler.GetMetrics(rr, req)
		require.Equal(t, http.StatusServiceUnavailable, rr.Code)

		rr = httptest.NewRecorder()
		handler.GetGatewayHealth(rr, req)
		require.Equal(t, http.StatusServiceUnavailable, rr.Code)
	})

	t.Run("enabled service returns metrics and gateway health", func(t *testing.T) {
		cfg := &config.Config{
			EnableIPFSStreaming:            true,
			IPFSGatewayURLs:                []string{"https://example-gateway.invalid"},
			IPFSStreamingTimeout:           time.Second,
			IPFSStreamingMaxRetries:        1,
			IPFSGatewayHealthCheckInterval: 0,
		}
		service := ucipfs.NewService(cfg)
		t.Cleanup(service.Close)

		handler := NewIPFSMetricsHandlers(service)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/ipfs/metrics", nil)
		rr := httptest.NewRecorder()
		handler.GetMetrics(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

		var metricsResp map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &metricsResp))
		require.True(t, metricsResp["success"].(bool))
		metricsData := metricsResp["data"].(map[string]any)
		require.Contains(t, metricsData, "ipfs")
		require.Contains(t, metricsData, "local")
		require.Contains(t, metricsData, "cache")

		rr = httptest.NewRecorder()
		handler.GetGatewayHealth(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, "application/json", rr.Header().Get("Content-Type"))

		var gatewaysResp map[string]any
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &gatewaysResp))
		require.True(t, gatewaysResp["success"].(bool))
		gatewaysData := gatewaysResp["data"].(map[string]any)
		require.Contains(t, gatewaysData, "gateways")
	})
}
