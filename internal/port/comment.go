package port

import (
	"vidra-core/internal/domain"
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
	// Admin listing with filtering for instance-wide comment management.
	ListAll(ctx context.Context, opts domain.AdminCommentListOptions) ([]*domain.CommentWithUser, int64, error)
	// Approve sets a comment's approved flag and clears held_for_review.
	Approve(ctx context.Context, id uuid.UUID) error
	// BulkRemoveByAccount soft-deletes all comments by an account name.
	BulkRemoveByAccount(ctx context.Context, accountName string) (int64, error)
}
