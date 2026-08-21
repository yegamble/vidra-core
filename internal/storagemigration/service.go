// Package storagemigration moves a media library from one storage backend to
// another — local disk to S3, bucket to bucket — as a resumable, integrity-
// verified background job (phase-2 storage, work items 4 and 5).
//
// # Shape
//
// A CAMPAIGN (storage_migrations) is the operator-visible unit: two stores, a
// state, and counters. Under it, one row per OBJECT
// (storage_migration_objects) is the per-object location record of
// docs/productionization/interfaces.md section 3 — 'verified' and
// 'source_deleted' mean "the target holds these bytes", which is what dual-read
// and the delete-source pass both key on.
//
// Two workers drive it, split the way the worker-role doctrine requires:
//
//	CopyOnce   UNLEADERED. It claims object rows with FOR UPDATE SKIP LOCKED and
//	           renews a lease while it streams, so every instance can run it and
//	           more instances simply copy faster.
//	SweepOnce  LEADER-GATED. It enumerates the source, reconciles, watches for
//	           cutover, and eventually deletes source objects. Each of those acts
//	           on whatever it finds store-wide; two of them at once would fight.
//
// # Object keys never change
//
// A backend-to-backend move copies each object to the SAME key. media_ipfs_pins
// is primary-keyed on storage keys (migration 0071) and video_files.storage_key
// is an opaque relative key by the 0008 doctrine — both keep pointing at the
// right bytes with no ledger migration of their own. Anything that rewrote keys
// here would have to migrate those tables in the same transaction, which is
// exactly the coupling the opaque-key doctrine exists to avoid.
//
// # The cutover contract (load-bearing)
//
// The process cannot ask an operator what phase they are in, so it reads it off
// the two backends it is holding, compared against the identity strings the
// campaign recorded (storage.Describe):
//
//	primary == source_desc && target == target_desc  → FORWARD: copy source→target
//	primary == target_desc && target == source_desc  → SWAPPED: cutover happened
//	anything else                                    → refuse to act, loudly
//
// So cutover is: the operator swaps BOTH env sets (STORAGE_* now names the new
// store, STORAGE_MIGRATION_TARGET_* names the old one) and restarts. That swap
// is what tells this package the new store is live, AND it is what hands the
// delete-source pass a handle on the store it is about to empty. Nothing here
// ever constructs a backend from a persisted description — a process only ever
// uses the two handles it was configured with, and the descriptions are used
// solely to decide whether those handles are the ones this campaign means.
package storagemigration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/lease"
	"github.com/vidra/vidra-core/internal/mediahash"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Campaign states. They are the database CHECK vocabulary; keep the two in sync.
const (
	// StateEnumerating: listing the source store to build the object ledger.
	StateEnumerating = "enumerating"
	// StateCopying: objects are being claimed and copied.
	StateCopying = "copying"
	// StateSynced: every enumerated object is in the target and a delta pass
	// found nothing new. This is the state to cut over from.
	StateSynced = "synced"
	// StateCutover: the api is serving from the target; the grace clock runs.
	StateCutover = "cutover"
	// StateDeletingSource: removing the old store's copies, batch by batch.
	StateDeletingSource = "deleting_source"
	// StateDone / StateCancelled / StateFailed are terminal.
	StateDone      = "done"
	StateCancelled = "cancelled"
	StateFailed    = "failed"
)

// Object states.
const (
	ObjectPending       = "pending"
	ObjectCopying       = "copying"
	ObjectVerified      = "verified"
	ObjectFailed        = "failed"
	ObjectSourceDeleted = "source_deleted"
)

// Errors callers branch on.
var (
	// ErrNoTarget means STORAGE_MIGRATION_TARGET_* is not configured, so there is
	// nowhere to migrate to and no campaign can be started.
	ErrNoTarget = errors.New("storagemigration: no storage migration target is configured")
	// ErrAlreadyActive means a campaign is already running. Exactly one at a time
	// — two would race over the same objects and disagree about which store is
	// authoritative.
	ErrAlreadyActive = errors.New("storagemigration: a storage migration is already in progress")
	// ErrNotFound means the requested campaign does not exist.
	ErrNotFound = errors.New("storagemigration: no such storage migration")
	// ErrListingUnsupported means a backend cannot enumerate itself, so its whole
	// contents cannot be established and nothing may be migrated off it.
	ErrListingUnsupported = errors.New("storagemigration: storage backend cannot list its objects")
	// ErrIdentityUnknown means a configured backend will not state which store it
	// is (no storage.Describer). A migration whose stores cannot be told apart
	// must not run at all.
	ErrIdentityUnknown = errors.New("storagemigration: a configured storage backend does not report its identity")
)

