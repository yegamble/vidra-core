package video

import (
	"context"
	"net/http"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// videoTokenTTL is the lifetime of a video access token.
const videoTokenTTL = 4 * time.Hour

// videoSourceRepo is the minimal video interface for source/token handlers.
type videoSourceRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	Update(ctx context.Context, video *domain.Video) error
}

// VideoTokenStore is a minimal interface for storing video access tokens.
type VideoTokenStore interface {
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Get(ctx context.Context, key string) (string, error)
	Del(ctx context.Context, key string) error
}

func videoTokenKey(token string) string { return "video:access:" + token }

// DeleteVideoSourceHandler handles DELETE /api/v1/videos/{id}/source.
// Clears the source file reference from video metadata (owner or admin only).
func DeleteVideoSourceHandler(videoRepo videoSourceRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		videoID := chi.URLParam(r, "id")
		vid, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
			return
		}

		role, _ := r.Context().Value(middleware.UserRoleKey).(string)
		if vid.UserID != callerID && role != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner or an admin can delete the source"))
			return
		}

		if vid.OutputPaths == nil {
			vid.OutputPaths = make(map[string]string)
		}
		vid.OutputPaths["source"] = ""
		if vid.S3URLs == nil {
			vid.S3URLs = make(map[string]string)
		}
		vid.S3URLs["source"] = ""

		if err := videoRepo.Update(r.Context(), vid); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update video"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// CreateVideoTokenHandler handles POST /api/v1/videos/{id}/token.
// Generates a short-lived (4h) access token for the video (owner or admin only).
func CreateVideoTokenHandler(videoRepo videoSourceRepo, store VideoTokenStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		videoID := chi.URLParam(r, "id")
		vid, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
			return
		}

		role, _ := r.Context().Value(middleware.UserRoleKey).(string)
		if vid.UserID != callerID && role != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner or an admin can generate an access token"))
			return
		}

		token := uuid.New().String()
		if err := store.Set(r.Context(), videoTokenKey(token), videoID, videoTokenTTL); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create token"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"token":     token,
			"expiresIn": int(videoTokenTTL.Seconds()), // 14400
		})
	}
}
