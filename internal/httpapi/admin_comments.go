package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// adminCommentView is the admin/moderator comments-overview projection. Remote
// (federated) comments are flagged with their origin domain (remote-content §6).
type adminCommentView struct {
	ID                string    `json:"id"`
	VideoID           string    `json:"video_id"`
	VideoTitle        string    `json:"video_title"`
	Body              string    `json:"body"`
	AuthorUsername    string    `json:"author_username"`
	AuthorDisplayName string    `json:"author_display_name"`
	Remote            bool      `json:"remote"`
	AuthorDomain      string    `json:"author_domain,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	// Deleted marks a §1 tombstone; the body renders as "[deleted]".
	Deleted bool `json:"deleted"`
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
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, err := s.commentsvc.ListForAdmin(c.Request().Context(), q, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]adminCommentView, 0, len(items))
	for _, it := range items {
		v := adminCommentView{
			ID:                it.ID.String(),
			VideoID:           it.VideoID.String(),
			VideoTitle:        it.VideoTitle,
			Body:              it.Body,
			AuthorUsername:    it.AuthorUsername,
			AuthorDisplayName: it.AuthorDisplayName,
			Remote:            it.Remote,
			AuthorDomain:      it.AuthorDomain,
			CreatedAt:         it.CreatedAt,
			Deleted:           it.Deleted,
		}
		if it.Deleted {
			v.Body = tombstoneBody
		}
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, adminCommentListResponse{Comments: views, Limit: page.Limit, Offset: page.Offset})
}
