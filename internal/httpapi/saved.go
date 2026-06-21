package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// handleSaveVideo adds a public, published video to the caller's library
// (idempotent). Behind requireAuth.
func (s *Server) handleSaveVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	videoID, err := s.publicVideoID(c)
	if err != nil {
		return err
	}
	if err := s.videosvc.Save(c.Request().Context(), videoID, userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleUnsaveVideo removes a video from the caller's library (idempotent). Behind
// requireAuth. The video need not still be public, so a user can always clean up.
func (s *Server) handleUnsaveVideo(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	videoID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.videosvc.Unsave(c.Request().Context(), videoID, userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleListSavedVideos returns the caller's saved videos as feed cards,
// newest-saved first. Behind requireAuth. Pagination via ?limit/?offset.
func (s *Server) handleListSavedVideos(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	items, err := s.videosvc.ListSaved(c.Request().Context(), userID, int32(limit), int32(offset))
	if err != nil {
		return err
	}
	views := make([]videoView, 0, len(items))
	for _, it := range items {
		views = append(views, feedItemView(it))
	}
	return c.JSON(http.StatusOK, videoFeedResponse{Videos: views, Sort: "recent", Limit: limit, Offset: offset})
}
