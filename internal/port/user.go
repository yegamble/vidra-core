package port

import (
	"context"
	"database/sql"
	"vidra-core/internal/domain"
)

type UserRepository interface {
	Create(ctx context.Context, user *domain.User, passwordHash string) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	GetByUsername(ctx context.Context, username string) (*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id string) error
	GetPasswordHash(ctx context.Context, userID string) (string, error)
	// Anonymize soft-deletes a user: sets is_active=false and anonymizes PII fields.
	// The user's videos are retained with "Deleted User" attribution.
	Anonymize(ctx context.Context, userID string) error
	UpdatePassword(ctx context.Context, userID, passwordHash string) error
	List(ctx context.Context, limit, offset int) ([]*domain.User, error)
	Count(ctx context.Context) (int64, error)
	// Upsert avatar identifiers for a user
	SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error
	// MarkEmailAsVerified marks a user's email as verified
	MarkEmailAsVerified(ctx context.Context, userID string) error
}
