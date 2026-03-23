package metrics

import (
	"encoding/json"
	"net/http"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// PlaybackMetricsRequest represents the PeerTube-compatible playback metrics payload.
type PlaybackMetricsRequest struct {
	PlayerMode        string  `json:"playerMode"`
	Resolution        int     `json:"resolution"`
	FPS               int     `json:"fps"`
	P2PEnabled        bool    `json:"p2pEnabled"`
	P2PPeers          int     `json:"p2pPeers,omitempty"`
	ResolutionChanges int     `json:"resolutionChanges"`
	Errors            int     `json:"errors"`
	DownloadedBytes   int64   `json:"downloadedBytesP2P"`
	UploadedBytes     int64   `json:"uploadedBytesP2P"`
	VideoID           string  `json:"videoId"`
	BufferStalled     int     `json:"bufferStalled,omitempty"`
	WatchDuration     float64 `json:"watchDuration,omitempty"`
}

// PlaybackMetricsResponse is returned on successful metric ingestion.
type PlaybackMetricsResponse struct {
	ReceivedAt time.Time `json:"receivedAt"`
}

// PlaybackHandler handles playback metrics collection.
type PlaybackHandler struct{}

// NewPlaybackHandler creates a new PlaybackHandler.
func NewPlaybackHandler() *PlaybackHandler {
	return &PlaybackHandler{}
}

// ReportPlaybackMetrics accepts playback metrics from clients.
// POST /api/v1/metrics/playback
//
// This endpoint accepts PeerTube-compatible playback metrics and acknowledges
// receipt. Actual metric processing/storage can be wired in later.
func (h *PlaybackHandler) ReportPlaybackMetrics(w http.ResponseWriter, r *http.Request) {
	var req PlaybackMetricsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	if req.VideoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "videoId is required"))
		return
	}

	// Acknowledge receipt. In future, this could persist to a metrics store.
	shared.WriteJSON(w, http.StatusOK, PlaybackMetricsResponse{
		ReceivedAt: time.Now().UTC(),
	})
}
