package port

import (
	"athena/internal/domain"
	"context"

	"github.com/google/uuid"
)

type CommentRepository interface {
	Create(ctx context.Context, comment *domain.Comment) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error)
	GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error)
	Update(ctx context.Context, id uuid.UUID, body string) error
	Delete(ctx context.Context, id uuid.UUID) error
	ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error)
	ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error)
	ListRepliesBatch(ctx context.Context, parentIDs []uuid.UUID, limit int) (map[uuid.UUID][]*domain.CommentWithUser, error)
	CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error)
	FlagComment(ctx context.Context, flag *domain.CommentFlag) error
	UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error
	GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error)
	UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error
	IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error)
}
