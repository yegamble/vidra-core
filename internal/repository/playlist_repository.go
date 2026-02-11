package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type playlistRepository struct {
	db *sqlx.DB
}

func NewPlaylistRepository(db *sqlx.DB) usecase.PlaylistRepository {
	return &playlistRepository{db: db}
}

// Create creates a new playlist
func (r *playlistRepository) Create(ctx context.Context, playlist *domain.Playlist) error {
	playlist.ID = uuid.New()
	playlist.CreatedAt = time.Now()
	playlist.UpdatedAt = time.Now()

	query := `
		INSERT INTO playlists (
			id, user_id, name, description, privacy, thumbnail_url,
			is_watch_later, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)`

	_, err := r.db.ExecContext(
		ctx, query,
		playlist.ID, playlist.UserID, playlist.Name, playlist.Description,
		playlist.Privacy, playlist.ThumbnailURL, playlist.IsWatchLater,
		playlist.CreatedAt, playlist.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create playlist: %w", err)
	}

	return nil
}

// GetByID retrieves a playlist by ID
func (r *playlistRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Playlist, error) {
	playlist := &domain.Playlist{}
	query := `
		SELECT p.id, p.user_id, p.name, p.description, p.privacy,
		       p.thumbnail_url, p.is_watch_later, p.created_at, p.updated_at,
		       COUNT(pi.id) as item_count
		FROM playlists p
		LEFT JOIN playlist_items pi ON p.id = pi.playlist_id
		WHERE p.id = $1
		GROUP BY p.id`

	err := r.db.GetContext(ctx, playlist, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get playlist: %w", err)
	}

	return playlist, nil
}

