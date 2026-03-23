package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// QuotaVideoRepository is the minimal interface needed for quota computation.
type QuotaVideoRepository interface {
	GetVideoQuotaUsed(ctx context.Context, userID string) (int64, error)
}

type videoQuotaResponse struct {
	VideoQuotaUsed      int64 `json:"videoQuotaUsed"`
	VideoQuotaUsedDaily int64 `json:"videoQuotaUsedDaily"`
}

// GetVideoQuotaUsedHandler handles GET /api/v1/users/me/video-quota-used.
// Returns total storage bytes used by the authenticated user's videos.
func GetVideoQuotaUsedHandler(repo QuotaVideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		total, err := repo.GetVideoQuotaUsed(r.Context(), userID)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to compute video quota"))
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(videoQuotaResponse{
			VideoQuotaUsed:      total,
			VideoQuotaUsedDaily: 0, // daily tracking not yet implemented
		})
	}
}
