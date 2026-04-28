package inner_circle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrPostNotFound is returned when a post lookup or update targets a missing row.
var ErrPostNotFound = errors.New("inner_circle: post not found")

// PostRepository persists and queries channel_posts.
type PostRepository struct {
	db *sqlx.DB
}

// NewPostRepository wraps the given DB.
func NewPostRepository(db *sqlx.DB) *PostRepository {
	return &PostRepository{db: db}
}

// Create inserts a new post and returns the persisted row.
func (r *PostRepository) Create(ctx context.Context, channelID uuid.UUID, body string, tierID *string) (*domain.ChannelPost, error) {
	const query = `
		INSERT INTO channel_posts (channel_id, body, tier_id)
		VALUES ($1, $2, $3)
		RETURNING id, channel_id, body, tier_id, created_at, updated_at
	`
	var p domain.ChannelPost
	if err := r.db.QueryRowxContext(ctx, query, channelID, body, tierID).StructScan(&p); err != nil {
		return nil, fmt.Errorf("inner_circle: insert post: %w", err)
	}
	return &p, nil
}

// Update overwrites body and/or tier_id on an existing post owned by the
// channel.
func (r *PostRepository) Update(ctx context.Context, postID, channelID uuid.UUID, body *string, tierID *string, clearTier bool) (*domain.ChannelPost, error) {
	// Build dynamic SET clause to allow partial updates.
	setParts := []string{}
	args := []interface{}{postID, channelID}
	argN := 3
	if body != nil {
		setParts = append(setParts, fmt.Sprintf("body = $%d", argN))
		args = append(args, *body)
		argN++
	}
	if clearTier {
		setParts = append(setParts, "tier_id = NULL")
	} else if tierID != nil {
		setParts = append(setParts, fmt.Sprintf("tier_id = $%d", argN))
		args = append(args, *tierID)
		argN++
	}
	if len(setParts) == 0 {
		return r.Get(ctx, postID, channelID)
	}
	setParts = append(setParts, "updated_at = NOW()")
	query := fmt.Sprintf(`
		UPDATE channel_posts SET %s WHERE id = $1 AND channel_id = $2
		RETURNING id, channel_id, body, tier_id, created_at, updated_at
	`, joinSet(setParts))
	var p domain.ChannelPost
	if err := r.db.QueryRowxContext(ctx, query, args...).StructScan(&p); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPostNotFound
		}
		return nil, fmt.Errorf("inner_circle: update post: %w", err)
	}
	return &p, nil
}

func joinSet(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += ", "
		}
		out += p
	}
	return out
}

// Get returns one post by ID + channel for ownership-scoped reads.
func (r *PostRepository) Get(ctx context.Context, postID, channelID uuid.UUID) (*domain.ChannelPost, error) {
	const query = `
		SELECT id, channel_id, body, tier_id, created_at, updated_at
		FROM channel_posts WHERE id = $1 AND channel_id = $2
	`
	var p domain.ChannelPost
	if err := r.db.QueryRowxContext(ctx, query, postID, channelID).StructScan(&p); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrPostNotFound
		}
		return nil, fmt.Errorf("inner_circle: get post: %w", err)
	}
	return &p, nil
}

// Delete removes a post by ID + channel. Returns ErrPostNotFound when no row matched.
func (r *PostRepository) Delete(ctx context.Context, postID, channelID uuid.UUID) error {
	const query = `DELETE FROM channel_posts WHERE id = $1 AND channel_id = $2`
	res, err := r.db.ExecContext(ctx, query, postID, channelID)
	if err != nil {
		return fmt.Errorf("inner_circle: delete post: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrPostNotFound
	}
	return nil
}

// List returns up to limit posts for a channel, newest first, before the
// optional cursor (post id). Cursor is opaque to callers but in practice is
// the id of the oldest item from the previous page.
func (r *PostRepository) List(ctx context.Context, channelID uuid.UUID, cursor *uuid.UUID, limit int) ([]domain.ChannelPost, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	args := []interface{}{channelID}
	query := `
		SELECT id, channel_id, body, tier_id, created_at, updated_at
		FROM channel_posts WHERE channel_id = $1`
	if cursor != nil {
		args = append(args, *cursor)
		query += ` AND created_at < (SELECT created_at FROM channel_posts WHERE id = $2)`
	}
	args = append(args, limit)
	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", len(args))

	rows, err := r.db.QueryxContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("inner_circle: list posts: %w", err)
	}
	defer rows.Close()

	out := make([]domain.ChannelPost, 0, limit)
	for rows.Next() {
		var p domain.ChannelPost
		if err := rows.StructScan(&p); err != nil {
			return nil, fmt.Errorf("inner_circle: scan post: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
