package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

const maxCommentLen = 2000

// commentView is the public projection of a comment, with its author's identity.
// AuthorID is the author's account id (so a signed-in viewer can mute them).
type commentView struct {
	ID                string    `json:"id"`
	VideoID           string    `json:"video_id"`
	Body              string    `json:"body"`
	AuthorID          string    `json:"author_id"`
	AuthorUsername    string    `json:"author_username"`
	AuthorDisplayName string    `json:"author_display_name"`
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

func newCommentView(c sqlcgen.Comment, authorUsername, authorDisplayName string) commentView {
	return commentView{
		ID:                c.ID.String(),
		VideoID:           c.VideoID.String(),
		Body:              c.Body,
		AuthorID:          c.UserID.String(),
		AuthorUsername:    authorUsername,
		AuthorDisplayName: authorDisplayName,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
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

// createCommentRequest is the POST /videos/{id}/comments body.
type createCommentRequest struct {
	Body string `json:"body"`
}

func (r createCommentRequest) Validate() []FieldError {
	body := strings.TrimSpace(r.Body)
	switch {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxCommentLen:
		return []FieldError{{Field: "body", Message: "must be at most 2000 characters"}}
	}
	return nil
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
	created, err := s.commentsvc.Create(ctx, videoID, userID, strings.TrimSpace(in.Body))
	if err != nil {
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
	return c.JSON(http.StatusCreated, newCommentView(created, author.Username, author.DisplayName))
}

// commentListResponse is the paginated comment list for a video.
type commentListResponse struct {
	Comments []commentView `json:"comments"`
	Limit    int           `json:"limit"`
	Offset   int           `json:"offset"`
}

// handleListComments returns a public+published video's comments, newest first.
// No auth required. Pagination via ?limit (1–100, default 20) and ?offset.
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
	items, err := s.commentsvc.ListByVideo(c.Request().Context(), videoID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]commentView, 0, len(items))
	for _, it := range items {
		views = append(views, newCommentView(it.Comment, it.AuthorUsername, it.AuthorDisplayName))
	}
	return c.JSON(http.StatusOK, commentListResponse{Comments: views, Limit: limit, Offset: offset})
}

// handleDeleteComment removes the caller's own comment. Behind requireAuth.
func (s *Server) handleDeleteComment(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "comment not found")
	}
	if err := s.commentsvc.Delete(c.Request().Context(), id, userID); err != nil {
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
