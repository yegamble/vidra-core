package usecase

import (
	"context"

	"athena/internal/domain"
)

// TwoFABackupCodeRepository defines methods for managing 2FA backup codes
type TwoFABackupCodeRepository interface {
	// Create creates a new backup code
	Create(ctx context.Context, code *domain.TwoFABackupCode) error

	// GetUnusedForUser retrieves all unused backup codes for a user
	GetUnusedForUser(ctx context.Context, userID string) ([]*domain.TwoFABackupCode, error)

	// MarkAsUsed marks a backup code as used
	MarkAsUsed(ctx context.Context, codeID string) error

	// DeleteAllForUser deletes all backup codes for a user
	DeleteAllForUser(ctx context.Context, userID string) error
}
