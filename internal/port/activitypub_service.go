package port

import (
	"context"
	"net/http"
	"vidra-core/internal/domain"
)

type ActivityPubService interface {
	// Actor management
	GetLocalActor(ctx context.Context, username string) (*domain.Actor, error)
	FetchRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error)

	// Activity handling
	HandleInboxActivity(ctx context.Context, activity map[string]interface{}, r *http.Request) error

	// Activity delivery
	DeliverActivity(ctx context.Context, actorID, inboxURL string, activity interface{}) error

	// Collections
	GetOutbox(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error)
	GetFollowers(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error)
	GetFollowing(ctx context.Context, username string, page int, limit int) (*domain.OrderedCollectionPage, error)

	// Collection counts
	GetOutboxCount(ctx context.Context, username string) (int, error)
	GetFollowersCount(ctx context.Context, username string) (int, error)
	GetFollowingCount(ctx context.Context, username string) (int, error)
}
