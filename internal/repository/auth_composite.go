package repository

import (
	"context"
	"time"

	"athena/internal/usecase"
)

type sessionRepository interface {
	CreateSession(ctx context.Context, sessionID, userID string, expiresAt time.Time) error
	GetSession(ctx context.Context, sessionID string) (string, error)
	DeleteSession(ctx context.Context, sessionID string) error
	DeleteAllUserSessions(ctx context.Context, userID string) error
}

type compositeAuthRepository struct {
	dbRepo    usecase.AuthRepository
	redisRepo sessionRepository
}

func NewCompositeAuthRepository(dbRepo usecase.AuthRepository, redisRepo sessionRepository) usecase.AuthRepository {
	return &compositeAuthRepository{dbRepo: dbRepo, redisRepo: redisRepo}
}

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
