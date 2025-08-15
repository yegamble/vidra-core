package usecase

import (
    "athena/internal/domain"
    "context"
)

type VideoRepository interface {
    Create(ctx context.Context, video *domain.Video) error
}

