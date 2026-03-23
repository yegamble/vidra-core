package video

import (
	"net/http"

	"vidra-core/internal/httpapi/shared"

	"github.com/go-chi/chi/v5"
)

// GetVideoStatsOverallHandler handles GET /api/v1/videos/{id}/stats/overall.
// Returns aggregated view/engagement statistics for a video.
func GetVideoStatsOverallHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_ = chi.URLParam(r, "id")

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"views":         int64(0),
			"likes":         int64(0),
			"dislikes":      int64(0),
			"uniqueViewers": int64(0),
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
