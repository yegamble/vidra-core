package storagemigration

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/jobstatus"
	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The controls an operator drives a campaign with.
//
// A34 found the surface was start, cancel and read — and vidra-user rendered
// only the read, so both writes were API-only. A move is the most destructive
// thing an instance does to itself, and "start it and hope" is not an operator
// interface. What is here is the rest of the verbs, each of them a refusal as
// much as an action:
//
//	Preview   what a move WOULD copy, before a campaign row exists at all
//	Pause     stop claiming objects, remembering the phase to come back to
//	Resume    put it back in that phase
//	Abort     cancel, optionally removing the partial copies from the
//	          destination — the thing nothing in the product did
//	Switch    record the cutover NOW rather than waiting for the sweep to
//	          notice, and say exactly why not when the swap has not happened
//	Release   end the grace window early and start deleting the old store
//
// TRANSITIONS. Every one of these is guarded in SQL on the states it is legal
// from, and the guard's rowcount — not a read-then-write — is what decides.
// That is what makes them safe against the sweep worker, which is changing the
// same row on its own clock:
//
//	pause    enumerating | copying | synced          -> paused
//	resume   paused                                  -> resume_state
//	abort    enumerating | copying | synced | paused -> aborting -> cancelled
//	cancel   anything not terminal                   -> cancelled
//	switch   copying | synced                        -> cutover   (topology must be SWAPPED)
//	release  cutover                                 -> deleting_source (every object accounted for)
//
// Anything else is ErrIllegalTransition, which the api renders as a 409 naming
// the state the campaign is actually in. Never a 500, and never a silent no-op:
// an operator who pressed a button on a stale page must be told what changed.

// Campaign states added by migration 0145.
const (
	// StatePaused: a live campaign that is not claiming objects. The phase it
	// came out of is in resume_state, and paused_reason says why.
	StatePaused = "paused"
	// StateAborting: cancelled AND removing the partial copies it wrote to the
	// destination, batch by batch, before it reaches StateCancelled.
	StateAborting = "aborting"
)

// Pause reasons. Short and FIXED: the column is rendered to an operator and
// carried into the operational projection.
const (
	// PauseReasonTargetWriteDenied: the migration target refused a write probe.
	// Set by the workers, cleared by them when the probe recovers — an operator
	// never has to resume a campaign whose cause fixed itself.
	PauseReasonTargetWriteDenied = "target_write_denied"
	// PauseReasonOperator: an admin pressed pause. Only an admin clears it.
	PauseReasonOperator = "operator"
)

// pausedByTargetMessage is what a campaign paused by the write probe says on the
// admin surface. It is a sentence rather than a code because it is the only
// place an operator learns that the instance is fine and the MOVE is not.
const pausedByTargetMessage = "the migration target refused a write probe, so copying is paused; this instance is otherwise unaffected and keeps serving from the store that holds authority. Copying resumes on its own once the target accepts writes again"

// Errors the control surface adds.
var (
	// ErrTargetWriteDenied means the migration target will not accept a write
	// right now. It refuses a START (walking an operator into a campaign that
	// would pause on its first tick is worse than telling them) and a RESUME
	// (which would pause again within the minute).
	ErrTargetWriteDenied = errors.New("storagemigration: the migration target is not accepting writes")
	// ErrCutoverNotObserved means Switch was asked for while this process is
	// still serving from the campaign's SOURCE. Cutover is an environment swap
	// plus a restart; this endpoint records one that has ALREADY happened, and
	// cannot perform one.
	ErrCutoverNotObserved = errors.New("storagemigration: this process is still serving from the migration source")
	// ErrObjectsUncopied means Release was asked for while objects are still
	// only in the source. Deleting then would delete the only copy.
	ErrObjectsUncopied = errors.New("storagemigration: objects have not been copied out of the source yet")
)

// IllegalTransitionError is a control applied to a campaign in a state that does
// not permit it. It names the state so the api can tell an operator what
// happened rather than answering a bare conflict.
type IllegalTransitionError struct {
	Action string
	State  string
}

func (e *IllegalTransitionError) Error() string {
	return fmt.Sprintf("storagemigration: cannot %s a campaign in state %q", e.Action, e.State)
}

