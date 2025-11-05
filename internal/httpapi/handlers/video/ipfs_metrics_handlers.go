package video

import (
	"encoding/json"
	"net/http"

	ucipfs "athena/internal/usecase/ipfs_streaming"
)

// IPFSMetricsHandlers handles IPFS streaming metrics endpoints
type IPFSMetricsHandlers struct {
	ipfsService *ucipfs.Service
}

// NewIPFSMetricsHandlers creates a new IPFS metrics handler
func NewIPFSMetricsHandlers(ipfsService *ucipfs.Service) *IPFSMetricsHandlers {
	return &IPFSMetricsHandlers{
		ipfsService: ipfsService,
	}
}

// GetMetrics returns IPFS streaming metrics
func (h *IPFSMetricsHandlers) GetMetrics(w http.ResponseWriter, r *http.Request) {
	if h.ipfsService == nil || !h.ipfsService.IsEnabled() {
		http.Error(w, "IPFS streaming not enabled", http.StatusServiceUnavailable)
		return
	}

	metrics := h.ipfsService.GetMetrics()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"ipfs": map[string]interface{}{
			"requests":  metrics.IPFSRequests,
			"successes": metrics.IPFSSuccesses,
			"failures":  metrics.IPFSFailures,
		},
		"local": map[string]interface{}{
			"requests":  metrics.LocalRequests,
			"successes": metrics.LocalSuccesses,
			"failures":  metrics.LocalFailures,
		},
		"cache": map[string]interface{}{
			"hits":   metrics.CacheHits,
			"misses": metrics.CacheMisses,
		},
	})
}

// GetGatewayHealth returns IPFS gateway health status
func (h *IPFSMetricsHandlers) GetGatewayHealth(w http.ResponseWriter, r *http.Request) {
	if h.ipfsService == nil || !h.ipfsService.IsEnabled() {
		http.Error(w, "IPFS streaming not enabled", http.StatusServiceUnavailable)
		return
	}

	gateways := h.ipfsService.GetGatewayHealth()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"gateways": gateways,
	})
}
