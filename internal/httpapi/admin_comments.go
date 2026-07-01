package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// adminCommentView is the admin/moderator comments-overview projection.
type adminCommentView struct {
	ID                string    `json:"id"`
	VideoID           string    `json:"video_id"`
	VideoTitle        string    `json:"video_title"`
	Body              string    `json:"body"`
	AuthorUsername    string    `json:"author_username"`
	AuthorDisplayName string    `json:"author_display_name"`
	CreatedAt         time.Time `json:"created_at"`
}

// adminCommentListResponse is the paginated admin comments overview.
type adminCommentListResponse struct {
	Comments []adminCommentView `json:"comments"`
	Limit    int                `json:"limit"`
	Offset   int                `json:"offset"`
}

// handleListAdminComments returns all comments newest first for moderators/admins,
// each with its author + video context. Behind requireRole(admin, moderator).
// Optional ?q filters by body; pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListAdminComments(c echo.Context) error {
	q := c.QueryParam("q")
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.commentsvc.ListForAdmin(c.Request().Context(), q, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]adminCommentView, 0, len(items))
	for _, it := range items {
		views = append(views, adminCommentView{
			ID:                it.ID.String(),
			VideoID:           it.VideoID.String(),
			VideoTitle:        it.VideoTitle,
			Body:              it.Body,
			AuthorUsername:    it.AuthorUsername,
			AuthorDisplayName: it.AuthorDisplayName,
			CreatedAt:         it.CreatedAt,
		})
	}
	return c.JSON(http.StatusOK, adminCommentListResponse{Comments: views, Limit: limit, Offset: offset})
}
