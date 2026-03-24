package social

import (
	"context"
	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type CommentServiceInterface interface {
	CreateComment(ctx context.Context, userID uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error)
	GetComment(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error)
	ListComments(ctx context.Context, videoID uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error)
	UpdateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.UpdateCommentRequest) error
	DeleteComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, isAdmin bool) error
	FlagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.FlagCommentRequest) error
	UnflagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID) error
	ModerateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, status domain.CommentStatus, isAdmin bool) error
}
