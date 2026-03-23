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

// ChapterRepository defines data operations for video chapters.
type ChapterRepository interface {
	GetByVideoID(ctx context.Context, videoID string) ([]*domain.VideoChapter, error)
	ReplaceAll(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error
}

// ChapterVideoRepository defines the video lookup needed for ownership checks.
type ChapterVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// ChapterHandlers handles video chapter endpoints.
type ChapterHandlers struct {
	chapterRepo ChapterRepository
	videoRepo   ChapterVideoRepository
}

// NewChapterHandlers creates a new ChapterHandlers.
func NewChapterHandlers(chapterRepo ChapterRepository, videoRepo ChapterVideoRepository) *ChapterHandlers {
	return &ChapterHandlers{chapterRepo: chapterRepo, videoRepo: videoRepo}
}

// GetChapters handles GET /api/v1/videos/{id}/chapters.
func (h *ChapterHandlers) GetChapters(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	chapters, err := h.chapterRepo.GetByVideoID(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load chapters"))
		return
	}

	// Return empty slice instead of null
	if chapters == nil {
		chapters = []*domain.VideoChapter{}
	}

	// Unwrap pointers for response
	result := make([]domain.VideoChapter, len(chapters))
	for i, c := range chapters {
		result[i] = *c
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

// PutChapters handles PUT /api/v1/videos/{id}/chapters.
// Replaces all chapters for a video. Only the video owner may update chapters.
func (h *ChapterHandlers) PutChapters(w http.ResponseWriter, r *http.Request) {
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

	// Verify ownership
	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		if err == domain.ErrVideoNotFound || err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load video"))
		return
	}

	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can update chapters"))
		return
	}

	type chapterInput struct {
		Timecode int    `json:"timecode"`
		Title    string `json:"title"`
	}
	var req struct {
		Chapters []chapterInput `json:"chapters"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	chapters := make([]*domain.VideoChapter, len(req.Chapters))
	for i, c := range req.Chapters {
		chapters[i] = &domain.VideoChapter{
			VideoID:  videoID,
			Timecode: c.Timecode,
			Title:    c.Title,
			Position: i + 1,
		}
	}

	if err := h.chapterRepo.ReplaceAll(r.Context(), videoID, chapters); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update chapters"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
