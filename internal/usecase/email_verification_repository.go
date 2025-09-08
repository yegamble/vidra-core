package usecase

import (
	"athena/internal/domain"
	"context"
)

// EmailVerificationRepository defines the interface for email verification operations
type EmailVerificationRepository interface {
	// CreateVerificationToken creates a new email verification token
	CreateVerificationToken(ctx context.Context, token *domain.EmailVerificationToken) error

	// GetVerificationToken retrieves a verification token by its value
	GetVerificationToken(ctx context.Context, token string) (*domain.EmailVerificationToken, error)

	// GetVerificationTokenByCode retrieves a verification token by code and user ID
	GetVerificationTokenByCode(ctx context.Context, code string, userID string) (*domain.EmailVerificationToken, error)

	// MarkTokenAsUsed marks a verification token as used
	MarkTokenAsUsed(ctx context.Context, tokenID string) error

	// DeleteExpiredTokens deletes all expired verification tokens
	DeleteExpiredTokens(ctx context.Context) error

	// GetLatestTokenForUser gets the latest unused token for a user
	GetLatestTokenForUser(ctx context.Context, userID string) (*domain.EmailVerificationToken, error)

	// RevokeAllUserTokens invalidates all unused tokens for a user
	RevokeAllUserTokens(ctx context.Context, userID string) error
}
