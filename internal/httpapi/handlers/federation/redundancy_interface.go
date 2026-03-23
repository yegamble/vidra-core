package federation

import (
	"context"

	"athena/internal/domain"
)

type RedundancyServiceInterface interface {
	ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error)
	RegisterInstancePeer(ctx context.Context, peer *domain.InstancePeer) error
	GetInstancePeer(ctx context.Context, id string) (*domain.InstancePeer, error)
	UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error
	DeleteInstancePeer(ctx context.Context, id string) error

	CreateRedundancy(ctx context.Context, videoID, instanceID string, strategy domain.RedundancyStrategy, priority int) (*domain.VideoRedundancy, error)
	GetRedundancy(ctx context.Context, id string) (*domain.VideoRedundancy, error)
	ListVideoRedundancies(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error)
	CancelRedundancy(ctx context.Context, id string) error
	DeleteRedundancy(ctx context.Context, id string) error
	SyncRedundancy(ctx context.Context, redundancyID string) error

	ListPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error)
	CreatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error
	GetPolicy(ctx context.Context, id string) (*domain.RedundancyPolicy, error)
	UpdatePolicy(ctx context.Context, policy *domain.RedundancyPolicy) error
	DeletePolicy(ctx context.Context, id string) error
	EvaluatePolicies(ctx context.Context) (int, error)

	GetStats(ctx context.Context) (map[string]interface{}, error)
	GetVideoHealth(ctx context.Context, videoID string) (float64, error)
}

type InstanceDiscoveryInterface interface {
	DiscoverInstance(ctx context.Context, instanceURL string) (*domain.InstancePeer, error)
}
