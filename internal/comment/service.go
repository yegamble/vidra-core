// Package comment implements flat video comments for vidra-core. It is
// HTTP-agnostic and testable without a server. Video visibility (whether a video
// is commentable) is enforced by the HTTP layer, which owns the video service.
package comment

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no comment matches the lookup.
	ErrNotFound = errors.New("comment: not found")
	// ErrForbidden means the caller is not the comment's author.
	ErrForbidden = errors.New("comment: not the author")
	// ErrParentNotFound means a reply's parent_id does not reference an existing
	// comment on the same video.
	ErrParentNotFound = errors.New("comment: parent not found")
)

// Repository is the data access the comment service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateComment(ctx context.Context, arg sqlcgen.CreateCommentParams) (sqlcgen.Comment, error)
	ListCommentsByVideo(ctx context.Context, arg sqlcgen.ListCommentsByVideoParams) ([]sqlcgen.ListCommentsByVideoRow, error)
	ListAdminComments(ctx context.Context, arg sqlcgen.ListAdminCommentsParams) ([]sqlcgen.ListAdminCommentsRow, error)
	GetComment(ctx context.Context, id uuid.UUID) (sqlcgen.Comment, error)
	DeleteComment(ctx context.Context, id uuid.UUID) error
}

// Service holds the comment application logic.
type Service struct {
	repo Repository
}

// NewService builds the comment service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// WithAuthor is a comment plus its author's display identity, for list responses.
type WithAuthor struct {
	Comment           sqlcgen.Comment
	AuthorUsername    string
	AuthorDisplayName string
}

// Create posts a comment by userID on videoID. The caller is responsible for
// confirming the video is commentable (exists + visible) first. When parentID is
// non-nil the comment is a reply, and the parent must be an existing comment on
// the SAME video — otherwise ErrParentNotFound (so a reply can't be smuggled onto
// another video's thread, and existence of arbitrary comment ids isn't leaked).
func (s *Service) Create(ctx context.Context, videoID, userID uuid.UUID, body string, parentID *uuid.UUID) (sqlcgen.Comment, error) {
	var parent pgtype.UUID
	if parentID != nil {
		p, err := s.repo.GetComment(ctx, *parentID)
		if err != nil || p.VideoID != videoID {
			return sqlcgen.Comment{}, ErrParentNotFound
		}
		parent = pgtype.UUID{Bytes: *parentID, Valid: true}
	}
	return s.repo.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID:  videoID,
		UserID:   userID,
		Body:     body,
		ParentID: parent,
	})
}

// ListByVideo returns a video's comments newest-first, each with its author's
// identity. The caller clamps limit/offset. When viewerAuthed is true, comments
// from accounts viewerID has muted are hidden; for an anonymous viewer
// (viewerAuthed false) nothing is filtered.
func (s *Service) ListByVideo(ctx context.Context, videoID, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]WithAuthor, error) {
	rows, err := s.repo.ListCommentsByVideo(ctx, sqlcgen.ListCommentsByVideoParams{
		VideoID:      videoID,
		ViewerID:     pgtype.UUID{Bytes: viewerID, Valid: viewerAuthed},
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WithAuthor, 0, len(rows))
	for _, r := range rows {
		out = append(out, WithAuthor{
			Comment: sqlcgen.Comment{
				ID: r.ID, VideoID: r.VideoID, UserID: r.UserID, Body: r.Body,
				ParentID: r.ParentID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			},
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
		})
	}
	return out, nil
}

// Delete removes a comment. The comment's author may always delete it; a
// moderator/admin (isModerator) may delete anyone's. An unknown id is
// ErrNotFound; a non-author non-moderator is ErrForbidden.
func (s *Service) Delete(ctx context.Context, commentID, userID uuid.UUID, isModerator bool) error {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return ErrNotFound
	}
	if !isModerator && c.UserID != userID {
		return ErrForbidden
	}
	return s.repo.DeleteComment(ctx, commentID)
}

// AdminComment is a comment as seen in the admin/moderator comments overview:
// the body, its author's identity, and the video it's on.
type AdminComment struct {
	ID                uuid.UUID
	VideoID           uuid.UUID
	VideoTitle        string
	Body              string
	AuthorUsername    string
	AuthorDisplayName string
	CreatedAt         time.Time
}

// ListForAdmin returns all comments newest first for the admin/moderator
// overview. A non-empty query filters by body substring. The caller clamps
// limit/offset.
func (s *Service) ListForAdmin(ctx context.Context, query string, limit, offset int32) ([]AdminComment, error) {
	var q *string
	if trimmed := strings.TrimSpace(query); trimmed != "" {
		q = &trimmed
	}
	rows, err := s.repo.ListAdminComments(ctx, sqlcgen.ListAdminCommentsParams{
		Query:        q,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	items := make([]AdminComment, 0, len(rows))
	for _, r := range rows {
		items = append(items, AdminComment{
			ID:                r.ID,
			VideoID:           r.VideoID,
			VideoTitle:        r.VideoTitle,
			Body:              r.Body,
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
			CreatedAt:         r.CreatedAt,
		})
	}
	return items, nil
}