// illegal builds the error for a control whose guarded UPDATE matched no row,
// re-reading the campaign so the message names the state it is ACTUALLY in
// rather than the one the caller's page showed.
func (s *Service) illegal(ctx context.Context, id uuid.UUID, action string) error {
	row, err := s.repo.GetStorageMigration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return &IllegalTransitionError{Action: action, State: row.State}
}

// Preview is what a move WOULD do, computed without creating anything.
type Preview struct {
	SourceDesc string `json:"source_desc"`
	TargetDesc string `json:"target_desc"`
	Objects    int64  `json:"objects"`
	Bytes      int64  `json:"bytes"`
	// BytesKnown is false when the source cannot report sizes cheaply. The
	// count is still exact; the byte total is simply absent rather than a
	// number an operator would take for a fact.
	BytesKnown bool `json:"bytes_known"`
}

// Preview enumerates the source and reports what a campaign would copy, WITHOUT
// creating a campaign row, writing a ledger entry, or touching the target.
//
// It exists because "start a storage migration" is a button whose consequence
// an operator cannot otherwise see. The two identity strings say which store to
// which, and the count and byte total say how long it will take and whether the
// destination has room. Everything a confirmation dialog needs, and nothing
// that has to be undone if the answer is no.
func (s *Service) Preview(ctx context.Context) (Preview, error) {
	if s.target == nil {
		return Preview{}, ErrNoTarget
	}
	source, dest := storage.Describe(s.primary), storage.Describe(s.target)
	if source == "" || dest == "" {
		return Preview{}, ErrIdentityUnknown
	}
	out := Preview{SourceDesc: source, TargetDesc: dest}
	if sized, ok := s.primary.(storage.SizedRootLister); ok {
		objs, err := sized.ListAllObjects(ctx)
		if err != nil {
			return Preview{}, err
		}
		out.BytesKnown = true
		out.Objects = int64(len(objs))
		for _, o := range objs {
			out.Bytes += o.Size
		}
		return out, nil
	}
	lister, ok := s.primary.(storage.RootLister)
	if !ok {
		return Preview{}, ErrListingUnsupported
	}
	keys, err := lister.ListAllKeys(ctx)
	if err != nil {
		return Preview{}, err
	}
	out.Objects = int64(len(keys))
	return out, nil
}

// Pause parks a live campaign. Idempotent by construction: the guarded UPDATE
// matches only the three pre-cutover phases, so pausing an already-paused
// campaign matches nothing and is reported as the illegal transition it is.
func (s *Service) Pause(ctx context.Context, id uuid.UUID, reason string) (Campaign, error) {
	if reason != PauseReasonOperator && reason != PauseReasonTargetWriteDenied {
		return Campaign{}, fmt.Errorf("storagemigration: unknown pause reason %q", reason)
	}
	note := ""
	if reason == PauseReasonTargetWriteDenied {
		note = pausedByTargetMessage
	}
	n, err := s.repo.PauseStorageMigration(ctx, sqlcgen.PauseStorageMigrationParams{
		ID: id, PausedReason: reason, LastError: note,
	})
	if err != nil {
		return Campaign{}, err
	}
	if n == 0 {
		return Campaign{}, s.illegal(ctx, id, "pause")
	}
	s.logger.WarnContext(ctx, "storage migration paused", "campaign", id.String(), "reason", reason)
	return s.reread(ctx, id)
}

