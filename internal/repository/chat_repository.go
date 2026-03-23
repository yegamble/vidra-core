package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
)

// ChatRepository defines the interface for chat data operations
type ChatRepository interface {
	// Messages
	CreateMessage(ctx context.Context, msg *domain.ChatMessage) error
	GetMessages(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ChatMessage, error)
	GetMessagesSince(ctx context.Context, streamID uuid.UUID, since time.Time) ([]*domain.ChatMessage, error)
	DeleteMessage(ctx context.Context, messageID uuid.UUID) error
	GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.ChatMessage, error)

	// Moderators
	AddModerator(ctx context.Context, mod *domain.ChatModerator) error
	RemoveModerator(ctx context.Context, streamID, userID uuid.UUID) error
	IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	GetModerators(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatModerator, error)

	// Bans
	BanUser(ctx context.Context, ban *domain.ChatBan) error
	UnbanUser(ctx context.Context, streamID, userID uuid.UUID) error
	IsUserBanned(ctx context.Context, streamID, userID uuid.UUID) (bool, error)
	GetBans(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatBan, error)
	GetBanByID(ctx context.Context, banID uuid.UUID) (*domain.ChatBan, error)
	CleanupExpiredBans(ctx context.Context) (int, error)

	// Statistics
	GetStreamStats(ctx context.Context, streamID uuid.UUID) (*domain.ChatStreamStats, error)
	GetMessageCount(ctx context.Context, streamID uuid.UUID) (int, error)
}

// chatRepository implements ChatRepository
type chatRepository struct {
	db *sqlx.DB
}

// NewChatRepository creates a new chat repository
func NewChatRepository(db *sqlx.DB) ChatRepository {
	return &chatRepository{db: db}
}

// ============================================================================
// Messages
// ============================================================================