// Failure categories recorded in storage_migration_objects.last_error. They are
// deliberately SHORT and FIXED: this column is projected into the admin jobs
// overview, which must never carry a storage key, an endpoint, or a raw backend
// error. The detail goes to the system log, which is where detail belongs.
const (
	failSourceMissing  = "source object missing"
	failVerification   = "verification failed: the object read back from the target is not the object that was written"
	failRecordedHash   = "the source object does not match the sha256 recorded for this file"
	failTransientCopy  = "copy failed; retrying"
	failAttemptsSpent  = "copy failed repeatedly; giving up on this object"
	failKeyUnsupported = "the target store will not accept this object key"
)

// maxAttempts is how many times one object is retried before it dead-letters.
// It matches the transcode queue's budget: enough that a restarting object store
// does not lose objects, few enough that a genuinely unreadable one stops
// consuming a worker slot every tick.
const maxAttempts = 5

// enumerateChunk is how many keys are offered to the database per statement.
// A store with 200,000 objects must not become one array parameter.
const enumerateChunk = 1000

// deleteBatch is how many source objects one delete pass removes. Small enough
// that a leadership change mid-pass costs at most this many re-listed rows.
const deleteBatch = 200

// Repository is the data access this package needs. *sqlcgen.Queries satisfies
// it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateStorageMigration(ctx context.Context, arg sqlcgen.CreateStorageMigrationParams) (sqlcgen.StorageMigration, error)
	GetStorageMigration(ctx context.Context, id uuid.UUID) (sqlcgen.StorageMigration, error)
	GetActiveStorageMigration(ctx context.Context) (sqlcgen.StorageMigration, error)
	ListStorageMigrations(ctx context.Context, limit int32) ([]sqlcgen.StorageMigration, error)
	SetStorageMigrationState(ctx context.Context, arg sqlcgen.SetStorageMigrationStateParams) error
	SetStorageMigrationError(ctx context.Context, arg sqlcgen.SetStorageMigrationErrorParams) error
	MarkStorageMigrationCutover(ctx context.Context, id uuid.UUID) error
	CancelStorageMigration(ctx context.Context, id uuid.UUID) (int64, error)
	RefreshStorageMigrationCounters(ctx context.Context, id uuid.UUID) error
	UpsertStorageMigrationObjects(ctx context.Context, arg sqlcgen.UpsertStorageMigrationObjectsParams) (int64, error)
	ClaimDueStorageMigrationObjects(ctx context.Context, limit int32) ([]sqlcgen.ClaimDueStorageMigrationObjectsRow, error)
	RenewStorageMigrationObjectLease(ctx context.Context, objectKey string) error
	MarkStorageMigrationObjectVerified(ctx context.Context, arg sqlcgen.MarkStorageMigrationObjectVerifiedParams) error
	RescheduleStorageMigrationObject(ctx context.Context, arg sqlcgen.RescheduleStorageMigrationObjectParams) error
	FailStorageMigrationObject(ctx context.Context, arg sqlcgen.FailStorageMigrationObjectParams) error
	MarkStorageMigrationObjectSourceDeleted(ctx context.Context, objectKey string) error
	ListVerifiedStorageMigrationObjects(ctx context.Context, arg sqlcgen.ListVerifiedStorageMigrationObjectsParams) ([]string, error)
	CountUncopiedStorageMigrationObjects(ctx context.Context, campaignID uuid.UUID) (int64, error)
	CountUnfinishedStorageMigrationObjects(ctx context.Context, campaignID uuid.UUID) (int64, error)
	CountStorageMigrationObjectsByState(ctx context.Context, campaignID uuid.UUID) ([]sqlcgen.CountStorageMigrationObjectsByStateRow, error)
	GetVideoFileSHA256ByStorageKey(ctx context.Context, storageKey string) (string, error)
}

// Config is the non-wiring knobs.
type Config struct {
	// Grace is how long after cutover the source keeps its copies. It is the undo
	// window: while it runs, reverting the environment swap is a restart rather
	// than a restore.
	Grace time.Duration
	// Logger receives every state change. A nil logger falls back to the default.
	Logger *slog.Logger
}

