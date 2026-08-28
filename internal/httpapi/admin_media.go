package httpapi

import (
	"errors"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/mediagc"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
)

// mediaGCRequest is the POST /admin/media/gc body. dry_run defaults to true so a
// caller must opt in to actual deletion (dry_run=false).
type mediaGCRequest struct {
	DryRun *bool `json:"dry_run"`
}

// mediaGCResponse reports a sweep outcome: what was scanned, the orphan keys,
// how many were deleted (0 on a dry run), and — when a requested delete deleted
// nothing — which safety rail stopped it. It mirrors mediagc.Result field for
// field; the two and api/openapi.yaml move together.
type mediaGCResponse struct {
	DryRun             bool     `json:"dry_run"`
	Mode               string   `json:"mode"`
	Scanned            int      `json:"scanned"`
	Orphans            []string `json:"orphans"`
	Deleted            int      `json:"deleted"`
	OrphanPercent      int      `json:"orphan_percent"`
	BreakerTripped     bool     `json:"breaker_tripped"`
	BucketOwnership    string   `json:"bucket_ownership"`
	ForcedDryRun       bool     `json:"forced_dry_run"`
	ForcedDryRunReason string   `json:"forced_dry_run_reason,omitempty"`
}

// adoptBucketResponse reports the ownership state after an adoption, and the key
// the marker was written to so an operator can go and look at it.
type adoptBucketResponse struct {
	BucketOwnership string `json:"bucket_ownership"`
	MarkerKey       string `json:"marker_key"`
}

// handleAdminMediaGC runs the media garbage collector: it lists stored objects
// under the known prefixes and deletes those with no database reference. Behind
// requireRole(admin). Defaults to a dry run (dry_run=false actually deletes).
// Audited either way; the result is never secret so it is returned in full.
//
// A requested delete can still delete nothing — an unowned/conflicting bucket or
// a tripped orphan-ratio breaker downgrades the sweep to a dry run rather than
// failing it, because the orphan list is the answer the operator needs in order
// to decide what to do about it.
func (s *Server) handleAdminMediaGC(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
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
	s.audit(c, observability.ActionMediaGC, observability.ResultSuccess, userID.String(), res.Summary())

	orphans := res.Orphans
	if orphans == nil {
		orphans = []string{}
	}
	return c.JSON(http.StatusOK, mediaGCResponse{
		DryRun:             res.DryRun,
		Mode:               res.Mode,
		Scanned:            res.Scanned,
		Orphans:            orphans,
		Deleted:            res.Deleted,
		OrphanPercent:      res.OrphanPercent,
		BreakerTripped:     res.BreakerTripped,
		BucketOwnership:    res.BucketOwnership,
		ForcedDryRun:       res.ForcedDryRun,
		ForcedDryRunReason: res.ForcedDryRunReason,
	})
}

// handleAdminAdoptBucket claims the configured object store for this install by
// writing the instance identity into the ownership marker, which re-enables
// destructive sweeps against it.
//
// It is a separate, explicit, audited action rather than something boot decides,
// because the only bucket that needs adopting is one that already holds objects
// whose owner boot could not establish — an operator saying "yes, that media is
// mine" is the missing piece of evidence, and nothing else can supply it.
func (s *Server) handleAdminAdoptBucket(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.mediagcsvc.AdoptBucket(c.Request().Context()); err != nil {
		switch {
		case errors.Is(err, mediagc.ErrAdoptNotApplicable):
			s.audit(c, observability.ActionMediaGCAdoptBucket, observability.ResultFailure, userID.String(), "not_applicable")
			return echo.NewHTTPError(http.StatusConflict, "this instance stores media on local disk, so there is no bucket to adopt")
		case errors.Is(err, mediagc.ErrNoInstanceIdentity):
			s.audit(c, observability.ActionMediaGCAdoptBucket, observability.ResultFailure, userID.String(), "no_instance_identity")
			return echo.NewHTTPError(http.StatusServiceUnavailable, "this instance has no identity to stamp on the bucket; run the migrations and restart")
		default:
			// The underlying error can name the endpoint and bucket; the reason
			// stays a category so the audit log carries no store detail.
			s.audit(c, observability.ActionMediaGCAdoptBucket, observability.ResultFailure, userID.String(), "marker_write_failed")
			return echo.NewHTTPError(http.StatusBadGateway, "the ownership marker could not be written to the object store")
		}
	}
	ownership := string(s.mediagcsvc.Ownership())
	s.audit(c, observability.ActionMediaGCAdoptBucket, observability.ResultSuccess, userID.String(), "ownership="+ownership)
	return c.JSON(http.StatusOK, adoptBucketResponse{
		BucketOwnership: ownership,
		MarkerKey:       storage.OwnerMarkerKey,
	})
}
