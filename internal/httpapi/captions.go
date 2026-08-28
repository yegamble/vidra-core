package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/video"
)

// captionView is the metadata projection of a caption track. The VTT itself is
// fetched from GET /api/v1/videos/{id}/captions/{language}.
type captionView struct {
	Language  string    `json:"language"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

// captionListResponse is a video's caption tracks.
type captionListResponse struct {
	Captions []captionView `json:"captions"`
}

// handleUploadCaption stores a WebVTT caption track for a video owned by the
// authenticated user (multipart: "file" + "language" [+ "label"]). Owner-only; a
// non-owner/unknown video is 404, a bad language or non-WebVTT file is 422.
func (s *Server) handleUploadCaption(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	fh, err := c.FormFile("file")
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, `multipart form field "file" is required`)
	}
	f, err := fh.Open()
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Owner OR editor collaborator (migration 0097); the write executes as the
	// channel owner once authorized.
	v, canManage := s.canManageVideo(c.Request().Context(), userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ct, err := s.videosvc.AddCaption(c.Request().Context(), v.OwnerID, id, video.CaptionInput{
		Language: c.FormValue("language"),
		Label:    c.FormValue("label"),
		Reader:   f,
	})
	if err != nil {
		if errors.Is(err, video.ErrInvalidCaption) {
			return echo.NewHTTPError(http.StatusUnprocessableEntity, "caption must be a WebVTT file with a valid language tag")
		}
		return videoError(err)
	}
	return c.JSON(http.StatusCreated, captionView{Language: ct.Language, Label: ct.Label, CreatedAt: ct.CreatedAt})
}

// captionVideoID resolves the video a caption read targets. For a normal video it
// applies the public+published rule (publicVideoID). For a password-protected
// video (CORE-17) it instead gates on owner/moderator/valid-playback-token — a
// wrong/absent credential is 401 password_required, matching the detail — and
// still requires the video to be published and not blocked, so the token holder
// can load caption tracks for the video they unlocked.
func (s *Server) captionVideoID(c echo.Context) (uuid.UUID, error) {
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return uuid.UUID{}, err
	}
	v, err := s.videosvc.GetByID(c.Request().Context(), id)
	if err != nil {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if v.Privacy != video.PrivacyPassword {
		return s.publicVideoID(c)
	}
	if err := s.passwordGate(c, id, v.Privacy, v.OwnerID); err != nil {
		return uuid.UUID{}, err
	}
	if v.State != "published" {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if hidden, err := s.videoHiddenByBlock(c, id); err != nil {
		return uuid.UUID{}, err
	} else if hidden {
		return uuid.UUID{}, echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	return id, nil
}

// handleListCaptions lists a video's caption tracks. No account auth: a public
// video is open; a password video needs a valid playback token (CORE-17).
func (s *Server) handleListCaptions(c echo.Context) error {
	videoID, err := s.captionVideoID(c)
	if err != nil {
		return err
	}
	items, err := s.videosvc.ListCaptions(c.Request().Context(), videoID)
	if err != nil {
		return err
	}
	views := make([]captionView, 0, len(items))
	for _, ct := range items {
		views = append(views, captionView{Language: ct.Language, Label: ct.Label, CreatedAt: ct.CreatedAt})
	}
	return c.JSON(http.StatusOK, captionListResponse{Captions: views})
}

// handleDownloadCaption serves a video's WebVTT caption for a language. No account
// auth: a public video is open; a password video needs a valid playback token
// (Bearer or ?pt=, CORE-17). An unknown language is 404.
func (s *Server) handleDownloadCaption(c echo.Context) error {
	videoID, err := s.captionVideoID(c)
	if err != nil {
		return err
	}
	rc, err := s.videosvc.OpenCaption(c.Request().Context(), videoID, strings.TrimSpace(c.Param("lang")))
	if err != nil {
		if errors.Is(err, video.ErrCaptionNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "caption not found")
		}
		return err
	}
	defer func() { _ = rc.Close() }()
	// Caption tracks had no cache policy at all before the delivery wave; they
	// now take the same short private window as the other small per-video assets
	// (and no-store when the request carries a playback token).
	setMediaCacheControl(c, delivery.ClassCaption)
	return c.Stream(http.StatusOK, "text/vtt; charset=utf-8", rc)
}

// handleDeleteCaption removes a caption track from a video owned by the caller.
// Behind requireAuth. Non-owner/unknown video → 404. Idempotent.
func (s *Server) handleDeleteCaption(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "video not found")
	if err != nil {
		return err
	}
	// Owner OR editor collaborator (migration 0097).
	v, canManage := s.canManageVideo(c.Request().Context(), userID, id)
	if !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.videosvc.DeleteCaption(c.Request().Context(), v.OwnerID, id, strings.TrimSpace(c.Param("lang"))); err != nil {
		return videoError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
