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

// importVideoRequest is the POST /videos/:id/import body. resolver is optional
// (default auto): auto tries a direct media fetch and falls back to the yt-dlp
// platform extractor when enabled; direct forces a plain HTTP(S) fetch; ytdlp
// forces the extractor.
type importVideoRequest struct {
	URL      string `json:"url"`
	Resolver string `json:"resolver"`
}

func (r importVideoRequest) Validate() []FieldError {
	var errs []FieldError
	if strings.TrimSpace(r.URL) == "" {
		errs = append(errs, FieldError{Field: "url", Message: "is required"})
	}
	switch strings.TrimSpace(strings.ToLower(r.Resolver)) {
	case "", "auto", "direct", "ytdlp":
	default:
		errs = append(errs, FieldError{Field: "resolver", Message: "must be auto, direct, or ytdlp"})
	}
	return errs
}

// importJobView is the public projection of an async URL-import job. `error` is
// a safe, human-readable reason on failure (never a raw internal error or the
// source URL); it is omitted while the job has not failed. resolver is the
// concrete mechanism (direct|ytdlp; or auto until the worker resolves it) and
// stage is coarse progress (queued|resolving|downloading|processing) while
// running.
type importJobView struct {
	ID        string    `json:"id"`
	VideoID   string    `json:"video_id"`
	State     string    `json:"state"`
	Resolver  string    `json:"resolver"`
	Stage     string    `json:"stage,omitempty"`
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
		Resolver:  row.Resolver,
		Stage:     row.Stage,
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
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if !s.importsEnabled() {
		return &FeatureDisabledError{Feature: "imports"}
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	var in importVideoRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// Runtime gate on the platform-import path (import_http_enabled, W8): an
	// explicit resolver=ytdlp while the admin setting is off is 403
	// feature_disabled. The boot-capability case (yt-dlp not wired at all)
	// keeps its 503 below; resolver=auto simply never falls back to yt-dlp
	// while the setting is off (service-side gate).
	if strings.TrimSpace(strings.ToLower(in.Resolver)) == "ytdlp" && !s.importHTTPEnabled() {
		return &FeatureDisabledError{Feature: "import_http"}
	}
	ctx := c.Request().Context()
	// Authorize before any enqueue: a non-manager (or unknown video) gets 404 and
	// no import runs on their behalf. Owner OR editor collaborator (migration
	// 0097); the import pipeline attaches the original as the channel owner.
	if _, canManage := s.canManageVideo(ctx, userID, id); !canManage {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	job, err := s.importsvc.Enqueue(ctx, id, in.URL, in.Resolver)
	if err != nil {
		switch {
		case errors.Is(err, videoimport.ErrInvalidURL):
			return &ValidationError{Fields: []FieldError{{Field: "url", Message: "must be a public http(s) URL"}}}
		case errors.Is(err, videoimport.ErrInvalidResolver):
			return &ValidationError{Fields: []FieldError{{Field: "resolver", Message: "must be auto, direct, or ytdlp"}}}
		case errors.Is(err, videoimport.ErrResolverDisabled):
			// The requested resolver (e.g. ytdlp) is turned off on this instance.
			return echo.NewHTTPError(http.StatusServiceUnavailable, "platform-URL import is not enabled on this instance")
		default:
			return err
		}
	}
	return c.JSON(http.StatusAccepted, newImportJobResponse(job))
}

// handleGetVideoImport reports the status of a video's most recent URL import
// (owner only). Non-owner/unknown video → 404; a video that was never imported
// → 404.
func (s *Server) handleGetVideoImport(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "video not found")
	}
	ctx := c.Request().Context()
	if _, canManage := s.canManageVideo(ctx, userID, id); !canManage {
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
