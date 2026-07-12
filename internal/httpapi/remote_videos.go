package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/remotevideo"
)

// remoteVideoView is the remote-watch projection of a federated video
// (.ralph/specs/federation-remote-content.md §4). Metadata only: playback uses
// stream_url from the origin (when present) and watch_url always links the
// origin's watch page.
type remoteVideoView struct {
	ID              string     `json:"id"`
	Remote          bool       `json:"remote"` // always true; parity with local video cards
	Domain          string     `json:"domain"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	ObjectURL       string     `json:"object_url"`
	WatchURL        string     `json:"watch_url"`
	StreamURL       *string    `json:"stream_url,omitempty"`
	DurationSeconds *int32     `json:"duration_seconds,omitempty"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	HasThumbnail    bool       `json:"has_thumbnail"`
}

// newRemoteVideoView shapes one remote-video read model as its public view
// (shared by GET /remote-videos/{id} and the remote-URI search results).
func newRemoteVideoView(rv remotevideo.RemoteVideo) remoteVideoView {
	return remoteVideoView{
		ID:              rv.ID.String(),
		Remote:          true,
		Domain:          rv.Domain,
		Title:           rv.Title,
		Description:     rv.Description,
		ObjectURL:       rv.ObjectURL,
		WatchURL:        rv.WatchURL,
		StreamURL:       rv.StreamURL,
		DurationSeconds: rv.DurationSeconds,
		PublishedAt:     rv.PublishedAt,
		HasThumbnail:    rv.HasThumbnail,
	}
}

// handleGetRemoteVideo returns one ingested remote video's stored metadata.
// Public. Unknown id — or a video hidden because its origin instance is
// admin-blocked — is 404.
func (s *Server) handleGetRemoteVideo(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	rv, err := s.remotevideosvc.Get(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, remotevideo.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	return c.JSON(http.StatusOK, newRemoteVideoView(rv))
}

// handleGetRemoteVideoThumbnail streams the locally cached poster of a remote
// video (cached best-effort at ingestion, §5). Public. A video without a
// cached thumbnail is 404.
func (s *Server) handleGetRemoteVideoThumbnail(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
	}
	rc, err := s.remotevideosvc.OpenThumbnail(c.Request().Context(), id)
	if err != nil {
		if errors.Is(err, remotevideo.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "thumbnail not found")
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	return c.Stream(http.StatusOK, "image/jpeg", rc)
}
