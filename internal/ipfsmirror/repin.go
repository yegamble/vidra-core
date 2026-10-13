package ipfsmirror

import (
	"context"
	"encoding/json"
	"errors"
	"path"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Bulk re-pin to the playback-only set (Tier A F6 follow-up).
//
// WHY. F6 made pinDirectory add only the files the HLS routes serve, but it runs only
// when a row is (re)pinned. A video pinned BEFORE F6 keeps its old root - download
// MP4s and vp9.webm included - until it is re-transcoded, so deploying Tier A barely
// lowers disk use and any "measure Tier A" step would measure the old trees.
//
// HOW. The job owns no pin logic. It moves a stale 'pinned' row back to 'pending'
// (RearmIPFSPinForRepin) and the ordinary path does the rest: admission, reservation,
// the F2 trailer check, the F3 budget, then MarkIPFSPinned records the new root and
// swapUnpin unpins the old one only when CountIPFSPinsSharingCID says nobody else
// shares it. Old blocks are freed by the node's GC (docs/operations.md, "Node GC").
//
// DETECTION IS SELF-VERIFYING, so no vintage marker (and no migration) is needed: a
// row is stale when its recorded root size is not smaller than the served bytes plus
// half the non-served bytes the promoted generation holds in the store. A row already
// re-pinned fails that test, so a restarted or repeated run converges instead of
// looping, and a false positive costs one idempotent re-add (same tree, same CID,
// swapUnpin no-ops).
//
// STATE lives in instance_settings under one key, outside the settings registry
// (peertubeimport does the same): the keyset cursor, counters and the few rows armed
// but not yet settled. Only the leader ticks it; a crash resumes from the cursor.

const (
	repinSettingKey = "ipfs.repin_playback_only"
	repinBatch      = 8 // rows armed per tick; the next batch waits for these to settle

	RepinIdle      = "idle"
	RepinRunning   = "running"
	RepinPaused    = "paused"
	RepinCompleted = "completed"
)

var (
	// ErrRepinRunning means a run is already in progress.
	ErrRepinRunning = errors.New("ipfsmirror: a playback-only re-pin is already running")
	// ErrRepinUnsupported means this build/backend cannot recompute the served set.
	ErrRepinUnsupported = errors.New("ipfsmirror: bulk re-pin needs the ledger store, an object inventory and the master-key lookup")
)

type repinRepo interface {
	ListIPFSPinsForRepin(context.Context, sqlcgen.ListIPFSPinsForRepinParams) ([]sqlcgen.MediaIpfsPin, error)
	CountIPFSPinsForRepinAfter(context.Context, string) (int64, error)
	RearmIPFSPinForRepin(context.Context, sqlcgen.RearmIPFSPinForRepinParams) (int64, error)
	GetInstanceSetting(context.Context, string) (sqlcgen.InstanceSetting, error)
	UpsertInstanceSetting(context.Context, sqlcgen.UpsertInstanceSettingParams) error
}

type repinInFlight struct {
	ObjectKey string `json:"object_key"`
	OldCID    string `json:"old_cid"`
	OldBytes  int64  `json:"old_bytes"`
}

// repinState is the persisted run. Counters are cumulative for the run.
type repinState struct {
	Phase       string          `json:"phase"`
	StartedAt   time.Time       `json:"started_at"`
	FinishedAt  *time.Time      `json:"finished_at,omitempty"`
	PauseReason string          `json:"pause_reason,omitempty"`
	Cursor      string          `json:"cursor"`
	Scanned     int64           `json:"scanned"`
	Queued      int64           `json:"queued"`
	Done        int64           `json:"done"`
	Skipped     int64           `json:"skipped"` // already playback-only, or the row moved on
	Blocked     int64           `json:"blocked"` // the admission policy would not re-admit the row
	Failed      int64           `json:"failed"`
	BytesSaved  int64           `json:"bytes_saved_estimate"`
	InFlight    []repinInFlight `json:"in_flight"`
}

// RepinStatus is the admin-facing view of a run.
type RepinStatus struct {
	Phase              string
	StartedAt          *time.Time
	FinishedAt         *time.Time
	PauseReason        string
	Scanned            int64
	Queued             int64
	Done               int64
	Skipped            int64
	Blocked            int64
	Failed             int64
	InFlight           int
	Remaining          int64 // pinned HLS rows not examined yet: an upper bound on the work left
	BytesSavedEstimate int64
}

var repinMu sync.Mutex

func (s *Service) repinDeps() (repinRepo, error) {
	rr, ok := s.repo.(repinRepo)
	_, inv := s.blobs.(storage.ObjectInventory)
	_, mk := s.lookups.(masterKeyReader)
	if !s.enabled || !ok || !inv || !mk {
		return nil, ErrRepinUnsupported
	}
	return rr, nil
}

func (s *Service) loadRepin(ctx context.Context, rr repinRepo) (repinState, error) {
	row, err := rr.GetInstanceSetting(ctx, repinSettingKey)
	if errors.Is(err, pgx.ErrNoRows) {
		return repinState{Phase: RepinIdle}, nil
	}
	if err != nil {
		return repinState{}, err
	}
	var st repinState
	if err := json.Unmarshal([]byte(row.Value), &st); err != nil || st.Phase == "" {
		return repinState{Phase: RepinIdle}, nil // unreadable state restarts cleanly
	}
	return st, nil
}

func (s *Service) saveRepin(ctx context.Context, rr repinRepo, st repinState) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return rr.UpsertInstanceSetting(ctx, sqlcgen.UpsertInstanceSettingParams{Key: repinSettingKey, Value: string(b), UpdatedBy: pgtype.UUID{}})
}

