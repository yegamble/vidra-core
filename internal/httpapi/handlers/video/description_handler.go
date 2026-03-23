package video

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// GetVideoDescriptionHandler handles GET /api/v1/videos/{id}/description.
// Returns only the full description text for clients that don't need the full video object.
func GetVideoDescriptionHandler(repo ChapterVideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID := chi.URLParam(r, "id")
		if videoID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
			return
		}

		video, err := repo.GetByID(r.Context(), videoID)
		if err != nil {
			if err == domain.ErrVideoNotFound || err == domain.ErrNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to load video"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]string{"description": video.Description})
	}
}