// Service runs storage migration campaigns over a pair of backends.
//
// primary is the backend this process is CONFIGURED to serve from (STORAGE_*)
// and target is the second one (STORAGE_MIGRATION_TARGET_*). Which of them is
// the campaign's source and which its destination is decided per campaign by
// comparing identities — see the package comment.
type Service struct {
	repo    Repository
	primary storage.Backend
	target  storage.Backend
	grace   time.Duration
	logger  *slog.Logger
}

// NewService builds the service. target may be nil, which is the feature-off
// configuration: every pass is an immediate no-op and Start answers ErrNoTarget,
// but the read/cancel surface still works so an operator can always see and stop
// a campaign left behind by an earlier configuration.
func NewService(repo Repository, primary, target storage.Backend, cfg Config) *Service {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	grace := cfg.Grace
	if grace < 0 {
		grace = 0
	}
	return &Service{repo: repo, primary: primary, target: target, grace: grace, logger: logger}
}

// Enabled reports whether a migration target is configured.
func (s *Service) Enabled() bool { return s.target != nil }

// Campaign is the operator-facing projection of one migration.
type Campaign struct {
	ID                uuid.UUID  `json:"id"`
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
}

func campaignFrom(row sqlcgen.StorageMigration) Campaign {
	c := Campaign{
		ID: row.ID, State: row.State, SourceDesc: row.SourceDesc, TargetDesc: row.TargetDesc,
		ObjectsTotal: row.ObjectsTotal, ObjectsDone: row.ObjectsDone, ObjectsFailed: row.ObjectsFailed,
		LastError: row.LastError, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ObservedCutoverAt.Valid {
		t := row.ObservedCutoverAt.Time
		c.ObservedCutoverAt = &t
	}
	return c
}

// Start opens a campaign from the configured primary store to the configured
// target store. The two identities are recorded now, and every later copy or
// delete is refused unless the process's handles still match them.
func (s *Service) Start(ctx context.Context) (Campaign, error) {
	if s.target == nil {
		return Campaign{}, ErrNoTarget
	}
	source, dest := storage.Describe(s.primary), storage.Describe(s.target)
	if source == "" || dest == "" {
		return Campaign{}, ErrIdentityUnknown
	}
	if source == dest {
		// Config validation catches the configured case; this catches the one it
		// cannot see (two differently-spelled settings resolving to one store).
		return Campaign{}, fmt.Errorf("storagemigration: the source and target are the same store (%s)", source)
	}
	if _, ok := s.primary.(storage.RootLister); !ok {
		return Campaign{}, ErrListingUnsupported
	}
	if _, err := s.repo.GetActiveStorageMigration(ctx); err == nil {
		return Campaign{}, ErrAlreadyActive
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, err
	}
	row, err := s.repo.CreateStorageMigration(ctx, sqlcgen.CreateStorageMigrationParams{
		SourceDesc: source, TargetDesc: dest,
	})
	if err != nil {
		// The single-active partial unique index is the real guard; the check
		// above is only there to give a clean 409 in the common case.
		if isUniqueViolation(err) {
			return Campaign{}, ErrAlreadyActive
		}
		return Campaign{}, err
	}
	s.logger.InfoContext(ctx, "storage migration started",
		"campaign", row.ID.String(), "source", source, "target", dest,
		"grace", s.grace.String())
	return campaignFrom(row), nil
}

// Cancel stops a live campaign. Object rows stop being claimed immediately (the
// claim query joins the campaign state), and nothing is undone: objects already
// copied stay in the target, where they are harmless — they are byte-identical
// copies under identical keys, and the next campaign re-verifies them anyway.
func (s *Service) Cancel(ctx context.Context, id uuid.UUID) (Campaign, error) {
	if _, err := s.repo.GetStorageMigration(ctx, id); errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, ErrNotFound
	} else if err != nil {
		return Campaign{}, err
	}
	if _, err := s.repo.CancelStorageMigration(ctx, id); err != nil {
		return Campaign{}, err
	}
	row, err := s.repo.GetStorageMigration(ctx, id)
	if err != nil {
		return Campaign{}, err
	}
	s.logger.WarnContext(ctx, "storage migration cancelled", "campaign", id.String(), "state", row.State)
	return campaignFrom(row), nil
}

