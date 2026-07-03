package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/captionjob"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// autoCaptionRequest is the optional POST /videos/:id/captions/auto body. When
// language is omitted the server uses WHISPER_DEFAULT_LANGUAGE.
type autoCaptionRequest struct {
	Language string `json:"language"`
}

// captionJobView is the public projection of an auto-caption job. `error` is a
// safe, human-readable reason on failure (never a raw internal error or the
// Whisper endpoint URL); it is omitted while the job has not failed.
type captionJobView struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	Language  string    `json:"language"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// captionJobResponse wraps a caption job (POST request + GET status).
type captionJobResponse struct {
	CaptionJob captionJobView `json:"caption_job"`
}

func newCaptionJobResponse(row sqlcgen.CaptionJob) captionJobResponse {
	return captionJobResponse{CaptionJob: captionJobView{
		ID:        row.ID.String(),
		VideoID:   row.VideoID.String(),
		Language:  row.Language,
		State:     row.State,
		Error:     row.Error,
		Attempts:  int(row.Attempts),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}}
}

// handleRequestAutoCaption enqueues an auto-caption (Whisper) job for a video
// (owner only) and returns 202 with the queued job. Ownership is checked before
// any enqueue so a non-owner or unknown video is 404 and nothing runs on their
// behalf. When auto-captioning is disabled it is 503; a bad language tag is 422;
// while a job is already in flight for the video it is 409. The audio-extraction,
// transcription, and caption upsert run in the background worker; watch progress
// via GET /videos/:id/captions/auto.
func (s *Server) handleRequestAutoCaption(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	// Feature gate first: a disabled instance answers 503 without disclosing
	// whether the video exists.
	if !s.captionjobsvc.Enabled() {
		return echo.NewHTTPError(http.StatusServiceUnavailable, "auto-captioning is not enabled on this server")
	}
	var in autoCaptionRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Authorize before any enqueue: a non-owner (or unknown video) gets 404.
	if v, gerr := s.videosvc.GetByID(ctx, id); gerr != nil || v.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	job, err := s.captionjobsvc.Enqueue(ctx, id, strings.TrimSpace(in.Language))
	if err != nil {
		switch {
		case errors.Is(err, captionjob.ErrDisabled):
			return echo.NewHTTPError(http.StatusServiceUnavailable, "auto-captioning is not enabled on this server")
		case errors.Is(err, captionjob.ErrInvalidLanguage):
			return &ValidationError{Fields: []FieldError{{Field: "language", Message: "must be a valid BCP-47 language tag"}}}
		case errors.Is(err, captionjob.ErrJobActive):
			return echo.NewHTTPError(http.StatusConflict, "an auto-caption job is already in progress for this video")
		default:
			return err
		}
	}
	return c.JSON(http.StatusAccepted, newCaptionJobResponse(job))
}

// handleGetAutoCaption reports the status of a video's most recent auto-caption
// job (owner only). Non-owner/unknown video → 404; a video that never requested
// auto-captioning → 404.
func (s *Server) handleGetAutoCaption(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ctx := c.Request().Context()
	if v, gerr := s.videosvc.GetByID(ctx, id); gerr != nil || v.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	job, err := s.captionjobsvc.LatestForVideo(ctx, id)
	if err != nil {
		if errors.Is(err, captionjob.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "no auto-caption job found")
		}
		return err
	}
	return c.JSON(http.StatusOK, newCaptionJobResponse(job))
}
