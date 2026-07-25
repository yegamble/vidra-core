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
	// ErrNotTopLevel means a pin was attempted on a reply; only a top-level
	// comment (parent_id IS NULL) can be pinned.
	ErrNotTopLevel = errors.New("comment: not a top-level comment")
	// ErrTombstoned means a pin or heart was attempted on a tombstoned comment
	// (its author's account was deleted; the body is "[deleted]").
	ErrTombstoned = errors.New("comment: comment is tombstoned")
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
	// Creator pin + heart (0099): local metadata, no federation. GetCommentWithMeta
	// resolves a single comment with author identity + heart flag + whether it is
	// its video's pinned comment; SetCommentHearted flips the heart flag;
	// SetVideoPinnedComment sets (or clears, with a NULL id) the video's pin.
	GetCommentWithMeta(ctx context.Context, id uuid.UUID) (sqlcgen.GetCommentWithMetaRow, error)
	SetCommentHearted(ctx context.Context, arg sqlcgen.SetCommentHeartedParams) (sqlcgen.Comment, error)
	SetVideoPinnedComment(ctx context.Context, arg sqlcgen.SetVideoPinnedCommentParams) error
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
	// Pinned is true when this comment is its video's pinned comment (0099). It
	// is computed per row (not a comment column), so the HTTP layer projects it
	// onto the view separately, like Remote/AuthorDomain.
	Pinned bool
}

// withAuthorFromMeta projects a single-comment GetCommentWithMeta row (author
// identity + heart flag + pinned flag) into a WithAuthor — the same shape the
// list path builds, so the pin/heart endpoints render identically to the list.
func withAuthorFromMeta(r sqlcgen.GetCommentWithMetaRow) WithAuthor {
	wa := WithAuthor{
		Comment: sqlcgen.Comment{
			ID: r.ID, VideoID: r.VideoID, UserID: r.UserID, Body: r.Body,
			ParentID: r.ParentID, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
			RemoteActorUrl: r.RemoteActorUrl, DeletedAt: r.DeletedAt, Hearted: r.Hearted,
		},
		AuthorUsername:    r.AuthorUsername,
		AuthorDisplayName: r.AuthorDisplayName,
		Pinned:            r.Pinned,
	}
	if r.RemoteActorUrl != nil {
		wa.Remote = true
		wa.AuthorDomain = r.AuthorDomain
		wa.AuthorUsername = r.RemoteAuthorName
		wa.AuthorDisplayName = r.RemoteAuthorName
	}
	return wa
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
				RemoteActorUrl: r.RemoteActorUrl, DeletedAt: r.DeletedAt, Hearted: r.Hearted,
			},
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
			Pinned:            r.Pinned,
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

// Get returns a comment by id, or ErrNotFound. The HTTP layer uses it to resolve
// a comment's video for the video-owner authorization check that guards a pin or
// heart action, before performing the action.
func (s *Service) Get(ctx context.Context, commentID uuid.UUID) (sqlcgen.Comment, error) {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		return sqlcgen.Comment{}, ErrNotFound
	}
	return c, nil
}

// IsPinned reports whether commentID is currently its video's pinned comment.
// The HTTP layer uses it to project the correct `pinned` flag onto a single-
// comment response (e.g. an edit) without re-listing the thread. An unknown
// comment is ErrNotFound.
func (s *Service) IsPinned(ctx context.Context, commentID uuid.UUID) (bool, error) {
	row, err := s.repo.GetCommentWithMeta(ctx, commentID)
	if err != nil {
		return false, ErrNotFound
	}
	return row.Pinned, nil
}

