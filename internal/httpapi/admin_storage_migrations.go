package httpapi

import (
	"context"
	"errors"
	"fmt"
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
	// PausedReason is why a paused campaign is paused, "" otherwise.
	PausedReason string `json:"paused_reason,omitempty"`
	// ResumeState is the phase a resume would return it to.
	ResumeState string `json:"resume_state,omitempty"`
	// Objects is the per-state breakdown, present on the detail view only.
	Objects map[string]int64 `json:"objects,omitempty"`
	// Failures is the per-CATEGORY breakdown, detail view only.
	//
	// It is the finding A34 left open: `objects_failed` counts only rows whose
	// whole five-attempt budget is spent, so a campaign every object of which
	// is being refused reports 0 failed and an empty last_error for as long as
	// the backoff ladder takes. The operator watches progress stall and is told
	// nothing. Splitting terminal from retrying is what makes the stall legible
	// while it is happening.
	Failures []storagemigration.Failure `json:"failures,omitempty"`
}

// storageMigrationPreviewResponse is what a move WOULD copy — the answer to the
// question a confirmation dialog has to ask before an operator agrees to move a
// whole media library. It creates nothing.
type storageMigrationPreviewResponse struct {
	DryRun     bool   `json:"dry_run"`
	SourceDesc string `json:"source_desc"`
	TargetDesc string `json:"target_desc"`
	Objects    int64  `json:"objects"`
	Bytes      int64  `json:"bytes"`
	BytesKnown bool   `json:"bytes_known"`
}

// startStorageMigrationRequest is the POST body. An empty body is a real start,
// which is what every existing client sends.
type startStorageMigrationRequest struct {
	// DryRun asks for the preview instead of the campaign. 200 with counts, no
	// row, nothing to cancel.
	DryRun bool `json:"dry_run"`
}

// abortStorageMigrationRequest is the POST body for the abort.
type abortStorageMigrationRequest struct {
	// CleanDestination removes the partial copies this campaign wrote to the
	// destination. Default false, which is the historical behaviour and the
	// safe one: the copies are byte-identical objects under identical keys and
	// are inert until some future campaign re-verifies them.
	CleanDestination bool `json:"clean_destination"`
	// Confirm must be the literal "PURGE" when CleanDestination is set. The
	// same typed confirmation the media-GC page uses for its own destructive
	// button, and for the same reason: this is the one thing in the product
	// that deletes from the DESTINATION, and it is being asked for on the way
	// OUT of a destructive operation, when an operator is already rattled.
	Confirm string `json:"confirm"`
}

// purgeConfirmation is the word an operator has to type. Shared with the media
// GC page's destructive sweep so there is one such word on this instance.
const purgeConfirmation = "PURGE"

// storageMigrationListResponse is the campaign history, newest first.
type storageMigrationListResponse struct {
	Migrations []storageMigrationResponse `json:"migrations"`
}

func storageMigrationView(c storagemigration.Campaign, objects map[string]int64, failures []storagemigration.Failure) storageMigrationResponse {
	return storageMigrationResponse{
		ID: c.ID.String(), State: c.State, SourceDesc: c.SourceDesc, TargetDesc: c.TargetDesc,
		ObjectsTotal: c.ObjectsTotal, ObjectsDone: c.ObjectsDone, ObjectsFailed: c.ObjectsFailed,
		LastError: c.LastError, PausedReason: c.PausedReason, ResumeState: c.ResumeState,
		ObservedCutoverAt: c.ObservedCutoverAt,
		CreatedAt:         c.CreatedAt, UpdatedAt: c.UpdatedAt, Objects: objects, Failures: failures,
	}
}

