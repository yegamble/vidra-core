package port

import (
	"athena/internal/domain"
	"context"
	"github.com/google/uuid"
)

// CaptionRepository defines the interface for caption data operations
type CaptionRepository interface {
	Create(ctx context.Context, caption *domain.Caption) error
	GetByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error)
	GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.Caption, error)
	GetByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error)
	Update(ctx context.Context, caption *domain.Caption) error
	Delete(ctx context.Context, captionID uuid.UUID) error
	DeleteByVideoID(ctx context.Context, videoID uuid.UUID) error
	CountByVideoID(ctx context.Context, videoID uuid.UUID) (int, error)
}
