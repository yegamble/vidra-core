package video

import (
	"athena/internal/middleware"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/livestream"
	"athena/internal/repository"
)

// HLSHandlers handles HLS streaming endpoints
type HLSHandlers struct {
	cfg        *config.Config
	streamRepo repository.LiveStreamRepository
	transcoder *livestream.HLSTranscoder
}

// NewHLSHandlers creates a new HLS handlers instance
func NewHLSHandlers(
	cfg *config.Config,
	streamRepo repository.LiveStreamRepository,
	transcoder *livestream.HLSTranscoder,
) *HLSHandlers {
	return &HLSHandlers{
		cfg:        cfg,
		streamRepo: streamRepo,
		transcoder: transcoder,
	}
}

// GetMasterPlaylist serves the master HLS playlist
func (h *HLSHandlers) GetMasterPlaylist(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	// Get stream
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	// Check privacy
	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	// Check if stream is live
	if !stream.IsLive() {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("STREAM_NOT_LIVE", "Stream is not currently live"))
		return
	}

	// Serve master playlist file
	playlistPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), "master.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("PLAYLIST_NOT_FOUND", "HLS playlist not yet available"))
		return
	}

	// Set headers
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	// Serve file
	http.ServeFile(w, r, playlistPath)
}

// GetVariantPlaylist serves a quality variant playlist
func (h *HLSHandlers) GetVariantPlaylist(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	variant := chi.URLParam(r, "variant")
	if variant == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VARIANT", "Variant parameter required"))
		return
	}

	// Validate variant name (prevent path traversal)
	if !isValidVariantName(variant) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VARIANT", "Invalid variant name"))
		return
	}

	// Get stream
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	// Check privacy
	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	// Check if stream is live
	if !stream.IsLive() {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("STREAM_NOT_LIVE", "Stream is not currently live"))
		return
	}

	// Serve variant playlist file
	playlistPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), variant, "index.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("PLAYLIST_NOT_FOUND", "Variant playlist not found"))
		return
	}

	// Set headers
	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	// Serve file
	http.ServeFile(w, r, playlistPath)
}

// GetSegment serves an HLS segment file
func (h *HLSHandlers) GetSegment(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	variant := chi.URLParam(r, "variant")
	if variant == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VARIANT", "Variant parameter required"))
		return
	}

	segment := chi.URLParam(r, "segment")
	if segment == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_SEGMENT", "Segment parameter required"))
		return
	}

	// Validate names (prevent path traversal)
	if !isValidVariantName(variant) || !isValidSegmentName(segment) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PATH", "Invalid path parameters"))
		return
	}

	// Get stream
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	// Check privacy
	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	// Note: Allow segments even if stream ended (for DVR/replay)

	// Serve segment file
	segmentPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), variant, segment)
	if _, err := os.Stat(segmentPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("SEGMENT_NOT_FOUND", "Segment not found"))
		return
	}

	// Set headers for MPEG-TS segments
	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	// Serve file
	http.ServeFile(w, r, segmentPath)
}

// checkStreamAccess checks if the request has access to the stream based on privacy
func (h *HLSHandlers) checkStreamAccess(r *http.Request, stream *domain.LiveStream) error {
	switch stream.Privacy {
	case "public":
		// Anyone can access
		return nil

	case "unlisted":
		// Anyone with the link can access
		return nil

	case "private":
		// Only authenticated owner can access
		userID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			return domain.NewDomainError("UNAUTHORIZED", "Authentication required for private streams")
		}
		if userID != stream.UserID {
			return domain.NewDomainError("FORBIDDEN", "You don't have access to this private stream")
		}
		return nil

	default:
		return domain.NewDomainError("INVALID_PRIVACY", "Invalid stream privacy setting")
	}
}

// isValidVariantName validates variant names to prevent path traversal
func isValidVariantName(name string) bool {
	// Only allow alphanumeric and 'p' suffix (e.g., "1080p", "720p")
	validVariants := map[string]bool{
		"1080p": true,
		"720p":  true,
		"480p":  true,
		"360p":  true,
	}
	return validVariants[name]
}

// isValidSegmentName validates segment names to prevent path traversal
func isValidSegmentName(name string) bool {
	// Must start with "segment_" and end with ".ts"
	if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".ts") {
		return false
	}

	// Must not contain path separators
	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return false
	}

	return true
}

// GetStreamHLSInfo returns HLS information for a stream
func (h *HLSHandlers) GetStreamHLSInfo(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	// Get stream
	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	// Check if stream is live
	if !stream.IsLive() {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":      true,
			"is_available": false,
			"message":      "Stream is not currently live",
		})
		return
	}

	// Check if transcoding
	isTranscoding := h.transcoder.IsTranscoding(streamID)

	// Get session if available
	var variants []string
	if session, exists := h.transcoder.GetSession(streamID); exists {
		for _, v := range session.Variants {
			variants = append(variants, v.Name)
		}
	}

	// Build response
	baseURL := fmt.Sprintf("%s://%s", getScheme(r), r.Host)
	hlsURL := fmt.Sprintf("%s/api/v1/streams/%s/hls/master.m3u8", baseURL, streamID)

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success":      true,
		"is_available": isTranscoding,
		"hls_url":      hlsURL,
		"variants":     variants,
		"stream_id":    streamID,
		"status":       stream.Status,
		"viewer_count": stream.ViewerCount,
	})
}

// getScheme returns the scheme (http or https) from the request
func getScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}
