package port

import (
	"vidra-core/internal/domain"
	"context"
	"time"
)

type ActivityPubRepository interface {
	// Actor keys
	GetActorKeys(ctx context.Context, actorID string) (publicKey, privateKey string, err error)
	StoreActorKeys(ctx context.Context, actorID, publicKey, privateKey string) error

	// Remote actors
	GetRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error)
	GetRemoteActors(ctx context.Context, actorURIs []string) ([]*domain.APRemoteActor, error)
	UpsertRemoteActor(ctx context.Context, actor *domain.APRemoteActor) error

	// Activities
	StoreActivity(ctx context.Context, activity *domain.APActivity) error
	GetActivity(ctx context.Context, activityURI string) (*domain.APActivity, error)
	GetActivitiesByActor(ctx context.Context, actorID string, limit, offset int) ([]*domain.APActivity, int, error)

	// Followers
	GetFollower(ctx context.Context, actorID, followerID string) (*domain.APFollower, error)
	UpsertFollower(ctx context.Context, follower *domain.APFollower) error
	DeleteFollower(ctx context.Context, actorID, followerID string) error
	GetFollowers(ctx context.Context, actorID string, state string, limit, offset int) ([]*domain.APFollower, int, error)
	GetFollowing(ctx context.Context, followerID string, state string, limit, offset int) ([]*domain.APFollower, int, error)

	// Deduplication
	IsActivityReceived(ctx context.Context, activityURI string) (bool, error)
	MarkActivityReceived(ctx context.Context, activityURI string) error

	// Video reactions
	UpsertVideoReaction(ctx context.Context, videoID, actorURI, reactionType, activityURI string) error
	DeleteVideoReaction(ctx context.Context, activityURI string) error

	// Video shares
	UpsertVideoShare(ctx context.Context, videoID, actorURI, activityURI string) error
	DeleteVideoShare(ctx context.Context, activityURI string) error

	// Delivery queue
	EnqueueDelivery(ctx context.Context, delivery *domain.APDeliveryQueue) error
	BulkEnqueueDelivery(ctx context.Context, deliveries []*domain.APDeliveryQueue) error
	GetPendingDeliveries(ctx context.Context, limit int) ([]*domain.APDeliveryQueue, error)
	UpdateDeliveryStatus(ctx context.Context, deliveryID string, status string, attempts int, lastError *string, nextAttempt time.Time) error
}
