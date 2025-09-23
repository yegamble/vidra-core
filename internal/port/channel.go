package port

import (
	"context"

	"athena/internal/domain"

	"github.com/google/uuid"
)

// ChannelRepository defines the interface for channel data operations
type ChannelRepository interface {
	Create(ctx context.Context, channel *domain.Channel) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error)
	GetByHandle(ctx context.Context, handle string) (*domain.Channel, error)
	List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error)
	Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error)
	Delete(ctx context.Context, id uuid.UUID) error
	GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error)
	GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error)
	CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error)
}
