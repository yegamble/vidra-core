package port

import (
	"context"

	"athena/internal/domain"
)

// RedundancyRepository defines the interface for redundancy data persistence.
type RedundancyRepository interface {
	// Instance peer operations
	CreateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error
	GetInstancePeerByID(ctx context.Context, id string) (*domain.InstancePeer, error)
	ListInstancePeers(ctx context.Context, limit, offset int, activeOnly bool) ([]*domain.InstancePeer, error)
	UpdateInstancePeer(ctx context.Context, peer *domain.InstancePeer) error
	UpdateInstancePeerContact(ctx context.Context, id string) error
	DeleteInstancePeer(ctx context.Context, id string) error
	GetActiveInstancesWithCapacity(ctx context.Context, videoSizeBytes int64) ([]*domain.InstancePeer, error)

	// Video redundancy operations
	CreateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error
	GetVideoRedundancyByID(ctx context.Context, id string) (*domain.VideoRedundancy, error)
	GetVideoRedundanciesByVideoID(ctx context.Context, videoID string) ([]*domain.VideoRedundancy, error)
	GetVideoRedundanciesByInstanceID(ctx context.Context, instanceID string) ([]*domain.VideoRedundancy, error)
	UpdateVideoRedundancy(ctx context.Context, redundancy *domain.VideoRedundancy) error
	UpdateRedundancyProgress(ctx context.Context, id string, bytesTransferred, speedBPS int64) error
	CancelRedundanciesByInstanceID(ctx context.Context, instanceID string) error
	DeleteVideoRedundancy(ctx context.Context, id string) error
	ListPendingRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error)
	ListFailedRedundancies(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error)
	ListRedundanciesForResync(ctx context.Context, limit int) ([]*domain.VideoRedundancy, error)

	// Policy operations
	CreateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error
	GetRedundancyPolicyByID(ctx context.Context, id string) (*domain.RedundancyPolicy, error)
	ListRedundancyPolicies(ctx context.Context, enabledOnly bool) ([]*domain.RedundancyPolicy, error)
	ListPoliciesToEvaluate(ctx context.Context) ([]*domain.RedundancyPolicy, error)
	UpdateRedundancyPolicy(ctx context.Context, policy *domain.RedundancyPolicy) error
	UpdatePolicyEvaluationTime(ctx context.Context, id string) error
	DeleteRedundancyPolicy(ctx context.Context, id string) error

	// Sync log operations
	CreateSyncLog(ctx context.Context, log *domain.RedundancySyncLog) error
	CleanupOldSyncLogs(ctx context.Context) (int, error)

	// Statistics and health
	GetRedundancyStats(ctx context.Context) (map[string]interface{}, error)
	GetVideoRedundancyHealth(ctx context.Context, videoID string) (float64, error)
	CheckInstanceHealth(ctx context.Context) (int, error)
}

// RedundancyVideoRepository defines the video operations needed by the redundancy service.
type RedundancyVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	GetVideosForRedundancy(ctx context.Context, strategy domain.RedundancyStrategy, limit int) ([]*domain.Video, error)
}
