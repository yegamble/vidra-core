package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/videoimport"
)

// importVideoRequest is the POST /videos/:id/import body.
type importVideoRequest struct {
	URL string `json:"url"`
}

func (r importVideoRequest) Validate() []FieldError {
	if strings.TrimSpace(r.URL) == "" {
		return []FieldError{{Field: "url", Message: "is required"}}
	}
	return nil
}

// importJobView is the public projection of an async URL-import job. `error` is
// a safe, human-readable reason on failure (never a raw internal error or the
// source URL); it is omitted while the job has not failed.
type importJobView struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	State     string    `json:"state"`
	Error     string    `json:"error,omitempty"`
	Attempts  int       `json:"attempts"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// importJobResponse wraps an import job (POST enqueue + GET status).
type importJobResponse struct {
	ImportJob importJobView `json:"import_job"`
}

func newImportJobResponse(row sqlcgen.ImportJob) importJobResponse {
	return importJobResponse{ImportJob: importJobView{
		ID:        row.ID.String(),
		VideoID:   row.VideoID.String(),
		State:     row.State,
		Error:     row.Error,
		Attempts:  int(row.Attempts),
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}}
}

// handleImportVideoFile enqueues an asynchronous URL import of a video's
// original file (owner only) and returns 202 with the queued job — it no longer
// blocks the request on the fetch. Ownership is checked before any enqueue so a
// non-owner (or unknown video) is 404 and no job is created on their behalf. The
// URL is validated up front (scheme/host, literal non-public addresses); a
// missing/malformed/non-public URL is 422. The actual SSRF-guarded fetch,
// size/quota enforcement, and AttachOriginal → Process pipeline run in the
// background worker; watch progress via GET /videos/:id/import.
func (s *Server) handleImportVideoFile(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in importVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	// Authorize before any enqueue: a non-owner (or unknown video) gets 404 and no
	// import runs on their behalf.
	if v, gerr := s.videosvc.GetByID(ctx, id); gerr != nil || v.OwnerID != userID {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	job, err := s.importsvc.Enqueue(ctx, id, in.URL)
	if err != nil {
		if errors.Is(err, videoimport.ErrInvalidURL) {
			return &ValidationError{Fields: []FieldError{{Field: "url", Message: "must be a public http(s) URL"}}}
		}
		return err
	}
	return c.JSON(http.StatusAccepted, newImportJobResponse(job))
}

// handleGetVideoImport reports the status of a video's most recent URL import
// (owner only). Non-owner/unknown video → 404; a video that was never imported
// → 404.
func (s *Server) handleGetVideoImport(c echo.Context) error {
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
	job, err := s.importsvc.LatestForVideo(ctx, id)
	if err != nil {
		if errors.Is(err, videoimport.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "no import found")
		}
		return err
	}
	return c.JSON(http.StatusOK, newImportJobResponse(job))
}
