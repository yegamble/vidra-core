package comment

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
)

type Service struct {
	commentRepo port.CommentRepository
	videoRepo   port.VideoRepository
	userRepo    port.UserRepository
	channelRepo port.ChannelRepository
}

func NewService(
	commentRepo port.CommentRepository,
	videoRepo port.VideoRepository,
	userRepo port.UserRepository,
	channelRepo port.ChannelRepository,
) *Service {
	return &Service{
		commentRepo: commentRepo,
		videoRepo:   videoRepo,
		userRepo:    userRepo,
		channelRepo: channelRepo,
	}
}

func (s *Service) CreateComment(ctx context.Context, userID uuid.UUID, req *domain.CreateCommentRequest) (*domain.Comment, error) {
	// Verify video exists
	video, err := s.videoRepo.GetByID(ctx, req.VideoID.String())
	if err != nil {
		return nil, fmt.Errorf("video not found: %w", err)
	}

	// Check if comments are allowed based on video privacy
	if video.Privacy != domain.PrivacyPublic && video.Privacy != domain.PrivacyUnlisted {
		return nil, fmt.Errorf("comments not allowed on private videos")
	}

	// If replying to a comment, verify parent exists
	if req.ParentID != nil {
		parent, err := s.commentRepo.GetByID(ctx, *req.ParentID)
		if err != nil {
			return nil, fmt.Errorf("parent comment not found: %w", err)
		}

		// Ensure parent is from the same video
		if parent.VideoID != req.VideoID {
			return nil, fmt.Errorf("parent comment is from a different video")
		}

		// Don't allow replies to deleted comments
		if parent.Status != domain.CommentStatusActive {
			return nil, fmt.Errorf("cannot reply to deleted or hidden comment")
		}
	}

	comment := &domain.Comment{
		VideoID:  req.VideoID,
		UserID:   userID,
		ParentID: req.ParentID,
		Body:     req.Body,
		Status:   domain.CommentStatusActive,
	}

	if err := s.commentRepo.Create(ctx, comment); err != nil {
		return nil, fmt.Errorf("failed to create comment: %w", err)
	}

	return comment, nil
}

func (s *Service) GetComment(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	comment, err := s.commentRepo.GetByIDWithUser(ctx, id)
	if err != nil {
		return nil, err
	}

	// Don't show deleted comments unless for moderation
	if comment.Status == domain.CommentStatusDeleted {
		return nil, domain.ErrNotFound
	}

	return comment, nil
}

func (s *Service) UpdateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.UpdateCommentRequest) error {
	// Verify ownership
	isOwner, err := s.commentRepo.IsOwner(ctx, commentID, userID)
	if err != nil {
		return fmt.Errorf("failed to check ownership: %w", err)
	}

	if !isOwner {
		return domain.ErrUnauthorized
	}

	// Update the comment
	if err := s.commentRepo.Update(ctx, commentID, req.Body); err != nil {
		return fmt.Errorf("failed to update comment: %w", err)
	}

	return nil
}

func (s *Service) DeleteComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, isAdmin bool) error {
	// Check if user is the owner or an admin
	if !isAdmin {
		isOwner, err := s.commentRepo.IsOwner(ctx, commentID, userID)
		if err != nil {
			return fmt.Errorf("failed to check ownership: %w", err)
		}

		if !isOwner {
			// Check if user owns the video
			comment, err := s.commentRepo.GetByID(ctx, commentID)
			if err != nil {
				return err
			}

			video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
			if err != nil {
				return err
			}

			// Get channel owner
			channel, err := s.channelRepo.GetByID(ctx, video.ChannelID)
			if err != nil {
				return err
			}

			if channel.AccountID != userID {
				return domain.ErrUnauthorized
			}
		}
	}

	// Soft delete the comment
	if err := s.commentRepo.Delete(ctx, commentID); err != nil {
		return fmt.Errorf("failed to delete comment: %w", err)
	}

	return nil
}