// Get returns one campaign and its per-state object breakdown.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (Campaign, map[string]int64, error) {
	row, err := s.repo.GetStorageMigration(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return Campaign{}, nil, ErrNotFound
	}
	if err != nil {
		return Campaign{}, nil, err
	}
	counts, err := s.objectCounts(ctx, id)
	if err != nil {
		return Campaign{}, nil, err
	}
	return campaignFrom(row), counts, nil
}

// List returns the most recent campaigns, newest first.
func (s *Service) List(ctx context.Context, limit int32) ([]Campaign, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.repo.ListStorageMigrations(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Campaign, 0, len(rows))
	for _, row := range rows {
		out = append(out, campaignFrom(row))
	}
	return out, nil
}

// objectCounts returns every object state present for a campaign, with the
// states that have no rows spelled out as zero so a caller never has to know
// the vocabulary.
func (s *Service) objectCounts(ctx context.Context, id uuid.UUID) (map[string]int64, error) {
	rows, err := s.repo.CountStorageMigrationObjectsByState(ctx, id)
	if err != nil {
		return nil, err
	}
	counts := map[string]int64{
		ObjectPending: 0, ObjectCopying: 0, ObjectVerified: 0,
		ObjectFailed: 0, ObjectSourceDeleted: 0,
	}
	for _, r := range rows {
		counts[r.State] = r.Count
	}
	return counts, nil
}

// topology is which way round this process's two backends sit relative to a
// campaign. It is the whole cutover mechanism (see the package comment).
type topology int

const (
	// topologyMismatch: at least one handle is not a store this campaign knows.
	// Nothing may be copied or deleted — the process cannot tell which store is
	// which, and both possible guesses are destructive.
	topologyMismatch topology = iota
	// topologyForward: primary is the source, target is the destination.
	topologyForward
	// topologySwapped: the operator swapped both environment sets and restarted,
	// so the api now serves from the destination and holds the old source.
	topologySwapped
)

func (s *Service) topologyFor(c sqlcgen.StorageMigration) topology {
	primary, second := storage.Describe(s.primary), storage.Describe(s.target)
	if primary == "" || second == "" {
		return topologyMismatch
	}
	switch {
	case primary == c.SourceDesc && second == c.TargetDesc:
		return topologyForward
	case primary == c.TargetDesc && second == c.SourceDesc:
		return topologySwapped
	default:
		return topologyMismatch
	}
}

// SweepOnce is the LEADER-GATED pass: enumerate, reconcile, notice cutover, and
// eventually delete the source. It is the unit the sweep worker ticks.
//
// Every branch is safe to interrupt at any point: state advances only after the
// work it describes is durable, and every step is idempotent, so a leadership
// change mid-pass costs at most one repeated pass.
func (s *Service) SweepOnce(ctx context.Context) error {
	if s.target == nil {
		return nil
	}
	camp, err := s.repo.GetActiveStorageMigration(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}

	switch s.topologyFor(camp) {
	case topologyForward:
		return s.sweepForward(ctx, camp)
	case topologySwapped:
		return s.sweepSwapped(ctx, camp)
	default:
		// Neither reading holds. Acting now would mean guessing which handle is
		// the source, and one of the two guesses deletes the library.
		s.logger.ErrorContext(ctx, "storage migration stalled: the configured stores are not this campaign's stores",
			"campaign", camp.ID.String(), "campaign_source", camp.SourceDesc, "campaign_target", camp.TargetDesc,
			"configured_primary", storage.Describe(s.primary), "configured_target", storage.Describe(s.target))
		return s.note(ctx, camp.ID, "the configured storage backends are not this migration's source and target; restore the environment or cancel this migration")
	}
}

// sweepForward runs the pre-cutover phases, where the primary IS the source.
func (s *Service) sweepForward(ctx context.Context, camp sqlcgen.StorageMigration) error {
	switch camp.State {
	case StateEnumerating:
		added, err := s.enumerate(ctx, camp.ID)
		if err != nil {
			return err
		}
		if err := s.repo.RefreshStorageMigrationCounters(ctx, camp.ID); err != nil {
			return err
		}
		s.logger.InfoContext(ctx, "storage migration enumerated the source store",
			"campaign", camp.ID.String(), "objects", added)
		return s.setState(ctx, camp.ID, StateCopying, "")

	case StateCopying, StateSynced:
		return s.reconcile(ctx, camp)

	case StateCutover, StateDeletingSource:
		// The campaign believes cutover happened, but this process is still
		// configured the original way round — the operator reverted the swap, or
		// only reverted half of it. Deleting anything now would delete from the
		// LIVE store.
		s.logger.ErrorContext(ctx, "storage migration is past cutover but the api is serving from the migration SOURCE again; nothing will be deleted",
			"campaign", camp.ID.String(), "state", camp.State)
		return s.note(ctx, camp.ID, "the api is serving from this migration's source again, so the cutover was undone; finish the environment swap or cancel this migration")
	}
	return nil
}

