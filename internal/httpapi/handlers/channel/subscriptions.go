package channel

import (
	"net/http"
	"strings"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"
	ucchannel "vidra-core/internal/usecase/channel"

	"github.com/google/uuid"
)

// common helper to enforce auth and parse pagination
func requireAuthAndPagination(w http.ResponseWriter, r *http.Request) (string, int, int, int, int, bool) {
	me, _ := r.Context().Value(middleware.UserIDKey).(string)
	if me == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return "", 0, 0, 0, 0, false
	}
	// Parse pagination parameters with backward compatibility
	page, limit, offset, pageSize := shared.ParsePagination(r, 20)
	return me, limit, offset, page, pageSize, true
}

// SubscribeToUserHandler subscribes the authenticated user to the target user
func SubscribeToUserHandler(subRepo usecase.SubscriptionRepository, userRepo usecase.UserRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, _ := r.Context().Value(middleware.UserIDKey).(string)
		if me == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		targetID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		// Validate that target user exists for clearer errors
		if _, err := userRepo.GetByID(r.Context(), targetID); err != nil {
			if err == domain.ErrUserNotFound {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "Target user not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify target user"))
			return
		}

		if err := subRepo.Subscribe(r.Context(), me, targetID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SUBSCRIBE_FAILED", "Failed to subscribe"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnsubscribeFromUserHandler unsubscribes the authenticated user from the target user
func UnsubscribeFromUserHandler(subRepo usecase.SubscriptionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, _ := r.Context().Value(middleware.UserIDKey).(string)
		if me == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		targetID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		if err := subRepo.Unsubscribe(r.Context(), me, targetID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UNSUBSCRIBE_FAILED", "Failed to unsubscribe"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// ListMySubscriptionsHandler returns the list of channels the authenticated user is subscribed to
func ListMySubscriptionsHandler(subRepo usecase.SubscriptionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, limit, offset, page, pageSize, ok := requireAuthAndPagination(w, r)
		if !ok {
			return
		}

		// PeerTube v7.0: support sort param (e.g., channelUpdatedAt)
		sortParam := r.URL.Query().Get("sort")

		users, total, err := subRepo.ListSubscriptions(r.Context(), me, limit, offset, sortParam)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list subscriptions"))
			return
		}
		meta := &shared.Meta{Total: total, Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
		shared.WriteJSONWithMeta(w, http.StatusOK, users, meta)
	}
}

// ListSubscriptionVideosHandler returns public videos from channels the user subscribes to
func ListSubscriptionVideosHandler(subRepo usecase.SubscriptionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, limit, offset, page, pageSize, ok := requireAuthAndPagination(w, r)
		if !ok {
			return
		}

		meUUID, err := uuid.Parse(me)
		if err != nil {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("INVALID_USER_ID", "Invalid user ID"))
			return
		}

		videos, total, err := subRepo.GetSubscriptionVideos(r.Context(), meUUID, limit, offset)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list subscription videos"))
			return
		}
		meta := &shared.Meta{Total: int64(total), Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
		shared.WriteJSONWithMeta(w, http.StatusOK, videos, meta)
	}
}

// CheckSubscriptionsExistHandler handles GET /api/v1/users/me/subscriptions/exist?uris=uri1,uri2
// Returns a map of URI → subscribed boolean for up to 50 URIs.
// URIs may be channel UUID strings or channel handles (resolved via channelSvc when non-nil).
func CheckSubscriptionsExistHandler(subRepo usecase.SubscriptionRepository, channelSvc *ucchannel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscriberID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		uriParam := r.URL.Query().Get("uris")
		if uriParam == "" {
			shared.WriteJSON(w, http.StatusOK, map[string]bool{})
			return
		}

		parts := strings.Split(uriParam, ",")
		if len(parts) > 50 {
			parts = parts[:50]
		}

		result := make(map[string]bool, len(parts))
		for _, uri := range parts {
			uri = strings.TrimSpace(uri)
			if uri == "" {
				continue
			}

			var channelID uuid.UUID
			if id, err := uuid.Parse(uri); err == nil {
				channelID = id
			} else if channelSvc != nil {
				ch, err := channelSvc.GetChannelByHandle(r.Context(), uri)
				if err != nil {
					result[uri] = false
					continue
				}
				channelID = ch.ID
			} else {
				result[uri] = false
				continue
			}

			subscribed, err := subRepo.IsSubscribed(r.Context(), subscriberID, channelID)
			if err != nil {
				result[uri] = false
				continue
			}
			result[uri] = subscribed
		}

		shared.WriteJSON(w, http.StatusOK, result)
	}
}
