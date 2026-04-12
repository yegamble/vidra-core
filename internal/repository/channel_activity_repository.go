package repository

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
)

type channelActivityRepository struct {
	db *sqlx.DB
}

// NewChannelActivityRepository creates a new channel activity repository.
func NewChannelActivityRepository(db *sqlx.DB) *channelActivityRepository {
	return &channelActivityRepository{db: db}
}

// CreateActivity inserts a new channel activity record.
func (r *channelActivityRepository) CreateActivity(ctx context.Context, activity *domain.ChannelActivity) error {
	if activity.ID == uuid.Nil {
		activity.ID = uuid.New()
	}
	query := `INSERT INTO channel_activities (id, channel_id, user_id, action_type, target_type, target_id, metadata, created_at)
	          VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`
	_, err := r.db.ExecContext(ctx, query,
		activity.ID, activity.ChannelID, activity.UserID,
		activity.ActionType, activity.TargetType, activity.TargetID,
		"{}", activity.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("create channel activity: %w", err)
	}
	return nil
}

// ListByChannel returns paginated activities for a channel.
func (r *channelActivityRepository) ListByChannel(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]domain.ChannelActivity, int64, error) {
	var total int64
	countQuery := `SELECT COUNT(*) FROM channel_activities WHERE channel_id = $1`
	if err := r.db.GetContext(ctx, &total, countQuery, channelID); err != nil {
		return nil, 0, fmt.Errorf("count channel activities: %w", err)
	}

	query := `SELECT id, channel_id, user_id, action_type, target_type, target_id, created_at
	          FROM channel_activities WHERE channel_id = $1
	          ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	var activities []domain.ChannelActivity
	if err := r.db.SelectContext(ctx, &activities, query, channelID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("list channel activities: %w", err)
	}
	if activities == nil {
		activities = []domain.ChannelActivity{}
	}
	return activities, total, nil
}
