package httpapi

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/observability"
)

// mediaGCRequest is the POST /admin/media/gc body. dry_run defaults to true so a
// caller must opt in to actual deletion (dry_run=false).
type mediaGCRequest struct {
	DryRun *bool `json:"dry_run"`
}

// mediaGCResponse reports a sweep outcome: what was scanned, the orphan keys,
// and how many were deleted (0 on a dry run).
type mediaGCResponse struct {
	DryRun  bool     `json:"dry_run"`
	Scanned int      `json:"scanned"`
	Orphans []string `json:"orphans"`
	Deleted int      `json:"deleted"`
}

// handleAdminMediaGC runs the media garbage collector: it lists stored objects
// under the known prefixes and deletes those with no database reference. Behind
// requireRole(admin). Defaults to a dry run (dry_run=false actually deletes).
// Audited either way; the result is never secret so it is returned in full.
func (s *Server) handleAdminMediaGC(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	var in mediaGCRequest
	// The body is optional; a malformed body still defaults to a safe dry run.
	_ = c.Bind(&in)
	dryRun := true
	if in.DryRun != nil {
		dryRun = *in.DryRun
	}

	res, err := s.mediagcsvc.Sweep(c.Request().Context(), dryRun)
	if err != nil {
		if errors.Is(err, mediagc.ErrListingUnsupported) {
			s.audit(c, observability.ActionMediaGC, observability.ResultFailure, userID.String(), "listing_unsupported")
			return echo.NewHTTPError(http.StatusServiceUnavailable, "storage backend does not support media garbage collection")
		}
		return err
	}
	mode := "dry_run"
	if !dryRun {
		mode = "delete"
	}
	s.audit(c, observability.ActionMediaGC, observability.ResultSuccess, userID.String(),
		fmt.Sprintf("mode=%s scanned=%d orphans=%d deleted=%d", mode, res.Scanned, len(res.Orphans), res.Deleted))

	orphans := res.Orphans
	if orphans == nil {
		orphans = []string{}
	}
	return c.JSON(http.StatusOK, mediaGCResponse{
		DryRun:  res.DryRun,
		Scanned: res.Scanned,
		Orphans: orphans,
		Deleted: res.Deleted,
	})
}
