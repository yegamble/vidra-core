package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storagemigration"
)

// storageMigrationResponse is one campaign as an operator sees it. It carries
// the two store IDENTITY strings (endpoint/bucket, or a filesystem path) because
// "which store is this moving to" is the whole question an operator is asking —
// but never a credential, which is what authorises a store rather than names it.
type storageMigrationResponse struct {
	ID                string     `json:"id"`
	State             string     `json:"state"`
	SourceDesc        string     `json:"source_desc"`
	TargetDesc        string     `json:"target_desc"`
	ObjectsTotal      int64      `json:"objects_total"`
	ObjectsDone       int64      `json:"objects_done"`
	ObjectsFailed     int64      `json:"objects_failed"`
	LastError         string     `json:"last_error"`
	ObservedCutoverAt *time.Time `json:"observed_cutover_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
	// Objects is the per-state breakdown, present on the detail view only.
	Objects map[string]int64 `json:"objects,omitempty"`
}

// storageMigrationListResponse is the campaign history, newest first.
type storageMigrationListResponse struct {
	Migrations []storageMigrationResponse `json:"migrations"`
}

func storageMigrationView(c storagemigration.Campaign, objects map[string]int64) storageMigrationResponse {
	return storageMigrationResponse{
		ID: c.ID.String(), State: c.State, SourceDesc: c.SourceDesc, TargetDesc: c.TargetDesc,
		ObjectsTotal: c.ObjectsTotal, ObjectsDone: c.ObjectsDone, ObjectsFailed: c.ObjectsFailed,
		LastError: c.LastError, ObservedCutoverAt: c.ObservedCutoverAt,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt, Objects: objects,
	}
}

// handleAdminStartStorageMigration opens a campaign that copies every object in
// the configured media store into the configured migration target, verifying
// each copy by reading it back. Behind requireRole(admin); audited.
//
// It does NOT change what this instance serves from. Cutover stays an explicit,
// operator-driven environment change precisely because it is the step that
// cannot be undone by cancelling a job — see docs/operations.md, "Moving the
// media store".
func (s *Server) handleAdminStartStorageMigration(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	camp, err := s.storagemigrationsvc.Start(c.Request().Context())
	switch {
	case errors.Is(err, storagemigration.ErrNoTarget):
		s.audit(c, observability.ActionStorageMigrationStart, observability.ResultFailure, userID.String(), "no_target_configured")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "no storage migration target is configured; set STORAGE_MIGRATION_TARGET_* and restart")
	case errors.Is(err, storagemigration.ErrAlreadyActive):
		s.audit(c, observability.ActionStorageMigrationStart, observability.ResultFailure, userID.String(), "already_active")
		return echo.NewHTTPError(http.StatusConflict, "a storage migration is already in progress")
	case errors.Is(err, storagemigration.ErrListingUnsupported):
		s.audit(c, observability.ActionStorageMigrationStart, observability.ResultFailure, userID.String(), "listing_unsupported")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "the configured media store cannot enumerate its objects, so it cannot be migrated")
	case errors.Is(err, storagemigration.ErrIdentityUnknown):
		s.audit(c, observability.ActionStorageMigrationStart, observability.ResultFailure, userID.String(), "identity_unknown")
		return echo.NewHTTPError(http.StatusServiceUnavailable, "a configured storage backend does not report which store it is, so a migration cannot be tracked safely")
	case err != nil:
		return err
	}
	s.audit(c, observability.ActionStorageMigrationStart, observability.ResultSuccess, userID.String(),
		"campaign="+camp.ID.String()+" source="+camp.SourceDesc+" target="+camp.TargetDesc)
	return c.JSON(http.StatusCreated, storageMigrationView(camp, nil))
}

// handleAdminCancelStorageMigration stops a live campaign. Objects already
// copied stay in the target: they are byte-identical copies under identical
// keys, so they are inert until some future campaign re-verifies them, and
// deleting them would be a destructive action taken on the way OUT of a
// destructive operation.
func (s *Server) handleAdminCancelStorageMigration(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid storage migration id")
	}
	camp, err := s.storagemigrationsvc.Cancel(c.Request().Context(), id)
	switch {
	case errors.Is(err, storagemigration.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "no such storage migration")
	case err != nil:
		return err
	}
	s.audit(c, observability.ActionStorageMigrationCancel, observability.ResultSuccess, userID.String(),
		"campaign="+camp.ID.String()+" state="+camp.State)
	return c.JSON(http.StatusOK, storageMigrationView(camp, nil))
}

// handleAdminListStorageMigrations returns the campaign history, newest first.
func (s *Server) handleAdminListStorageMigrations(c echo.Context) error {
	camps, err := s.storagemigrationsvc.List(c.Request().Context(), 50)
	if err != nil {
		return err
	}
	out := storageMigrationListResponse{Migrations: make([]storageMigrationResponse, 0, len(camps))}
	for _, camp := range camps {
		out.Migrations = append(out.Migrations, storageMigrationView(camp, nil))
	}
	return c.JSON(http.StatusOK, out)
}

// handleAdminGetStorageMigration returns one campaign with its per-state object
// breakdown — the numbers that say whether it is safe to cut over yet.
func (s *Server) handleAdminGetStorageMigration(c echo.Context) error {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid storage migration id")
	}
	camp, objects, err := s.storagemigrationsvc.Get(c.Request().Context(), id)
	switch {
	case errors.Is(err, storagemigration.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "no such storage migration")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, storageMigrationView(camp, objects))
}
