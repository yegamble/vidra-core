package port

import (
	"athena/internal/domain"
	"context"
)

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
	UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error
	UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error
	Count(ctx context.Context) (int64, error)
	GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error)
	GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error)
	CreateRemoteVideo(ctx context.Context, video *domain.Video) error
}
