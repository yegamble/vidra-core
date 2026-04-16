package port

import (
	"context"
	"vidra-core/internal/domain"
)

// VideoProcessingParams groups the parameters for UpdateProcessingInfo.
type VideoProcessingParams struct {
	VideoID       string
	Status        domain.ProcessingStatus
	Duration      int
	Metadata      domain.VideoMetadata
	OutputPaths   map[string]string
	ThumbnailPath string
	PreviewPath   string
}

// VideoProcessingWithCIDsParams extends VideoProcessingParams with IPFS CID fields.
type VideoProcessingWithCIDsParams struct {
	VideoProcessingParams
	ProcessedCIDs map[string]string
	ThumbnailCID  string
	PreviewCID    string // NOTE: PreviewCID is accepted but not persisted (pre-existing)
}

type VideoRepository interface {
	Create(ctx context.Context, video *domain.Video) error
	GetByID(ctx context.Context, id string) (*domain.Video, error)
	GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error)
	GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error)
	GetByChannelID(ctx context.Context, channelID string, limit, offset int) ([]*domain.Video, int64, error)
	Update(ctx context.Context, video *domain.Video) error
	Delete(ctx context.Context, id string, userID string) error
	List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
	Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
	UpdateProcessingInfo(ctx context.Context, params VideoProcessingParams) error
	UpdateProcessingInfoWithCIDs(ctx context.Context, params VideoProcessingWithCIDsParams) error
	Count(ctx context.Context) (int64, error)
	GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error)
	GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error)
	CreateRemoteVideo(ctx context.Context, video *domain.Video) error
	GetVideoQuotaUsed(ctx context.Context, userID string) (int64, error)
	AppendOutputPath(ctx context.Context, videoID string, key string, path string) error
}
