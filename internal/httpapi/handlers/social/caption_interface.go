package social

import (
	"context"
	"io"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type CaptionServiceInterface interface {
	CreateCaption(ctx context.Context, videoID uuid.UUID, req *domain.CreateCaptionRequest, file io.Reader) (*domain.Caption, error)
	GetCaptionsByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.CaptionListResponse, error)
	GetCaptionByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error)
	GetCaptionByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error)
	GetCaptionContent(ctx context.Context, captionID uuid.UUID) (io.ReadCloser, string, error)
	UpdateCaption(ctx context.Context, captionID uuid.UUID, req *domain.UpdateCaptionRequest) (*domain.Caption, error)
	DeleteCaption(ctx context.Context, captionID uuid.UUID) error
}

type CaptionVideoRepository interface {
	GetByID(ctx context.Context, videoID string) (*domain.Video, error)
}
