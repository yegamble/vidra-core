package repository

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ChannelRepository handles database operations for channels
type ChannelRepository struct {
	db *sqlx.DB
}

// NewChannelRepository creates a new channel repository
func NewChannelRepository(db *sqlx.DB) *ChannelRepository {
	return &ChannelRepository{db: db}
}

// Create creates a new channel
func (r *ChannelRepository) Create(ctx context.Context, channel *domain.Channel) error {
	query := `
		INSERT INTO channels (
			id, account_id, handle, display_name, description, support,
			is_local, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
		) RETURNING created_at, updated_at`

	channel.ID = uuid.New()
	channel.IsLocal = true // Default to local channel

	err := r.db.QueryRowContext(
		ctx, query,
		channel.ID, channel.AccountID, channel.Handle, channel.DisplayName,
		channel.Description, channel.Support, channel.IsLocal,
	).Scan(&channel.CreatedAt, &channel.UpdatedAt)

	if err != nil {
		if strings.Contains(err.Error(), "unique constraint") && strings.Contains(err.Error(), "handle") {
			return domain.ErrDuplicateEntry
		}
		return fmt.Errorf("failed to create channel: %w", err)
	}

	return nil
}

// GetByID retrieves a channel by ID
func (r *ChannelRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	query := `
        SELECT
            c.id, c.account_id, c.handle, c.display_name, c.description, c.support,
            c.is_local, c.atproto_did, c.atproto_pds_url,
            c.avatar_filename, c.avatar_ipfs_cid, c.banner_filename, c.banner_ipfs_cid,
            c.followers_count, c.following_count, c.videos_count,
            c.created_at, c.updated_at
        FROM channels c
        WHERE c.id = $1`

	var channel domain.Channel
	err := r.db.GetContext(ctx, &channel, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get channel: %w", err)
	}

	// Load account information
	if err := r.loadChannelAccount(ctx, &channel); err != nil {
		return nil, err
	}

	return &channel, nil
}

// GetByHandle retrieves a channel by handle
func (r *ChannelRepository) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	query := `
        SELECT
            c.id, c.account_id, c.handle, c.display_name, c.description, c.support,
            c.is_local, c.atproto_did, c.atproto_pds_url,
            c.avatar_filename, c.avatar_ipfs_cid, c.banner_filename, c.banner_ipfs_cid,
            c.followers_count, c.following_count, c.videos_count,
            c.created_at, c.updated_at
        FROM channels c
        WHERE c.handle = $1`

	var channel domain.Channel
	err := r.db.GetContext(ctx, &channel, query, handle)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get channel by handle: %w", err)
	}

	// Load account information
	if err := r.loadChannelAccount(ctx, &channel); err != nil {
		return nil, err
	}

	return &channel, nil
}

