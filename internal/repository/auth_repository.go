package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"athena/internal/usecase"

	"github.com/jmoiron/sqlx"
)

type authRepository struct {
	db *sqlx.DB
}

func NewAuthRepository(db *sqlx.DB) usecase.AuthRepository {
	return &authRepository{db: db}
}

func (r *authRepository) CreateRefreshToken(ctx context.Context, token *usecase.RefreshToken) error {
	query := `
		INSERT INTO refresh_tokens (id, user_id, token, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)`

	_, err := r.db.ExecContext(ctx, query,
		token.ID, token.UserID, token.Token, token.ExpiresAt, token.CreatedAt)

	if err != nil {
		return fmt.Errorf("failed to create refresh token: %w", err)
	}

	return nil
}

func (r *authRepository) GetRefreshToken(ctx context.Context, token string) (*usecase.RefreshToken, error) {
	query := `
        SELECT id, user_id, token, expires_at, created_at, revoked_at
        FROM refresh_tokens
        WHERE token = $1 AND revoked_at IS NULL AND expires_at >= NOW()`

	var refreshToken usecase.RefreshToken
	err := r.db.GetContext(ctx, &refreshToken, query, token)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("refresh token not found or expired")
		}
		return nil, fmt.Errorf("failed to get refresh token: %w", err)
	}

	return &refreshToken, nil
}

func (r *authRepository) RevokeRefreshToken(ctx context.Context, token string) error {
	query := `UPDATE refresh_tokens SET revoked_at = NOW() WHERE token = $1`

	result, err := r.db.ExecContext(ctx, query, token)
	if err != nil {
		return fmt.Errorf("failed to revoke refresh token: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("refresh token not found")
	}

	return nil
}

func (r *authRepository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	query := `UPDATE refresh_tokens SET revoked_at = NOW() WHERE user_id = $1 AND revoked_at IS NULL`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to revoke all user tokens: %w", err)
	}

	return nil
}

func (r *authRepository) CleanExpiredTokens(ctx context.Context) error {
	query := `DELETE FROM refresh_tokens WHERE expires_at < NOW() OR revoked_at < NOW() - INTERVAL '30 days'`

	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to clean expired tokens: %w", err)
	}

	return nil
}

func (r *authRepository) CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error {
	query := `
		INSERT INTO sessions (id, user_id, expires_at, created_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (id) DO UPDATE SET
			user_id = EXCLUDED.user_id,
			expires_at = EXCLUDED.expires_at,
			created_at = NOW()`

	_, err := r.db.ExecContext(ctx, query, sessionID, userID, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to create session: %w", err)
	}

	return nil
}

func (r *authRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	query := `SELECT user_id FROM sessions WHERE id = $1 AND expires_at > NOW()`

	var userID string
	err := r.db.GetContext(ctx, &userID, query, sessionID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("session not found or expired")
		}
		return "", fmt.Errorf("failed to get session: %w", err)
	}

	return userID, nil
}

func (r *authRepository) DeleteSession(ctx context.Context, sessionID string) error {
	query := `DELETE FROM sessions WHERE id = $1`

	_, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to delete session: %w", err)
	}

	return nil
}

func (r *authRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	query := `DELETE FROM sessions WHERE user_id = $1`

	_, err := r.db.ExecContext(ctx, query, userID)
	if err != nil {
		return fmt.Errorf("failed to delete all user sessions: %w", err)
	}

	return nil
}
