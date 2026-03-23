package video

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// SourceReplaceVideoRepository is the subset of video repo needed for ownership checks.
type SourceReplaceVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// SourceReplaceHandlers handles video source replacement endpoints.
type SourceReplaceHandlers struct {
	videoRepo SourceReplaceVideoRepository
}

// NewSourceReplaceHandlers creates a new SourceReplaceHandlers.
func NewSourceReplaceHandlers(videoRepo SourceReplaceVideoRepository) *SourceReplaceHandlers {
	return &SourceReplaceHandlers{videoRepo: videoRepo}
}

// InitiateReplace handles POST /api/v1/videos/{id}/source/replace-resumable.
func (h *SourceReplaceHandlers) InitiateReplace(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can replace the source"))
		return
	}

	var req struct {
		Filename string `json:"filename"`
		FileSize int64  `json:"fileSize"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.Filename == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILENAME", "Filename is required"))
		return
	}

	// Return a placeholder session for the replacement upload.
	// Full resumable-upload orchestration reuses the existing upload service;
	// this endpoint signals acceptance and returns the video ID as session key.
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"videoId":  videoID,
		"filename": req.Filename,
		"fileSize": req.FileSize,
	})
}

// UploadReplaceChunk handles PUT /api/v1/videos/{id}/source/replace-resumable.
func (h *SourceReplaceHandlers) UploadReplaceChunk(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can upload replacement chunks"))
		return
	}

	// Stub: accept the chunk. Full implementation wires into the upload service.
	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"videoId": videoID,
		"status":  "chunk_received",
	})
}

// CancelReplace handles DELETE /api/v1/videos/{id}/source/replace-resumable.
func (h *SourceReplaceHandlers) CancelReplace(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can cancel replacement"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
