package port

import (
	"athena/internal/domain"
	"context"
)

// ServerFollowingRepository defines storage for instance following relationships.
type ServerFollowingRepository interface {
	ListFollowers(ctx context.Context) ([]*domain.ServerFollowing, error)
	ListFollowing(ctx context.Context) ([]*domain.ServerFollowing, error)
	Follow(ctx context.Context, host string) error
	Unfollow(ctx context.Context, host string) error
	SetFollowerState(ctx context.Context, host string, state domain.ServerFollowingState) error
	DeleteFollower(ctx context.Context, host string) error
}
