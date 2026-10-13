package httpapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/mention"
	"github.com/vidra/vidra-core/internal/observability"
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
	// HeldForReview: awaiting approval (0161); only author + reviewers see it.
	HeldForReview bool `json:"held_for_review"`
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
		HeldForReview:     c.HeldForReview,
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
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return sqlcgen.GetVideoByIDRow{}, err
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

// canReviewComments: may the caller see/approve/reject v's held comments (0161)?
func (s *Server) canReviewComments(ctx context.Context, userID uuid.UUID, role string, v sqlcgen.GetVideoByIDRow) bool {
	return isStaff(role) || v.OwnerID == userID || s.canManageChannelContent(ctx, userID, v.ChannelID)
}

// watchedWordHolds: does body match a "hold" watched-word list for videoID
// (instance lists + the video owner's)? Best-effort — a lookup failure must not
// block the comment, so it fails open (published, still flagged afterwards).
func (s *Server) watchedWordHolds(ctx context.Context, videoID uuid.UUID, body string) bool {
	if s.watchwordsvc == nil {
		return false
	}
	hold, err := s.watchwordsvc.ShouldHold(ctx, videoID, body)
	if err != nil {
		s.logger.WarnContext(ctx, "watched-word hold check failed", "error", err, "video_id", videoID)
		return false
	}
	return hold
}

