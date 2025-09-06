package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"
)

type subscriptionRepository struct {
	db *sqlx.DB
}

func NewSubscriptionRepository(db *sqlx.DB) usecase.SubscriptionRepository {
	return &subscriptionRepository{db: db}
}

func (r *subscriptionRepository) Subscribe(ctx context.Context, subscriberID, channelID string) error {
	// Idempotent insert; DB trigger adjusts counts when rows change
	query := `
        INSERT INTO subscriptions (subscriber_id, channel_id, created_at)
        VALUES ($1, $2, NOW())
        ON CONFLICT (subscriber_id, channel_id) DO NOTHING`

	if _, err := r.db.ExecContext(ctx, query, subscriberID, channelID); err != nil {
		return fmt.Errorf("failed to subscribe: %w", err)
	}
	return nil
}

func (r *subscriptionRepository) Unsubscribe(ctx context.Context, subscriberID, channelID string) error {
	query := `DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`
	if _, err := r.db.ExecContext(ctx, query, subscriberID, channelID); err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}
	return nil
}

// Internal row mapping reused from user repository pattern
type subUserRow struct {
	ID                string          `db:"id"`
	Username          string          `db:"username"`
	Email             string          `db:"email"`
	DisplayName       string          `db:"display_name"`
	AvatarID          sql.NullString  `db:"avatar_id"`
	AvatarIPFSCID     sql.NullString  `db:"avatar_ipfs_cid"`
	AvatarWebPIPFSCID sql.NullString  `db:"avatar_webp_ipfs_cid"`
	Bio               string          `db:"bio"`
	BitcoinWallet     string          `db:"bitcoin_wallet"`
	Role              domain.UserRole `db:"role"`
	IsActive          bool            `db:"is_active"`
	SubscriberCount   int64           `db:"subscriber_count"`
	CreatedAt         time.Time       `db:"created_at"`
	UpdatedAt         time.Time       `db:"updated_at"`
}

func mapSubUserRow(rrow subUserRow) *domain.User {
	u := &domain.User{
		ID:              rrow.ID,
		Username:        rrow.Username,
		Email:           rrow.Email,
		DisplayName:     rrow.DisplayName,
		Bio:             rrow.Bio,
		BitcoinWallet:   rrow.BitcoinWallet,
		Role:            rrow.Role,
		IsActive:        rrow.IsActive,
		SubscriberCount: rrow.SubscriberCount,
		CreatedAt:       rrow.CreatedAt,
		UpdatedAt:       rrow.UpdatedAt,
	}
	if rrow.AvatarID.Valid || rrow.AvatarIPFSCID.Valid || rrow.AvatarWebPIPFSCID.Valid {
		u.Avatar = &domain.Avatar{
			ID:          rrow.AvatarID.String,
			IPFSCID:     rrow.AvatarIPFSCID,
			WebPIPFSCID: rrow.AvatarWebPIPFSCID,
		}
	}
	return u
}

func (r *subscriptionRepository) ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error) {
	// Count total
	var total int64
	if err := r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`, subscriberID); err != nil {
		return nil, 0, fmt.Errorf("failed to count subscriptions: %w", err)
	}

	// Select subscribed channels with avatar and subscriber_count
	query := `
        SELECT u.id, u.username, u.email, u.display_name,
               a.id            AS avatar_id,
               a.ipfs_cid      AS avatar_ipfs_cid,
               a.webp_ipfs_cid AS avatar_webp_ipfs_cid,
               u.bio, u.bitcoin_wallet, u.role, u.is_active, u.subscriber_count, u.created_at, u.updated_at
        FROM subscriptions s
        JOIN users u ON u.id = s.channel_id
        LEFT JOIN user_avatars a ON a.user_id = u.id
        WHERE s.subscriber_id = $1
        ORDER BY u.username ASC
        LIMIT $2 OFFSET $3`

	var rows []subUserRow
	if err := r.db.SelectContext(ctx, &rows, query, subscriberID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to list subscriptions: %w", err)
	}

	users := make([]*domain.User, 0, len(rows))
	for _, rr := range rows {
		users = append(users, mapSubUserRow(rr))
	}
	return users, total, nil
}

func (r *subscriptionRepository) ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error) {
	// Count
	countQuery := `
        SELECT COUNT(*)
        FROM videos v
        WHERE v.user_id IN (SELECT channel_id FROM subscriptions WHERE subscriber_id = $1)
          AND v.privacy = 'public' AND v.status = 'completed'`
	var total int64
	if err := r.db.GetContext(ctx, &total, countQuery, subscriberID); err != nil {
		return nil, 0, fmt.Errorf("failed to count subscription videos: %w", err)
	}

	// Select
	query := `
        SELECT id, thumbnail_id, title, description, duration, views,
               privacy, status, upload_date, user_id,
               original_cid, processed_cids, thumbnail_cid,
               tags, category_id, language, file_size, mime_type, metadata,
               created_at, updated_at
        FROM videos
        WHERE user_id IN (SELECT channel_id FROM subscriptions WHERE subscriber_id = $1)
          AND privacy = 'public' AND status = 'completed'
        ORDER BY upload_date DESC
        LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, subscriberID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list subscription videos: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var videos []*domain.Video
	for rows.Next() {
		v, err := scanVideoRow(rows)
		if err != nil {
			return nil, 0, err
		}
		videos = append(videos, v)
	}
	return videos, total, nil
}

func (r *subscriptionRepository) CountSubscribers(ctx context.Context, channelID string) (int64, error) {
	var count int64
	if err := r.db.GetContext(ctx, &count, `SELECT subscriber_count FROM users WHERE id = $1`, channelID); err != nil {
		if err == sql.ErrNoRows {
			return 0, domain.ErrUserNotFound
		}
		return 0, fmt.Errorf("failed to get subscriber count: %w", err)
	}
	return count, nil
}
