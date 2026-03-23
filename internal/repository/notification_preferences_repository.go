package repository

import (
	"context"
	"database/sql"
	"errors"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

// NotificationPreferencesRepository manages notification_preferences table.
type NotificationPreferencesRepository struct {
	db *sqlx.DB
}

// NewNotificationPreferencesRepository creates a new repository.
func NewNotificationPreferencesRepository(db *sqlx.DB) *NotificationPreferencesRepository {
	return &NotificationPreferencesRepository{db: db}
}

// GetPreferences returns the user's preferences, inserting defaults if none exist.
func (r *NotificationPreferencesRepository) GetPreferences(ctx context.Context, userID string) (*domain.NotificationPreferences, error) {
	var prefs domain.NotificationPreferences
	query := `SELECT user_id, comment_enabled, like_enabled, subscribe_enabled,
	                 mention_enabled, reply_enabled, upload_enabled, system_enabled, email_enabled
	          FROM notification_preferences WHERE user_id = $1`
	err := r.db.GetContext(ctx, &prefs, query, userID)
	if errors.Is(err, sql.ErrNoRows) {
		defaults := domain.DefaultNotificationPreferences(userID)
		if upsertErr := r.UpsertPreferences(ctx, defaults); upsertErr != nil {
			return nil, upsertErr
		}
		return defaults, nil
	}
	if err != nil {
		return nil, err
	}
	return &prefs, nil
}

// UpsertPreferences creates or updates the user's preferences.
func (r *NotificationPreferencesRepository) UpsertPreferences(ctx context.Context, prefs *domain.NotificationPreferences) error {
	query := `
		INSERT INTO notification_preferences
			(user_id, comment_enabled, like_enabled, subscribe_enabled,
			 mention_enabled, reply_enabled, upload_enabled, system_enabled, email_enabled)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (user_id) DO UPDATE SET
			comment_enabled   = EXCLUDED.comment_enabled,
			like_enabled      = EXCLUDED.like_enabled,
			subscribe_enabled = EXCLUDED.subscribe_enabled,
			mention_enabled   = EXCLUDED.mention_enabled,
			reply_enabled     = EXCLUDED.reply_enabled,
			upload_enabled    = EXCLUDED.upload_enabled,
			system_enabled    = EXCLUDED.system_enabled,
			email_enabled     = EXCLUDED.email_enabled`
	_, err := r.db.ExecContext(ctx, query,
		prefs.UserID,
		prefs.Comment,
		prefs.Like,
		prefs.Subscribe,
		prefs.Mention,
		prefs.Reply,
		prefs.Upload,
		prefs.System,
		prefs.EmailEnabled,
	)
	return err
}
