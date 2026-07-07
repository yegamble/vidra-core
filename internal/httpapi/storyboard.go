package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

// handleGetVideoStoryboardImage serves a video's storyboard sprite sheet
// (kind='storyboard'). Same visibility as the detail endpoint (private → owner
// only, blocked → moderators only); a video without a stored storyboard is 404.
func (s *Server) handleGetVideoStoryboardImage(c echo.Context) error {
	return s.serveVideoStoryboard(c, "storyboard", "image/jpeg")
}

// handleGetVideoStoryboardVTT serves a video's storyboard WebVTT sprite map
// (kind='storyboard_vtt'). Same visibility as the sprite sheet.
func (s *Server) handleGetVideoStoryboardVTT(c echo.Context) error {
	return s.serveVideoStoryboard(c, "storyboard_vtt", "text/vtt")
}

// serveVideoStoryboard is the shared visibility-gated storyboard file server.
func (s *Server) serveVideoStoryboard(c echo.Context, kind, contentType string) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenFromViewer(c, id); err != nil {
		return err
	} else if hidden {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.passwordGateByID(c, id); err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, kind)
	if err != nil {
		return videoError(err)
	}
	return s.serveStoredObject(c, f.StorageKey, contentType)
}