// reconcile is the steady-state pass while copying: when nothing is left to
// copy, re-enumerate and decide whether the stores are level.
//
// The delta pass is what makes "synced" mean anything on a live instance:
// uploads keep landing in the source while the copy runs, so "the ledger is
// drained" is not the same claim as "the stores match". Re-offering every key
// and counting how many are NEW is the cheapest honest version of that check.
func (s *Service) reconcile(ctx context.Context, camp sqlcgen.StorageMigration) error {
	left, err := s.repo.CountUncopiedStorageMigrationObjects(ctx, camp.ID)
	if err != nil {
		return err
	}
	if err := s.repo.RefreshStorageMigrationCounters(ctx, camp.ID); err != nil {
		return err
	}
	if left > 0 {
		return nil // the copy worker still has work; nothing to decide yet
	}
	added, err := s.enumerate(ctx, camp.ID)
	if err != nil {
		return err
	}
	if err := s.repo.RefreshStorageMigrationCounters(ctx, camp.ID); err != nil {
		return err
	}
	if added > 0 {
		if camp.State != StateCopying {
			s.logger.InfoContext(ctx, "storage migration found new objects in the source; copying again",
				"campaign", camp.ID.String(), "objects", added)
			return s.setState(ctx, camp.ID, StateCopying, "")
		}
		return nil
	}
	if camp.State != StateSynced {
		s.logger.InfoContext(ctx, "storage migration is synced: every object in the source is verified in the target",
			"campaign", camp.ID.String(), "objects", camp.ObjectsTotal)
		return s.setState(ctx, camp.ID, StateSynced, "")
	}
	return nil
}

// sweepSwapped runs the post-cutover phases, where the primary is the
// DESTINATION and the migration target handle points at the OLD source.
func (s *Service) sweepSwapped(ctx context.Context, camp sqlcgen.StorageMigration) error {
	switch camp.State {
	case StateEnumerating:
		s.logger.ErrorContext(ctx, "storage migration: the media store was swapped before anything had been enumerated",
			"campaign", camp.ID.String())
		return s.note(ctx, camp.ID, "the storage environment was swapped before this migration had enumerated anything; restore the previous values or cancel this migration")

	case StateCopying, StateSynced:
		if err := s.repo.MarkStorageMigrationCutover(ctx, camp.ID); err != nil {
			return err
		}
		s.logger.WarnContext(ctx, "storage migration cutover observed: the api is now serving from the migration target",
			"campaign", camp.ID.String(), "serving", camp.TargetDesc,
			"source_deleted_after", s.grace.String())
		return nil

	case StateCutover:
		return s.maybeBeginDeletingSource(ctx, camp)

	case StateDeletingSource:
		return s.deleteSourceBatch(ctx, camp)
	}
	return nil
}

// maybeBeginDeletingSource opens the delete phase once the grace window has
// elapsed AND every object is accounted for. Both conditions are refusals, not
// errors: a campaign that waits forever costs storage, and one that deletes
// early costs the library.
func (s *Service) maybeBeginDeletingSource(ctx context.Context, camp sqlcgen.StorageMigration) error {
	if !camp.ObservedCutoverAt.Valid {
		// Should be impossible (the cutover write sets it), and if it ever is not,
		// "no clock" must mean "no deletion".
		return s.note(ctx, camp.ID, "the cutover time was not recorded, so the grace period cannot be measured; cancel and restart this migration")
	}
	if elapsed := time.Since(camp.ObservedCutoverAt.Time); elapsed < s.grace {
		return nil
	}
	left, err := s.repo.CountUncopiedStorageMigrationObjects(ctx, camp.ID)
	if err != nil {
		return err
	}
	if left > 0 {
		s.logger.ErrorContext(ctx, "storage migration will not delete the source: objects were never copied out of it",
			"campaign", camp.ID.String(), "uncopied", left)
		return s.note(ctx, camp.ID, "the environment was swapped before every object had been copied, so the old store still holds objects the new one does not; nothing will be deleted")
	}
	s.logger.WarnContext(ctx, "storage migration grace period elapsed; deleting the source copies",
		"campaign", camp.ID.String(), "source", camp.SourceDesc)
	return s.setState(ctx, camp.ID, StateDeletingSource, "")
}

