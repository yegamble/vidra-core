// Package authoredremotecomment implements the write side of locally-authored
// comments ON REMOTE (federated) videos — the home-instance-hosts-and-federates
// model (migration 0147). It is the deliberate reversal of the shipped decision
// that "comments live on the origin instance": the owner ruled that the AUTHOR'S
// HOME INSTANCE hosts and moderates the comment and federates it to the origin.
//
// This package OWNS the authored_remote_comments table (create/list/edit/delete
// with authorization); the mirror table remote_video_comments (0140) stays a
// read-only mirror and is untouched. It is HTTP-agnostic and testable without a
// server. The video's existence/visibility is enforced by the HTTP layer (it owns
// the remote-video service); federation is a set of best-effort hooks so this
// package does not depend on internal/federation.
package authoredremotecomment

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrNotFound means no authored remote comment matches the lookup.
	ErrNotFound = errors.New("authoredremotecomment: not found")
	// ErrForbidden means the caller is neither the author nor a moderator.
	ErrForbidden = errors.New("authoredremotecomment: forbidden")
)

// Repository is the data access the service needs. *sqlcgen.Queries satisfies it
// directly; tests substitute an in-memory fake.
type Repository interface {
	CreateAuthoredRemoteComment(ctx context.Context, arg sqlcgen.CreateAuthoredRemoteCommentParams) (sqlcgen.AuthoredRemoteComment, error)
	GetAuthoredRemoteComment(ctx context.Context, id uuid.UUID) (sqlcgen.AuthoredRemoteComment, error)
	ListAuthoredRemoteCommentsByVideo(ctx context.Context, arg sqlcgen.ListAuthoredRemoteCommentsByVideoParams) ([]sqlcgen.ListAuthoredRemoteCommentsByVideoRow, error)
	CountAuthoredRemoteCommentsByVideo(ctx context.Context, arg sqlcgen.CountAuthoredRemoteCommentsByVideoParams) (int64, error)
	UpdateAuthoredRemoteCommentBody(ctx context.Context, arg sqlcgen.UpdateAuthoredRemoteCommentBodyParams) (sqlcgen.AuthoredRemoteComment, error)
	DeleteAuthoredRemoteComment(ctx context.Context, id uuid.UUID) (int64, error)
}

// Service holds the authored-remote-comment application logic.
type Service struct {
	repo    Repository
	baseURL string // canonical public origin, used to mint the local AP object id

	// Federation seams (best-effort, synchronous): invoked after a create/edit/
	// delete so federation can mint + deliver the Create/Update/Delete{Note} to
	// the origin without this package importing internal/federation. Nil = no
	// federation (federation disabled).
	onCreate func(ctx context.Context, commentID uuid.UUID)
	onUpdate func(ctx context.Context, commentID uuid.UUID)
	onDelete func(ctx context.Context, commentID, remoteVideoID, userID uuid.UUID, objectURL string)
}

// Option configures a Service.
type Option func(*Service)

// WithBaseURL sets the canonical public origin used to mint the local AP object
// id for each authored comment (baseURL + /remote-comments/<id>).
func WithBaseURL(u string) Option { return func(s *Service) { s.baseURL = u } }

// WithCreateHook registers the post-create federation callback.
func WithCreateHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onCreate = fn }
}

// WithUpdateHook registers the post-edit federation callback.
func WithUpdateHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onUpdate = fn }
}

// WithDeleteHook registers the post-delete federation callback, passing the ids
// the row no longer holds (it is hard-deleted) so federation can fan a Delete out.
func WithDeleteHook(fn func(ctx context.Context, commentID, remoteVideoID, userID uuid.UUID, objectURL string)) Option {
	return func(s *Service) { s.onDelete = fn }
}

