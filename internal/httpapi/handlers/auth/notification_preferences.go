package auth

import (
	"context"
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
)

// NotificationPrefRepository is the storage interface for notification preferences.
type NotificationPrefRepository interface {
	GetPreferences(ctx context.Context, userID string) (*domain.NotificationPreferences, error)
	UpsertPreferences(ctx context.Context, prefs *domain.NotificationPreferences) error
}

// GetNotificationPreferencesHandler handles GET /api/v1/users/me/notification-preferences
func GetNotificationPreferencesHandler(repo NotificationPrefRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		prefs, err := repo.GetPreferences(r.Context(), userID)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get notification preferences"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, prefs)
	}
}

// UpdateNotificationPreferencesHandler handles PUT /api/v1/users/me/notification-preferences
// Decodes into a map first to distinguish explicitly-set false from absent fields,
// preventing Go's zero-value from zeroing unmentioned boolean preferences.
func UpdateNotificationPreferencesHandler(repo NotificationPrefRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		var raw map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "Invalid request body"))
			return
		}

		// Fetch existing preferences (creates defaults if none exist).
		prefs, err := repo.GetPreferences(r.Context(), userID)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to get notification preferences"))
			return
		}

		// Merge only the fields that were explicitly provided.
		if v, ok := raw["comment"].(bool); ok {
			prefs.Comment = v
		}
		if v, ok := raw["like"].(bool); ok {
			prefs.Like = v
		}
		if v, ok := raw["subscribe"].(bool); ok {
			prefs.Subscribe = v
		}
		if v, ok := raw["mention"].(bool); ok {
			prefs.Mention = v
		}
		if v, ok := raw["reply"].(bool); ok {
			prefs.Reply = v
		}
		if v, ok := raw["upload"].(bool); ok {
			prefs.Upload = v
		}
		if v, ok := raw["system"].(bool); ok {
			prefs.System = v
		}
		if v, ok := raw["email_enabled"].(bool); ok {
			prefs.EmailEnabled = v
		}

		if err := repo.UpsertPreferences(r.Context(), prefs); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to update notification preferences"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, prefs)
	}
}
