package port

import (
	"context"
	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

// SubscriptionRepository defines operations for managing channel subscriptions
type SubscriptionRepository interface {
	// Channel-based subscription methods (NEW)
	SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error
	UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error
	IsSubscribed(ctx context.Context, subscriberID, channelID uuid.UUID) (bool, error)
	ListUserSubscriptions(ctx context.Context, subscriberID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error)

	// Legacy user-based methods (DEPRECATED - for backward compatibility)
	Subscribe(ctx context.Context, subscriberID, channelID string) error
	Unsubscribe(ctx context.Context, subscriberID, channelID string) error
	ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error)
	ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error)
	CountSubscribers(ctx context.Context, channelID string) (int64, error)
	GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error)
}
