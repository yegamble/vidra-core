package port

import (
	"context"

	"vidra-core/internal/domain"
)

// NotificationPreferenceRepository manages per-user notification toggle settings.
type NotificationPreferenceRepository interface {
	// GetPreferences returns a user's preferences, creating defaults if none exist.
	GetPreferences(ctx context.Context, userID string) (*domain.NotificationPreferences, error)
	// UpsertPreferences creates or updates a user's preferences.
	UpsertPreferences(ctx context.Context, prefs *domain.NotificationPreferences) error
}
