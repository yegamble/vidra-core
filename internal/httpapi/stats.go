package httpapi

import (
	"errors"
	"net/http"

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

// accountStatsTotals is the account-wide engagement rollup (GET /me/stats).
type accountStatsTotals struct {
	Views     int64 `json:"views"`
	Likes     int64 `json:"likes"`
	Dislikes  int64 `json:"dislikes"`
	Comments  int64 `json:"comments"`
	Followers int64 `json:"followers"`
	Videos    int64 `json:"videos"`
}

// accountChannelStats is one channel's row in the account stats breakdown.
type accountChannelStats struct {
	ID          string `json:"id"`
	Handle      string `json:"handle"`
	DisplayName string `json:"display_name"`
	Views       int64  `json:"views"`
	Followers   int64  `json:"followers"`
	Videos      int64  `json:"videos"`
	Views28d    int64  `json:"views_28d"`
}

// accountStatsResponse is the owner-only GET /me/stats body: account-wide
// totals, the aggregated 30-day daily series, and a per-channel breakdown.
type accountStatsResponse struct {
	Totals     accountStatsTotals    `json:"totals"`
	DailyViews []dailyViewsView      `json:"daily_views"`
	Channels   []accountChannelStats `json:"channels"`
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	ch, err := s.channelsvc.GetByHandle(ctx, c.Param("handle"))
	if err != nil {
		if errors.Is(err, channel.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "channel not found")
		}
		return err
	}
	// Owner OR editor collaborators (migration 0097) may view channel stats;
	// reported as 404 (not 403) to everyone else so existence is not leaked.
	if ch.OwnerID != userID && !s.canManageChannelContent(ctx, userID, ch.ID) {
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

// handleGetAccountStats returns the authenticated user's stats aggregated
// across every channel they OWN (the studio "All channels" scope): account-wide
// totals, the merged 30-day daily series, and a per-channel breakdown. Behind
// requireAuth; owner-scoped by construction (only the caller's own channels).
func (s *Server) handleGetAccountStats(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	stats, err := s.videosvc.AccountStats(ctx, userID)
	if err != nil {
		return err
	}
	// Follower counts live in the channel domain; fetch them in one grouped
	// query and merge (per channel + summed into the account total).
	followers, err := s.channelsvc.FollowerCountsByOwner(ctx, userID)
	if err != nil {
		return err
	}
	resp := accountStatsResponse{
		Totals: accountStatsTotals{
			Views:    stats.Totals.Views,
			Likes:    stats.Totals.Likes,
			Dislikes: stats.Totals.Dislikes,
			Comments: stats.Totals.Comments,
			Videos:   stats.Totals.Videos,
		},
		DailyViews: dailyViews(stats.Daily),
		Channels:   make([]accountChannelStats, 0, len(stats.Channels)),
	}
	var totalFollowers int64
	for _, ch := range stats.Channels {
		f := followers[ch.ID]
		totalFollowers += f
		resp.Channels = append(resp.Channels, accountChannelStats{
			ID:          ch.ID.String(),
			Handle:      ch.Handle,
			DisplayName: ch.DisplayName,
			Views:       ch.Views,
			Followers:   f,
			Videos:      ch.Videos,
			Views28d:    ch.Views28d,
		})
	}
	resp.Totals.Followers = totalFollowers
	return c.JSON(http.StatusOK, resp)
}
