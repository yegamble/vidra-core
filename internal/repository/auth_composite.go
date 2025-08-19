package repository

import (
	"context"
	"time"

	"athena/internal/usecase"
)

// compositeAuthRepository delegates refresh tokens to a DB-backed repo
// and sessions to a Redis-backed repo.
type compositeAuthRepository struct {
	dbRepo    usecase.AuthRepository
	redisRepo *redisSessionRepository
}

func NewCompositeAuthRepository(dbRepo usecase.AuthRepository, redisRepo *redisSessionRepository) usecase.AuthRepository {
	return &compositeAuthRepository{dbRepo: dbRepo, redisRepo: redisRepo}
}

// Refresh token management delegates to dbRepo
func (c *compositeAuthRepository) CreateRefreshToken(ctx context.Context, token *usecase.RefreshToken) error {
	return c.dbRepo.CreateRefreshToken(ctx, token)
}

func (c *compositeAuthRepository) GetRefreshToken(ctx context.Context, token string) (*usecase.RefreshToken, error) {
	return c.dbRepo.GetRefreshToken(ctx, token)
}

func (c *compositeAuthRepository) RevokeRefreshToken(ctx context.Context, token string) error {
	return c.dbRepo.RevokeRefreshToken(ctx, token)
}

func (c *compositeAuthRepository) RevokeAllUserTokens(ctx context.Context, userID string) error {
	return c.dbRepo.RevokeAllUserTokens(ctx, userID)
}

func (c *compositeAuthRepository) CleanExpiredTokens(ctx context.Context) error {
	return c.dbRepo.CleanExpiredTokens(ctx)
}

// Session management delegates to redisRepo
func (c *compositeAuthRepository) CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error {
	return c.redisRepo.CreateSession(ctx, sessionID, userID, expiresAt)
}

func (c *compositeAuthRepository) GetSession(ctx context.Context, sessionID string) (string, error) {
	return c.redisRepo.GetSession(ctx, sessionID)
}

func (c *compositeAuthRepository) DeleteSession(ctx context.Context, sessionID string) error {
	return c.redisRepo.DeleteSession(ctx, sessionID)
}

func (c *compositeAuthRepository) DeleteAllUserSessions(ctx context.Context, userID string) error {
	return c.redisRepo.DeleteAllUserSessions(ctx, userID)
}
