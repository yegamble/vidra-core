package channel

import (
	"context"
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase"
	ucchannel "athena/internal/usecase/channel"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// resolveChannelHandle converts a handle string (UUID or named handle) to a uuid.UUID.
// If channelSvc is nil, only UUID handles are accepted.
func resolveChannelHandle(ctx context.Context, handle string, channelSvc *ucchannel.Service) (uuid.UUID, error) {
	if id, err := uuid.Parse(handle); err == nil {
		return id, nil
	}
	if channelSvc != nil {
		ch, err := channelSvc.GetChannelByHandle(ctx, handle)
		if err != nil {
			return uuid.UUID{}, err
		}
		return ch.ID, nil
	}
	return uuid.UUID{}, domain.ErrNotFound
}

// GetSubscriptionByHandleHandler handles GET /api/v1/users/me/subscriptions/{subscriptionHandle}.
// Returns 200 with subscription info if subscribed, 404 otherwise.
func GetSubscriptionByHandleHandler(subRepo usecase.SubscriptionRepository, channelSvc *ucchannel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscriberID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		handle := chi.URLParam(r, "subscriptionHandle")
		channelID, err := resolveChannelHandle(r.Context(), handle, channelSvc)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Channel not found"))
			return
		}

		subscribed, err := subRepo.IsSubscribed(r.Context(), subscriberID, channelID)
		if err != nil || !subscribed {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Subscription not found"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"subscribed": true,
			"channelId":  channelID.String(),
		})
	}
}

// SubscribeByHandleHandler handles POST /api/v1/users/me/subscriptions.
// Body: {"uri":"channelHandle"} where channelHandle is a UUID or named handle.
func SubscribeByHandleHandler(subRepo usecase.SubscriptionRepository, channelSvc *ucchannel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscriberID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		var req struct {
			URI string `json:"uri"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.URI == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "uri is required"))
			return
		}

		channelID, err := resolveChannelHandle(r.Context(), req.URI, channelSvc)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Channel not found"))
			return
		}

		if err := subRepo.SubscribeToChannel(r.Context(), subscriberID, channelID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SUBSCRIBE_FAILED", "Failed to subscribe"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnsubscribeByHandleHandler handles DELETE /api/v1/users/me/subscriptions/{subscriptionHandle}.
func UnsubscribeByHandleHandler(subRepo usecase.SubscriptionRepository, channelSvc *ucchannel.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		subscriberID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		handle := chi.URLParam(r, "subscriptionHandle")
		channelID, err := resolveChannelHandle(r.Context(), handle, channelSvc)
		if err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("NOT_FOUND", "Channel not found"))
			return
		}

		if err := subRepo.UnsubscribeFromChannel(r.Context(), subscriberID, channelID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UNSUBSCRIBE_FAILED", "Failed to unsubscribe"))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
