package compat

import (
	"encoding/json"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/port"
)

// VideoCompatHandlers provides PeerTube-compatible endpoint aliases for video operations.
type VideoCompatHandlers struct {
	videoRepo port.VideoRepository
}

// NewVideoCompatHandlers creates handlers for PeerTube video compatibility endpoints.
func NewVideoCompatHandlers(videoRepo port.VideoRepository) *VideoCompatHandlers {
	return &VideoCompatHandlers{videoRepo: videoRepo}
}

// GetVideoDescription handles GET /api/v1/videos/{id}/description
// PeerTube returns { description: "..." } as a separate endpoint.
// Vidra includes description in the main video response — this extracts it.
func (h *VideoCompatHandlers) GetVideoDescription(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_ID", "Video ID is required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"description": video.Description,
	})
}

// VideoWatchingRequest represents PeerTube's PUT /videos/{id}/watching request body.
type VideoWatchingRequest struct {
	CurrentTime float64 `json:"currentTime"`
}

// TrackWatching handles PUT /api/v1/videos/{id}/watching
// PeerTube sends { currentTime: N } to track watch progress.
func (h *VideoCompatHandlers) TrackWatching(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_ID", "Video ID is required"))
		return
	}

	var req VideoWatchingRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid request body"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
