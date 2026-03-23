package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/security"
)

// ActivityPubRepository handles ActivityPub data persistence
type ActivityPubRepository struct {
	db         *sqlx.DB
	encryption *security.ActivityPubKeyEncryption
}

// NewActivityPubRepository creates a new ActivityPub repository
func NewActivityPubRepository(db *sqlx.DB, encryption *security.ActivityPubKeyEncryption) *ActivityPubRepository {
	return &ActivityPubRepository{
		db:         db,
		encryption: encryption,
	}
}

// Actor Keys Methods

// GetActorKeys retrieves the public/private key pair for a local actor
// The private key is automatically decrypted before being returned
func (r *ActivityPubRepository) GetActorKeys(ctx context.Context, actorID string) (publicKey, privateKey string, err error) {
	query := `SELECT public_key_pem, private_key_pem FROM ap_actor_keys WHERE actor_id = $1`
	var encryptedPrivateKey string
	err = r.db.QueryRowContext(ctx, query, actorID).Scan(&publicKey, &encryptedPrivateKey)
	if err != nil {
		return "", "", fmt.Errorf("failed to get actor keys: %w", err)
	}

	// Decrypt the private key
	if r.encryption != nil {
		privateKey, err = r.encryption.DecryptPrivateKey(encryptedPrivateKey)
		if err != nil {
			return "", "", fmt.Errorf("failed to decrypt private key: %w", err)
		}
	} else {
		// Fallback for testing or when encryption is not configured
		privateKey = encryptedPrivateKey
	}

	return publicKey, privateKey, nil
}

// StoreActorKeys stores the public/private key pair for a local actor
// The private key is automatically encrypted before being stored
func (r *ActivityPubRepository) StoreActorKeys(ctx context.Context, actorID, publicKey, privateKey string) error {
	// Encrypt the private key before storing
	var encryptedPrivateKey string
	var err error
	if r.encryption != nil {
		encryptedPrivateKey, err = r.encryption.EncryptPrivateKey(privateKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt private key: %w", err)
		}
	} else {
		// Fallback for testing or when encryption is not configured
		encryptedPrivateKey = privateKey
	}

	query := `
		INSERT INTO ap_actor_keys (actor_id, public_key_pem, private_key_pem)
		VALUES ($1, $2, $3)
		ON CONFLICT (actor_id) DO UPDATE
		SET public_key_pem = EXCLUDED.public_key_pem,
		    private_key_pem = EXCLUDED.private_key_pem,
		    updated_at = CURRENT_TIMESTAMP
	`
	_, err = r.db.ExecContext(ctx, query, actorID, publicKey, encryptedPrivateKey)
	if err != nil {
		return fmt.Errorf("failed to store actor keys: %w", err)
	}
	return nil
}

// Remote Actor Methods

// GetRemoteActor retrieves a cached remote actor by URI
func (r *ActivityPubRepository) GetRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error) {
	var actor domain.APRemoteActor
	query := `SELECT * FROM ap_remote_actors WHERE actor_uri = $1`
	err := r.db.GetContext(ctx, &actor, query, actorURI)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get remote actor: %w", err)
	}
	return &actor, nil
}

// GetRemoteActors retrieves multiple cached remote actors by their URIs
func (r *ActivityPubRepository) GetRemoteActors(ctx context.Context, actorURIs []string) ([]*domain.APRemoteActor, error) {
	if len(actorURIs) == 0 {
		return nil, nil
	}

	query, args, err := sqlx.In(`SELECT * FROM ap_remote_actors WHERE actor_uri IN (?)`, actorURIs)
	if err != nil {
		return nil, fmt.Errorf("failed to build IN query: %w", err)
	}

	query = r.db.Rebind(query)
	var actors []*domain.APRemoteActor
	err = r.db.SelectContext(ctx, &actors, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote actors: %w", err)
	}

	return actors, nil
}