// Update updates a playlist
func (r *playlistRepository) Update(ctx context.Context, id uuid.UUID, updates domain.UpdatePlaylistRequest) error {
	query := `
		UPDATE playlists
		SET updated_at = $1`

	args := []interface{}{time.Now()}
	argCount := 1

	if updates.Name != nil {
		argCount++
		query += fmt.Sprintf(", name = $%d", argCount)
		args = append(args, *updates.Name)
	}

	if updates.Description != nil {
		argCount++
		query += fmt.Sprintf(", description = $%d", argCount)
		args = append(args, *updates.Description)
	}

	if updates.Privacy != nil {
		argCount++
		query += fmt.Sprintf(", privacy = $%d", argCount)
		args = append(args, *updates.Privacy)
	}

	if updates.ThumbnailURL != nil {
		argCount++
		query += fmt.Sprintf(", thumbnail_url = $%d", argCount)
		args = append(args, *updates.ThumbnailURL)
	}

	argCount++
	query += fmt.Sprintf(" WHERE id = $%d", argCount)
	args = append(args, id)

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update playlist: %w", err)
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

// Delete deletes a playlist
func (r *playlistRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM playlists WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete playlist: %w", err)
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

// List lists playlists with filtering and pagination
func (r *playlistRepository) List(ctx context.Context, opts domain.PlaylistListOptions) ([]*domain.Playlist, int, error) {
	playlists := []*domain.Playlist{}

	// Build query
	query := `
		SELECT p.id, p.user_id, p.name, p.description, p.privacy,
		       p.thumbnail_url, p.is_watch_later, p.created_at, p.updated_at,
		       COUNT(pi.id) as item_count
		FROM playlists p
		LEFT JOIN playlist_items pi ON p.id = pi.playlist_id
		WHERE 1=1`

	countQuery := `SELECT COUNT(DISTINCT p.id) FROM playlists p WHERE 1=1`

	args := []interface{}{}
	countArgs := []interface{}{}
	argCount := 0

	if opts.UserID != nil {
		argCount++
		query += fmt.Sprintf(" AND p.user_id = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.user_id = $%d", argCount)
		args = append(args, *opts.UserID)
		countArgs = append(countArgs, *opts.UserID)
	}

	if opts.Privacy != nil {
		argCount++
		query += fmt.Sprintf(" AND p.privacy = $%d", argCount)
		countQuery += fmt.Sprintf(" AND p.privacy = $%d", argCount)
		args = append(args, *opts.Privacy)
		countArgs = append(countArgs, *opts.Privacy)
	}

	// Group by for aggregation
	query += " GROUP BY p.id"

	// Add ordering
	switch opts.OrderBy {
	case "name":
		query += " ORDER BY p.name ASC"
	case "updated_at":
		query += " ORDER BY p.updated_at DESC"
	default:
		query += " ORDER BY p.created_at DESC"
	}

	// Add pagination
	argCount++
	query += fmt.Sprintf(" LIMIT $%d", argCount)
	args = append(args, opts.Limit)

	argCount++
	query += fmt.Sprintf(" OFFSET $%d", argCount)
	args = append(args, opts.Offset)

	// Execute queries
	err := r.db.SelectContext(ctx, &playlists, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list playlists: %w", err)
	}

	// Get total count
	var total int
	err = r.db.GetContext(ctx, &total, countQuery, countArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to get playlist count: %w", err)
	}

	return playlists, total, nil
}

// AddItem adds a video to a playlist
func (r *playlistRepository) AddItem(ctx context.Context, playlistID, videoID uuid.UUID, position *int) error {
	itemID := uuid.New()
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Idempotent behavior: if video is already in playlist, do nothing.
	var exists bool
	if err := tx.GetContext(
		ctx,
		&exists,
		`SELECT EXISTS(SELECT 1 FROM playlist_items WHERE playlist_id = $1 AND video_id = $2)`,
		playlistID,
		videoID,
	); err != nil {
		return fmt.Errorf("failed to check existing playlist item: %w", err)
	}
	if exists {
		return tx.Commit()
	}

	// If position is not specified, append to end
	insertPosition := 0
	if position == nil {
		var maxPos int
		if err := tx.GetContext(ctx, &maxPos,
			`SELECT COALESCE(MAX(position), -1) FROM playlist_items WHERE playlist_id = $1`,
			playlistID); err != nil {
			return fmt.Errorf("failed to get max position: %w", err)
		}
		insertPosition = maxPos + 1
	} else {
		insertPosition = *position
		if insertPosition < 0 {
			insertPosition = 0
		}
	}

	// Make room for explicit/target position to satisfy unique (playlist_id, position).
	if _, err := tx.ExecContext(
		ctx,
		`UPDATE playlist_items
		 SET position = position + 1
		 WHERE playlist_id = $1 AND position >= $2`,
		playlistID,
		insertPosition,
	); err != nil {
		return fmt.Errorf("failed to shift playlist positions: %w", err)
	}

	query := `
		INSERT INTO playlist_items (id, playlist_id, video_id, position, added_at)
		VALUES ($1, $2, $3, $4, $5)`
	_, err = tx.ExecContext(ctx, query, itemID, playlistID, videoID, insertPosition, time.Now())
	if err != nil {
		return fmt.Errorf("failed to add item to playlist: %w", err)
	}

	return tx.Commit()
}

// RemoveItem removes a video from a playlist
func (r *playlistRepository) RemoveItem(ctx context.Context, playlistID, itemID uuid.UUID) error {
	query := `DELETE FROM playlist_items WHERE playlist_id = $1 AND id = $2`

	result, err := r.db.ExecContext(ctx, query, playlistID, itemID)
	if err != nil {
		return fmt.Errorf("failed to remove item from playlist: %w", err)
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

// GetItems retrieves items from a playlist
func (r *playlistRepository) GetItems(ctx context.Context, playlistID uuid.UUID, limit, offset int) ([]*domain.PlaylistItem, error) {
	items := []*domain.PlaylistItem{}

	query := `
		SELECT pi.id, pi.playlist_id, pi.video_id, pi.position, pi.added_at,
		       v.id as "video.id", v.title as "video.title",
		       v.description as "video.description", v.duration as "video.duration",
		       v.views as "video.views", v.privacy as "video.privacy",
		       v.created_at as "video.created_at", v.updated_at as "video.updated_at"
		FROM playlist_items pi
		JOIN videos v ON pi.video_id = v.id
		WHERE pi.playlist_id = $1
		ORDER BY pi.position
		LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, playlistID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get playlist items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		item := &domain.PlaylistItem{}
		video := &domain.Video{}

		err := rows.Scan(
			&item.ID, &item.PlaylistID, &item.VideoID, &item.Position, &item.AddedAt,
			&video.ID, &video.Title, &video.Description, &video.Duration,
			&video.Views, &video.Privacy, &video.CreatedAt, &video.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan playlist item: %w", err)
		}

		item.Video = video
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating playlist items: %w", err)
	}

	return items, nil
}

// ReorderItem changes the position of an item in a playlist
func (r *playlistRepository) ReorderItem(ctx context.Context, playlistID, itemID uuid.UUID, newPosition int) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Get current position
	var currentPosition int
	err = tx.GetContext(ctx, &currentPosition,
		`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`,
		playlistID, itemID)
	if err != nil {
		if err == sql.ErrNoRows {
			return domain.ErrNotFound
		}
		return fmt.Errorf("failed to get current position: %w", err)
	}

	if currentPosition == newPosition {
		return nil // No change needed
	}

	// Move target item out of the constrained range first to avoid
	// transient (playlist_id, position) unique conflicts while shifting.
	const tempPosition = 2147483647 // max PostgreSQL INTEGER
	_, err = tx.ExecContext(ctx,
		`UPDATE playlist_items SET position = $1 WHERE id = $2`,
		tempPosition, itemID)
	if err != nil {
		return fmt.Errorf("failed to reserve temporary item position: %w", err)
	}

	// Shift other items
	if newPosition < currentPosition {
		// Moving up - shift items down
		_, err = tx.ExecContext(ctx,
			`UPDATE playlist_items
			 SET position = position + 1
			 WHERE playlist_id = $1
			 AND position >= $2
			 AND position < $3`,
			playlistID, newPosition, currentPosition)
	} else {
		// Moving down - shift items up
		_, err = tx.ExecContext(ctx,
			`UPDATE playlist_items
			 SET position = position - 1
			 WHERE playlist_id = $1
			 AND position > $2
			 AND position <= $3`,
			playlistID, currentPosition, newPosition)
	}

	if err != nil {
		return fmt.Errorf("failed to shift items: %w", err)
	}

	// Update the item's position
	_, err = tx.ExecContext(ctx,
		`UPDATE playlist_items SET position = $1 WHERE id = $2`,
		newPosition, itemID)
	if err != nil {
		return fmt.Errorf("failed to update item position: %w", err)
	}

	return tx.Commit()
}

// IsOwner checks if a user owns a playlist
func (r *playlistRepository) IsOwner(ctx context.Context, playlistID, userID uuid.UUID) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM playlists WHERE id = $1 AND user_id = $2)`

	err := r.db.GetContext(ctx, &exists, query, playlistID, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check playlist ownership: %w", err)
	}

	return exists, nil
}

// GetOrCreateWatchLater gets or creates a user's watch later playlist
func (r *playlistRepository) GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error) {
	// Try to get existing watch later playlist
	playlist := &domain.Playlist{}
	query := `
		SELECT id, user_id, name, description, privacy,
		       thumbnail_url, is_watch_later, created_at, updated_at
		FROM playlists
		WHERE user_id = $1 AND is_watch_later = true`

	err := r.db.GetContext(ctx, playlist, query, userID)
	if err == nil {
		return playlist, nil
	}

	if err != sql.ErrNoRows {
		return nil, fmt.Errorf("failed to get watch later playlist: %w", err)
	}

	// Create watch later playlist
	description := "Videos to watch later"
	playlist = &domain.Playlist{
		UserID:       userID,
		Name:         "Watch Later",
		Description:  &description,
		Privacy:      domain.PrivacyPrivate,
		IsWatchLater: true,
	}

	err = r.Create(ctx, playlist)
	if err != nil {
		return nil, fmt.Errorf("failed to create watch later playlist: %w", err)
	}

	return playlist, nil
}
