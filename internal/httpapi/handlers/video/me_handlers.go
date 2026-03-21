package video

import (
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// GetMyVideosHandler handles GET /api/v1/users/me/videos.
// Returns the authenticated user's uploaded videos.
func GetMyVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		limit, offset := parsePagination(r)
		videos, total, err := repo.GetByUserID(r.Context(), userID, limit, offset)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get videos"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"total": total,
			"data":  videos,
		})
	}
}

// GetMyCommentsHandler handles GET /api/v1/users/me/comments.
// Comment-by-user listing is not yet supported; returns an empty list.
func GetMyCommentsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"total": 0,
			"data":  []interface{}{},
		})
	}
}
