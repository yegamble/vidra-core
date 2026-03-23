package video

import (
	"context"
	"encoding/json"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// ownershipVideoRepo is the minimal video interface needed for ownership handlers.
type ownershipVideoRepo interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	Update(ctx context.Context, video *domain.Video) error
}

// GiveOwnershipHandler handles POST /api/v1/videos/{id}/give-ownership.
// The authenticated video owner initiates a transfer to another user (by username/ID).
func GiveOwnershipHandler(ownershipRepo port.VideoOwnershipRepository, videoRepo ownershipVideoRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		videoID := chi.URLParam(r, "id")
		video, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
			return
		}

		if video.UserID != callerID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "You do not own this video"))
			return
		}

		var req struct {
			Username string `json:"username"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Username == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "username is required"))
			return
		}

		change := &domain.VideoOwnershipChange{
			VideoID:     videoID,
			InitiatorID: callerID,
			NextOwnerID: req.Username,
			Status:      domain.VideoOwnershipChangePending,
		}
		if err := ownershipRepo.Create(r.Context(), change); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to create ownership change"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListOwnershipChangesHandler handles GET /api/v1/users/me/videos/ownership.
// Returns pending ownership change requests where the caller is the prospective new owner.
func ListOwnershipChangesHandler(ownershipRepo port.VideoOwnershipRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		changes, err := ownershipRepo.ListPendingForUser(r.Context(), callerID)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list ownership changes"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"total": len(changes),
			"data":  changes,
		})
	}
}

// AcceptOwnershipHandler handles POST /api/v1/users/me/videos/ownership/{id}/accept.
// The prospective new owner accepts the transfer, which updates the video's owner.
func AcceptOwnershipHandler(ownershipRepo port.VideoOwnershipRepository, videoRepo ownershipVideoRepo) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		changeIDStr := chi.URLParam(r, "id")
		changeID, err := uuid.Parse(changeIDStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid ownership change ID"))
			return
		}

		change, err := ownershipRepo.GetByID(r.Context(), changeID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Ownership change not found"))
			return
		}

		if change.NextOwnerID != callerID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "You are not the intended new owner"))
			return
		}

		video, err := videoRepo.GetByID(r.Context(), change.VideoID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Video not found"))
			return
		}
		video.UserID = callerID
		if err := videoRepo.Update(r.Context(), video); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update video owner"))
			return
		}

		if err := ownershipRepo.UpdateStatus(r.Context(), changeID, domain.VideoOwnershipChangeAccepted); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update ownership change status"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// RefuseOwnershipHandler handles POST /api/v1/users/me/videos/ownership/{id}/refuse.
// The prospective new owner refuses the transfer.
func RefuseOwnershipHandler(ownershipRepo port.VideoOwnershipRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		callerID, ok := r.Context().Value(middleware.UserIDKey).(string)
		if !ok || callerID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		changeIDStr := chi.URLParam(r, "id")
		changeID, err := uuid.Parse(changeIDStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid ownership change ID"))
			return
		}

		change, err := ownershipRepo.GetByID(r.Context(), changeID)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Ownership change not found"))
			return
		}

		if change.NextOwnerID != callerID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "You are not the intended new owner"))
			return
		}

		if err := ownershipRepo.UpdateStatus(r.Context(), changeID, domain.VideoOwnershipChangeRefused); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update ownership change status"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
