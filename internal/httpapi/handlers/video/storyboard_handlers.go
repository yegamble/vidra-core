package video

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
)

// StoryboardRepository defines data operations for video storyboards.
type StoryboardRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error)
}

// StoryboardHandlers handles video storyboard endpoints.
type StoryboardHandlers struct {
	storyboardRepo StoryboardRepository
}

// NewStoryboardHandlers creates a new StoryboardHandlers.
func NewStoryboardHandlers(storyboardRepo StoryboardRepository) *StoryboardHandlers {
	return &StoryboardHandlers{storyboardRepo: storyboardRepo}
}

// ListStoryboards handles GET /api/v1/videos/{id}/storyboards.
func (h *StoryboardHandlers) ListStoryboards(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	storyboards, err := h.storyboardRepo.ListByVideoID(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list storyboards"))
		return
	}
	if storyboards == nil {
		storyboards = []domain.VideoStoryboard{}
	}

	shared.WriteJSON(w, http.StatusOK, storyboards)
}
