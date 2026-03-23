package video

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// FileManagementVideoRepository is the subset of video repo needed for ownership checks.
type FileManagementVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// FileManagementHandlers handles video file management endpoints.
type FileManagementHandlers struct {
	videoRepo FileManagementVideoRepository
}

// NewFileManagementHandlers creates a new FileManagementHandlers.
func NewFileManagementHandlers(videoRepo FileManagementVideoRepository) *FileManagementHandlers {
	return &FileManagementHandlers{videoRepo: videoRepo}
}

// GetFileMetadata handles GET /api/v1/videos/{id}/metadata/{videoFileId}.
func (h *FileManagementHandlers) GetFileMetadata(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	videoFileID := chi.URLParam(r, "videoFileId")
	if videoFileID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILE_ID", "Video file ID is required"))
		return
	}

	// Return metadata stub. The actual file metadata lookup depends on the
	// encoding/storage subsystem; this provides the API surface.
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"videoId":     videoID,
		"videoFileId": videoFileID,
		"metadata":    map[string]interface{}{},
	})
}

func (h *FileManagementHandlers) requireOwner(w http.ResponseWriter, r *http.Request) (*domain.Video, bool) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return nil, false
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return nil, false
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return nil, false
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can manage files"))
		return nil, false
	}
	return video, true
}

// DeleteAllHLS handles DELETE /api/v1/videos/{id}/hls.
func (h *FileManagementHandlers) DeleteAllHLS(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireOwner(w, r); !ok {
		return
	}
	// Stub: actual HLS file deletion is handled by the storage/worker subsystems.
	w.WriteHeader(http.StatusNoContent)
}

// DeleteHLSFile handles DELETE /api/v1/videos/{id}/hls/{videoFileId}.
func (h *FileManagementHandlers) DeleteHLSFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireOwner(w, r); !ok {
		return
	}
	videoFileID := chi.URLParam(r, "videoFileId")
	if videoFileID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILE_ID", "Video file ID is required"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteAllWebVideos handles DELETE /api/v1/videos/{id}/web-videos.
func (h *FileManagementHandlers) DeleteAllWebVideos(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireOwner(w, r); !ok {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteWebVideoFile handles DELETE /api/v1/videos/{id}/web-videos/{videoFileId}.
func (h *FileManagementHandlers) DeleteWebVideoFile(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireOwner(w, r); !ok {
		return
	}
	videoFileID := chi.URLParam(r, "videoFileId")
	if videoFileID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILE_ID", "Video file ID is required"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
