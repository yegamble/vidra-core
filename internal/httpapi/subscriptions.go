package httpapi

import (
	"net/http"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// common helper to enforce auth and parse pagination
func requireAuthAndPagination(w http.ResponseWriter, r *http.Request) (string, int, int, int, int, bool) {
	me, _ := r.Context().Value(middleware.UserIDKey).(string)
	if me == "" {
		WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
		return "", 0, 0, 0, 0, false
	}
	// Parse pagination parameters with backward compatibility
	page, limit, offset, pageSize := ParsePagination(r, 20)
	return me, limit, offset, page, pageSize, true
}

// SubscribeToUserHandler subscribes the authenticated user to the target user
func SubscribeToUserHandler(subRepo usecase.SubscriptionRepository, userRepo usecase.UserRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, _ := r.Context().Value(middleware.UserIDKey).(string)
		if me == "" {
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		targetID, ok := requireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		// Validate that target user exists for clearer errors
		if _, err := userRepo.GetByID(r.Context(), targetID); err != nil {
			if err == domain.ErrUserNotFound {
				WriteError(w, http.StatusNotFound, domain.NewDomainError("USER_NOT_FOUND", "Target user not found"))
				return
			}
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to verify target user"))
			return
		}

		if err := subRepo.Subscribe(r.Context(), me, targetID); err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SUBSCRIBE_FAILED", "Failed to subscribe"))
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
			WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		targetID, ok := requireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		if err := subRepo.Unsubscribe(r.Context(), me, targetID); err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UNSUBSCRIBE_FAILED", "Failed to unsubscribe"))
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

		users, total, err := subRepo.ListSubscriptions(r.Context(), me, limit, offset)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list subscriptions"))
			return
		}
		meta := &Meta{Total: total, Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
		WriteJSONWithMeta(w, http.StatusOK, users, meta)
	}
}

// ListSubscriptionVideosHandler returns public videos from channels the user subscribes to
func ListSubscriptionVideosHandler(subRepo usecase.SubscriptionRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		me, limit, offset, page, pageSize, ok := requireAuthAndPagination(w, r)
		if !ok {
			return
		}

		videos, total, err := subRepo.ListSubscriptionVideos(r.Context(), me, limit, offset)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list subscription videos"))
			return
		}
		meta := &Meta{Total: total, Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
		WriteJSONWithMeta(w, http.StatusOK, videos, meta)
	}
}
