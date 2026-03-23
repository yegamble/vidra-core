package port

import (
	"athena/internal/domain"
	"context"

	"github.com/google/uuid"
)

// VideoOwnershipRepository manages video ownership change requests.
type VideoOwnershipRepository interface {
	Create(ctx context.Context, change *domain.VideoOwnershipChange) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VideoOwnershipChange, error)
	ListPendingForUser(ctx context.Context, userID string) ([]*domain.VideoOwnershipChange, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.VideoOwnershipChangeStatus) error
}