// storageMigrationControlError maps the control surface's sentinels onto the
// typed answers an operator page can act on. Everything that is "you cannot do
// that to a campaign in this state" is a 409 NAMING the state, never a bare
// conflict and never a 500: the commonest cause is a stale page, and the
// operator needs to know what changed under them.
func (s *Server) storageMigrationControlError(err error) error {
	var ill *storagemigration.IllegalTransitionError
	switch {
	case errors.Is(err, storagemigration.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "no such storage migration")
	case errors.Is(err, storagemigration.ErrNoTarget):
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"no storage migration target is configured; set STORAGE_MIGRATION_TARGET_* and restart")
	case errors.Is(err, storagemigration.ErrTargetWriteDenied):
		return echo.NewHTTPError(http.StatusConflict,
			"the migration target is not accepting writes, so there is nothing for a copy worker to do. Fix the target credential — the instance itself is unaffected and keeps serving — and copying resumes on its own within five minutes")
	case errors.Is(err, storagemigration.ErrCutoverNotObserved):
		return echo.NewHTTPError(http.StatusConflict,
			"this process is still serving from the migration's SOURCE, so there is no cutover to record. Cutover is an environment change: point STORAGE_* at the new store and STORAGE_MIGRATION_TARGET_* at the old one, restart, then record it here")
	case errors.Is(err, storagemigration.ErrObjectsUncopied):
		return echo.NewHTTPError(http.StatusConflict,
			"objects have not all been copied out of the source yet, so releasing it would delete the only copy of some of them. Nothing was deleted")
	case errors.As(err, &ill):
		return echo.NewHTTPError(http.StatusConflict,
			"this migration is "+ill.State+", so that is not something that can be done to it now — reload the page to see where it actually is")
	}
	return err
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
	var in startStorageMigrationRequest
	// A body is optional: every client before the preview existed sent none.
	_ = c.Bind(&in)
	if in.DryRun {
		return s.previewStorageMigration(c, userID)
	}
	camp, err := s.storagemigrationsvc.Start(c.Request().Context())
	switch {
	case errors.Is(err, storagemigration.ErrTargetWriteDenied):
		s.audit(c, observability.ActionStorageMigrationStart, observability.ResultFailure, userID.String(), "target_write_denied")
		return s.storageMigrationControlError(err)
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
	return c.JSON(http.StatusCreated, storageMigrationView(camp, nil, nil))
}

// previewStorageMigration answers what a campaign WOULD copy. 200, not 201: it
// creates nothing, so there is nothing to point a Location at and nothing to
// cancel if the operator decides against it.
func (s *Server) previewStorageMigration(c echo.Context, userID uuid.UUID) error {
	pv, err := s.storagemigrationsvc.Preview(c.Request().Context())
	switch {
	case errors.Is(err, storagemigration.ErrNoTarget),
		errors.Is(err, storagemigration.ErrIdentityUnknown):
		return s.storageMigrationControlError(err)
	case errors.Is(err, storagemigration.ErrListingUnsupported):
		return echo.NewHTTPError(http.StatusServiceUnavailable,
			"the configured media store cannot enumerate its objects, so it cannot be migrated")
	case err != nil:
		return err
	}
	// Counts only, per the audit contract — never a key, never a credential.
	s.audit(c, observability.ActionStorageMigrationPreview, observability.ResultSuccess, userID.String(),
		fmt.Sprintf("objects=%d bytes=%d", pv.Objects, pv.Bytes))
	return c.JSON(http.StatusOK, storageMigrationPreviewResponse{
		DryRun: true, SourceDesc: pv.SourceDesc, TargetDesc: pv.TargetDesc,
		Objects: pv.Objects, Bytes: pv.Bytes, BytesKnown: pv.BytesKnown,
	})
}

// handleAdminPauseStorageMigration parks a live campaign at an operator's
// request. Nothing is undone and nothing is deleted: the copy worker stops
// claiming objects and the ledger stands exactly where it is.
func (s *Server) handleAdminPauseStorageMigration(c echo.Context) error {
	return s.storageMigrationControl(c, observability.ActionStorageMigrationPause,
		func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error) {
			camp, err := s.storagemigrationsvc.Pause(ctx, id, storagemigration.PauseReasonOperator)
			return camp, "reason=operator", err
		})
}

// handleAdminResumeStorageMigration puts a paused campaign back in the phase it
// came out of.
func (s *Server) handleAdminResumeStorageMigration(c echo.Context) error {
	return s.storageMigrationControl(c, observability.ActionStorageMigrationResume,
		func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error) {
			camp, err := s.storagemigrationsvc.Resume(ctx, id)
			return camp, "", err
		})
}

