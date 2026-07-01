package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

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
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
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

	ct, err := s.videosvc.AddCaption(c.Request().Context(), userID, id, video.CaptionInput{
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

// handleListCaptions lists a public, published video's caption tracks. No auth.
func (s *Server) handleListCaptions(c echo.Context) error {
	videoID, err := s.publicVideoID(c)
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

// handleDownloadCaption serves a public, published video's WebVTT caption for a
// language. No auth. An unknown language is 404.
func (s *Server) handleDownloadCaption(c echo.Context) error {
	videoID, err := s.publicVideoID(c)
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
	return c.Stream(http.StatusOK, "text/vtt; charset=utf-8", rc)
}

// handleDeleteCaption removes a caption track from a video owned by the caller.
// Behind requireAuth. Non-owner/unknown video → 404. Idempotent.
func (s *Server) handleDeleteCaption(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	if err := s.videosvc.DeleteCaption(c.Request().Context(), userID, id, strings.TrimSpace(c.Param("lang"))); err != nil {
		return videoError(err)
	}
	return c.NoContent(http.StatusNoContent)
}