// NewService builds the service.
func NewService(repo Repository, opts ...Option) *Service {
	s := &Service{repo: repo}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Create stores a new locally-authored comment on the remote video identified by
// remoteVideoID, in reply to the origin video's object id (originObjectURL). The
// caller has already confirmed the remote video exists and is visible. The local
// AP object id is minted here (baseURL + /remote-comments/<id>). Fires the create
// hook so federation delivers a Create{Note} to the origin.
func (s *Service) Create(ctx context.Context, remoteVideoID, userID uuid.UUID, body, originObjectURL string) (sqlcgen.AuthoredRemoteComment, error) {
	id := uuid.New()
	created, err := s.repo.CreateAuthoredRemoteComment(ctx, sqlcgen.CreateAuthoredRemoteCommentParams{
		ID:            id,
		RemoteVideoID: remoteVideoID,
		UserID:        userID,
		Body:          body,
		ObjectUrl:     s.localCommentURL(id),
		InReplyTo:     originObjectURL,
	})
	if err != nil {
		return sqlcgen.AuthoredRemoteComment{}, err
	}
	if s.onCreate != nil {
		s.onCreate(ctx, created.ID)
	}
	return created, nil
}

// ListByRemoteVideo returns a remote video's locally-authored comments, oldest
// first, each with its author's identity and delivery status. When viewerAuthed,
// comments by an account the viewer has muted/blocked are hidden (parity with the
// local comment thread). The caller clamps limit/offset.
func (s *Service) ListByRemoteVideo(ctx context.Context, remoteVideoID, viewerID uuid.UUID, viewerAuthed bool, limit, offset int32) ([]sqlcgen.ListAuthoredRemoteCommentsByVideoRow, int64, error) {
	viewer := uuidOrNull(viewerID, viewerAuthed)
	rows, err := s.repo.ListAuthoredRemoteCommentsByVideo(ctx, sqlcgen.ListAuthoredRemoteCommentsByVideoParams{
		RemoteVideoID: remoteVideoID,
		ViewerID:      viewer,
		ResultLimit:   limit,
		ResultOffset:  offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountAuthoredRemoteCommentsByVideo(ctx, sqlcgen.CountAuthoredRemoteCommentsByVideoParams{
		RemoteVideoID: remoteVideoID,
		ViewerID:      viewer,
	})
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// Get returns one authored remote comment by id, or ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (sqlcgen.AuthoredRemoteComment, error) {
	c, err := s.repo.GetAuthoredRemoteComment(ctx, id)
	if err != nil {
		return sqlcgen.AuthoredRemoteComment{}, ErrNotFound
	}
	return c, nil
}

// Edit changes the body of the caller's OWN authored remote comment (only the
// author may edit; moderators remove, not edit). An unknown id is ErrNotFound;
// another user's comment is ErrForbidden. The caller trims/validates the body.
// Marks the row edited, resets delivery_state to pending, and fires the update
// hook so federation delivers an Update{Note}.
func (s *Service) Edit(ctx context.Context, id, userID uuid.UUID, body string) (sqlcgen.AuthoredRemoteComment, error) {
	c, err := s.repo.GetAuthoredRemoteComment(ctx, id)
	if err != nil {
		return sqlcgen.AuthoredRemoteComment{}, ErrNotFound
	}
	if c.UserID != userID {
		return sqlcgen.AuthoredRemoteComment{}, ErrForbidden
	}
	updated, err := s.repo.UpdateAuthoredRemoteCommentBody(ctx, sqlcgen.UpdateAuthoredRemoteCommentBodyParams{
		ID:   id,
		Body: body,
	})
	if err != nil {
		return sqlcgen.AuthoredRemoteComment{}, err
	}
	if s.onUpdate != nil {
		s.onUpdate(ctx, updated.ID)
	}
	return updated, nil
}

// Delete removes an authored remote comment. The author may always delete their
// own; a moderator/admin (isModerator) may delete anyone's (the home instance owns
// moderation — the ruling). An unknown id is ErrNotFound; a non-author non-
// moderator is ErrForbidden. Hard delete, then fire the delete hook so federation
// delivers a Delete of the local Note to the origin.
func (s *Service) Delete(ctx context.Context, id, userID uuid.UUID, isModerator bool) error {
	c, err := s.repo.GetAuthoredRemoteComment(ctx, id)
	if err != nil {
		return ErrNotFound
	}
	if !isModerator && c.UserID != userID {
		return ErrForbidden
	}
	if _, err := s.repo.DeleteAuthoredRemoteComment(ctx, id); err != nil {
		return err
	}
	if s.onDelete != nil {
		s.onDelete(ctx, c.ID, c.RemoteVideoID, c.UserID, c.ObjectUrl)
	}
	return nil
}

// localCommentURL is the ActivityPub object URL this instance mints for a locally-
// authored remote comment. Distinct from the local-comment path (/comments/) so
// the two id spaces never collide.
func (s *Service) localCommentURL(id uuid.UUID) string {
	return s.baseURL + "/remote-comments/" + id.String()
}

// uuidOrNull renders a viewer id as a nullable pgtype.UUID: valid only for an
// authenticated viewer, NULL (which makes the per-viewer mute/block clauses
// trivially true) for an anonymous one.
func uuidOrNull(id uuid.UUID, valid bool) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: valid}
}