func (s *Service) ListComments(ctx context.Context, videoID uuid.UUID, parentID *uuid.UUID, limit, offset int, orderBy string) ([]*domain.CommentWithUser, error) {
	// Verify video exists
	if _, err := s.videoRepo.GetByID(ctx, videoID.String()); err != nil {
		return nil, fmt.Errorf("video not found: %w", err)
	}

	// Default pagination
	if limit <= 0 || limit > 100 {
		limit = 20
	}

	if offset < 0 {
		offset = 0
	}

	if orderBy != "oldest" && orderBy != "newest" {
		orderBy = "newest"
	}

	opts := domain.CommentListOptions{
		VideoID:  videoID,
		ParentID: parentID,
		Limit:    limit,
		Offset:   offset,
		OrderBy:  orderBy,
	}

	comments, err := s.commentRepo.ListByVideo(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list comments: %w", err)
	}

	// For top-level comments, fetch replies count
	if parentID == nil {
		for _, comment := range comments {
			replies, err := s.commentRepo.ListReplies(ctx, comment.ID, 3, 0)
			if err == nil && len(replies) > 0 {
				// Convert to Comment type for replies
				for _, reply := range replies {
					comment.Replies = append(comment.Replies, &domain.Comment{
						ID:        reply.ID,
						VideoID:   reply.VideoID,
						UserID:    reply.UserID,
						ParentID:  reply.ParentID,
						Body:      reply.Body,
						Status:    reply.Status,
						FlagCount: reply.FlagCount,
						EditedAt:  reply.EditedAt,
						CreatedAt: reply.CreatedAt,
						UpdatedAt: reply.UpdatedAt,
						User: &domain.User{
							ID:       reply.UserID.String(),
							Username: reply.Username,
						},
					})
				}
			}
		}
	}

	return comments, nil
}

func (s *Service) FlagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, req *domain.FlagCommentRequest) error {
	// Verify comment exists
	comment, err := s.commentRepo.GetByID(ctx, commentID)
	if err != nil {
		return err
	}

	// Can't flag your own comment
	if comment.UserID == userID {
		return fmt.Errorf("cannot flag your own comment")
	}

	// Can't flag deleted comments
	if comment.Status != domain.CommentStatusActive {
		return fmt.Errorf("cannot flag deleted or hidden comment")
	}

	flag := &domain.CommentFlag{
		CommentID: commentID,
		UserID:    userID,
		Reason:    req.Reason,
		Details:   req.Details,
	}

	if err := s.commentRepo.FlagComment(ctx, flag); err != nil {
		return fmt.Errorf("failed to flag comment: %w", err)
	}

	// Auto-hide comments with too many flags
	if comment.FlagCount >= 5 {
		if err := s.commentRepo.UpdateStatus(ctx, commentID, domain.CommentStatusFlagged); err != nil {
			// Log error but don't fail the flag operation
			fmt.Printf("failed to auto-hide flagged comment %s: %v\n", commentID, err)
		}
	}

	return nil
}

func (s *Service) UnflagComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID) error {
	if err := s.commentRepo.UnflagComment(ctx, commentID, userID); err != nil {
		return fmt.Errorf("failed to unflag comment: %w", err)
	}
	return nil
}

func (s *Service) GetCommentFlags(ctx context.Context, commentID uuid.UUID, userID uuid.UUID, isAdmin bool) ([]*domain.CommentFlag, error) {
	// Only admins or video owners can see flags
	if !isAdmin {
		comment, err := s.commentRepo.GetByID(ctx, commentID)
		if err != nil {
			return nil, err
		}

		video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
		if err != nil {
			return nil, err
		}

		channel, err := s.channelRepo.GetByID(ctx, video.ChannelID)
		if err != nil {
			return nil, err
		}

		if channel.AccountID != userID {
			return nil, domain.ErrUnauthorized
		}
	}

	flags, err := s.commentRepo.GetFlags(ctx, commentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get comment flags: %w", err)
	}

	return flags, nil
}

func (s *Service) ModerateComment(ctx context.Context, userID uuid.UUID, commentID uuid.UUID, status domain.CommentStatus, isAdmin bool) error {
	// Only admins or video owners can moderate
	if !isAdmin {
		comment, err := s.commentRepo.GetByID(ctx, commentID)
		if err != nil {
			return err
		}

		video, err := s.videoRepo.GetByID(ctx, comment.VideoID.String())
		if err != nil {
			return err
		}

		channel, err := s.channelRepo.GetByID(ctx, video.ChannelID)
		if err != nil {
			return err
		}

		if channel.AccountID != userID {
			return domain.ErrUnauthorized
		}
	}

	if err := s.commentRepo.UpdateStatus(ctx, commentID, status); err != nil {
		return fmt.Errorf("failed to update comment status: %w", err)
	}

	return nil
}