// Resume puts a paused campaign back in the phase it came out of.
//
// It refuses while the target is still write-denied, rather than resuming a
// campaign the next tick would pause again — a button that appears to work and
// silently undoes itself within the minute is worse than a refusal that says
// why.
func (s *Service) Resume(ctx context.Context, id uuid.UUID) (Campaign, error) {
	if ok, _ := s.targetWritable(); !ok {
		return Campaign{}, ErrTargetWriteDenied
	}
	n, err := s.repo.ResumeStorageMigration(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if n == 0 {
		return Campaign{}, s.illegal(ctx, id, "resume")
	}
	s.logger.InfoContext(ctx, "storage migration resumed", "campaign", id.String())
	return s.reread(ctx, id)
}

// Abort cancels a campaign, optionally removing the partial copies it wrote to
// the destination.
//
// WITHOUT cleanup it is exactly Cancel, and the copies stay — byte-identical
// objects under identical keys, inert until some future campaign re-verifies
// them. WITH cleanup the campaign enters StateAborting and the leader-gated
// sweep removes them batch by batch, then finishes at StateCancelled.
//
// Cleanup is a separate flag, and the api puts a typed confirmation in front of
// it, because it is the one destructive act taken on the way OUT of a
// destructive operation. The old default — leave them — was the safe answer to
// a question nobody could answer for the operator; it is still the default, and
// now there is a way to say otherwise.
func (s *Service) Abort(ctx context.Context, id uuid.UUID, cleanDestination bool) (Campaign, error) {
	if !cleanDestination {
		return s.Cancel(ctx, id)
	}
	n, err := s.repo.AbortStorageMigrationWithCleanup(ctx, sqlcgen.AbortStorageMigrationWithCleanupParams{
		ID: id,
		LastError: "aborting: removing the partial copies this migration wrote to the destination. " +
			"The source is untouched throughout",
	})
	if err != nil {
		return Campaign{}, err
	}
	if n == 0 {
		return Campaign{}, s.illegal(ctx, id, "abort with destination clean-up")
	}
	s.logger.WarnContext(ctx, "storage migration aborted; the destination's partial copies will be removed",
		"campaign", id.String())
	return s.reread(ctx, id)
}

// Switch records the cutover NOW instead of waiting for the leader sweep to
// notice it, and refuses — with a reason — when the swap has not happened.
//
// It cannot PERFORM a cutover. Which store this process serves from is decided
// by the environment it was started with, and the campaign reads that off its
// two handles rather than being told (see the package comment). What an operator
// gains is the difference between waiting up to a minute and hoping, and being
// told at once either "recorded" or "this process is still serving from the
// source — the swap did not take, or only half of it did".
func (s *Service) Switch(ctx context.Context, id uuid.UUID) (Campaign, error) {
	row, err := s.repo.GetStorageMigration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, err
	}
	if row.State != StateCopying && row.State != StateSynced {
		return Campaign{}, &IllegalTransitionError{Action: "record a cutover for", State: row.State}
	}
	if s.topologyFor(row) != topologySwapped {
		return Campaign{}, ErrCutoverNotObserved
	}
	if err := s.repo.MarkStorageMigrationCutover(ctx, id); err != nil {
		return Campaign{}, err
	}
	s.logger.WarnContext(ctx, "storage migration cutover recorded by an operator",
		"campaign", id.String(), "serving", row.TargetDesc, "source_deleted_after", s.grace.String())
	return s.reread(ctx, id)
}

// Release ends the grace window early and opens the delete-source phase.
//
// The grace window is an UNDO window: while it runs, reverting the environment
// swap is a restart rather than a restore. Ending it is therefore an operator
// decision, and until now the only way to make it was to edit
// STORAGE_MIGRATION_GRACE_HOURS and restart — a config change to express a
// one-off intent, on a running instance, mid-migration.
//
// The one precondition that CANNOT be waived is re-checked here rather than
// trusted from the counters: every object must be accounted for. A campaign
// whose environment was swapped before the copy finished has objects the new
// store does not hold, and this is the request that would delete their only
// remaining copy.
func (s *Service) Release(ctx context.Context, id uuid.UUID) (Campaign, error) {
	row, err := s.repo.GetStorageMigration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, err
	}
	if row.State != StateCutover {
		return Campaign{}, &IllegalTransitionError{Action: "release the source of", State: row.State}
	}
	left, err := s.repo.CountUncopiedStorageMigrationObjects(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if left > 0 {
		return Campaign{}, ErrObjectsUncopied
	}
	n, err := s.repo.ReleaseStorageMigrationSource(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	if n == 0 {
		return Campaign{}, s.illegal(ctx, id, "release the source of")
	}
	s.logger.WarnContext(ctx, "storage migration source released by an operator; deleting the old store's copies",
		"campaign", id.String(), "source", row.SourceDesc)
	return s.reread(ctx, id)
}

// Failure is one failure CATEGORY and how many objects are in it.
type Failure struct {
	// Category is one of the short fixed strings this package writes; never a
	// key, an endpoint or a raw backend error.
	Category string `json:"category"`
	// Terminal is objects that have spent their whole attempt budget.
	Terminal int64 `json:"terminal"`
	// Retrying is objects that failed and are still being retried. It is the
	// half that was invisible: a row only reaches 'failed' after five attempts,
	// so a campaign whose every object is refused reports objects_failed 0 and
	// last_error "" for as long as the backoff takes.
	Retrying int64 `json:"retrying"`
}

// failures returns the per-category breakdown for one campaign.
func (s *Service) failures(ctx context.Context, id uuid.UUID) ([]Failure, error) {
	rows, err := s.repo.CountStorageMigrationObjectFailuresByCategory(ctx, id)
	if err != nil {
		return nil, err
	}
	out := make([]Failure, 0, len(rows))
	for _, r := range rows {
		out = append(out, Failure{Category: r.Category, Terminal: r.Terminal, Retrying: r.Retrying})
	}
	return out, nil
}

// reread returns the campaign as it stands after a control changed it.
func (s *Service) reread(ctx context.Context, id uuid.UUID) (Campaign, error) {
	row, err := s.repo.GetStorageMigration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	}
	if err != nil {
		return Campaign{}, err
	}
	return campaignFrom(row), nil
}

