package port

import (
	"context"
	"time"
)

// TokenSession represents an active user token/refresh session.
type TokenSession struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// TokenSessionRepository defines storage operations for user token sessions.
type TokenSessionRepository interface {
	ListUserTokenSessions(ctx context.Context, userID string) ([]*TokenSession, error)
	RevokeTokenSession(ctx context.Context, tokenSessionID string) error
}
