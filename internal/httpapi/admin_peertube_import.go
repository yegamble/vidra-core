package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/peertubeimport"
)

// peerTubeImportProvider is the seam the admin PeerTube-import handlers depend
// on. *peertubeimport.Service satisfies it; faked in tests.
type peerTubeImportProvider interface {
	Configured() bool
	CreateRun(ctx context.Context, mode string, policy peertubeimport.ConflictPolicy, sourceAuthoritative bool, adminID uuid.UUID) (peertubeimport.Run, error)
	GetRun(ctx context.Context, id uuid.UUID) (peertubeimport.Run, error)
	ListRuns(ctx context.Context, limit, offset int32) ([]peertubeimport.Run, error)
}

// peertubeImportLaunchRequest is the POST body. mode is "dry_run" (report only,
// writes nothing) or "run" (perform the import). conflict_policy is optional
// (server default when empty). The source connection is taken from SERVER CONFIG
// only — the browser NEVER sends a DSN or credential.
type peertubeImportLaunchRequest struct {
	Mode           string `json:"mode"`
	ConflictPolicy string `json:"conflict_policy"`
	// SourceAuthoritative is a SECOND, orthogonal axis, not another
	// conflict_policy value: the policy resolves natural-key collisions at insert
	// time, this says whether a re-run may update rows the import already owns
	// when the two sides have diverged. Absent = false = gap-fill, which is what
	// every import has always done.
	SourceAuthoritative bool `json:"source_authoritative"`
}

// peertubeImportListResponse wraps the recent-runs list for the admin UI.
type peertubeImportListResponse struct {
	Runs []peertubeimport.Run `json:"runs"`
}

// handleLaunchPeerTubeImport launches an import run (admin-only). Returns 202 +
// the run so the UI can poll it. 503 when import is not configured on the server,
// 409 when a run is already active, 400 for a bad mode/policy. The source
// connection is server-config-only; nothing about it is accepted from the request.
func (s *Server) handleLaunchPeerTubeImport(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in peertubeImportLaunchRequest
	if err := c.Bind(&in); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid request body")
	}
	policy, err := peertubeimport.ParseConflictPolicy(in.ConflictPolicy)
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid conflict_policy (want skip|rename|merge|fail)")
	}
	run, err := s.peertubeimportsvc.CreateRun(c.Request().Context(), in.Mode, policy, in.SourceAuthoritative, userID)
	switch {
	case errors.Is(err, peertubeimport.ErrNotConfigured):
		return echo.NewHTTPError(http.StatusServiceUnavailable, "PeerTube import is not configured on this instance")
	case errors.Is(err, peertubeimport.ErrInvalidMode):
		return echo.NewHTTPError(http.StatusBadRequest, "mode must be dry_run or run")
	case errors.Is(err, peertubeimport.ErrBusy):
		return echo.NewHTTPError(http.StatusConflict, "an import run is already in progress")
	case err != nil:
		return err
	}
	s.audit(c, observability.ActionPeerTubeImportStart, observability.ResultSuccess, userID.String(),
		"mode="+in.Mode+" policy="+policy.String()+
			" source_authoritative="+strconv.FormatBool(in.SourceAuthoritative))
	return c.JSON(http.StatusAccepted, run)
}

// handleListPeerTubeImports returns recent import runs newest-first (admin-only),
// so the UI can render history + the active run's live progress.
func (s *Server) handleListPeerTubeImports(c echo.Context) error {
	runs, err := s.peertubeimportsvc.ListRuns(c.Request().Context(), 20, 0)
	if err != nil {
		return err
	}
	if runs == nil {
		runs = []peertubeimport.Run{}
	}
	return c.JSON(http.StatusOK, peertubeImportListResponse{Runs: runs})
}

// handleGetPeerTubeImport returns one run by id (admin-only) — the progress-poll
// endpoint. 404 when the id is unknown.
func (s *Server) handleGetPeerTubeImport(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid run id")
	}
	run, err := s.peertubeimportsvc.GetRun(c.Request().Context(), id)
	if errors.Is(err, peertubeimport.ErrRunNotFound) {
		return echo.NewHTTPError(http.StatusNotFound, "run not found")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, run)
}
