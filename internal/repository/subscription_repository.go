package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type subscriptionRepository struct {
	db *sqlx.DB
	tm *TransactionManager
}

func NewSubscriptionRepository(db *sqlx.DB) usecase.SubscriptionRepository {
	return &subscriptionRepository{
		db: db,
		tm: NewTransactionManager(db),
	}
}

// SubscribeToChannel subscribes a user to a channel
func (r *subscriptionRepository) SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	// Use transaction for atomicity
	return r.tm.WithTransaction(ctx, nil, func(tx *sqlx.Tx) error {
		// Check if user is trying to subscribe to their own channel
		var accountID uuid.UUID
		checkQuery := `SELECT account_id FROM channels WHERE id = $1`
		if err := tx.GetContext(ctx, &accountID, checkQuery, channelID); err != nil {
			if err == sql.ErrNoRows {
				return domain.ErrNotFound
			}
			return fmt.Errorf("failed to check channel ownership: %w", err)
		}

		if accountID == subscriberID {
			return fmt.Errorf("cannot subscribe to your own channel")
		}

		// Idempotent insert; DB trigger adjusts counts when rows change
		query := `
            INSERT INTO subscriptions (subscriber_id, channel_id, created_at)
            VALUES ($1, $2, NOW())
            ON CONFLICT (subscriber_id, channel_id) DO NOTHING`

		if _, err := tx.ExecContext(ctx, query, subscriberID, channelID); err != nil {
			return fmt.Errorf("failed to subscribe: %w", err)
		}
		return nil
	})
}

// UnsubscribeFromChannel unsubscribes a user from a channel
func (r *subscriptionRepository) UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	query := `DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`
	result, err := r.db.ExecContext(ctx, query, subscriberID, channelID)
	if err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// IsSubscribed checks if a user is subscribed to a channel
func (r *subscriptionRepository) IsSubscribed(ctx context.Context, subscriberID, channelID uuid.UUID) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2)`

	if err := r.db.GetContext(ctx, &exists, query, subscriberID, channelID); err != nil {
		return false, fmt.Errorf("failed to check subscription: %w", err)
	}

	return exists, nil
}

// ListUserSubscriptions lists all channels a user is subscribed to
func (r *subscriptionRepository) ListUserSubscriptions(ctx context.Context, subscriberID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`
	if err := r.db.GetContext(ctx, &total, countQuery, subscriberID); err != nil {
		return nil, fmt.Errorf("failed to count subscriptions: %w", err)
	}

	// Select subscribed channels with details
	query := `
        SELECT
            s.id, s.subscriber_id, s.channel_id, s.created_at,
            c.id as "channel.id",
            c.account_id as "channel.account_id",
            c.handle as "channel.handle",
            c.display_name as "channel.display_name",
            c.description as "channel.description",
            c.is_local as "channel.is_local",
            c.subscriber_count as "channel.followers_count",
            c.videos_count as "channel.videos_count",
            c.created_at as "channel.created_at",
            c.updated_at as "channel.updated_at"
        FROM subscriptions s
        JOIN channels c ON s.channel_id = c.id
        WHERE s.subscriber_id = $1
        ORDER BY s.created_at DESC
        LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, subscriberID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list subscriptions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subscriptions []domain.Subscription
	for rows.Next() {
		var sub domain.Subscription
		var channel domain.Channel

		err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.ChannelID, &sub.CreatedAt,
			&channel.ID, &channel.AccountID, &channel.Handle, &channel.DisplayName,
			&channel.Description, &channel.IsLocal, &channel.FollowersCount,
			&channel.VideosCount, &channel.CreatedAt, &channel.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan subscription: %w", err)
		}

		sub.Channel = &channel
		subscriptions = append(subscriptions, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating subscriptions: %w", err)
	}

	return &domain.SubscriptionResponse{
		Total: total,
		Data:  subscriptions,
	}, nil
}

// ListChannelSubscribers lists all users subscribed to a channel
func (r *subscriptionRepository) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	// Count total
	var total int
	countQuery := `SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`
	if err := r.db.GetContext(ctx, &total, countQuery, channelID); err != nil {
		return nil, fmt.Errorf("failed to count subscribers: %w", err)
	}

	// Select subscribers with user details
	query := `
        SELECT
            s.id, s.subscriber_id, s.channel_id, s.created_at,
            u.id as "subscriber.id",
            u.username as "subscriber.username",
            u.email as "subscriber.email",
            u.display_name as "subscriber.display_name",
            u.bio as "subscriber.bio",
            u.role as "subscriber.role",
            u.is_active as "subscriber.is_active",
            u.created_at as "subscriber.created_at",
            u.updated_at as "subscriber.updated_at"
        FROM subscriptions s
        JOIN users u ON s.subscriber_id = u.id::uuid
        WHERE s.channel_id = $1
        ORDER BY s.created_at DESC
        LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, channelID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list subscribers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subscriptions []domain.Subscription
	for rows.Next() {
		var sub domain.Subscription
		var user domain.User

		err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.ChannelID, &sub.CreatedAt,
			&user.ID, &user.Username, &user.Email, &user.DisplayName,
			&user.Bio, &user.Role, &user.IsActive, &user.CreatedAt, &user.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan subscriber: %w", err)
		}

		sub.Subscriber = &user
		subscriptions = append(subscriptions, sub)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating subscribers: %w", err)
	}

	return &domain.SubscriptionResponse{
		Total: total,
		Data:  subscriptions,
	}, nil
}