// handleAdminAbortStorageMigration cancels a campaign and, when asked with the
// typed confirmation, removes the partial copies it wrote to the destination.
func (s *Server) handleAdminAbortStorageMigration(c echo.Context) error {
	var in abortStorageMigrationRequest
	_ = c.Bind(&in)
	if in.CleanDestination && in.Confirm != purgeConfirmation {
		return &ValidationError{Fields: []FieldError{{
			Field: "confirm",
			Message: "must be the word " + purgeConfirmation +
				" to remove the copies this migration wrote to the destination",
		}}}
	}
	return s.storageMigrationControl(c, observability.ActionStorageMigrationAbort,
		func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error) {
			camp, err := s.storagemigrationsvc.Abort(ctx, id, in.CleanDestination)
			return camp, fmt.Sprintf("clean_destination=%t", in.CleanDestination), err
		})
}

// handleAdminSwitchStorageMigration records a cutover the operator has ALREADY
// performed in the environment, instead of waiting up to a minute for the
// leader sweep to notice it — and says exactly why not when the swap has not
// taken effect in this process.
func (s *Server) handleAdminSwitchStorageMigration(c echo.Context) error {
	return s.storageMigrationControl(c, observability.ActionStorageMigrationSwitch,
		func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error) {
			camp, err := s.storagemigrationsvc.Switch(ctx, id)
			return camp, "", err
		})
}

// handleAdminReleaseStorageMigrationSource ends the grace window early and opens
// the delete-source phase. This is the act that makes a move irreversible.
func (s *Server) handleAdminReleaseStorageMigrationSource(c echo.Context) error {
	return s.storageMigrationControl(c, observability.ActionStorageMigrationRelease,
		func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error) {
			camp, err := s.storagemigrationsvc.Release(ctx, id)
			return camp, "", err
		})
}

// storageMigrationControl is the shape every control shares: admin principal,
// campaign id, the action, an audit row either way, and the campaign as it
// stands afterwards. Written once so a new control cannot arrive without an
// audit row, which is how one of them would eventually.
func (s *Server) storageMigrationControl(
	c echo.Context,
	action string,
	do func(ctx context.Context, id uuid.UUID) (storagemigration.Campaign, string, error),
) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "invalid storage migration id")
	}
	camp, detail, cerr := do(c.Request().Context(), id)
	if cerr != nil {
		s.audit(c, action, observability.ResultFailure, userID.String(),
			"campaign="+id.String()+" "+storageMigrationRefusal(cerr))
		return s.storageMigrationControlError(cerr)
	}
	reason := "campaign=" + camp.ID.String() + " state=" + camp.State
	if detail != "" {
		reason += " " + detail
	}
	s.audit(c, action, observability.ResultSuccess, userID.String(), reason)
	return c.JSON(http.StatusOK, storageMigrationView(camp, nil, nil))
}

// storageMigrationRefusal is the audit REASON for a refused control: a short
// category, never the error's own sentence, which can name a store.
func storageMigrationRefusal(err error) string {
	var ill *storagemigration.IllegalTransitionError
	switch {
	case errors.Is(err, storagemigration.ErrNotFound):
		return "not_found"
	case errors.Is(err, storagemigration.ErrTargetWriteDenied):
		return "target_write_denied"
	case errors.Is(err, storagemigration.ErrCutoverNotObserved):
		return "cutover_not_observed"
	case errors.Is(err, storagemigration.ErrObjectsUncopied):
		return "objects_uncopied"
	case errors.As(err, &ill):
		return "illegal_transition state=" + ill.State
	}
	return "error"
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
	return c.JSON(http.StatusOK, storageMigrationView(camp, nil, nil))
}

// handleAdminListStorageMigrations returns the campaign history, newest first.
func (s *Server) handleAdminListStorageMigrations(c echo.Context) error {
	camps, err := s.storagemigrationsvc.List(c.Request().Context(), 50)
	if err != nil {
		return err
	}
	out := storageMigrationListResponse{Migrations: make([]storageMigrationResponse, 0, len(camps))}
	for _, camp := range camps {
		out.Migrations = append(out.Migrations, storageMigrationView(camp, nil, nil))
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
	camp, objects, failures, err := s.storagemigrationsvc.Get(c.Request().Context(), id)
	switch {
	case errors.Is(err, storagemigration.ErrNotFound):
		return echo.NewHTTPError(http.StatusNotFound, "no such storage migration")
	case err != nil:
		return err
	}
	return c.JSON(http.StatusOK, storageMigrationView(camp, objects, failures))
}