// Pin makes commentID its video's single pinned comment (YouTube-style creator
// pin). The HTTP layer has already authorized the caller (video owner/editor, or
// staff). The comment must exist (ErrNotFound), be top-level (parent_id IS NULL,
// else ErrNotTopLevel), and not be tombstoned (ErrTombstoned). The single pin
// column means setting it atomically replaces any existing pin. Local metadata
// only — deliberately does NOT go through Edit or fire any federation hook.
// Returns the updated view (pinned=true).
func (s *Service) Pin(ctx context.Context, commentID uuid.UUID) (WithAuthor, error) {
	row, err := s.repo.GetCommentWithMeta(ctx, commentID)
	if err != nil {
		return WithAuthor{}, ErrNotFound
	}
	if row.DeletedAt.Valid {
		return WithAuthor{}, ErrTombstoned
	}
	if row.ParentID.Valid {
		return WithAuthor{}, ErrNotTopLevel
	}
	if err := s.repo.SetVideoPinnedComment(ctx, sqlcgen.SetVideoPinnedCommentParams{
		VideoID:         row.VideoID,
		PinnedCommentID: pgtype.UUID{Bytes: commentID, Valid: true},
	}); err != nil {
		return WithAuthor{}, err
	}
	row.Pinned = true
	return withAuthorFromMeta(row), nil
}

// Unpin clears the video's pin, but ONLY when commentID is the current pin — so
// it never clobbers a different pinned comment. An unknown comment is
// ErrNotFound. A no-op when this comment is not pinned. Local metadata only (no
// federation). Returns the updated view (pinned=false).
func (s *Service) Unpin(ctx context.Context, commentID uuid.UUID) (WithAuthor, error) {
	row, err := s.repo.GetCommentWithMeta(ctx, commentID)
	if err != nil {
		return WithAuthor{}, ErrNotFound
	}
	if row.Pinned {
		if err := s.repo.SetVideoPinnedComment(ctx, sqlcgen.SetVideoPinnedCommentParams{
			VideoID:         row.VideoID,
			PinnedCommentID: pgtype.UUID{}, // NULL = unpin
		}); err != nil {
			return WithAuthor{}, err
		}
		row.Pinned = false
	}
	return withAuthorFromMeta(row), nil
}

// Heart marks a comment with the creator heart (the video owner "likes" it). The
// HTTP layer has already authorized the caller. The comment must exist
// (ErrNotFound) and not be tombstoned (ErrTombstoned); it may be any depth and
// remote-authored (local metadata only, no federation). Returns the updated
// view (hearted=true).
func (s *Service) Heart(ctx context.Context, commentID uuid.UUID) (WithAuthor, error) {
	row, err := s.repo.GetCommentWithMeta(ctx, commentID)
	if err != nil {
		return WithAuthor{}, ErrNotFound
	}
	if row.DeletedAt.Valid {
		return WithAuthor{}, ErrTombstoned
	}
	return s.applyHearted(ctx, row, true)
}

// Unheart removes the creator heart. An unknown comment is ErrNotFound; a
// tombstoned comment is allowed (removal is always permitted). Local metadata
// only (no federation). Returns the updated view (hearted=false).
func (s *Service) Unheart(ctx context.Context, commentID uuid.UUID) (WithAuthor, error) {
	row, err := s.repo.GetCommentWithMeta(ctx, commentID)
	if err != nil {
		return WithAuthor{}, ErrNotFound
	}
	return s.applyHearted(ctx, row, false)
}

// applyHearted writes the heart flag and returns the refreshed view.
func (s *Service) applyHearted(ctx context.Context, row sqlcgen.GetCommentWithMetaRow, hearted bool) (WithAuthor, error) {
	if _, err := s.repo.SetCommentHearted(ctx, sqlcgen.SetCommentHeartedParams{ID: row.ID, Hearted: hearted}); err != nil {
		return WithAuthor{}, err
	}
	row.Hearted = hearted
	return withAuthorFromMeta(row), nil
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
	// Deleted marks a §1 tombstone (the author's account was hard-deleted):
	// the body is empty in storage and views render "[deleted]".
	Deleted bool
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
			Deleted:           r.DeletedAt.Valid,
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