// UpsertRemoteActor inserts or updates a remote actor in the cache
func (r *ActivityPubRepository) UpsertRemoteActor(ctx context.Context, actor *domain.APRemoteActor) error {
	query := `
		INSERT INTO ap_remote_actors (
			id, actor_uri, type, username, domain, display_name, summary,
			inbox_url, outbox_url, shared_inbox, followers_url, following_url,
			public_key_id, public_key_pem, icon_url, image_url, last_fetched_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17
		)
		ON CONFLICT (actor_uri) DO UPDATE SET
			type = EXCLUDED.type,
			display_name = EXCLUDED.display_name,
			summary = EXCLUDED.summary,
			inbox_url = EXCLUDED.inbox_url,
			outbox_url = EXCLUDED.outbox_url,
			shared_inbox = EXCLUDED.shared_inbox,
			followers_url = EXCLUDED.followers_url,
			following_url = EXCLUDED.following_url,
			public_key_id = EXCLUDED.public_key_id,
			public_key_pem = EXCLUDED.public_key_pem,
			icon_url = EXCLUDED.icon_url,
			image_url = EXCLUDED.image_url,
			last_fetched_at = EXCLUDED.last_fetched_at,
			updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`

	if actor.ID == "" {
		actor.ID = uuid.New().String()
	}
	now := time.Now()
	actor.LastFetchedAt = &now

	return r.db.QueryRowContext(ctx, query,
		actor.ID, actor.ActorURI, actor.Type, actor.Username, actor.Domain,
		actor.DisplayName, actor.Summary, actor.InboxURL, actor.OutboxURL,
		actor.SharedInbox, actor.FollowersURL, actor.FollowingURL,
		actor.PublicKeyID, actor.PublicKeyPem, actor.IconURL, actor.ImageURL,
		actor.LastFetchedAt,
	).Scan(&actor.ID)
}

// Activity Methods

// StoreActivity stores an activity in the database
func (r *ActivityPubRepository) StoreActivity(ctx context.Context, activity *domain.APActivity) error {
	if activity.ID == "" {
		activity.ID = uuid.New().String()
	}

	query := `
		INSERT INTO ap_activities (
			id, activity_uri, actor_id, type, object_id, object_type,
			target_id, published, activity_json, local
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (activity_uri) DO NOTHING
		RETURNING id
	`

	activityURI := activity.ID
	if activityURI == "" {
		activityURI = uuid.New().String()
	}

	err := r.db.QueryRowContext(ctx, query,
		activity.ID, activityURI, activity.ActorID, activity.Type,
		activity.ObjectID, activity.ObjectType, activity.TargetID,
		activity.Published, activity.ActivityJSON, activity.Local,
	).Scan(&activity.ID)

	if err != nil && err.Error() == "no rows in result set" {
		// Activity already exists (conflict)
		return nil
	}

	return err
}

// GetActivity retrieves an activity by URI
func (r *ActivityPubRepository) GetActivity(ctx context.Context, activityURI string) (*domain.APActivity, error) {
	var activity domain.APActivity
	query := `SELECT * FROM ap_activities WHERE activity_uri = $1`
	err := r.db.GetContext(ctx, &activity, query, activityURI)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get activity: %w", err)
	}
	return &activity, nil
}

// GetActivitiesByActor retrieves activities by actor ID with pagination
func (r *ActivityPubRepository) GetActivitiesByActor(ctx context.Context, actorID string, limit, offset int) ([]*domain.APActivity, int, error) {
	var activities []*domain.APActivity
	query := `
		SELECT * FROM ap_activities
		WHERE actor_id = $1
		ORDER BY published DESC
		LIMIT $2 OFFSET $3
	`
	err := r.db.SelectContext(ctx, &activities, query, actorID, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get activities: %w", err)
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) FROM ap_activities WHERE actor_id = $1`
	err = r.db.GetContext(ctx, &total, countQuery, actorID)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count activities: %w", err)
	}

	return activities, total, nil
}

// Follower Methods

// GetFollower retrieves a follower relationship
func (r *ActivityPubRepository) GetFollower(ctx context.Context, actorID, followerID string) (*domain.APFollower, error) {
	var follower domain.APFollower
	query := `SELECT * FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`
	err := r.db.GetContext(ctx, &follower, query, actorID, followerID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get follower: %w", err)
	}
	return &follower, nil
}

// UpsertFollower inserts or updates a follower relationship
func (r *ActivityPubRepository) UpsertFollower(ctx context.Context, follower *domain.APFollower) error {
	if follower.ID == "" {
		follower.ID = uuid.New().String()
	}

	query := `
		INSERT INTO ap_followers (id, actor_id, follower_id, state)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (actor_id, follower_id) DO UPDATE
		SET state = EXCLUDED.state, updated_at = CURRENT_TIMESTAMP
		RETURNING id
	`

	return r.db.QueryRowContext(ctx, query,
		follower.ID, follower.ActorID, follower.FollowerID, follower.State,
	).Scan(&follower.ID)
}

// DeleteFollower deletes a follower relationship
func (r *ActivityPubRepository) DeleteFollower(ctx context.Context, actorID, followerID string) error {
	query := `DELETE FROM ap_followers WHERE actor_id = $1 AND follower_id = $2`
	_, err := r.db.ExecContext(ctx, query, actorID, followerID)
	if err != nil {
		return fmt.Errorf("failed to delete follower: %w", err)
	}
	return nil
}

// GetFollowers retrieves followers with pagination
func (r *ActivityPubRepository) GetFollowers(ctx context.Context, actorID string, state string, limit, offset int) ([]*domain.APFollower, int, error) {
	var followers []*domain.APFollower
	query := `
		SELECT * FROM ap_followers
		WHERE actor_id = $1 AND state = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`
	err := r.db.SelectContext(ctx, &followers, query, actorID, state, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get followers: %w", err)
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) FROM ap_followers WHERE actor_id = $1 AND state = $2`
	err = r.db.GetContext(ctx, &total, countQuery, actorID, state)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count followers: %w", err)
	}

	return followers, total, nil
}

