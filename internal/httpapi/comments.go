package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
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
}

func newCommentView(c sqlcgen.Comment, authorUsername, authorDisplayName string) commentView {
	return commentView{
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
	}
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

// publicVideoID parses the :id param and confirms the video exists and is
// public + published, so it can carry public interactions (comments, ratings).
// Anything else (missing, draft, unlisted, private) is a 404 — interactions on
// those are a later slice.
func (s *Server) publicVideoID(c echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil || v.State != "published" || v.Privacy != "public" {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return uuid.UUID{}, err
	} else if hidden {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	return id, nil
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
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
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
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

// handleListComments returns a public+published video's comments, newest first.
// Auth is optional (optionalAuth): a signed-in viewer's muted accounts are hidden;
// anonymous viewers see all. Pagination via ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListComments(c echo.Context) error {
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	viewerID, _, authed := principalFromContext(c)
	items, err := s.commentsvc.ListByVideo(c.Request().Context(), videoID, viewerID, authed, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]commentView, 0, len(items))
	for _, it := range items {
		v := newCommentView(it.Comment, it.AuthorUsername, it.AuthorDisplayName)
		v.Remote = it.Remote
		v.AuthorDomain = it.AuthorDomain
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, commentListResponse{Comments: views, Limit: limit, Offset: offset})
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
	return c.JSON(http.StatusOK, newCommentView(updated, author.Username, author.DisplayName))
}