// targetWritable reports whether the migration target is currently accepting
// writes, and the class when it is not.
//
// An unwired hook answers TRUE, the same rule storage.WriteHealth follows for an
// unprobed monitor: "no evidence" is not evidence of a refusal, and this gate
// must never be the reason a move stops on an instance that has simply not been
// asked.
func (s *Service) targetWritable() (bool, storage.ErrorClass) {
	if s.targetWrite == nil {
		return true, ""
	}
	return s.targetWrite.Writable()
}

// reconcileTargetWritability is the automatic half of the pause. It runs at the
// top of every sweep, and it is what makes the boot change safe: a target that
// went write-denied pauses the campaign instead of taking the instance down,
// and a target that recovered un-pauses it without anybody pressing anything.
//
// It returns true when the caller should STOP — the campaign is paused and there
// is nothing for this tick to do.
//
// An operator pause is never lifted here. The two reasons are deliberately
// different columns' worth of authority: the probe may clear only what the probe
// set, or an admin who paused a move to take a maintenance window would find it
// running again five minutes later.
func (s *Service) reconcileTargetWritability(ctx context.Context, camp sqlcgen.StorageMigration) bool {
	// Its CALLER guarantees the forward topology — see SweepOnce, where that is
	// the whole reason this lives inside the switch. Here the remaining guard is
	// on state: StateAborting DELETES from the destination, its deletes are
	// already best-effort per object, and it cannot be paused anyway (the
	// guarded UPDATE would match nothing while this function logged that it had
	// paused something and told the sweep to stop).
	switch camp.State {
	case StateEnumerating, StateCopying, StateSynced, StatePaused:
	default:
		return false
	}
	writable, class := s.targetWritable()
	switch {
	case !writable && camp.State == StatePaused:
		return true // already parked for this, or for an operator; nothing to do
	case !writable:
		if _, err := s.repo.PauseStorageMigration(ctx, sqlcgen.PauseStorageMigrationParams{
			ID: camp.ID, PausedReason: PauseReasonTargetWriteDenied, LastError: pausedByTargetMessage,
		}); err != nil {
			s.logger.WarnContext(ctx, "storage migration could not be paused after the target refused writes",
				"campaign", camp.ID.String(), "class", string(class), "error", err.Error())
			return true
		}
		s.logger.WarnContext(ctx, "storage migration paused: the target store refused a write probe",
			"campaign", camp.ID.String(), "class", string(class),
			"consequence", "copying stops until the target accepts writes again; this instance keeps serving reads from the store that holds authority, and nothing is deleted")
		return true
	case camp.State == StatePaused && camp.PausedReason == PauseReasonTargetWriteDenied:
		if _, err := s.repo.ResumeStorageMigration(ctx, camp.ID); err != nil {
			s.logger.WarnContext(ctx, "storage migration could not be resumed after the target accepted writes again",
				"campaign", camp.ID.String(), "error", err.Error())
			return true
		}
		s.logger.InfoContext(ctx, "storage migration resumed: the target store accepts writes again",
			"campaign", camp.ID.String())
		return true // one action per tick; the next one copies
	case camp.State == StatePaused:
		return true // paused by an operator, and only an operator resumes it
	}
	return false
}