// StartRepin begins a run from the first row. ErrRepinRunning when one is active.
func (s *Service) StartRepin(ctx context.Context) (RepinStatus, error) {
	rr, err := s.repinDeps()
	if err != nil {
		return RepinStatus{}, err
	}
	repinMu.Lock()
	defer repinMu.Unlock()
	st, err := s.loadRepin(ctx, rr)
	if err != nil {
		return RepinStatus{}, err
	}
	if st.Phase == RepinRunning || st.Phase == RepinPaused {
		return RepinStatus{}, ErrRepinRunning
	}
	st = repinState{Phase: RepinRunning, StartedAt: time.Now().UTC()}
	if err := s.saveRepin(ctx, rr, st); err != nil {
		return RepinStatus{}, err
	}
	return s.repinView(ctx, rr, st), nil
}

// RepinStatus reports the current or last run (Phase "idle" when none ever started).
func (s *Service) RepinStatus(ctx context.Context) (RepinStatus, error) {
	rr, err := s.repinDeps()
	if err != nil {
		return RepinStatus{}, err
	}
	st, err := s.loadRepin(ctx, rr)
	if err != nil {
		return RepinStatus{}, err
	}
	return s.repinView(ctx, rr, st), nil
}

func (s *Service) repinView(ctx context.Context, rr repinRepo, st repinState) RepinStatus {
	v := RepinStatus{Phase: st.Phase, PauseReason: st.PauseReason, Scanned: st.Scanned, Queued: st.Queued, Done: st.Done,
		Skipped: st.Skipped, Blocked: st.Blocked, Failed: st.Failed, InFlight: len(st.InFlight), BytesSavedEstimate: st.BytesSaved}
	if st.Phase != RepinIdle {
		started := st.StartedAt
		v.StartedAt, v.FinishedAt = &started, st.FinishedAt
	}
	if st.Phase == RepinRunning || st.Phase == RepinPaused {
		if n, err := rr.CountIPFSPinsForRepinAfter(ctx, st.Cursor); err == nil {
			v.Remaining = n
		}
	}
	return v
}

// RepinTick advances a running job by one bounded step. Call it from a LEADER-gated
// ticker. A no-op unless a run is active.
func (s *Service) RepinTick(ctx context.Context) error {
	rr, err := s.repinDeps()
	if err != nil {
		return nil // not wired on this build: nothing to advance
	}
	repinMu.Lock()
	defer repinMu.Unlock()
	st, err := s.loadRepin(ctx, rr)
	if err != nil || (st.Phase != RepinRunning && st.Phase != RepinPaused) {
		return err
	}
	if err := s.settleRepin(ctx, &st); err != nil {
		return err
	}
	if len(st.InFlight) == 0 {
		if err := s.advanceRepin(ctx, rr, &st); err != nil {
			_ = s.saveRepin(ctx, rr, st) // keep the progress made before the error
			return err
		}
	}
	return s.saveRepin(ctx, rr, st)
}

// settleRepin classifies every armed row by what the ordinary pin path did to it.
func (s *Service) settleRepin(ctx context.Context, st *repinState) error {
	keep := st.InFlight[:0]
	for _, f := range st.InFlight {
		row, err := s.repo.GetIPFSPinByObjectKey(ctx, f.ObjectKey)
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			st.Skipped++
		case err != nil:
			return err
		case row.State == "pending":
			keep = append(keep, f)
		case row.State == "pinned" && row.Cid != f.OldCID:
			st.Done++
			st.BytesSaved += max(0, f.OldBytes-row.ByteSize)
		case row.State == "failed":
			st.Failed++
		default: // identical root, or the row went to unpinning/unpinned under a privacy flip
			st.Skipped++
		}
	}
	st.InFlight = keep
	return nil
}

