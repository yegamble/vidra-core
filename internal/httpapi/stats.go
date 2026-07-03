package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/video"
)

// dailyViewsView is one day of a stats series ("YYYY-MM-DD", UTC).
type dailyViewsView struct {
	Day   string `json:"day"`
	Views int64  `json:"views"`
}

// videoStatsResponse is the owner-only GET /videos/{id}/stats body.
type videoStatsResponse struct {
	Views      int64            `json:"views"`
	Likes      int64            `json:"likes"`
	Dislikes   int64            `json:"dislikes"`
	Comments   int64            `json:"comments"`
	DailyViews []dailyViewsView `json:"daily_views"`
}

// channelStatsResponse is the owner-only GET /channels/{handle}/stats body.
type channelStatsResponse struct {
	Views      int64            `json:"views"`
	Likes      int64            `json:"likes"`
	Dislikes   int64            `json:"dislikes"`
	Comments   int64            `json:"comments"`
	Followers  int64            `json:"followers"`
	Videos     int64            `json:"videos"`
	DailyViews []dailyViewsView `json:"daily_views"`
}

func dailyViews(days []video.DayViews) []dailyViewsView {
	out := make([]dailyViewsView, 0, len(days))
	for _, d := range days {
		out = append(out, dailyViewsView{Day: d.Day.UTC().Format("2006-01-02"), Views: d.Views})
	}
	return out
}

// handleGetVideoStats returns a video's engagement totals and 30-day daily
// views series to its owner only (product-decisions §8). Behind requireAuth;
// a non-owner or unknown id is 404 so existence is not leaked.
func (s *Server) handleGetVideoStats(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	stats, err := s.videosvc.VideoStats(c.Request().Context(), userID, id)
	if err != nil {
		return videoError(err) // ErrNotFound / ErrForbidden -> 404
	}
	return c.JSON(http.StatusOK, videoStatsResponse{
		Views:      stats.Views,
		Likes:      stats.Likes,
		Dislikes:   stats.Dislikes,
		Comments:   stats.Comments,
		DailyViews: dailyViews(stats.Daily),
	})
}

// handleGetChannelStats returns a channel's aggregated engagement totals
// (plus follower and video counts) and 30-day daily views series to its owner
// only. Behind requireAuth; a non-owner or unknown handle is 404.
func (s *Server) handleGetChannelStats(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "channel not found")
		}
		return err
	}
	if ch.OwnerID != userID {
		// Owner-only, reported as 404 (not 403) to everyone else.
		return echo.NewHTTPError(http.StatusNotFound, "channel not found")
	}
	stats, err := s.videosvc.ChannelStats(ctx, ch.ID)
	if err != nil {
		return err
	}
	followers, err := s.channelsvc.FollowerCount(ctx, ch.ID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, channelStatsResponse{
		Views:      stats.Views,
		Likes:      stats.Likes,
		Dislikes:   stats.Dislikes,
		Comments:   stats.Comments,
		Followers:  followers,
		Videos:     stats.Videos,
		DailyViews: dailyViews(stats.Daily),
	})
}
