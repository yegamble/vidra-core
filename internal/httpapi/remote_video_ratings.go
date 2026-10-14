package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/federation"
	"github.com/vidra/vidra-core/internal/remotevideo"
)

// remoteVideoRatingView is the caller's own vote on a remote video. The counts
// belong to the origin instance, so none are reported here.
type remoteVideoRatingView struct {
	MyRating *string `json:"my_rating"`
}

func newRemoteVideoRatingView(r string) remoteVideoRatingView {
	if r == "" {
		return remoteVideoRatingView{}
	}
	return remoteVideoRatingView{MyRating: &r}
}

// remoteVideoForRating resolves the caller and a visible remote video: unknown,
// origin-blocked or per-video-blocked is 404, as for remote comments.
func (s *Server) remoteVideoForRating(c echo.Context) (userID, videoID uuid.UUID, err error) {
	if userID, _, err = mustPrincipal(c); err != nil {
		return
	}
	if videoID, err = pathUUID(c, "id", "remote video not found"); err != nil {
		return
	}
	if _, err = s.remotevideosvc.Get(c.Request().Context(), videoID); errors.Is(err, remotevideo.ErrNotFound) {
		err = echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	return
}

// handleGetRemoteVideoRating returns the caller's vote. Behind requireAuth.
func (s *Server) handleGetRemoteVideoRating(c echo.Context) error {
	userID, videoID, err := s.remoteVideoForRating(c)
	if err != nil {
		return err
	}
	r, err := s.fedsvc.RemoteVideoRating(c.Request().Context(), videoID, userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, newRemoteVideoRatingView(r))
}

// handlePutRemoteVideoRating sets the caller's vote and federates it to the
// origin as a Like/Dislike (with an Undo of a previous vote). Behind requireAuth.
func (s *Server) handlePutRemoteVideoRating(c echo.Context) error {
	userID, videoID, err := s.remoteVideoForRating(c)
	if err != nil {
		return err
	}
	var in setRatingRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	switch err := s.fedsvc.RateRemoteVideo(c.Request().Context(), videoID, userID, in.Rating); {
	case errors.Is(err, federation.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, newRemoteVideoRatingView(in.Rating))
}

// handleDeleteRemoteVideoRating clears the caller's vote (idempotent) and
// federates its Undo. Behind requireAuth.
func (s *Server) handleDeleteRemoteVideoRating(c echo.Context) error {
	userID, videoID, err := s.remoteVideoForRating(c)
	if err != nil {
		return err
	}
	if err := s.fedsvc.ClearRemoteVideoRating(c.Request().Context(), videoID, userID); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, remoteVideoRatingView{})
}