// deleteSourceBatch removes one batch of source objects, then ends the campaign
// when nothing is left.
//
// The identity assertion is repeated here even though the caller already
// established the topology, because this is the only irreversible step in the
// package and it costs one string comparison.
func (s *Service) deleteSourceBatch(ctx context.Context, camp sqlcgen.StorageMigration) error {
	if got := storage.Describe(s.target); got != camp.SourceDesc {
		return fmt.Errorf("storagemigration: refusing to delete: the migration target handle is %q, not this campaign's source %q", got, camp.SourceDesc)
	}
	keys, err := s.repo.ListVerifiedStorageMigrationObjects(ctx, sqlcgen.ListVerifiedStorageMigrationObjectsParams{
		CampaignID: camp.ID, Limit: deleteBatch,
	})
	if err != nil {
		return err
	}
	deleted := 0
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if derr := s.target.Delete(ctx, key); derr != nil {
			// Best-effort per object: one unreachable object must not stall the
			// rest, and the row stays 'verified' so the next pass retries it.
			s.logger.WarnContext(ctx, "storage migration could not delete a source object",
				"campaign", camp.ID.String(), "object_key", key, "error", derr.Error())
			continue
		}
		if err := s.repo.MarkStorageMigrationObjectSourceDeleted(ctx, key); err != nil {
			return err
		}
		deleted++
	}
	if err := s.repo.RefreshStorageMigrationCounters(ctx, camp.ID); err != nil {
		return err
	}
	if deleted > 0 {
		s.logger.InfoContext(ctx, "storage migration deleted source objects",
			"campaign", camp.ID.String(), "deleted", deleted)
	}
	left, err := s.repo.CountUnfinishedStorageMigrationObjects(ctx, camp.ID)
	if err != nil {
		return err
	}
	if left > 0 {
		return nil
	}
	s.logger.InfoContext(ctx, "storage migration complete", "campaign", camp.ID.String(),
		"now_serving", camp.TargetDesc, "failed_objects", camp.ObjectsFailed)
	return s.setState(ctx, camp.ID, StateDone, "")
}

// enumerate lists the SOURCE store (which in the forward topology is the
// primary) and records every key it does not already know, returning how many
// were new. It is the same call for the first pass and every delta pass — the
// ON CONFLICT DO NOTHING upsert is what makes a re-enumeration cheap.
func (s *Service) enumerate(ctx context.Context, campaignID uuid.UUID) (int64, error) {
	lister, ok := s.primary.(storage.RootLister)
	if !ok {
		return 0, ErrListingUnsupported
	}
	keys, err := lister.ListAllKeys(ctx)
	if err != nil {
		return 0, err
	}
	var added int64
	for start := 0; start < len(keys); start += enumerateChunk {
		end := min(start+enumerateChunk, len(keys))
		n, uerr := s.repo.UpsertStorageMigrationObjects(ctx, sqlcgen.UpsertStorageMigrationObjectsParams{
			CampaignID: campaignID, ObjectKeys: keys[start:end],
		})
		if uerr != nil {
			return added, uerr
		}
		added += n
	}
	return added, nil
}

// CopyOnce claims up to batch due objects and copies each one, returning how
// many it claimed. It is the unit the UNLEADERED copy worker ticks: every
// instance may run it concurrently, because the claim is FOR UPDATE SKIP LOCKED
// and the lease is renewed while each object streams.
//
// A per-object failure is recorded on that object and never aborts the batch:
// one unreadable object must not stall a library. Only a database error, which
// makes the whole pass meaningless, is returned.
func (s *Service) CopyOnce(ctx context.Context, batch int32) (int, error) {
	if s.target == nil {
		return 0, nil
	}
	camp, err := s.repo.GetActiveStorageMigration(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if camp.State != StateCopying && camp.State != StateSynced {
		return 0, nil
	}
	if s.topologyFor(camp) != topologyForward {
		// Copying with the handles the other way round would write the store being
		// decommissioned back over the live one. The sweep pass reports why.
		return 0, nil
	}
	if batch <= 0 {
		batch = 1
	}
	rows, err := s.repo.ClaimDueStorageMigrationObjects(ctx, batch)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if cerr := ctx.Err(); cerr != nil {
			return len(rows), cerr
		}
		s.copyOne(ctx, row)
	}
	if len(rows) > 0 {
		if err := s.repo.RefreshStorageMigrationCounters(ctx, camp.ID); err != nil {
			return len(rows), err
		}
	}
	return len(rows), nil
}