// GetSubscriptionVideos gets videos from subscribed channels
func (r *subscriptionRepository) GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error) {
	// Count total videos from subscribed channels
	var total int
	countQuery := `
        SELECT COUNT(*)
        FROM videos v
        JOIN channels c ON v.channel_id = c.id
        JOIN subscriptions s ON s.channel_id = c.id
        WHERE s.subscriber_id = $1
            AND v.privacy = 'public'
            AND v.status = 'ready'`

	if err := r.db.GetContext(ctx, &total, countQuery, subscriberID); err != nil {
		return nil, 0, fmt.Errorf("failed to count subscription videos: %w", err)
	}

	// Get videos from subscribed channels
	query := `
        SELECT
            v.id, v.title, v.description, v.duration, v.views,
            v.privacy, v.status, v.upload_date, v.user_id, v.channel_id,
            v.original_cid, v.processed_cids, v.thumbnail_cid,
            v.output_paths, v.thumbnail_path, v.preview_path,
            v.tags, v.category_id, v.language, v.file_size,
            v.mime_type, v.metadata, v.created_at, v.updated_at
        FROM videos v
        JOIN channels c ON v.channel_id = c.id
        JOIN subscriptions s ON s.channel_id = c.id
        WHERE s.subscriber_id = $1
            AND v.privacy = 'public'
            AND v.status = 'completed'
        ORDER BY v.upload_date DESC
        LIMIT $2 OFFSET $3`

	var videos []domain.Video
	if err := r.db.SelectContext(ctx, &videos, query, subscriberID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("failed to get subscription videos: %w", err)
	}

	return videos, total, nil
}

// Backward compatibility methods (deprecated - will be removed)

// Subscribe subscribes a user to another user (DEPRECATED - use SubscribeToChannel)
func (r *subscriptionRepository) Subscribe(ctx context.Context, subscriberID, userID string) error {
	// Convert user subscription to channel subscription
	// First, get the default channel for the target user
	var channelID uuid.UUID
	channelQuery := `
		SELECT id FROM channels
		WHERE account_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT 1`

	if err := r.db.GetContext(ctx, &channelID, channelQuery, userID); err != nil {
		if err == sql.ErrNoRows {
			// No channel exists for this user
			return fmt.Errorf("user has no channels")
		}
		return fmt.Errorf("failed to find user channel: %w", err)
	}

	subID, err := uuid.Parse(subscriberID)
	if err != nil {
		return fmt.Errorf("invalid subscriber ID: %w", err)
	}

	return r.SubscribeToChannel(ctx, subID, channelID)
}

// Unsubscribe unsubscribes a user from another user (DEPRECATED - use UnsubscribeFromChannel)
func (r *subscriptionRepository) Unsubscribe(ctx context.Context, subscriberID, userID string) error {
	// Convert user unsubscription to channel unsubscription
	// First, get all channels for the target user
	query := `
		DELETE FROM subscriptions
		WHERE subscriber_id = $1::uuid
		AND channel_id IN (
			SELECT id FROM channels WHERE account_id = $2::uuid
		)`

	if _, err := r.db.ExecContext(ctx, query, subscriberID, userID); err != nil {
		return fmt.Errorf("failed to unsubscribe: %w", err)
	}

	return nil
}

// ListSubscriptions lists user subscriptions (DEPRECATED - use ListUserSubscriptions)
func (r *subscriptionRepository) ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error) {
	subID, err := uuid.Parse(subscriberID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid subscriber ID: %w", err)
	}

	response, err := r.ListUserSubscriptions(ctx, subID, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	// Convert to user list for backward compatibility
	userMap := make(map[string]*domain.User)
	for _, sub := range response.Data {
		if sub.Channel != nil {
			// Get the user who owns this channel
			var user domain.User
			userQuery := `
				SELECT id, username, email, display_name, bio, bitcoin_wallet,
				       role, is_active, created_at, updated_at
				FROM users WHERE id = $1`

			if err := r.db.GetContext(ctx, &user, userQuery, sub.Channel.AccountID.String()); err == nil {
				userMap[user.ID] = &user
			}
		}
	}

	// Convert map to slice
	users := make([]*domain.User, 0, len(userMap))
	for _, user := range userMap {
		users = append(users, user)
	}

	return users, int64(response.Total), nil
}

// ListSubscriptionVideos returns public videos from subscribed channels (DEPRECATED)
func (r *subscriptionRepository) ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error) {
	subID, err := uuid.Parse(subscriberID)
	if err != nil {
		return nil, 0, fmt.Errorf("invalid subscriber ID: %w", err)
	}

	videos, total, err := r.GetSubscriptionVideos(ctx, subID, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	// Convert to pointer slice for backward compatibility
	videoPointers := make([]*domain.Video, len(videos))
	for i := range videos {
		videoPointers[i] = &videos[i]
	}

	return videoPointers, int64(total), nil
}

// CountSubscribers returns the subscriber count for a channel (DEPRECATED)
func (r *subscriptionRepository) CountSubscribers(ctx context.Context, channelID string) (int64, error) {
	var count int64
	query := `SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1::uuid`

	if err := r.db.GetContext(ctx, &count, query, channelID); err != nil {
		return 0, fmt.Errorf("failed to count subscribers: %w", err)
	}

	return count, nil
}

// GetSubscribers returns all subscribers for a channel (DEPRECATED)
func (r *subscriptionRepository) GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error) {
	chanID, err := uuid.Parse(channelID)
	if err != nil {
		return nil, fmt.Errorf("invalid channel ID: %w", err)
	}

	response, err := r.ListChannelSubscribers(ctx, chanID, 1000, 0) // Get up to 1000 subscribers
	if err != nil {
		return nil, err
	}

	// Convert to pointer slice
	subscriptions := make([]*domain.Subscription, len(response.Data))
	for i := range response.Data {
		subscriptions[i] = &response.Data[i]
	}

	return subscriptions, nil
}