func (s *Service) advanceRepin(ctx context.Context, rr repinRepo, st *repinState) error {
	rows, err := rr.ListIPFSPinsForRepin(ctx, sqlcgen.ListIPFSPinsForRepinParams{AfterKey: st.Cursor, BatchSize: repinBatch})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		now := time.Now().UTC()
		st.Phase, st.PauseReason, st.FinishedAt = RepinCompleted, "", &now
		return nil
	}
	for _, row := range rows {
		// Capacity first: never start a copy the node cannot take, and never examine
		// (list the store for) a row we would have to leave.
		reason, err := s.repinPauseReason(ctx, row.Network)
		if err != nil {
			return err
		}
		if reason != "" {
			st.Phase, st.PauseReason = RepinPaused, reason
			return nil
		}
		st.Phase, st.PauseReason = RepinRunning, ""
		if err := s.repinRow(ctx, rr, st, row); err != nil {
			return err // the cursor has not moved past this row: it is re-examined
		}
		st.Cursor = row.ObjectKey
		st.Scanned++
	}
	return nil
}

// repinRow examines one row and arms it when stale, tallying the outcome.
func (s *Service) repinRow(ctx context.Context, rr repinRepo, st *repinState, row sqlcgen.MediaIpfsPin) error {
	excess, stale, err := s.repinExcess(ctx, row)
	if err != nil {
		return err
	}
	if !stale {
		st.Skipped++
		return nil
	}
	ok, err := s.repinAdmissible(ctx, row)
	if err != nil {
		return err
	}
	if !ok {
		st.Blocked++
		return nil
	}
	n, err := rr.RearmIPFSPinForRepin(ctx, sqlcgen.RearmIPFSPinForRepinParams{ObjectKey: row.ObjectKey, Cid: row.Cid})
	if err != nil {
		return err
	}
	if n == 0 {
		st.Skipped++ // the row moved (privacy flip, re-transcode): the newer writer wins
		return nil
	}
	st.Queued++
	st.InFlight = append(st.InFlight, repinInFlight{ObjectKey: row.ObjectKey, OldCID: row.Cid, OldBytes: row.ByteSize})
	s.logger.Info("ipfs_repin_armed", "network", row.Network, "excess_bytes", excess)
	return nil
}

// repinExcess recomputes the served set from the store and reports whether the row's
// recorded root is the larger, pre-F6 one, and by roughly how much.
func (s *Service) repinExcess(ctx context.Context, row sqlcgen.MediaIpfsPin) (int64, bool, error) {
	if !row.VideoID.Valid {
		return 0, false, nil
	}
	master, found, err := s.lookups.(masterKeyReader).VideoHLSMasterKey(ctx, uuid.UUID(row.VideoID.Bytes))
	if err != nil {
		return 0, false, err
	}
	if !found || master == "" {
		return 0, false, nil
	}
	prefix := path.Dir(master) + "/"
	objects, err := s.blobs.(storage.ObjectInventory).ListObjects(ctx, prefix)
	if err != nil {
		return 0, false, err
	}
	var served, extra int64
	masterSeen := false
	for _, o := range objects {
		if hlsServed(prefix, master, o.Key) {
			served += o.Size
			masterSeen = masterSeen || o.Key == master
		} else {
			extra += o.Size
		}
	}
	// No tree to re-pin to (pinDirectory would refuse it), or nothing to shed.
	if !masterSeen || extra == 0 {
		return 0, false, nil
	}
	if row.ByteSize <= 0 {
		return extra, true, nil
	}
	return max(0, row.ByteSize-served), row.ByteSize >= served+extra/2, nil
}

// repinAdmissible is false when the publication policy would leave the re-armed row
// 'pending' forever (e.g. a 'legacy' row with backfill off).
func (s *Service) repinAdmissible(ctx context.Context, row sqlcgen.MediaIpfsPin) (bool, error) {
	if s.control == nil || normalizeNetwork(row.Network) != networkPublic {
		return true, nil
	}
	doc, err := s.control.Config(ctx)
	if err != nil || !doc.PolicyActive {
		return true, err
	}
	return policyAdmits(doc.Config, row), nil
}

// repinPauseReason is "" when the row's network can take another copy. It reads the
// same signals the pin path does: the managed runtime's admission pause (F4) or, for an
// unmanaged public node, the repo-size ceiling (F3). The private tier has no budget.
func (s *Service) repinPauseReason(ctx context.Context, network string) (string, error) {
	if normalizeNetwork(network) != networkPublic {
		return "", nil
	}
	if s.control != nil {
		doc, err := s.control.Config(ctx)
		if err != nil {
			return "", err
		}
		if doc.PolicyActive {
			rt, err := s.control.Runtime(ctx)
			if err != nil {
				return "", err
			}
			if rt.Capacity.AdmissionPausedReason != nil {
				return *rt.Capacity.AdmissionPausedReason, nil
			}
			return "", nil
		}
	}
	if nc, ok := s.networkClient(network); ok && s.unmanagedOverBudget(ctx, nc) {
		return reasonBudgetExhausted, nil
	}
	return "", nil
}
