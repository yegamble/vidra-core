package usecase

import (
	"context"
	"time"
)

type RefreshToken struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Token     string    `db:"token"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
	RevokedAt *time.Time `db:"revoked_at"`
}

type AuthRepository interface {
	// Refresh token management
	CreateRefreshToken(ctx context.Context, token *RefreshToken) error
	GetRefreshToken(ctx context.Context, token string) (*RefreshToken, error)
	RevokeRefreshToken(ctx context.Context, token string) error
	RevokeAllUserTokens(ctx context.Context, userID string) error
	CleanExpiredTokens(ctx context.Context) error
	
	// Session management (optional, if using Redis sessions)
	CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error
	GetSession(ctx context.Context, sessionID string) (string, error) // returns userID
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteAllUserSessions(ctx context.Context, userID string) error
}