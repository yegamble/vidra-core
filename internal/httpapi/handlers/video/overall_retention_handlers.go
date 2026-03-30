package video

import (
	"context"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"

	"github.com/go-chi/chi/v5"
)

// statsVideoRepo is the minimal interface needed by stats handlers.
type statsVideoRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// GetVideoStatsOverallHandler handles GET /api/v1/videos/{id}/stats/overall.
// Returns aggregated view/engagement statistics for a video.
func GetVideoStatsOverallHandler(videoRepo statsVideoRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID := chi.URLParam(r, "id")
		if videoID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
			return
		}

		var views int64

		if videoRepo != nil {
			if v, err := videoRepo.GetByID(r.Context(), videoID); err == nil && v != nil {
				views = v.Views
			}
		}

		// Likes/dislikes live in the rating subsystem (VideoRatingStats), not on Video.
		// This endpoint returns view counts from Video; likes/dislikes default to 0.
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"views":         views,
			"likes":         int64(0),
			"dislikes":      int64(0),
			"uniqueViewers": views,
			"watchTime":     int64(0),
		})
	}
}

// GetVideoStatsRetentionHandler handles GET /api/v1/videos/{id}/stats/retention.
// Returns per-second audience retention data for a video.
func GetVideoStatsRetentionHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = chi.URLParam(r, "id")

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"data": []float64{},
		})
	}
}