// copyOne copies and verifies a single claimed object, then writes its terminal
// (or retry) state. It never returns an error: everything it can learn is
// recorded on the row, which is what the operator and the next pass read.
func (s *Service) copyOne(ctx context.Context, row sqlcgen.ClaimDueStorageMigrationObjectsRow) {
	// A whole video original over a slow link can outlive one 30-minute lease;
	// without renewal the sweep would hand it to a second worker mid-copy and
	// both would write the same target key.
	stop := lease.Keep(ctx, lease.DefaultInterval, "storage_migration_object", func(c context.Context) error {
		return s.repo.RenewStorageMigrationObjectLease(c, row.ObjectKey)
	})
	defer stop()

	sum, size, err := s.copyAndVerify(ctx, row.ObjectKey)
	if err == nil {
		if uerr := s.repo.MarkStorageMigrationObjectVerified(ctx, sqlcgen.MarkStorageMigrationObjectVerifiedParams{
			ObjectKey: row.ObjectKey, Sha256: sum, ByteSize: size,
		}); uerr != nil {
			s.logger.WarnContext(ctx, "storage migration could not record a verified object",
				"object_key", row.ObjectKey, "error", uerr.Error())
		}
		return
	}

	var term *terminalError
	if errors.As(err, &term) {
		s.logger.ErrorContext(ctx, "storage migration gave up on an object",
			"object_key", row.ObjectKey, "reason", term.category, "error", term.Error())
		s.fail(ctx, row.ObjectKey, term.category)
		return
	}

	// Transient: a timeout, a 5xx, a store restarting. Retry with backoff until
	// the attempt budget is spent, then dead-letter through the normal path.
	s.logger.WarnContext(ctx, "storage migration could not copy an object; will retry",
		"object_key", row.ObjectKey, "attempts", row.Attempts+1, "error", err.Error())
	if row.Attempts+1 >= maxAttempts {
		s.fail(ctx, row.ObjectKey, failAttemptsSpent)
		return
	}
	if rerr := s.repo.RescheduleStorageMigrationObject(ctx, sqlcgen.RescheduleStorageMigrationObjectParams{
		ObjectKey:     row.ObjectKey,
		NextAttemptAt: time.Now().Add(backoff(int(row.Attempts) + 1)),
		LastError:     failTransientCopy,
	}); rerr != nil {
		s.logger.WarnContext(ctx, "storage migration could not reschedule an object",
			"object_key", row.ObjectKey, "error", rerr.Error())
	}
}

func (s *Service) fail(ctx context.Context, key, category string) {
	if err := s.repo.FailStorageMigrationObject(ctx, sqlcgen.FailStorageMigrationObjectParams{
		ObjectKey: key, LastError: category,
	}); err != nil {
		s.logger.WarnContext(ctx, "storage migration could not dead-letter an object",
			"object_key", key, "error", err.Error())
	}
}

// terminalError is a failure no retry can fix, carrying the short category that
// goes in the database.
type terminalError struct {
	category string
	err      error
}

func (e *terminalError) Error() string {
	if e.err == nil {
		return e.category
	}
	return e.category + ": " + e.err.Error()
}
func (e *terminalError) Unwrap() error { return e.err }

func terminal(category string, err error) error { return &terminalError{category: category, err: err} }

