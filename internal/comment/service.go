// Package comment implements video comments for vidra-core. It is HTTP-agnostic
// and testable without a server. Video visibility (whether a video is
// commentable) is enforced by the HTTP layer, which owns the video service.
// A comment is authored EITHER by a local user (user_id) OR — since federated
// comments (remote-content §6) — by a remote ActivityPub actor; remote rows are
// written by internal/federation, this service only reads them.
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
	UpdateComment(ctx context.Context, arg sqlcgen.UpdateCommentParams) (sqlcgen.Comment, error)
	DeleteComment(ctx context.Context, id uuid.UUID) error
}

// Service holds the comment application logic.
type Service struct {
	repo Repository

	// Federation seams (remote-content §6): invoked best-effort after a LOCAL
	// user's comment on a video is created/edited/deleted, so federation can fan
	// Create/Update/Delete{Note} out to remote followers without this package
	// depending on internal/federation. All may be nil (federation disabled).
	onCreate func(ctx context.Context, commentID uuid.UUID)
	onUpdate func(ctx context.Context, commentID uuid.UUID)
	onDelete func(ctx context.Context, commentID, videoID, authorID uuid.UUID)
}

// Option configures a Service.
type Option func(*Service)

// WithCreateHook registers a callback invoked (best-effort, synchronously)
// after a local user's comment is created — the seam federation uses to fan a
// Create{Note} out to the channel's remote followers.
func WithCreateHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onCreate = fn }
}

// WithUpdateHook registers a callback invoked (best-effort) after a local
// user's comment body is edited — federation fans an Update{Note} out.
func WithUpdateHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onUpdate = fn }
}

// WithDeleteHook registers a callback invoked (best-effort) after a LOCAL
// user's comment is deleted, passing the ids the row no longer holds —
// federation fans a Delete out. Remote-authored comments never fire it (a
// local moderator delete of a remote comment is local-only, §6).
func WithDeleteHook(fn func(ctx context.Context, commentID, videoID, authorID uuid.UUID)) Option {
	return func(s *Service) { s.onDelete = fn }
}

// NewService builds the comment service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, o := range opts {
		o(s)
	}
	return s
}

// WithAuthor is a comment plus its author's display identity, for list
// responses. Remote is true for a federated comment (remote-content §6): then
// AuthorUsername carries the remote author-name snapshot, AuthorDomain the
// origin instance, and the comment has no local account id.
type WithAuthor struct {
	Comment           sqlcgen.Comment
	AuthorUsername    string
	AuthorDisplayName string
	Remote            bool
	AuthorDomain      string
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
	created, err := s.repo.CreateComment(ctx, sqlcgen.CreateCommentParams{
		VideoID:  videoID,
		UserID:   pgtype.UUID{Bytes: userID, Valid: true},
		Body:     body,
		ParentID: parent,
	})
	if err != nil {
		return sqlcgen.Comment{}, err
	}
	if s.onCreate != nil {
		s.onCreate(ctx, created.ID)
	}
	return created, nil
}

// ListByVideo returns a video's comments newest-first, each with its author's
// identity. The caller clamps limit/offset. When viewerAuthed is true, comments
// from accounts (and remote instances) viewerID has muted are hidden; for an
// anonymous viewer (viewerAuthed false) only the admin instance blocklist
// filters. Remote-authored rows are flagged Remote with their origin domain.
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
		wa := WithAuthor{
			Comment: sqlcgen.Comment{
				ID: r.ID, VideoID: r.VideoID, UserID: r.UserID, Body: r.Body,
				ParentID: r.ParentID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
				RemoteActorUrl: r.RemoteActorUrl,
			},
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
		}
		if r.RemoteActorUrl != nil {
			wa.Remote = true
			wa.AuthorDomain = r.AuthorDomain
			wa.AuthorUsername = r.RemoteAuthorName
			wa.AuthorDisplayName = r.RemoteAuthorName
		}
		out = append(out, wa)
	}
	return out, nil
}

// Delete removes a comment. The comment's author may always delete it; a
// moderator/admin (isModerator) may delete anyone's — including a remote-
// authored one (that delete is local-only; nothing federates upstream, §6). An
// unknown id is ErrNotFound; a non-author non-moderator is ErrForbidden.
func (s *Service) Delete(ctx context.Context, commentID, userID uuid.UUID, isModerator bool) error {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return ErrNotFound
	}
	isAuthor := c.UserID.Valid && uuid.UUID(c.UserID.Bytes) == userID
	if !isModerator && !isAuthor {
		return ErrForbidden
	}
	if err := s.repo.DeleteComment(ctx, commentID); err != nil {
		return err
	}
	// Only LOCAL comments federate their deletion.
	if s.onDelete != nil && c.UserID.Valid {
		s.onDelete(ctx, commentID, c.VideoID, uuid.UUID(c.UserID.Bytes))
	}
	return nil
}

// Edit changes a comment's body. Only the author may edit their own comment
// (moderators delete, not edit) — so a remote-authored comment is never
// editable locally. An unknown id is ErrNotFound; another user's comment is
// ErrForbidden. The caller trims/validates the body first.
func (s *Service) Edit(ctx context.Context, commentID, userID uuid.UUID, body string) (sqlcgen.Comment, error) {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return sqlcgen.Comment{}, ErrNotFound
	}
	if !c.UserID.Valid || uuid.UUID(c.UserID.Bytes) != userID {
		return sqlcgen.Comment{}, ErrForbidden
	}
	updated, err := s.repo.UpdateComment(ctx, sqlcgen.UpdateCommentParams{ID: commentID, Body: body})
	if err != nil {
		return sqlcgen.Comment{}, err
	}
	if s.onUpdate != nil {
		s.onUpdate(ctx, updated.ID)
	}
	return updated, nil
}

// AdminComment is a comment as seen in the admin/moderator comments overview:
// the body, its author's identity (local, or remote + origin domain), and the
// video it's on.
type AdminComment struct {
	ID                uuid.UUID
	VideoID           uuid.UUID
	VideoTitle        string
	Body              string
	AuthorUsername    string
	AuthorDisplayName string
	Remote            bool
	AuthorDomain      string
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
		ac := AdminComment{
			ID:                r.ID,
			VideoID:           r.VideoID,
			VideoTitle:        r.VideoTitle,
			Body:              r.Body,
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
			CreatedAt:         r.CreatedAt,
		}
		if r.RemoteActorUrl != nil {
			ac.Remote = true
			ac.AuthorDomain = r.AuthorDomain
			ac.AuthorUsername = r.RemoteAuthorName
			ac.AuthorDisplayName = r.RemoteAuthorName
		}
		items = append(items, ac)
	}
	return items, nil
}
