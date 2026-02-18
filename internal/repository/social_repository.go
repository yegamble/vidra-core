package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

// SocialRepository handles social interaction data persistence
type SocialRepository struct {
	db *sqlx.DB
}

// NewSocialRepository creates a new social repository instance
func NewSocialRepository(db *sqlx.DB) *SocialRepository {
	return &SocialRepository{db: db}
}

// UpsertActor creates or updates an ATProto actor
func (r *SocialRepository) UpsertActor(ctx context.Context, actor *domain.ATProtoActor) error {
	query := `
		INSERT INTO atproto_actors (
			did, handle, display_name, bio, avatar_url, banner_url,
			created_at, updated_at, indexed_at, labels, local_user_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		ON CONFLICT (did) DO UPDATE SET
			handle = EXCLUDED.handle,
			display_name = EXCLUDED.display_name,
			bio = EXCLUDED.bio,
			avatar_url = EXCLUDED.avatar_url,
			banner_url = EXCLUDED.banner_url,
			updated_at = EXCLUDED.updated_at,
			indexed_at = EXCLUDED.indexed_at,
			labels = EXCLUDED.labels,
			local_user_id = COALESCE(EXCLUDED.local_user_id, atproto_actors.local_user_id)
		RETURNING *`

	return r.db.GetContext(ctx, actor, query,
		actor.DID, actor.Handle, actor.DisplayName, actor.Bio,
		actor.AvatarURL, actor.BannerURL, actor.CreatedAt,
		actor.UpdatedAt, actor.IndexedAt, actor.Labels, actor.LocalUserID,
	)
}

// GetActorByDID retrieves an actor by DID
func (r *SocialRepository) GetActorByDID(ctx context.Context, did string) (*domain.ATProtoActor, error) {
	var actor domain.ATProtoActor
	query := `SELECT * FROM atproto_actors WHERE did = $1`
	err := r.db.GetContext(ctx, &actor, query, did)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("actor not found")
	}
	return &actor, err
}

// GetActorByHandle retrieves an actor by handle
func (r *SocialRepository) GetActorByHandle(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	var actor domain.ATProtoActor
	query := `SELECT * FROM atproto_actors WHERE handle = $1`
	err := r.db.GetContext(ctx, &actor, query, handle)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("actor not found")
	}
	return &actor, err
}

// CreateFollow records a follow relationship
func (r *SocialRepository) CreateFollow(ctx context.Context, follow *domain.Follow) error {
	query := `
		INSERT INTO atproto_follows (
			follower_did, following_did, uri, cid, created_at, raw
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (uri) DO UPDATE SET
			cid = EXCLUDED.cid,
			revoked_at = NULL,
			raw = EXCLUDED.raw
		RETURNING id`

	return r.db.GetContext(ctx, &follow.ID, query,
		follow.FollowerDID, follow.FollowingDID,
		follow.URI, follow.CID, follow.CreatedAt, follow.Raw,
	)
}

// RevokeFollow marks a follow as revoked (unfollowed)
func (r *SocialRepository) RevokeFollow(ctx context.Context, uri string) error {
	query := `
		UPDATE atproto_follows
		SET revoked_at = CURRENT_TIMESTAMP
		WHERE uri = $1 AND revoked_at IS NULL`
	_, err := r.db.ExecContext(ctx, query, uri)
	return err
}

// GetFollowers retrieves followers of an actor
func (r *SocialRepository) GetFollowers(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	query := `
		SELECT f.*, a.handle as follower_handle
		FROM atproto_follows f
		JOIN atproto_actors a ON f.follower_did = a.did
		WHERE f.following_did = $1 AND f.revoked_at IS NULL
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3`

	var follows []domain.Follow
	err := r.db.SelectContext(ctx, &follows, query, did, limit, offset)
	return follows, err
}