// copyAndVerify streams one object from the source into the target and then
// PROVES it arrived, returning the verified digest and byte count.
//
// The proof is deliberately end-to-end. PutSizedHashed's digest is taken from
// the bytes as they were READ FROM THE SOURCE, which establishes only that the
// source was read correctly — it says nothing about what the target actually
// stored. So the object is opened again FROM THE TARGET and hashed again, and
// the two must agree. That is the difference between "we sent the right bytes"
// and "the right bytes are there", and it is the whole basis on which the
// source copy is later deleted.
func (s *Service) copyAndVerify(ctx context.Context, key string) (string, int64, error) {
	rc, err := s.primary.Open(ctx, key)
	switch {
	case errors.Is(err, storage.ErrNotFound):
		// The source listed this key and now does not have it: deleted between
		// enumeration and copy, or a store that lies about its own contents.
		// Either way, re-reading it will not produce it.
		return "", 0, terminal(failSourceMissing, err)
	case errors.Is(err, storage.ErrInvalidKey):
		return "", 0, terminal(failKeyUnsupported, err)
	case err != nil:
		return "", 0, err
	}
	// SizeUnknown lets PutSizedHashed sniff the length itself, which it can do
	// for the local backend (an *os.File) — that is the local→S3 case, and the
	// one where a wrong answer costs a 16 MiB buffer per object.
	size, sent, err := storage.PutSizedHashed(ctx, s.target, key, rc, storage.SizeUnknown)
	closeErr := rc.Close()
	if err != nil {
		if errors.Is(err, storage.ErrInvalidKey) {
			return "", 0, terminal(failKeyUnsupported, err)
		}
		return "", 0, err
	}
	if closeErr != nil {
		return "", 0, closeErr
	}

	stored, err := s.hashTarget(ctx, key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			// Written, then not there. A store that loses an object it just
			// acknowledged is not a store this migration can verify against.
			return "", 0, terminal(failVerification, err)
		}
		return "", 0, err
	}
	if stored != sent {
		return "", 0, terminal(failVerification, nil)
	}

	// Cross-check against what the database says this file contains (work item
	// 2's hashes). A mismatch here is NOT a bad copy — the copy is provably
	// faithful — it is a source object that no longer matches its row, and
	// copying it again would reproduce the same wrong bytes. Recording it as a
	// failure is what puts it in front of an operator before the source is
	// deleted.
	if recorded, ok := s.recordedHash(ctx, key); ok && recorded != sent {
		return "", 0, terminal(failRecordedHash, nil)
	}
	return sent, size, nil
}

// hashTarget streams the object back out of the target and returns its digest.
// It never buffers: these are whole video originals.
func (s *Service) hashTarget(ctx context.Context, key string) (string, error) {
	rc, err := s.target.Open(ctx, key)
	if err != nil {
		return "", err
	}
	defer func() { _ = rc.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, rc); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// recordedHash returns the digest video_files holds for this key, and whether
// there is one worth comparing. Absent rows (HLS segments, avatars, everything
// that is not a video_files row), not-yet-backfilled rows, and the
// missing-object sentinel all answer "no comparison available" — the migration
// is not the place to re-litigate those.
func (s *Service) recordedHash(ctx context.Context, key string) (string, bool) {
	sum, err := s.repo.GetVideoFileSHA256ByStorageKey(ctx, key)
	if err != nil || sum == "" || sum == mediahash.SentinelMissing {
		return "", false
	}
	return sum, true
}

// setState advances the campaign and refreshes nothing else; the trigger on the
// table turns the write into the operator-visible job run.
func (s *Service) setState(ctx context.Context, id uuid.UUID, state, lastError string) error {
	return s.repo.SetStorageMigrationState(ctx, sqlcgen.SetStorageMigrationStateParams{
		ID: id, State: state, LastError: lastError,
	})
}

// note records a diagnostic without changing state: the campaign is stuck
// waiting for an operator, which is a fact to display, not a failure to retry.
func (s *Service) note(ctx context.Context, id uuid.UUID, msg string) error {
	return s.repo.SetStorageMigrationError(ctx, sqlcgen.SetStorageMigrationErrorParams{
		ID: id, LastError: msg,
	})
}

// backoff is the retry delay for attempt n (1-based): a minute, doubling, capped
// at an hour. Long enough that a restarting object store is back; short enough
// that a whole library is not held up by one flaky object.
func backoff(attempt int) time.Duration {
	d := time.Minute << min(attempt-1, 6)
	return min(d, time.Hour)
}

// isUniqueViolation reports whether err is Postgres 23505. The single-active
// index is the authority on "one campaign at a time", so the create path has to
// be able to recognise losing that race.
func isUniqueViolation(err error) bool {
	var pgErr interface{ SQLState() string }
	return errors.As(err, &pgErr) && pgErr.SQLState() == "23505"
}