// abortCleanupBatch removes one batch of the copies this campaign wrote to the
// DESTINATION, then ends the campaign when none are left.
//
// It is the mirror of deleteSourceBatch, and every guard is mirrored with it.
// The identity assertion is repeated even though the caller established the
// topology, because this is a destructive step and it costs one string
// comparison: in the FORWARD topology s.target IS the destination, and running
// this with the handles the other way round would empty the source.
//
// Only 'verified' rows are removed. A row that never reached that state was
// never confirmed to be on the destination, and a delete issued for a key whose
// copy might not be ours is the one mistake this package exists to prevent.
func (s *Service) abortCleanupBatch(ctx context.Context, camp sqlcgen.StorageMigration) error {
	if got := storage.Describe(s.target); got != camp.TargetDesc {
		return fmt.Errorf("storagemigration: refusing to clean up: the migration target handle is %q, not this campaign's destination %q", got, camp.TargetDesc)
	}
	keys, err := s.repo.ListDestinationCopiesToRemove(ctx, sqlcgen.ListDestinationCopiesToRemoveParams{
		CampaignID: camp.ID, Limit: deleteBatch,
	})
	if err != nil {
		return err
	}
	removed := 0
	for _, key := range keys {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if derr := s.target.Delete(ctx, key); derr != nil {
			// Best effort per object, exactly as the source delete is: one
			// unreachable object must not strand the abort. The row stays
			// 'verified' so the next pass retries it.
			s.logger.WarnContext(ctx, "storage migration could not remove a destination copy",
				"campaign", camp.ID.String(), "object_key", jobstatus.RedactDetail(key), "error", jobstatus.RedactError(derr))
			continue
		}
		if err := s.repo.DeleteStorageMigrationObject(ctx, key); err != nil {
			return err
		}
		removed++
	}
	if removed > 0 {
		s.logger.InfoContext(ctx, "storage migration removed destination copies",
			"campaign", camp.ID.String(), "removed", removed)
	}
	// Finish ONLY when the batch was short AND every key in it went. A short
	// batch alone is not enough: some of its deletes may have failed, those rows
	// stay 'verified' for the next pass to retry, and cancelling on top of them
	// would mark the campaign over with objects still on the destination — the
	// exact silence this clean-up exists to end. deleteSourceBatch has the same
	// property at the other end of a successful move: a permanently unreachable
	// object keeps the phase open, visibly, rather than being quietly forgotten.
	//
	// The campaign's own counters are deliberately NOT refreshed here. They are
	// computed from the ledger, and these rows are being deleted out of it, so
	// refreshing would walk objects_done back to zero and erase the record of
	// what this campaign actually copied before it was aborted.
	if len(keys) == int(deleteBatch) || removed < len(keys) {
		return nil // more to do, or some failed and will be retried
	}
	if _, err := s.repo.CancelStorageMigration(ctx, camp.ID); err != nil {
		return err
	}
	s.logger.WarnContext(ctx, "storage migration aborted: the destination's partial copies have been removed",
		"campaign", camp.ID.String(), "source", camp.SourceDesc)
	return nil
}

// stampWorker records which process is driving this campaign, so the operational
// run can be tied to a worker as well as to the request that started it. It is
// advisory: a campaign that could not be stamped is not a campaign that should
// stop.
func (s *Service) stampWorker(ctx context.Context, id uuid.UUID) {
	if s.workerID == "" {
		return
	}
	if err := s.repo.SetStorageMigrationWorker(ctx, sqlcgen.SetStorageMigrationWorkerParams{
		ID: id, WorkerID: s.workerID,
	}); err != nil {
		s.logger.WarnContext(ctx, "storage migration could not record which worker is driving it",
			"campaign", id.String(), "error", err.Error())
	}
}

// correlationFor reads the request identity bound to ctx, so the campaign a
// campaign-start creates names the admin request that asked for it.
func correlationFor(ctx context.Context) (requestID, correlationID string) {
	c := observability.CorrelationFromContext(ctx)
	return c.RequestID, c.CorrelationID
}
