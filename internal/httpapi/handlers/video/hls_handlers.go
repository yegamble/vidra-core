package video

import (
	"vidra-core/internal/middleware"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/repository"
	"vidra-core/internal/usecase/ipfs_streaming"
)

type HLSHandlers struct {
	cfg           *config.Config
	streamRepo    repository.LiveStreamRepository
	transcoder    HLSTranscoderInterface
	ipfsStreaming *ipfs_streaming.Service
}

func NewHLSHandlers(
	cfg *config.Config,
	streamRepo repository.LiveStreamRepository,
	transcoder HLSTranscoderInterface,
	ipfsStreaming *ipfs_streaming.Service,
) *HLSHandlers {
	return &HLSHandlers{
		cfg:           cfg,
		streamRepo:    streamRepo,
		transcoder:    transcoder,
		ipfsStreaming: ipfsStreaming,
	}
}

func (h *HLSHandlers) GetMasterPlaylist(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	if !stream.IsLive() {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("STREAM_NOT_LIVE", "Stream is not currently live"))
		return
	}

	playlistPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), "master.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("PLAYLIST_NOT_FOUND", "HLS playlist not yet available"))
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	http.ServeFile(w, r, playlistPath)
}

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

	if !isValidVariantName(variant) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VARIANT", "Invalid variant name"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	if !stream.IsLive() {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("STREAM_NOT_LIVE", "Stream is not currently live"))
		return
	}

	playlistPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), variant, "index.m3u8")
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("PLAYLIST_NOT_FOUND", "Variant playlist not found"))
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	http.ServeFile(w, r, playlistPath)
}

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

	if !isValidVariantName(variant) || !isValidSegmentName(segment) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PATH", "Invalid path parameters"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	if err := h.checkStreamAccess(r, stream); err != nil {
		shared.WriteError(w, http.StatusForbidden, err)
		return
	}

	// Note: Allow segments even if stream ended (for DVR/replay)

	segmentPath := filepath.Join(h.cfg.HLSOutputDir, streamID.String(), variant, segment)
	if _, err := os.Stat(segmentPath); os.IsNotExist(err) {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("SEGMENT_NOT_FOUND", "Segment not found"))
		return
	}

	w.Header().Set("Content-Type", "video/MP2T")
	w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Expose-Headers", "Content-Length")

	http.ServeFile(w, r, segmentPath)
}

func (h *HLSHandlers) checkStreamAccess(r *http.Request, stream *domain.LiveStream) error {
	switch stream.Privacy {
	case "public":
		return nil

	case "unlisted":
		return nil

	case "private":
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

func isValidVariantName(name string) bool {
	validVariants := map[string]bool{
		"1080p": true,
		"720p":  true,
		"480p":  true,
		"360p":  true,
	}
	return validVariants[name]
}

func isValidSegmentName(name string) bool {
	if !strings.HasPrefix(name, "segment_") || !strings.HasSuffix(name, ".ts") {
		return false
	}

	if strings.Contains(name, "/") || strings.Contains(name, "\\") || strings.Contains(name, "..") {
		return false
	}

	return true
}

func (h *HLSHandlers) GetStreamHLSInfo(w http.ResponseWriter, r *http.Request) {
	streamID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_STREAM_ID", "Invalid stream ID"))
		return
	}

	stream, err := h.streamRepo.GetByID(r.Context(), streamID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("STREAM_NOT_FOUND", "Stream not found"))
		return
	}

	if !stream.IsLive() {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"success":      true,
			"is_available": false,
			"message":      "Stream is not currently live",
		})
		return
	}

	isTranscoding := h.transcoder.IsTranscoding(streamID)

	var variants []string
	if session, exists := h.transcoder.GetSession(streamID); exists {
		for _, v := range session.Variants {
			variants = append(variants, v.Name)
		}
	}

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

func getScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if scheme := r.Header.Get("X-Forwarded-Proto"); scheme != "" {
		return scheme
	}
	return "http"
}
