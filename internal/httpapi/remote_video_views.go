package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/remotevideo"
)

// handleRecordRemoteVideoView is the remote twin of handleRecordVideoView: the
// player pings it when a remote video is watched, and the view is federated to
// the origin (which owns the count) as a View signed by the instance actor,
// once per viewer per window. Behind optionalAuth. Always 204 for a visible
// video — with federation disabled nothing is sent; unknown or blocked is 404.
func (s *Server) handleRecordRemoteVideoView(c echo.Context) error {
	id, err := pathUUID(c, "id", "remote video not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	notFound := echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	if _, err := s.remotevideosvc.Get(ctx, id); errors.Is(err, remotevideo.ErrNotFound) {
		return notFound
	} else if err != nil {
		return err
	}
	if s.cfg.FederationEnabled {
		viewerID, _, authed := principalFromContext(c)
		switch err := s.fedsvc.RecordRemoteVideoView(ctx, id, viewerKey(c, viewerID, authed)); {
		case errors.Is(err, federation.ErrNotFound):
			return notFound
		case err != nil:
			return err
		}
	}
	return c.NoContent(http.StatusNoContent)
}
