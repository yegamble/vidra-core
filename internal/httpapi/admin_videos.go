package httpapi

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
)

// adminVideoView is the admin/moderator videos-overview projection of a video.
type adminVideoView struct {
	ID                 string    `json:"id"`
	Title              string    `json:"title"`
	Privacy            string    `json:"privacy"`
	State              string    `json:"state"`
	ChannelHandle      string    `json:"channel_handle"`
	ChannelDisplayName string    `json:"channel_display_name"`
	Views              int64     `json:"views"`
	CreatedAt          time.Time `json:"created_at"`
	Blocked            bool      `json:"blocked"`
}

// adminVideoListResponse is the paginated admin videos overview.
type adminVideoListResponse struct {
	Videos []adminVideoView `json:"videos"`
	Limit  int              `json:"limit"`
	Offset int              `json:"offset"`
}

// handleListAdminVideos returns all videos (any privacy/state) newest first for
// moderators/admins, each with its current block status. Behind
// requireRole(admin, moderator). Optional ?q filters by title; pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListAdminVideos(c echo.Context) error {
	q := c.QueryParam("q")
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.videosvc.ListAdmin(c.Request().Context(), q, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]adminVideoView, 0, len(items))
	for _, it := range items {
		views = append(views, adminVideoView{
			ID:                 it.ID.String(),
			Title:              it.Title,
			Privacy:            it.Privacy,
			State:              it.State,
			ChannelHandle:      it.ChannelHandle,
			ChannelDisplayName: it.ChannelDisplayName,
			Views:              it.Views,
			CreatedAt:          it.CreatedAt,
			Blocked:            it.Blocked,
		})
	}
	return c.JSON(http.StatusOK, adminVideoListResponse{Videos: views, Limit: limit, Offset: offset})
}
