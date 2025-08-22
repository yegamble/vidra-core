package usecase

import (
	"athena/internal/domain"
	"context"
	"database/sql"
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User, passwordHash string) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id string) error
	GetPasswordHash(ctx context.Context, userID string) (string, error)
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	List(ctx context.Context, limit, offset int) ([]*domain.User, error)
	Count(ctx context.Context) (int64, error)
	// Upsert avatar identifiers for a user
	SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error
	// PGP key management
	SetPGPPublicKey(ctx context.Context, userID string, pgpPublicKey string) error
	SetPGPPublicKeyWithFingerprint(ctx context.Context, userID string, pgpPublicKey string, fingerprint string) error
	RemovePGPPublicKey(ctx context.Context, userID string) error
	GetPGPPublicKey(ctx context.Context, userID string) (*string, error)
	GetPGPFingerprint(ctx context.Context, userID string) (*string, error)
}