// GetFollowing retrieves accounts an actor is following
func (r *ActivityPubRepository) GetFollowing(ctx context.Context, followerID string, state string, limit, offset int) ([]*domain.APFollower, int, error) {
	var following []*domain.APFollower
	query := `
		SELECT * FROM ap_followers
		WHERE follower_id = $1 AND state = $2
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4
	`
	err := r.db.SelectContext(ctx, &following, query, followerID, state, limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get following: %w", err)
	}

	// Get total count
	var total int
	countQuery := `SELECT COUNT(*) FROM ap_followers WHERE follower_id = $1 AND state = $2`
	err = r.db.GetContext(ctx, &total, countQuery, followerID, state)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to count following: %w", err)
	}

	return following, total, nil
}

// Delivery Queue Methods

// EnqueueDelivery adds an activity to the delivery queue
func (r *ActivityPubRepository) EnqueueDelivery(ctx context.Context, delivery *domain.APDeliveryQueue) error {
	if delivery.ID == "" {
		delivery.ID = uuid.New().String()
	}

	query := `
		INSERT INTO ap_delivery_queue (
			id, activity_id, inbox_url, actor_id, attempts, max_attempts,
			next_attempt, last_error, status
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.ExecContext(ctx, query,
		delivery.ID, delivery.ActivityID, delivery.InboxURL, delivery.ActorID,
		delivery.Attempts, delivery.MaxAttempts, delivery.NextAttempt,
		delivery.LastError, delivery.Status,
	)

	return err
}

// BulkEnqueueDelivery adds multiple activities to the delivery queue in a single batch
func (r *ActivityPubRepository) BulkEnqueueDelivery(ctx context.Context, deliveries []*domain.APDeliveryQueue) error {
	if len(deliveries) == 0 {
		return nil
	}

	const numFields = 9
	query := `INSERT INTO ap_delivery_queue (
		id, activity_id, inbox_url, actor_id, attempts, max_attempts,
		next_attempt, last_error, status
	) VALUES `

	values := make([]interface{}, 0, len(deliveries)*numFields)
	placeholders := make([]string, 0, len(deliveries))

	for i, d := range deliveries {
		if d.ID == "" {
			d.ID = uuid.New().String()
		}

		offset := i * numFields
		placeholders = append(placeholders, fmt.Sprintf("($%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d, $%d)",
			offset+1, offset+2, offset+3, offset+4, offset+5, offset+6, offset+7, offset+8, offset+9))

		values = append(values, d.ID, d.ActivityID, d.InboxURL, d.ActorID,
			d.Attempts, d.MaxAttempts, d.NextAttempt, d.LastError, d.Status)
	}

	query += strings.Join(placeholders, ", ")

	_, err := r.db.ExecContext(ctx, query, values...)
	return err
}

// GetPendingDeliveries retrieves pending deliveries ready to be processed
func (r *ActivityPubRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*domain.APDeliveryQueue, error) {
	var deliveries []*domain.APDeliveryQueue
	query := `
		SELECT * FROM ap_delivery_queue
		WHERE status = 'pending' AND next_attempt <= $1
		ORDER BY next_attempt ASC
		LIMIT $2
	`
	err := r.db.SelectContext(ctx, &deliveries, query, time.Now(), limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending deliveries: %w", err)
	}
	return deliveries, nil
}

// UpdateDeliveryStatus updates the status of a delivery
func (r *ActivityPubRepository) UpdateDeliveryStatus(ctx context.Context, deliveryID string, status string, attempts int, lastError *string, nextAttempt time.Time) error {
	query := `
		UPDATE ap_delivery_queue
		SET status = $1, attempts = $2, last_error = $3, next_attempt = $4, updated_at = CURRENT_TIMESTAMP
		WHERE id = $5
	`
	_, err := r.db.ExecContext(ctx, query, status, attempts, lastError, nextAttempt, deliveryID)
	if err != nil {
		return fmt.Errorf("failed to update delivery status: %w", err)
	}
	return nil
}

// Deduplication Methods

// IsActivityReceived checks if an activity has been received before
func (r *ActivityPubRepository) IsActivityReceived(ctx context.Context, activityURI string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM ap_received_activities WHERE activity_uri = $1)`
	err := r.db.GetContext(ctx, &exists, query, activityURI)
	if err != nil {
		return false, fmt.Errorf("failed to check activity: %w", err)
	}
	return exists, nil
}

