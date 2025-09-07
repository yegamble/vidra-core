package usecase

import (
	"athena/internal/domain"
	"context"
)

// SubscriptionRepository defines operations for managing user subscriptions
type SubscriptionRepository interface {
	// Subscribe creates a subscription from subscriberID to channelID. Idempotent.
	Subscribe(ctx context.Context, subscriberID, channelID string) error
	// Unsubscribe removes a subscription. Idempotent.
	Unsubscribe(ctx context.Context, subscriberID, channelID string) error
	// ListSubscriptions returns the list of channels a user is subscribed to.
	ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error)
	// ListSubscriptionVideos returns public, completed videos from channels the user subscribes to.
	ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error)
	// CountSubscribers returns the subscriber count for a channel.
	CountSubscribers(ctx context.Context, channelID string) (int64, error)
	// GetSubscribers returns all subscribers for a channel.
	GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error)
}
