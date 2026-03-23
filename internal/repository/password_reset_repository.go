package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
)

// PasswordResetRepository defines the interface for password reset token storage.
type PasswordResetRepository interface {
	CreateToken(ctx context.Context, token *domain.PasswordResetToken) error
	GetByTokenHash(ctx context.Context, hash string) (*domain.PasswordResetToken, error)
	MarkUsed(ctx context.Context, tokenID string) error
	DeleteExpiredTokens(ctx context.Context) error
}

type passwordResetRepository struct {
	db *sqlx.DB
}

// NewPasswordResetRepository creates a new password reset token repository.
func NewPasswordResetRepository(db *sqlx.DB) PasswordResetRepository {
	return &passwordResetRepository{db: db}
}

func (r *passwordResetRepository) CreateToken(ctx context.Context, token *domain.PasswordResetToken) error {
	query := `
		INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query,
		token.ID,
		token.UserID,
		token.TokenHash,
		token.ExpiresAt,
		token.CreatedAt,
	)
	return err
}

func (r *passwordResetRepository) GetByTokenHash(ctx context.Context, hash string) (*domain.PasswordResetToken, error) {
	var token domain.PasswordResetToken
	query := `
		SELECT id, user_id, token_hash, expires_at, created_at, used_at
		FROM password_reset_tokens
		WHERE token_hash = $1 AND used_at IS NULL
	`
	err := r.db.GetContext(ctx, &token, query, hash)
	if err == sql.ErrNoRows {
		return nil, domain.ErrInvalidToken
	}
	return &token, err
}

func (r *passwordResetRepository) MarkUsed(ctx context.Context, tokenID string) error {
	query := `
		UPDATE password_reset_tokens
		SET used_at = $1
		WHERE id = $2 AND used_at IS NULL
	`
	result, err := r.db.ExecContext(ctx, query, time.Now(), tokenID)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows == 0 {
		return domain.ErrInvalidToken
	}
	return nil
}

func (r *passwordResetRepository) DeleteExpiredTokens(ctx context.Context) error {
	query := `DELETE FROM password_reset_tokens WHERE expires_at < $1 AND used_at IS NULL`
	_, err := r.db.ExecContext(ctx, query, time.Now())
	return err
}