// handleCreateComment posts a comment on a public, published video. Behind requireAuth.
func (s *Server) handleCreateComment(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
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
	// The author is the authenticated user; load their identity for the response
	// — and do it BEFORE the write. requireAuth verifies the access JWT alone and
	// never re-reads the account, so a token minted before the account was
	// deactivated or §1-deleted still reaches this handler for the rest of its
	// JWT_ACCESS_TTL. UserByID is the only thing here that notices (it refuses an
	// inactive account); resolving after the insert meant the comment was already
	// committed when it did, and the client got a 500 for a write that happened.
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	// requires_approval (0161): a non-reviewer's comment is HELD until approved.
	create := s.commentsvc.Create
	body := strings.TrimSpace(in.Body)
	held := v.CommentsPolicy == video.CommentsPolicyRequiresApproval && !s.canReviewComments(ctx, userID, role, v)
	// A "hold" watched-word list (0172) parks the comment through this SAME path.
	// Decided before the write, so a held comment is never announced; the people
	// who manage the video are exempt, exactly as they are from the policy.
	if !held && s.watchedWordHolds(ctx, videoID, body) && !s.canReviewComments(ctx, userID, role, v) {
		held = true
	}
	if held {
		create = s.commentsvc.CreateHeld
	}
	created, err := create(ctx, videoID, userID, body, in.parentID())
	if err != nil {
		// A held parent answers exactly like an unknown one (0161).
		if errors.Is(err, comment.ErrParentNotFound) || errors.Is(err, comment.ErrParentHeld) {
			return &ValidationError{Fields: []FieldError{{Field: "parent_id", Message: "is not a comment on this video"}}}
		}
		return err
	}
	if !held {
		s.notifyCommentPublished(ctx, userID, created.ID, created.Body, v.OwnerID, false)
	} else if s.notifsvc != nil {
		// Tell the video's managers a comment awaits them (best-effort).
		if _, nerr := s.notifsvc.NotifyCommentHeld(ctx, created.ID); nerr != nil {
			s.logger.WarnContext(ctx, "notify comment held failed", "error", nerr, "comment_id", created.ID)
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

// notifyCommentPublished: a comment's notifications once public (create/approve, 0161).
func (s *Server) notifyCommentPublished(ctx context.Context, authorID, commentID uuid.UUID, body string, ownerID uuid.UUID, ownerKnows bool) {
	// Tell the PARENT COMMENT'S AUTHOR that they were replied to (best-effort).
	// This is a different recipient from the video owner below: the person being
	// answered usually does not own the video, and before this existed a reply
	// reached nobody but the owner. Resolved FIRST because the two comment
	// notifications are mutually exclusive when the parent's author IS the video
	// owner — that user is answered once, not told twice about one reply.
	// notifiedReplyTo stays uuid.Nil when nobody was notified (a top-level
	// comment, a self-reply, an opted-out or muted/blocked recipient), which
	// never matches a real owner id.
	notifiedReplyTo := uuid.Nil
	if s.notifsvc != nil {
		recipient, nerr := s.notifsvc.NotifyCommentReply(ctx, authorID, commentID)
		if nerr != nil {
			s.logger.WarnContext(ctx, "notify comment reply failed", "error", nerr, "comment_id", commentID)
		} else {
			notifiedReplyTo = recipient
		}
	}
	// Notify the video owner of the new comment (best-effort; skipped when no
	// notifier is wired, or when the owner was already answered as the parent
	// comment's author just above — one reply, one notification). Who the owner
	// is, and whether they have muted or blocked the commenter, is resolved
	// inside the notifier's SQL; the owner id here is only the mutual-exclusion
	// key, and comes from the video the caller already loaded.
	if s.notifsvc != nil && ownerID != notifiedReplyTo && !ownerKnows {
		if _, nerr := s.notifsvc.NotifyComment(ctx, authorID, commentID); nerr != nil {
			s.logger.WarnContext(ctx, "notify comment failed", "error", nerr, "comment_id", commentID)
		}
	}
	// Tell the local accounts @-mentioned in the body (best-effort). Runs after
	// the two notifications above on purpose: the fan-out skips anyone already
	// told about this comment, so a reply to the owner stays one notification.
	s.notifyCommentMentions(ctx, commentID, body)
}

// notifyCommentMentions raises comment_mention notifications for the local
// accounts a comment body @-mentions. Best-effort: a failure is logged (never
// the body) and never fails the comment. All gating — visibility, mute/block,
// prefs, "already notified" — is in the fan-out's SQL.
func (s *Server) notifyCommentMentions(ctx context.Context, commentID uuid.UUID, body string) {
	if s.notifsvc == nil {
		return
	}
	host := ""
	if u, err := url.Parse(strings.TrimSpace(s.cfg.PublicBaseURL)); err == nil {
		host = u.Host
	}
	names := mention.LocalUsernames(mention.Parse(body), host)
	if _, err := s.notifsvc.NotifyCommentMentions(ctx, commentID, names); err != nil {
		s.logger.WarnContext(ctx, "notify comment mentions failed", "error", err, "comment_id", commentID)
	}
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
	v, err := s.publicVideo(c)
	if err != nil {
		return err
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	ctx := c.Request().Context()
	viewerID, role, authed := principalFromContext(c)
	// Held comments (0161) reach reviewers; the SQL adds the viewer's own.
	includeHeld := authed && s.canReviewComments(ctx, viewerID, role, v)
	items, total, err := s.commentsvc.ListByVideo(ctx, v.ID, viewerID, authed, includeHeld, page.Limit32(), page.Offset32())
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
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Reviewers reject (delete) someone else's HELD comment (0161), audited.
	cmt, _, reviewer, lookupErr := s.commentVideo(ctx, userID, role, id)
	ok := lookupErr == nil
	isAuthor := cmt.UserID.Valid && uuid.UUID(cmt.UserID.Bytes) == userID
	rejecting := ok && cmt.HeldForReview && reviewer && !isAuthor
	if err := s.commentsvc.Delete(ctx, id, userID, isStaff(role) || rejecting); err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	if rejecting {
		s.auditEvent(c, audit.Event{
			Action: observability.ActionCommentReject, Result: observability.ResultSuccess,
			ActorID: userID.String(), ResourceType: "comment", ResourceID: id.String(),
		})
	}
	return c.NoContent(http.StatusNoContent)
}

// commentVideo loads a comment, its video, and whether the caller reviews it
// (0161). An unknown comment is comment.ErrNotFound; a failed video lookup is
// returned as is, so callers can fail closed.
func (s *Server) commentVideo(ctx context.Context, userID uuid.UUID, role string, id uuid.UUID) (sqlcgen.Comment, sqlcgen.GetVideoByIDRow, bool, error) {
	cmt, err := s.commentsvc.Get(ctx, id)
	if err != nil {
		return cmt, sqlcgen.GetVideoByIDRow{}, false, err
	}
	v, err := s.videosvc.GetByID(ctx, cmt.VideoID)
	if err != nil {
		return cmt, v, false, fmt.Errorf("comment video lookup: %w", err)
	}
	return cmt, v, s.canReviewComments(ctx, userID, role, v), nil
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
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
	}
	var in updateCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Same ordering rule as handleCreateComment: resolve the author first, so a
	// still-unexpired JWT from a deactivated or deleted account is refused
	// instead of editing the body and then failing the response.
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	// requires_approval (0161): a non-reviewer's changed body is re-held.
	// Fails closed: if the policy cannot be read, the edit is refused (5xx).
	edit := s.commentsvc.Edit
	_, v, reviewer, lookupErr := s.commentVideo(ctx, userID, role, id)
	if lookupErr != nil && !errors.Is(lookupErr, comment.ErrNotFound) {
		return lookupErr
	}
	editBody := strings.TrimSpace(in.Body)
	if lookupErr == nil && !reviewer &&
		(v.CommentsPolicy == video.CommentsPolicyRequiresApproval || s.watchedWordHolds(ctx, v.ID, editBody)) {
		edit = s.commentsvc.EditForReview
	}
	updated, err := edit(ctx, id, userID, editBody)
	if err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	if updated.HeldForReview && s.notifsvc != nil { // re-held: ping the reviewers
		if _, nerr := s.notifsvc.NotifyCommentHeld(ctx, updated.ID); nerr != nil {
			s.logger.WarnContext(ctx, "notify comment held failed", "error", nerr, "comment_id", updated.ID)
		}
	}
	// An edit can add mentions. Re-running the fan-out on the whole body is
	// safe: notifications_comment_mention_unique_idx makes an already-notified
	// (comment, user) a no-op, so only NEWLY added mentions notify.
	if !updated.HeldForReview { // a held comment's mentions wait for approval (0161)
		s.notifyCommentMentions(ctx, updated.ID, updated.Body)
	}
	// Re-flag the edited body against the moderation watched-words list
	// (best-effort; an edit can newly introduce a flagged term).
	if s.watchwordsvc != nil {
		if _, werr := s.watchwordsvc.FlagComment(ctx, updated.ID, updated.Body); werr != nil {
			s.logger.WarnContext(ctx, "watched-word flagging failed", "error", werr, "comment_id", updated.ID)
		}
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
	if isStaff(role) {
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
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
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
		case errors.Is(err, comment.ErrHeld):
			return &ValidationError{Fields: []FieldError{{Field: "id", Message: "approve the comment before pinning it"}}}
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

// approveCommentRequest names the version of the held comment the reviewer saw.
type approveCommentRequest struct {
	UpdatedAt *time.Time `json:"updated_at"`
}

func (r approveCommentRequest) Validate() []FieldError {
	if r.UpdatedAt == nil {
		return []FieldError{{Field: "updated_at", Message: "is required"}}
	}
	return nil
}

// handleApproveComment releases a held comment (0161): reviewers only, on THIS
// video. Only the transition notifies, federates and audits; repeats are 200.
func (s *Server) handleApproveComment(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	videoID, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
	}
	commentID, err := pathUUID(c, "commentId", "comment not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	cmt, v, reviewer, err := s.commentVideo(ctx, userID, role, commentID)
	if errors.Is(err, comment.ErrNotFound) || (err == nil && cmt.VideoID != videoID) {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	} else if err != nil {
		return err
	}
	if !reviewer {
		return echo.NewHTTPError(http.StatusForbidden, "not allowed to manage this video's comments")
	}
	var in approveCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	wa, released, err := s.commentsvc.Approve(ctx, commentID, *in.UpdatedAt)
	if err != nil {
		switch {
		case errors.Is(err, comment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, comment.ErrStale):
			return echo.NewHTTPError(http.StatusConflict, "the comment changed since you reviewed it")
		}
		return err
	}
	if released {
		// Third parties hear only about a video they can see right now.
		if wa.Comment.UserID.Valid && v.Privacy == "public" && v.State == "published" && !v.Blocked {
			s.notifyCommentPublished(ctx, uuid.UUID(wa.Comment.UserID.Bytes), commentID, wa.Comment.Body, v.OwnerID, v.OwnerID == userID)
		}
		s.auditEvent(c, audit.Event{
			Action: observability.ActionCommentApprove, Result: observability.ResultSuccess,
			ActorID: userID.String(), ResourceType: "comment", ResourceID: commentID.String(),
		})
	}
	return c.JSON(http.StatusOK, commentViewOf(wa))
}

// commentViewOf renders a service projection (author identity, remote origin,
// pinned) the way the thread renders it.
func commentViewOf(wa comment.WithAuthor) commentView {
	v := newCommentView(wa.Comment, wa.AuthorUsername, wa.AuthorDisplayName)
	v.Remote, v.AuthorDomain, v.Pinned = wa.Remote, wa.AuthorDomain, wa.Pinned
	return v
}

// managedCommentView is a review-queue entry: the comment plus its video.
type managedCommentView struct {
	commentView
	VideoTitle string `json:"video_title"`
}

// managedCommentListResponse is the GET /me/videos/comments page.
type managedCommentListResponse struct {
	Comments []managedCommentView `json:"comments"`
	pageMeta
}

// handleListManagedComments is the review queue (0161): comments on every
// video the caller manages (channels they own or edit), newest first. Behind
// requireAuth. ?held=true lists only comments awaiting approval, ?held=false
// only published ones; omitted lists both; anything else is 422.
func (s *Server) handleListManagedComments(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var held *bool
	switch q := c.QueryParam("held"); q {
	case "":
	case "true", "false":
		b := q == "true"
		held = &b
	default:
		return &ValidationError{Fields: []FieldError{{Field: "held", Message: "must be true or false"}}}
	}
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.commentsvc.ListForManager(c.Request().Context(), userID, held, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]managedCommentView, 0, len(items))
	for _, it := range items {
		views = append(views, managedCommentView{commentView: commentViewOf(it.WithAuthor), VideoTitle: it.VideoTitle})
	}
	return c.JSON(http.StatusOK, managedCommentListResponse{Comments: views, pageMeta: page.meta(total)})
}