// MarkActivityReceived marks an activity as received
func (r *ActivityPubRepository) MarkActivityReceived(ctx context.Context, activityURI string) error {
	query := `INSERT INTO ap_received_activities (activity_uri) VALUES ($1) ON CONFLICT DO NOTHING`
	_, err := r.db.ExecContext(ctx, query, activityURI)
	if err != nil {
		return fmt.Errorf("failed to mark activity received: %w", err)
	}
	return nil
}

// Video Reaction Methods

// UpsertVideoReaction inserts or updates a video reaction (like/dislike)
func (r *ActivityPubRepository) UpsertVideoReaction(ctx context.Context, videoID, actorURI, reactionType, activityURI string) error {
	query := `
		INSERT INTO ap_video_reactions (video_id, actor_uri, reaction_type, activity_uri)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (video_id, actor_uri, reaction_type) DO UPDATE
		SET activity_uri = EXCLUDED.activity_uri
	`
	_, err := r.db.ExecContext(ctx, query, videoID, actorURI, reactionType, activityURI)
	if err != nil {
		return fmt.Errorf("failed to upsert video reaction: %w", err)
	}
	return nil
}

// DeleteVideoReaction deletes a video reaction
func (r *ActivityPubRepository) DeleteVideoReaction(ctx context.Context, activityURI string) error {
	query := `DELETE FROM ap_video_reactions WHERE activity_uri = $1`
	_, err := r.db.ExecContext(ctx, query, activityURI)
	if err != nil {
		return fmt.Errorf("failed to delete video reaction: %w", err)
	}
	return nil
}

// GetVideoReactionStats retrieves reaction statistics for a video
func (r *ActivityPubRepository) GetVideoReactionStats(ctx context.Context, videoID string) (likes, dislikes int, err error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN reaction_type = 'like' THEN 1 ELSE 0 END), 0) as likes,
			COALESCE(SUM(CASE WHEN reaction_type = 'dislike' THEN 1 ELSE 0 END), 0) as dislikes
		FROM ap_video_reactions
		WHERE video_id = $1
	`
	err = r.db.QueryRowContext(ctx, query, videoID).Scan(&likes, &dislikes)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get video reaction stats: %w", err)
	}
	return likes, dislikes, nil
}

// Video Share Methods

// UpsertVideoShare inserts or updates a video share (announce)
func (r *ActivityPubRepository) UpsertVideoShare(ctx context.Context, videoID, actorURI, activityURI string) error {
	query := `
		INSERT INTO ap_video_shares (video_id, actor_uri, activity_uri)
		VALUES ($1, $2, $3)
		ON CONFLICT (video_id, actor_uri) DO UPDATE
		SET activity_uri = EXCLUDED.activity_uri
	`
	_, err := r.db.ExecContext(ctx, query, videoID, actorURI, activityURI)
	if err != nil {
		return fmt.Errorf("failed to upsert video share: %w", err)
	}
	return nil
}

// DeleteVideoShare deletes a video share
func (r *ActivityPubRepository) DeleteVideoShare(ctx context.Context, activityURI string) error {
	query := `DELETE FROM ap_video_shares WHERE activity_uri = $1`
	_, err := r.db.ExecContext(ctx, query, activityURI)
	if err != nil {
		return fmt.Errorf("failed to delete video share: %w", err)
	}
	return nil
}

// GetVideoShareCount retrieves the share count for a video
func (r *ActivityPubRepository) GetVideoShareCount(ctx context.Context, videoID string) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM ap_video_shares WHERE video_id = $1`
	err := r.db.GetContext(ctx, &count, query, videoID)
	if err != nil {
		return 0, fmt.Errorf("failed to get video share count: %w", err)
	}
	return count, nil
}
