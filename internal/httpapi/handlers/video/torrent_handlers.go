package video

import (
	"athena/internal/httpapi/shared"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"athena/internal/torrent"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// TorrentHandlers handles HTTP requests for torrent operations
type TorrentHandlers struct {
	manager *torrent.Manager
	tracker *torrent.Tracker
}

// NewTorrentHandlers creates a new torrent handlers instance
func NewTorrentHandlers(manager *torrent.Manager, tracker *torrent.Tracker) *TorrentHandlers {
	return &TorrentHandlers{
		manager: manager,
		tracker: tracker,
	}
}

// GetVideoTorrentFile serves the .torrent file for a video
// GET /api/v1/videos/:id/torrent
func (h *TorrentHandlers) GetVideoTorrentFile(w http.ResponseWriter, r *http.Request) {
	// Get video ID from URL
	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		http.Error(w, "Invalid video ID", http.StatusBadRequest)
		return
	}

	// Get the torrent file path from database
	torrentData, err := h.manager.GetVideoTorrent(r.Context(), videoID)
	if err != nil {
		http.Error(w, "Torrent not found", http.StatusNotFound)
		return
	}

	// Check if file exists
	if _, err := os.Stat(torrentData.TorrentFilePath); os.IsNotExist(err) {
		http.Error(w, "Torrent file not found", http.StatusNotFound)
		return
	}

	// Read torrent file
	data, err := os.ReadFile(torrentData.TorrentFilePath)
	if err != nil {
		http.Error(w, "Failed to read torrent file", http.StatusInternalServerError)
		return
	}

	// Set headers for torrent download
	filename := filepath.Base(torrentData.TorrentFilePath)
	w.Header().Set("Content-Type", "application/x-bittorrent")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))

	// Write torrent file
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(data); err != nil {
		// Log error but can't change response at this point
		return
	}
}

// GetVideoMagnetURI returns the magnet URI for a video
// GET /api/v1/videos/:id/magnet
func (h *TorrentHandlers) GetVideoMagnetURI(w http.ResponseWriter, r *http.Request) {
	// Get video ID from URL
	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteJSON(w, http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_video_id",
			Message: "Invalid video ID format",
		})
		return
	}

	// Get torrent from database
	torrentData, err := h.manager.GetVideoTorrent(r.Context(), videoID)
	if err != nil {
		shared.WriteJSON(w, http.StatusNotFound, ErrorResponse{
			Error:   "torrent_not_found",
			Message: "Torrent not found for this video",
		})
		return
	}

	// Return magnet URI
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"video_id":   videoID,
		"info_hash":  torrentData.InfoHash,
		"magnet_uri": torrentData.MagnetURI,
	})
}

// GetTorrentStats returns global torrent statistics
// GET /api/v1/torrents/stats
func (h *TorrentHandlers) GetTorrentStats(w http.ResponseWriter, r *http.Request) {
	// Get manager stats
	stats, err := h.manager.GetGlobalStats(r.Context())
	if err != nil {
		shared.WriteJSON(w, http.StatusInternalServerError, ErrorResponse{
			Error:   "stats_error",
			Message: "Failed to retrieve torrent statistics",
		})
		return
	}

	// Get tracker stats
	var trackerStats map[string]interface{}
	if h.tracker != nil {
		tStats := h.tracker.GetStats()
		trackerStats = map[string]interface{}{
			"total_announces":    tStats.TotalAnnounces,
			"total_scrapes":      tStats.TotalScrapes,
			"active_connections": tStats.ActiveConnections,
			"total_peers":        tStats.TotalPeers,
			"total_swarms":       tStats.TotalSwarms,
			"announce_errors":    tStats.AnnounceErrors,
			"connection_errors":  tStats.ConnectionErrors,
			"uptime_seconds":     tStats.StartTime.Unix(),
		}
	}

	// Combine stats
	response := map[string]interface{}{
		"manager": stats,
		"tracker": trackerStats,
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// GetSwarmInfo returns information about a specific torrent swarm
// GET /api/v1/torrents/:infoHash/swarm
func (h *TorrentHandlers) GetSwarmInfo(w http.ResponseWriter, r *http.Request) {
	// Get info hash from URL
	infoHash := chi.URLParam(r, "infoHash")
	if infoHash == "" {
		shared.WriteJSON(w, http.StatusBadRequest, ErrorResponse{
			Error:   "invalid_info_hash",
			Message: "Info hash is required",
		})
		return
	}

	// Get swarm info from tracker
	if h.tracker == nil {
		shared.WriteJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Error:   "tracker_unavailable",
			Message: "Tracker is not enabled",
		})
		return
	}

	info := h.tracker.GetSwarmInfo(infoHash)
	if info == nil {
		shared.WriteJSON(w, http.StatusNotFound, ErrorResponse{
			Error:   "swarm_not_found",
			Message: "No active swarm found for this info hash",
		})
		return
	}

	shared.WriteJSON(w, http.StatusOK, info)
}

// HandleTrackerWebSocket handles WebSocket connections for WebTorrent tracker
// WS /api/v1/tracker
func (h *TorrentHandlers) HandleTrackerWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.tracker == nil {
		http.Error(w, "Tracker not enabled", http.StatusServiceUnavailable)
		return
	}

	h.tracker.HandleWebSocket(w, r)
}

// GetTrackerStats returns tracker-specific statistics
// GET /api/v1/tracker/stats
func (h *TorrentHandlers) GetTrackerStats(w http.ResponseWriter, r *http.Request) {
	if h.tracker == nil {
		shared.WriteJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Error:   "tracker_unavailable",
			Message: "Tracker is not enabled",
		})
		return
	}

	stats := h.tracker.GetStats()

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"total_announces":    stats.TotalAnnounces,
		"total_scrapes":      stats.TotalScrapes,
		"active_connections": stats.ActiveConnections,
		"total_peers":        stats.TotalPeers,
		"total_swarms":       stats.TotalSwarms,
		"announce_errors":    stats.AnnounceErrors,
		"connection_errors":  stats.ConnectionErrors,
		"start_time":         stats.StartTime,
	})
}

// VideoTorrentResponse represents a video torrent API response
type VideoTorrentResponse struct {
	VideoID            uuid.UUID `json:"video_id"`
	InfoHash           string    `json:"info_hash"`
	MagnetURI          string    `json:"magnet_uri"`
	TorrentURL         string    `json:"torrent_url"`
	TotalSizeBytes     int64     `json:"total_size_bytes"`
	PieceLength        int       `json:"piece_length"`
	Seeders            int       `json:"seeders"`
	Leechers           int       `json:"leechers"`
	CompletedDownloads int       `json:"completed_downloads"`
	IsSeeding          bool      `json:"is_seeding"`
	CreatedAt          string    `json:"created_at"`
}
