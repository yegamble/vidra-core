package auth

import (
	"context"

	"vidra-core/internal/domain"
)

type TwoFAServiceInterface interface {
	GenerateSecret(ctx context.Context, userID string) (*domain.TwoFASetupResponse, error)
	VerifySetup(ctx context.Context, userID, code string) error
	Disable(ctx context.Context, userID, password, code string) error
	RegenerateBackupCodes(ctx context.Context, userID, code string) ([]string, error)
	GetStatus(ctx context.Context, userID string) (*domain.TwoFAStatusResponse, error)
}