// GetFollowing retrieves actors that an actor is following
func (r *SocialRepository) GetFollowing(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	query := `
		SELECT f.*, a.handle as following_handle
		FROM atproto_follows f
		JOIN atproto_actors a ON f.following_did = a.did
		WHERE f.follower_did = $1 AND f.revoked_at IS NULL
		ORDER BY f.created_at DESC
		LIMIT $2 OFFSET $3`

	var follows []domain.Follow
	err := r.db.SelectContext(ctx, &follows, query, did, limit, offset)
	return follows, err
}

// GetFollow retrieves a specific follow relationship
func (r *SocialRepository) GetFollow(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error) {
	query := `
		SELECT * FROM atproto_follows
		WHERE follower_did = $1 AND following_did = $2 AND revoked_at IS NULL`
	var follow domain.Follow
	err := r.db.GetContext(ctx, &follow, query, followerDID, followingDID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("follow not found")
		}
		return nil, err
	}
	return &follow, nil
}

// IsFollowing checks if one actor follows another
func (r *SocialRepository) IsFollowing(ctx context.Context, followerDID, followingDID string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM atproto_follows
			WHERE follower_did = $1 AND following_did = $2 AND revoked_at IS NULL
		)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, followerDID, followingDID)
	return exists, err
}

// CreateLike records a like
func (r *SocialRepository) CreateLike(ctx context.Context, like *domain.Like) error {
	query := `
		INSERT INTO atproto_likes (
			actor_did, subject_uri, subject_cid, uri, cid,
			created_at, video_id, post_id, raw
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (uri) DO UPDATE SET
			cid = EXCLUDED.cid,
			raw = EXCLUDED.raw
		RETURNING id`

	return r.db.GetContext(ctx, &like.ID, query,
		like.ActorDID, like.SubjectURI, like.SubjectCID,
		like.URI, like.CID, like.CreatedAt,
		like.VideoID, like.PostID, like.Raw,
	)
}

// DeleteLike removes a like
func (r *SocialRepository) DeleteLike(ctx context.Context, uri string) error {
	query := `DELETE FROM atproto_likes WHERE uri = $1`
	_, err := r.db.ExecContext(ctx, query, uri)
	return err
}

// GetLikes retrieves likes for a subject
func (r *SocialRepository) GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error) {
	query := `
		SELECT l.*, a.handle as actor_handle
		FROM atproto_likes l
		JOIN atproto_actors a ON l.actor_did = a.did
		WHERE l.subject_uri = $1
		ORDER BY l.created_at DESC
		LIMIT $2 OFFSET $3`

	var likes []domain.Like
	err := r.db.SelectContext(ctx, &likes, query, subjectURI, limit, offset)
	return likes, err
}

// GetLike retrieves a specific like
func (r *SocialRepository) GetLike(ctx context.Context, actorDID, subjectURI string) (*domain.Like, error) {
	query := `
		SELECT * FROM atproto_likes
		WHERE actor_did = $1 AND subject_uri = $2`
	var like domain.Like
	err := r.db.GetContext(ctx, &like, query, actorDID, subjectURI)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("like not found")
		}
		return nil, err
	}
	return &like, nil
}

