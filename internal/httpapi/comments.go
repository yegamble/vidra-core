package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

const maxCommentLen = 2000

// commentView is the public projection of a comment, with its author's identity.
// AuthorID is the author's account id (so a signed-in viewer can mute them) —
// null for a REMOTE (federated) comment, which instead is flagged remote with
// its origin domain and the remote author-name snapshot (remote-content §6).
// ParentID is null for a top-level comment, or the id of the comment this one
// replies to (so the client can build the thread tree).
type commentView struct {
	ID                string    `json:"id"`
	VideoID           string    `json:"video_id"`
	Body              string    `json:"body"`
	ParentID          *string   `json:"parent_id"`
	AuthorID          *string   `json:"author_id"`
	AuthorUsername    string    `json:"author_username"`
	AuthorDisplayName string    `json:"author_display_name"`
	Remote            bool      `json:"remote"`
	AuthorDomain      string    `json:"author_domain,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
	// Edited is true once the body has been edited (updated_at moved past
	// created_at), so the client can show an "(edited)" marker.
	Edited bool `json:"edited"`
	// Deleted marks a tombstone (product-decisions.md §1): the author's account
	// was hard-deleted, the stored body is empty, and the view renders the
	// "[deleted]" placeholder while the reply thread stays intact.
	Deleted bool `json:"deleted"`
	// Pinned is true when this comment is its video's creator-pinned comment
	// (0099); at most one top-level comment per video is pinned, and the list
	// returns it first.
	Pinned bool `json:"pinned"`
	// Hearted is true when the video's creator has hearted this comment (0099).
	Hearted bool `json:"hearted"`
}

// tombstoneBody is what views render in place of a §1-tombstoned comment body.
const tombstoneBody = "[deleted]"

func newCommentView(c sqlcgen.Comment, authorUsername, authorDisplayName string) commentView {
	v := commentView{
		ID:                c.ID.String(),
		VideoID:           c.VideoID.String(),
		Body:              c.Body,
		ParentID:          uuidPtrString(c.ParentID),
		AuthorID:          uuidPtrString(c.UserID),
		AuthorUsername:    authorUsername,
		AuthorDisplayName: authorDisplayName,
		Remote:            c.RemoteActorUrl != nil,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
		Edited:            c.UpdatedAt.After(c.CreatedAt),
		Hearted:           c.Hearted,
	}
	if c.DeletedAt.Valid {
		v.Deleted = true
		v.Body = tombstoneBody
		v.Edited = false // the tombstoning bump is not a user edit
	}
	return v
}

// uuidPtrString renders a nullable pgtype.UUID as *string: nil when NULL, else
// the canonical UUID string.
func uuidPtrString(u pgtype.UUID) *string {
	if !u.Valid {
		return nil
	}
	s := uuid.UUID(u.Bytes).String()
	return &s
}

// publicVideo parses the :id param and confirms the video exists and is
// public + published, so it can carry public interactions (comments, ratings).
// Anything else (missing, draft, unlisted, private) is a 404 — interactions on
// those are a later slice. Returns the full row so callers can consult
// per-video policy (comments_policy, config-parity W9) without a second fetch.
func (s *Server) publicVideo(c echo.Context) (sqlcgen.GetVideoByIDRow, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil || v.State != "published" || v.Privacy != "public" {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
	} else if hidden {
		return sqlcgen.GetVideoByIDRow{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	return v, nil
}

// publicVideoID is the id-only shorthand over publicVideo for interaction
// endpoints that do not consult per-video policy.
func (s *Server) publicVideoID(c echo.Context) (uuid.UUID, error) {
	v, err := s.publicVideo(c)
	return v.ID, err
}

// createCommentRequest is the POST /videos/{id}/comments body. parent_id is
// optional: when present it makes the comment a reply to that comment.
type createCommentRequest struct {
	Body     string  `json:"body"`
	ParentID *string `json:"parent_id"`
}

func (r createCommentRequest) Validate() []FieldError {
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxCommentLen:
		return []FieldError{{Field: "body", Message: "must be at most 2000 characters"}}
	}
	if r.ParentID != nil && strings.TrimSpace(*r.ParentID) != "" {
		if _, err := uuid.Parse(strings.TrimSpace(*r.ParentID)); err != nil {
			return []FieldError{{Field: "parent_id", Message: "must be a valid comment id"}}
		}
	}
	return nil
}

// parentID returns the parsed parent comment id, or nil for a top-level comment.
// It assumes Validate has already accepted the format.
func (r createCommentRequest) parentID() *uuid.UUID {
	if r.ParentID == nil || strings.TrimSpace(*r.ParentID) == "" {
		return nil
	}
	id, err := uuid.Parse(strings.TrimSpace(*r.ParentID))
	if err != nil {
		return nil
	}
	return &id
}

// handleCreateComment posts a comment on a public, published video. Behind requireAuth.
func (s *Server) handleCreateComment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	// Feature toggle (P10): the instance can turn off new comments. Reading
	// existing comments stays open; only posting is gated.
	if !s.commentsEnabled() {
		return &FeatureDisabledError{Feature: "comments"}
	}
	v, err := s.publicVideo(c)
	if err != nil {
		return err
	}
	// Per-video comment policy (config-parity W9): the owner can turn off new
	// comments for one video. Same feature-disabled shape as the instance gate;
	// reading existing comments stays open here too.
	if v.CommentsPolicy == video.CommentsPolicyDisabled {
		return &FeatureDisabledError{Feature: "comments"}
	}
	videoID := v.ID
	var in createCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	created, err := s.commentsvc.Create(ctx, videoID, userID, strings.TrimSpace(in.Body), in.parentID())
	if err != nil {
		if errors.Is(err, comment.ErrParentNotFound) {
			return &ValidationError{Fields: []FieldError{{Field: "parent_id", Message: "is not a comment on this video"}}}
		}
		return err
	}
	// The author is the authenticated user; load their identity for the response.
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	// Notify the video owner of the new comment (best-effort; skipped when no
	// notifier is wired or you comment on your own video).
	if s.notifsvc != nil {
		if v, verr := s.videosvc.GetByID(ctx, videoID); verr == nil {
			if nerr := s.notifsvc.NotifyComment(ctx, v.OwnerID, userID, videoID, created.ID); nerr != nil {
				s.logger.WarnContext(ctx, "notify comment failed", "error", nerr, "video_id", videoID)
			}
		}
	}
	// Flag the comment against the moderation watched-words list (best-effort;
	// records matches for the admin review queue — never blocks the post).
	if s.watchwordsvc != nil {
		if _, werr := s.watchwordsvc.FlagComment(ctx, created.ID, created.Body); werr != nil {
			s.logger.WarnContext(ctx, "watched-word flagging failed", "error", werr, "comment_id", created.ID)
		}
	}
	return c.JSON(http.StatusCreated, newCommentView(created, author.Username, author.DisplayName))
}

// commentListResponse is the paginated comment list for a video.
type commentListResponse struct {
	Comments []commentView `json:"comments"`
	pageMeta
}

// handleListComments returns a public+published video's comments, newest first.
// Auth is optional (optionalAuth): a signed-in viewer's muted accounts are hidden;
// anonymous viewers see all. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListComments(c echo.Context) error {
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	viewerID, _, authed := principalFromContext(c)
	items, total, err := s.commentsvc.ListByVideo(c.Request().Context(), videoID, viewerID, authed, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]commentView, 0, len(items))
	for _, it := range items {
		v := newCommentView(it.Comment, it.AuthorUsername, it.AuthorDisplayName)
		v.Remote = it.Remote
		v.AuthorDomain = it.AuthorDomain
		v.Pinned = it.Pinned
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, commentListResponse{Comments: views, pageMeta: page.meta(total)})
}

// handleDeleteComment removes a comment. Behind requireAuth. The comment's author
// may always delete it; a moderator/admin may delete anyone's.
func (s *Server) handleDeleteComment(c echo.Context) error {
	userID, role, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	isModerator := role == "admin" || role == "moderator"
	if err := s.commentsvc.Delete(c.Request().Context(), id, userID, isModerator); err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// updateCommentRequest is the PATCH /comments/{id} body.
type updateCommentRequest struct {
	Body string `json:"body"`
}

func (r updateCommentRequest) Validate() []FieldError {
	switch body := strings.TrimSpace(r.Body); {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxCommentLen:
		return []FieldError{{Field: "body", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleUpdateComment edits the caller's own comment. Behind requireAuth. Only
// the author may edit (moderators delete, not edit): another user's comment is
// 403, an unknown id is 404, a blank/too-long body is 422.
func (s *Server) handleUpdateComment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	var in updateCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	updated, err := s.commentsvc.Edit(ctx, id, userID, strings.TrimSpace(in.Body))
	if err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	// Re-flag the edited body against the moderation watched-words list
	// (best-effort; an edit can newly introduce a flagged term).
	if s.watchwordsvc != nil {
		if _, werr := s.watchwordsvc.FlagComment(ctx, updated.ID, updated.Body); werr != nil {
			s.logger.WarnContext(ctx, "watched-word flagging failed", "error", werr, "comment_id", updated.ID)
		}
	}
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		return err
	}
	view := newCommentView(updated, author.Username, author.DisplayName)
	// An edited comment may itself be the video's pinned comment (0099): reflect
	// that in the response so the client keeps the pin badge. Best-effort — a
	// lookup failure just leaves pinned false rather than failing the edit.
	if pinned, perr := s.commentsvc.IsPinned(ctx, updated.ID); perr == nil {
		view.Pinned = pinned
	}
	return c.JSON(http.StatusOK, view)
}

// canManageCommentVideo reports whether a caller may perform a creator action
// (pin/heart) on a comment: staff (admin/moderator) via the moderation escape,
// otherwise the manager of the comment's video (channel owner or editor).
func (s *Server) canManageCommentVideo(ctx context.Context, userID uuid.UUID, role string, videoID uuid.UUID) bool {
	if role == "admin" || role == "moderator" {
		return true
	}
	_, ok := s.canManageVideo(ctx, userID, videoID)
	return ok
}

// commentCreatorAction is the shared pipeline for the four creator-metadata
// endpoints (pin/unpin/heart/unheart): authenticate, resolve the comment (404 if
// unknown), authorize the caller against the comment's video (403 otherwise),
// run the action, map its validation sentinels to 422, and return the updated
// comment view (200). None of the actions touch the comment body or fire a
// federation hook — they are local metadata only.
func (s *Server) commentCreatorAction(c echo.Context, action func(context.Context, uuid.UUID) (comment.WithAuthor, error)) error {
	userID, role, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	ctx := c.Request().Context()
	cmt, err := s.commentsvc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, comment.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		}
		return err
	}
	if !s.canManageCommentVideo(ctx, userID, role, cmt.VideoID) {
		return echo.NewHTTPError(http.StatusForbidden, "not allowed to manage this video's comments")
	}
	wa, err := action(ctx, id)
	if err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrNotTopLevel):
			return &ValidationError{Fields: []FieldError{{Field: "id", Message: "only a top-level comment can be pinned"}}}
		case errors.Is(err, comment.ErrTombstoned):
			return &ValidationError{Fields: []FieldError{{Field: "id", Message: "cannot pin or heart a deleted comment"}}}
		}
		return err
	}
	view := newCommentView(wa.Comment, wa.AuthorUsername, wa.AuthorDisplayName)
	view.Remote = wa.Remote
	view.AuthorDomain = wa.AuthorDomain
	view.Pinned = wa.Pinned
	return c.JSON(http.StatusOK, view)
}

// handlePinComment pins a top-level comment as its video's single pinned comment.
// Behind requireAuth. The caller manages the video (owner/editor) or is staff;
// pinning replaces any existing pin. A reply or a tombstoned comment is 422.
func (s *Server) handlePinComment(c echo.Context) error {
	return s.commentCreatorAction(c, s.commentsvc.Pin)
}

// handleUnpinComment clears the video's pin when this comment is the current one.
// Behind requireAuth.
func (s *Server) handleUnpinComment(c echo.Context) error {
	return s.commentCreatorAction(c, s.commentsvc.Unpin)
}

// handleHeartComment adds the creator heart to a comment (any depth, including
// remote-authored). Behind requireAuth. A tombstoned comment is 422.
func (s *Server) handleHeartComment(c echo.Context) error {
	return s.commentCreatorAction(c, s.commentsvc.Heart)
}

// handleUnheartComment removes the creator heart. Behind requireAuth.
func (s *Server) handleUnheartComment(c echo.Context) error {
	return s.commentCreatorAction(c, s.commentsvc.Unheart)
}
