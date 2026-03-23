package repository

import (
	"context"
	"fmt"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// UserBlockRepository handles persistence for per-user account and server blocks.
type UserBlockRepository struct {
	db *sqlx.DB
}

// NewUserBlockRepository creates a new UserBlockRepository.
func NewUserBlockRepository(db *sqlx.DB) *UserBlockRepository {
	return &UserBlockRepository{db: db}
}

// BlockAccount creates an account block for the given user.
func (r *UserBlockRepository) BlockAccount(ctx context.Context, userID, targetAccountID uuid.UUID) (*domain.UserBlock, error) {
	block := &domain.UserBlock{
		ID:              uuid.New(),
		UserID:          userID,
		BlockType:       domain.BlockTypeAccount,
		TargetAccountID: &targetAccountID,
	}
	query := `
		INSERT INTO user_blocks (id, user_id, block_type, target_account_id)
		VALUES (:id, :user_id, :block_type, :target_account_id)
		ON CONFLICT (user_id, target_account_id) DO NOTHING
		RETURNING id, user_id, block_type, target_account_id, target_server_host, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, block)
	if err != nil {
		return nil, fmt.Errorf("blocking account: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.StructScan(block); err != nil {
			return nil, fmt.Errorf("scanning block: %w", err)
		}
	}
	return block, nil
}

// UnblockAccount removes an account block by target username lookup.
func (r *UserBlockRepository) UnblockAccount(ctx context.Context, userID uuid.UUID, targetAccountName string) error {
	query := `
		DELETE FROM user_blocks
		WHERE user_id = $1
		  AND block_type = 'account'
		  AND target_account_id = (SELECT id FROM users WHERE username = $2 LIMIT 1)`
	result, err := r.db.ExecContext(ctx, query, userID, targetAccountName)
	if err != nil {
		return fmt.Errorf("unblocking account: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListAccountBlocks returns all account blocks for a user.
func (r *UserBlockRepository) ListAccountBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_blocks WHERE user_id = $1 AND block_type = 'account'`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting account blocks: %w", err)
	}

	var blocks []*domain.UserBlock
	query := `
		SELECT id, user_id, block_type, target_account_id, target_server_host, created_at
		FROM user_blocks
		WHERE user_id = $1 AND block_type = 'account'
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	if err := r.db.SelectContext(ctx, &blocks, query, userID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("listing account blocks: %w", err)
	}
	return blocks, total, nil
}

// BlockServer creates a server/domain block for the given user.
func (r *UserBlockRepository) BlockServer(ctx context.Context, userID uuid.UUID, host string) (*domain.UserBlock, error) {
	block := &domain.UserBlock{
		ID:               uuid.New(),
		UserID:           userID,
		BlockType:        domain.BlockTypeServer,
		TargetServerHost: &host,
	}
	query := `
		INSERT INTO user_blocks (id, user_id, block_type, target_server_host)
		VALUES (:id, :user_id, :block_type, :target_server_host)
		ON CONFLICT (user_id, target_server_host) DO NOTHING
		RETURNING id, user_id, block_type, target_account_id, target_server_host, created_at`
	rows, err := r.db.NamedQueryContext(ctx, query, block)
	if err != nil {
		return nil, fmt.Errorf("blocking server: %w", err)
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.StructScan(block); err != nil {
			return nil, fmt.Errorf("scanning block: %w", err)
		}
	}
	return block, nil
}

// UnblockServer removes a server block by host.
func (r *UserBlockRepository) UnblockServer(ctx context.Context, userID uuid.UUID, host string) error {
	query := `DELETE FROM user_blocks WHERE user_id = $1 AND block_type = 'server' AND target_server_host = $2`
	result, err := r.db.ExecContext(ctx, query, userID, host)
	if err != nil {
		return fmt.Errorf("unblocking server: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}

// ListServerBlocks returns all server blocks for a user.
func (r *UserBlockRepository) ListServerBlocks(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.UserBlock, int64, error) {
	var total int64
	if err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM user_blocks WHERE user_id = $1 AND block_type = 'server'`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("counting server blocks: %w", err)
	}

	var blocks []*domain.UserBlock
	query := `
		SELECT id, user_id, block_type, target_account_id, target_server_host, created_at
		FROM user_blocks
		WHERE user_id = $1 AND block_type = 'server'
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`
	if err := r.db.SelectContext(ctx, &blocks, query, userID, limit, offset); err != nil {
		return nil, 0, fmt.Errorf("listing server blocks: %w", err)
	}
	return blocks, total, nil
}
