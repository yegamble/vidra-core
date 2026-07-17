package httpapi

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/ipfsmirror"
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
	v, err := s.videoVisibleForMedia(c, id)
	if err != nil {
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	f, err := s.videosvc.FileForView(c.Request().Context(), id, viewerID, authed, kind)
	if err != nil {
		return videoError(err)
	}
	// Keep the small VTT map on the stable application URL: its cue references
	// storyboard.jpg, whose own stable route may redirect to the immutable CID.
	if kind == "storyboard" && publicVideoForIPFS(v.Privacy, v.State) {
		if redirected, err := s.redirectPublicIPFS(c, f.StorageKey, ipfsmirror.ClassStoryboard); redirected {
			return err
		}
	}
	return s.serveStoredObject(c, f.StorageKey, contentType)
}
