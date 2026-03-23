package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"

	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"
)

// EmailVerificationRepository implements the email verification repository interface
type EmailVerificationRepository struct {
	db *sqlx.DB
}

// NewEmailVerificationRepository creates a new email verification repository
func NewEmailVerificationRepository(db *sqlx.DB) usecase.EmailVerificationRepository {
	return &EmailVerificationRepository{db: db}
}

// CreateVerificationToken creates a new email verification token
func (r *EmailVerificationRepository) CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken) error {
	query := `
		INSERT INTO email_verification_tokens (id, user_id, token, code, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.ExecContext(ctx, query,
		token.ID,
		token.UserID,
		token.Token,
		token.Code,
		token.ExpiresAt,
		token.CreatedAt,
	)
	return err
}

// GetVerificationToken retrieves a verification token by its value
func (r *EmailVerificationRepository) GetVerificationToken(ctx context.Context, token string) (*domain.EmailVerificationToken, error) {
	var verificationToken domain.EmailVerificationToken
	query := `
		SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE token = $1 AND used_at IS NULL
	`
	err := r.db.GetContext(ctx, &verificationToken, query, token)
	if err == sql.ErrNoRows {
		return nil, domain.ErrInvalidVerificationToken
	}
	return &verificationToken, err
}

// GetVerificationTokenByCode retrieves a verification token by code and user ID
func (r *EmailVerificationRepository) GetVerificationTokenByCode(ctx context.Context, code string, userID string) (*domain.EmailVerificationToken, error) {
	var verificationToken domain.EmailVerificationToken
	query := `
		SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE code = $1 AND user_id = $2 AND used_at IS NULL
		ORDER BY created_at DESC
		LIMIT 1
	`
	err := r.db.GetContext(ctx, &verificationToken, query, code, userID)
	if err == sql.ErrNoRows {
		return nil, domain.ErrInvalidVerificationCode
	}
	return &verificationToken, err
}

// MarkTokenAsUsed marks a verification token as used
func (r *EmailVerificationRepository) MarkTokenAsUsed(ctx context.Context, tokenID string) error {
	query := `
		UPDATE email_verification_tokens
		SET used_at = $1
		WHERE id = $2 AND used_at IS NULL
	`
	result, err := r.db.ExecContext(ctx, query, time.Now(), tokenID)
	if err != nil {
		return err
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rowsAffected == 0 {
		return domain.ErrInvalidVerificationToken
	}

	return nil
}

// DeleteExpiredTokens deletes all expired verification tokens
func (r *EmailVerificationRepository) DeleteExpiredTokens(ctx context.Context) error {
	query := `
		DELETE FROM email_verification_tokens
		WHERE expires_at < $1 AND used_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now())
	return err
}

// GetLatestTokenForUser gets the latest unused token for a user
func (r *EmailVerificationRepository) GetLatestTokenForUser(ctx context.Context, userID string) (*domain.EmailVerificationToken, error) {
	var verificationToken domain.EmailVerificationToken
	query := `
		SELECT id, user_id, token, code, expires_at, created_at, used_at
		FROM email_verification_tokens
		WHERE user_id = $1 AND used_at IS NULL AND expires_at > $2
		ORDER BY created_at DESC
		LIMIT 1
	`
	err := r.db.GetContext(ctx, &verificationToken, query, userID, time.Now())
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &verificationToken, err
}

// RevokeAllUserTokens invalidates all unused tokens for a user
func (r *EmailVerificationRepository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	query := `
		UPDATE email_verification_tokens
		SET used_at = $1
		WHERE user_id = $2 AND used_at IS NULL
	`
	_, err := r.db.ExecContext(ctx, query, time.Now(), userID)
	return err
}