// CreateMessage creates a new chat message
func (r *chatRepository) CreateMessage(ctx context.Context, msg *domain.ChatMessage) error {
	if err := msg.Validate(); err != nil {
		return fmt.Errorf("invalid chat message: %w", err)
	}

	// Marshal metadata to JSON
	metadataJSON, err := json.Marshal(msg.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO chat_messages (
			id, stream_id, user_id, username, message, type, metadata, deleted, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9
		)
	`

	_, err = r.db.ExecContext(
		ctx,
		query,
		msg.ID,
		msg.StreamID,
		msg.UserID,
		msg.Username,
		msg.Message,
		msg.Type,
		metadataJSON,
		msg.Deleted,
		msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to create chat message: %w", err)
	}

	return nil
}

// GetMessages retrieves chat messages for a stream with pagination
func (r *chatRepository) GetMessages(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ChatMessage, error) {
	query := `
		SELECT
			id, stream_id, user_id, username, message, type, metadata, deleted, created_at
		FROM chat_messages
		WHERE stream_id = $1
		AND deleted = FALSE
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`

	rows, err := r.db.QueryContext(ctx, query, streamID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get chat messages: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return r.scanMessages(rows)
}

// GetMessagesSince retrieves messages since a specific time
func (r *chatRepository) GetMessagesSince(ctx context.Context, streamID uuid.UUID, since time.Time) ([]*domain.ChatMessage, error) {
	query := `
		SELECT
			id, stream_id, user_id, username, message, type, metadata, deleted, created_at
		FROM chat_messages
		WHERE stream_id = $1
		AND created_at > $2
		AND deleted = FALSE
		ORDER BY created_at ASC
	`

	rows, err := r.db.QueryContext(ctx, query, streamID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages since: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return r.scanMessages(rows)
}

// GetMessageByID retrieves a message by ID
func (r *chatRepository) GetMessageByID(ctx context.Context, messageID uuid.UUID) (*domain.ChatMessage, error) {
	query := `
		SELECT
			id, stream_id, user_id, username, message, type, metadata, deleted, created_at
		FROM chat_messages
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, messageID)

	var msg domain.ChatMessage
	var metadataJSON []byte

	err := row.Scan(
		&msg.ID,
		&msg.StreamID,
		&msg.UserID,
		&msg.Username,
		&msg.Message,
		&msg.Type,
		&metadataJSON,
		&msg.Deleted,
		&msg.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	// Unmarshal metadata
	if err := json.Unmarshal(metadataJSON, &msg.Metadata); err != nil {
		return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
	}

	return &msg, nil
}

// DeleteMessage soft deletes a message
func (r *chatRepository) DeleteMessage(ctx context.Context, messageID uuid.UUID) error {
	query := `UPDATE chat_messages SET deleted = TRUE WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, messageID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// scanMessages scans rows into chat messages
func (r *chatRepository) scanMessages(rows *sql.Rows) ([]*domain.ChatMessage, error) {
	var messages []*domain.ChatMessage

	for rows.Next() {
		var msg domain.ChatMessage
		var metadataJSON []byte

		err := rows.Scan(
			&msg.ID,
			&msg.StreamID,
			&msg.UserID,
			&msg.Username,
			&msg.Message,
			&msg.Type,
			&metadataJSON,
			&msg.Deleted,
			&msg.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		// Unmarshal metadata
		if err := json.Unmarshal(metadataJSON, &msg.Metadata); err != nil {
			return nil, fmt.Errorf("failed to unmarshal metadata: %w", err)
		}

		messages = append(messages, &msg)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return messages, nil
}

// ============================================================================
// Moderators
// ============================================================================

// AddModerator adds a moderator to a stream
func (r *chatRepository) AddModerator(ctx context.Context, mod *domain.ChatModerator) error {
	if err := mod.Validate(); err != nil {
		return fmt.Errorf("invalid moderator: %w", err)
	}

	query := `
		INSERT INTO chat_moderators (id, stream_id, user_id, granted_by, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (stream_id, user_id) DO NOTHING
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		mod.ID,
		mod.StreamID,
		mod.UserID,
		mod.GrantedBy,
		mod.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to add moderator: %w", err)
	}

	return nil
}

// RemoveModerator removes a moderator from a stream
func (r *chatRepository) RemoveModerator(ctx context.Context, streamID, userID uuid.UUID) error {
	query := `DELETE FROM chat_moderators WHERE stream_id = $1 AND user_id = $2`

	result, err := r.db.ExecContext(ctx, query, streamID, userID)
	if err != nil {
		return fmt.Errorf("failed to remove moderator: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// IsModerator checks if a user is a moderator for a stream
func (r *chatRepository) IsModerator(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	query := `SELECT is_chat_moderator($1, $2)`

	var isMod bool
	err := r.db.QueryRowContext(ctx, query, streamID, userID).Scan(&isMod)
	if err != nil {
		return false, fmt.Errorf("failed to check moderator status: %w", err)
	}

	return isMod, nil
}

// GetModerators retrieves all moderators for a stream
func (r *chatRepository) GetModerators(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatModerator, error) {
	query := `
		SELECT id, stream_id, user_id, granted_by, created_at
		FROM chat_moderators
		WHERE stream_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, streamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get moderators: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	var moderators []*domain.ChatModerator
	for rows.Next() {
		var mod domain.ChatModerator
		err := rows.Scan(
			&mod.ID,
			&mod.StreamID,
			&mod.UserID,
			&mod.GrantedBy,
			&mod.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan moderator: %w", err)
		}
		moderators = append(moderators, &mod)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return moderators, nil
}

// ============================================================================
// Bans
// ============================================================================

// BanUser bans a user from a stream
func (r *chatRepository) BanUser(ctx context.Context, ban *domain.ChatBan) error {
	if err := ban.Validate(); err != nil {
		return fmt.Errorf("invalid ban: %w", err)
	}

	query := `
		INSERT INTO chat_bans (id, stream_id, user_id, banned_by, reason, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (stream_id, user_id)
		DO UPDATE SET
			banned_by = EXCLUDED.banned_by,
			reason = EXCLUDED.reason,
			expires_at = EXCLUDED.expires_at,
			created_at = EXCLUDED.created_at
	`

	_, err := r.db.ExecContext(
		ctx,
		query,
		ban.ID,
		ban.StreamID,
		ban.UserID,
		ban.BannedBy,
		ban.Reason,
		ban.ExpiresAt,
		ban.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to ban user: %w", err)
	}

	return nil
}

// UnbanUser removes a ban from a user
func (r *chatRepository) UnbanUser(ctx context.Context, streamID, userID uuid.UUID) error {
	query := `DELETE FROM chat_bans WHERE stream_id = $1 AND user_id = $2`

	result, err := r.db.ExecContext(ctx, query, streamID, userID)
	if err != nil {
		return fmt.Errorf("failed to unban user: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rows == 0 {
		return domain.ErrNotFound
	}

	return nil
}

// IsUserBanned checks if a user is banned from a stream
func (r *chatRepository) IsUserBanned(ctx context.Context, streamID, userID uuid.UUID) (bool, error) {
	query := `SELECT is_user_banned($1, $2)`

	var isBanned bool
	err := r.db.QueryRowContext(ctx, query, streamID, userID).Scan(&isBanned)
	if err != nil {
		return false, fmt.Errorf("failed to check ban status: %w", err)
	}

	return isBanned, nil
}

// GetBans retrieves all active bans for a stream
func (r *chatRepository) GetBans(ctx context.Context, streamID uuid.UUID) ([]*domain.ChatBan, error) {
	query := `
		SELECT id, stream_id, user_id, banned_by, reason, expires_at, created_at
		FROM chat_bans
		WHERE stream_id = $1
		AND (expires_at IS NULL OR expires_at > NOW())
		ORDER BY created_at DESC
	`

	rows, err := r.db.QueryContext(ctx, query, streamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get bans: %w", err)
	}
	defer func() {
		_ = rows.Close()
	}()

	return r.scanBans(rows)
}

// GetBanByID retrieves a ban by ID
func (r *chatRepository) GetBanByID(ctx context.Context, banID uuid.UUID) (*domain.ChatBan, error) {
	query := `
		SELECT id, stream_id, user_id, banned_by, reason, expires_at, created_at
		FROM chat_bans
		WHERE id = $1
	`

	row := r.db.QueryRowContext(ctx, query, banID)

	var ban domain.ChatBan
	err := row.Scan(
		&ban.ID,
		&ban.StreamID,
		&ban.UserID,
		&ban.BannedBy,
		&ban.Reason,
		&ban.ExpiresAt,
		&ban.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get ban: %w", err)
	}

	return &ban, nil
}

// CleanupExpiredBans removes all expired bans
func (r *chatRepository) CleanupExpiredBans(ctx context.Context) (int, error) {
	query := `SELECT cleanup_expired_bans()`

	var count int
	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired bans: %w", err)
	}

	return count, nil
}

// scanBans scans rows into chat bans
func (r *chatRepository) scanBans(rows *sql.Rows) ([]*domain.ChatBan, error) {
	var bans []*domain.ChatBan

	for rows.Next() {
		var ban domain.ChatBan
		err := rows.Scan(
			&ban.ID,
			&ban.StreamID,
			&ban.UserID,
			&ban.BannedBy,
			&ban.Reason,
			&ban.ExpiresAt,
			&ban.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan ban: %w", err)
		}
		bans = append(bans, &ban)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("rows iteration error: %w", err)
	}

	return bans, nil
}

// ============================================================================
// Statistics
// ============================================================================

// GetStreamStats retrieves aggregate statistics for a stream's chat
func (r *chatRepository) GetStreamStats(ctx context.Context, streamID uuid.UUID) (*domain.ChatStreamStats, error) {
	query := `
		SELECT
			stream_id,
			unique_chatters,
			message_count,
			moderation_actions,
			last_message_at,
			moderator_count,
			active_ban_count
		FROM chat_stream_stats
		WHERE stream_id = $1
	`

	row := r.db.QueryRowContext(ctx, query, streamID)

	var stats domain.ChatStreamStats
	var lastMessageAt sql.NullTime

	err := row.Scan(
		&stats.StreamID,
		&stats.UniqueChatters,
		&stats.MessageCount,
		&stats.ModerationActions,
		&lastMessageAt,
		&stats.ModeratorCount,
		&stats.ActiveBanCount,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Return empty stats if stream has no messages yet
			return &domain.ChatStreamStats{
				StreamID: streamID,
			}, nil
		}
		return nil, fmt.Errorf("failed to get stream stats: %w", err)
	}

	if lastMessageAt.Valid {
		stats.LastMessageAt = lastMessageAt.Time
	}

	return &stats, nil
}

// GetMessageCount retrieves the message count for a stream
func (r *chatRepository) GetMessageCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	query := `SELECT get_chat_message_count($1)`

	var count int
	err := r.db.QueryRowContext(ctx, query, streamID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get message count: %w", err)
	}

	return count, nil
}