// List retrieves a paginated list of channels
func (r *ChannelRepository) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	// Build WHERE clause
	whereClauses := []string{"1=1"}
	args := []interface{}{}
	argCount := 0

	if params.AccountID != nil {
		argCount++
		whereClauses = append(whereClauses, fmt.Sprintf("c.account_id = $%d", argCount))
		args = append(args, *params.AccountID)
	}

	if params.IsLocal != nil {
		argCount++
		whereClauses = append(whereClauses, fmt.Sprintf("c.is_local = $%d", argCount))
		args = append(args, *params.IsLocal)
	}

	if params.Search != "" {
		argCount++
		whereClauses = append(whereClauses, fmt.Sprintf("(c.handle ILIKE $%d OR c.display_name ILIKE $%d OR c.description ILIKE $%d)", argCount, argCount, argCount))
		searchTerm := "%" + params.Search + "%"
		args = append(args, searchTerm)
	}

	whereClause := strings.Join(whereClauses, " AND ")

	// Build ORDER BY clause
	orderBy := "c.created_at DESC" // default
	switch params.Sort {
	case "name":
		orderBy = "c.display_name ASC"
	case "-name":
		orderBy = "c.display_name DESC"
	case "createdAt":
		orderBy = "c.created_at ASC"
	case "-createdAt":
		orderBy = "c.created_at DESC"
	case "videosCount":
		orderBy = "c.videos_count ASC"
	case "-videosCount":
		orderBy = "c.videos_count DESC"
	}

	// Set pagination defaults
	if params.Page <= 0 {
		params.Page = 1
	}
	if params.PageSize <= 0 {
		params.PageSize = 20
	}
	if params.PageSize > 100 {
		params.PageSize = 100
	}

	offset := (params.Page - 1) * params.PageSize

	// Get total count
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM channels c WHERE %s`, whereClause)
	var total int
	err := r.db.GetContext(ctx, &total, countQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to count channels: %w", err)
	}

	// Get channels
	argCount++
	args = append(args, params.PageSize)
	argCount++
	args = append(args, offset)

	query := fmt.Sprintf(`
        SELECT
            c.id, c.account_id, c.handle, c.display_name, c.description, c.support,
            c.is_local, c.atproto_did, c.atproto_pds_url,
            c.avatar_filename, c.avatar_ipfs_cid, c.banner_filename, c.banner_ipfs_cid,
            c.followers_count, c.following_count, c.videos_count,
            c.created_at, c.updated_at
        FROM channels c
        WHERE %s
        ORDER BY %s
        LIMIT $%d OFFSET $%d`, whereClause, orderBy, argCount-1, argCount)

	var channels []domain.Channel
	err = r.db.SelectContext(ctx, &channels, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list channels: %w", err)
	}

	// Load account information for each channel
	for i := range channels {
		if err := r.loadChannelAccount(ctx, &channels[i]); err != nil {
			// Log error but don't fail the entire request
			continue
		}
	}

	return &domain.ChannelListResponse{
		Total:    total,
		Page:     params.Page,
		PageSize: params.PageSize,
		Data:     channels,
	}, nil
}

// Update updates a channel
func (r *ChannelRepository) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	// Build dynamic UPDATE query
	setClauses := []string{"updated_at = CURRENT_TIMESTAMP"}
	args := []interface{}{}
	argCount := 0

	if updates.DisplayName != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("display_name = $%d", argCount))
		args = append(args, *updates.DisplayName)
	}

	if updates.Description != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *updates.Description)
	}

	if updates.Support != nil {
		argCount++
		setClauses = append(setClauses, fmt.Sprintf("support = $%d", argCount))
		args = append(args, *updates.Support)
	}

	if len(setClauses) == 1 { // Only updated_at
		return nil, domain.ErrInvalidInput
	}

	argCount++
	args = append(args, id)

	query := fmt.Sprintf(`
		UPDATE channels
		SET %s
		WHERE id = $%d
		RETURNING id, account_id, handle, display_name, description, support,
			is_local, atproto_did, atproto_pds_url,
			avatar_filename, avatar_ipfs_cid, banner_filename, banner_ipfs_cid,
			followers_count, following_count, videos_count,
			created_at, updated_at`,
		strings.Join(setClauses, ", "), argCount)

	var channel domain.Channel
	err := r.db.GetContext(ctx, &channel, query, args...)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update channel: %w", err)
	}

	// Load account information
	if err := r.loadChannelAccount(ctx, &channel); err != nil {
		return nil, err
	}

	return &channel, nil
}

// Delete deletes a channel
func (r *ChannelRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM channels WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete channel: %w", err)
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

// GetChannelsByAccountID retrieves all channels for a given account
func (r *ChannelRepository) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	query := `
        SELECT
            c.id, c.account_id, c.handle, c.display_name, c.description, c.support,
            c.is_local, c.atproto_did, c.atproto_pds_url,
            c.avatar_filename, c.avatar_ipfs_cid, c.banner_filename, c.banner_ipfs_cid,
            c.followers_count, c.following_count, c.videos_count,
            c.created_at, c.updated_at
        FROM channels c
        WHERE c.account_id = $1
        ORDER BY c.created_at ASC`

	var channels []domain.Channel
	err := r.db.SelectContext(ctx, &channels, query, accountID)
	if err != nil {
		return nil, fmt.Errorf("failed to get channels by account: %w", err)
	}

	return channels, nil
}

// GetDefaultChannelForAccount retrieves the default (first) channel for an account
func (r *ChannelRepository) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	query := `
        SELECT
            c.id, c.account_id, c.handle, c.display_name, c.description, c.support,
            c.is_local, c.atproto_did, c.atproto_pds_url,
            c.avatar_filename, c.avatar_ipfs_cid, c.banner_filename, c.banner_ipfs_cid,
            c.followers_count, c.following_count, c.videos_count,
            c.created_at, c.updated_at
        FROM channels c
        WHERE c.account_id = $1
        ORDER BY c.created_at ASC
        LIMIT 1`

	var channel domain.Channel
	err := r.db.GetContext(ctx, &channel, query, accountID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get default channel: %w", err)
	}

	// Load account information
	if err := r.loadChannelAccount(ctx, &channel); err != nil {
		return nil, err
	}

	return &channel, nil
}

// loadChannelAccount loads the account information for a channel
func (r *ChannelRepository) loadChannelAccount(ctx context.Context, channel *domain.Channel) error {
	query := `
		SELECT id, username, email, display_name, bio, created_at, updated_at
		FROM users
		WHERE id = $1`

	var user domain.User
	err := r.db.GetContext(ctx, &user, query, channel.AccountID)
	if err != nil {
		if err == sql.ErrNoRows {
			// Account might have been deleted
			return nil
		}
		return fmt.Errorf("failed to load channel account: %w", err)
	}

	channel.Account = &user
	return nil
}

// CheckOwnership checks if a user owns a channel
func (r *ChannelRepository) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM channels WHERE id = $1 AND account_id = $2)`

	var exists bool
	err := r.db.GetContext(ctx, &exists, query, channelID, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check channel ownership: %w", err)
	}

	return exists, nil
}
