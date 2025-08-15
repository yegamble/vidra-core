package usecase

import (
    "athena/internal/domain"
    "context"
)

type VideoRepository interface {
    Create(ctx context.Context, video *domain.Video) error
    GetByID(ctx context.Context, id string) (*domain.Video, error)
    GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, error)
    Update(ctx context.Context, video *domain.Video) error
    Delete(ctx context.Context, id string, userID string) error
    List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
    Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
}