// HasLiked checks if an actor has liked a subject
func (r *SocialRepository) HasLiked(ctx context.Context, actorDID, subjectURI string) (bool, error) {
	query := `
		SELECT EXISTS(
			SELECT 1 FROM atproto_likes
			WHERE actor_did = $1 AND subject_uri = $2
		)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, actorDID, subjectURI)
	return exists, err
}

// CreateComment records a comment/reply
func (r *SocialRepository) CreateComment(ctx context.Context, comment *domain.SocialComment) error {
	query := `
		INSERT INTO atproto_comments (
			actor_did, actor_handle, uri, cid, text,
			parent_uri, parent_cid, root_uri, root_cid,
			created_at, indexed_at, video_id, post_id,
			labels, blocked, raw
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
		ON CONFLICT (uri) DO UPDATE SET
			text = EXCLUDED.text,
			cid = EXCLUDED.cid,
			labels = EXCLUDED.labels,
			blocked = EXCLUDED.blocked,
			raw = EXCLUDED.raw
		RETURNING id`

	return r.db.GetContext(ctx, &comment.ID, query,
		comment.ActorDID, comment.ActorHandle, comment.URI, comment.CID,
		comment.Text, comment.ParentURI, comment.ParentCID,
		comment.RootURI, comment.RootCID, comment.CreatedAt,
		comment.IndexedAt, comment.VideoID, comment.PostID,
		comment.Labels, comment.Blocked, comment.Raw,
	)
}

// DeleteComment removes a comment
func (r *SocialRepository) DeleteComment(ctx context.Context, uri string) error {
	query := `DELETE FROM atproto_comments WHERE uri = $1`
	_, err := r.db.ExecContext(ctx, query, uri)
	return err
}

// GetComments retrieves comments for a subject (root URI)
func (r *SocialRepository) GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
	query := `
		SELECT c.*, a.handle as actor_handle, a.display_name
		FROM atproto_comments c
		JOIN atproto_actors a ON c.actor_did = a.did
		WHERE c.root_uri = $1 AND c.blocked = FALSE
		ORDER BY c.created_at DESC
		LIMIT $2 OFFSET $3`

	var comments []domain.SocialComment
	err := r.db.SelectContext(ctx, &comments, query, rootURI, limit, offset)
	return comments, err
}

// GetCommentThread retrieves a comment thread (replies to a specific comment)
func (r *SocialRepository) GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
	query := `
		SELECT c.*, a.handle as actor_handle, a.display_name
		FROM atproto_comments c
		JOIN atproto_actors a ON c.actor_did = a.did
		WHERE c.parent_uri = $1 AND c.blocked = FALSE
		ORDER BY c.created_at ASC
		LIMIT $2 OFFSET $3`

	var comments []domain.SocialComment
	err := r.db.SelectContext(ctx, &comments, query, parentURI, limit, offset)
	return comments, err
}

// CreateModerationLabel applies a moderation label
func (r *SocialRepository) CreateModerationLabel(ctx context.Context, label *domain.ModerationLabel) error {
	query := `
		INSERT INTO atproto_moderation_labels (
			actor_did, label_type, reason, applied_by,
			uri, created_at, expires_at, raw
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		RETURNING id`

	return r.db.GetContext(ctx, &label.ID, query,
		label.ActorDID, label.LabelType, label.Reason,
		label.AppliedBy, label.URI, label.CreatedAt,
		label.ExpiresAt, label.Raw,
	)
}

// RemoveModerationLabel removes a moderation label
func (r *SocialRepository) RemoveModerationLabel(ctx context.Context, id string) error {
	query := `DELETE FROM atproto_moderation_labels WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// GetModerationLabels retrieves moderation labels for an actor
func (r *SocialRepository) GetModerationLabels(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error) {
	query := `
		SELECT * FROM atproto_moderation_labels
		WHERE actor_did = $1
		AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		ORDER BY created_at DESC`

	var labels []domain.ModerationLabel
	err := r.db.SelectContext(ctx, &labels, query, actorDID)
	return labels, err
}

// HasBlockedLabel checks if an actor has any blocking moderation labels
func (r *SocialRepository) HasBlockedLabel(ctx context.Context, actorDID string, blockLabels []string) (bool, error) {
	if len(blockLabels) == 0 {
		return false, nil
	}

	query := `
		SELECT EXISTS(
			SELECT 1 FROM atproto_moderation_labels
			WHERE actor_did = $1
			AND label_type = ANY($2)
			AND (expires_at IS NULL OR expires_at > CURRENT_TIMESTAMP)
		)`

	var exists bool
	err := r.db.GetContext(ctx, &exists, query, actorDID, blockLabels)
	return exists, err
}

// GetSocialStats retrieves social statistics for an actor
func (r *SocialRepository) GetSocialStats(ctx context.Context, did string) (*domain.SocialStats, error) {
	query := `
		SELECT
			(SELECT COUNT(*) FROM atproto_follows WHERE following_did = $1 AND revoked_at IS NULL) as followers,
			(SELECT COUNT(*) FROM atproto_follows WHERE follower_did = $1 AND revoked_at IS NULL) as follows,
			(SELECT COUNT(*) FROM atproto_likes WHERE actor_did = $1) as likes,
			(SELECT COUNT(*) FROM atproto_comments WHERE actor_did = $1 AND blocked = FALSE) as comments,
			0 as reposts`

	var stats domain.SocialStats
	err := r.db.GetContext(ctx, &stats, query, did)
	return &stats, err
}

// RefreshSocialStats triggers a refresh of the materialized view
func (r *SocialRepository) RefreshSocialStats(ctx context.Context) error {
	query := `SELECT refresh_social_stats()`
	_, err := r.db.ExecContext(ctx, query)
	return err
}

// LinkLocalUser links an ATProto actor to a local user
func (r *SocialRepository) LinkLocalUser(ctx context.Context, did, userID string) error {
	query := `
		UPDATE atproto_actors
		SET local_user_id = $2
		WHERE did = $1`
	_, err := r.db.ExecContext(ctx, query, did, userID)
	return err
}

// GetBlockedLabels retrieves the list of blocked label types from config
func (r *SocialRepository) GetBlockedLabels(ctx context.Context) ([]string, error) {
	query := `
		SELECT value FROM instance_config
		WHERE key = 'atproto_block_labels'`

	var jsonData json.RawMessage
	err := r.db.GetContext(ctx, &jsonData, query)
	if err != nil {
		if err == sql.ErrNoRows {
			return []string{}, nil
		}
		return nil, err
	}

	var labels []string
	err = json.Unmarshal(jsonData, &labels)
	return labels, err
}

// BatchUpsertActors efficiently upserts multiple actors
func (r *SocialRepository) BatchUpsertActors(ctx context.Context, actors []domain.ATProtoActor) error {
	if len(actors) == 0 {
		return nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO atproto_actors (
			did, handle, display_name, bio, avatar_url, banner_url,
			created_at, updated_at, indexed_at, labels
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (did) DO UPDATE SET
			handle = EXCLUDED.handle,
			display_name = EXCLUDED.display_name,
			bio = EXCLUDED.bio,
			avatar_url = EXCLUDED.avatar_url,
			banner_url = EXCLUDED.banner_url,
			updated_at = EXCLUDED.updated_at,
			indexed_at = EXCLUDED.indexed_at,
			labels = EXCLUDED.labels`)
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()

	for _, actor := range actors {
		_, err = stmt.ExecContext(ctx,
			actor.DID, actor.Handle, actor.DisplayName, actor.Bio,
			actor.AvatarURL, actor.BannerURL, actor.CreatedAt,
			actor.UpdatedAt, actor.IndexedAt, actor.Labels,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// CleanupExpiredLabels removes expired moderation labels
func (r *SocialRepository) CleanupExpiredLabels(ctx context.Context) (int64, error) {
	query := `
		DELETE FROM atproto_moderation_labels
		WHERE expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`
	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// GetVideoURI constructs an ATProto URI for a local video
func (r *SocialRepository) GetVideoURI(ctx context.Context, videoID string) (string, error) {
	// Get instance DID from config
	query := `SELECT value FROM instance_config WHERE key = 'atproto_did'`
	var jsonDID json.RawMessage
	err := r.db.GetContext(ctx, &jsonDID, query)
	if err != nil {
		return "", err
	}

	var did string
	if err = json.Unmarshal(jsonDID, &did); err != nil {
		return "", err
	}

	// Generate rkey from video ID (could also use timestamp-based)
	rkey := fmt.Sprintf("video_%s_%d", videoID, time.Now().Unix())
	return fmt.Sprintf("at://%s/app.bsky.feed.post/%s", did, rkey), nil
}
