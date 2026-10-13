package ipfsmirror

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// repinFake adds the bulk re-pin's ledger queries and the settings row it persists
// its run in to the in-memory fake, mirroring the SQL (hls + trailing slash + pinned,
// keyset by object_key, guarded re-arm).
type repinFake struct {
	*fakeRepo
	settings map[string]string
}

func (r *repinFake) pinnedHLS(after string) []sqlcgen.MediaIpfsPin {
	var out []sqlcgen.MediaIpfsPin
	for _, row := range r.rows {
		if row.MediaClass == "hls" && row.State == "pinned" && row.CarRoot != "" && strings.HasSuffix(row.ObjectKey, "/") && row.ObjectKey > after {
			out = append(out, *row)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ObjectKey < out[j].ObjectKey })
	return out
}

func (r *repinFake) ListIPFSPinsForRepin(_ context.Context, a sqlcgen.ListIPFSPinsForRepinParams) ([]sqlcgen.MediaIpfsPin, error) {
	rows := r.pinnedHLS(a.AfterKey)
	if len(rows) > int(a.BatchSize) {
		rows = rows[:a.BatchSize]
	}
	return rows, nil
}

func (r *repinFake) CountIPFSPinsForRepinAfter(_ context.Context, after string) (int64, error) {
	return int64(len(r.pinnedHLS(after))), nil
}

func (r *repinFake) RearmIPFSPinForRepin(_ context.Context, a sqlcgen.RearmIPFSPinForRepinParams) (int64, error) {
	row := r.rows[a.ObjectKey]
	if row == nil || row.State != "pinned" || row.Cid != a.Cid {
		return 0, nil
	}
	row.State = "pending"
	row.NextAttemptAt = time.Now().UTC().Add(-time.Second)
	return 1, nil
}

func (r *repinFake) GetInstanceSetting(_ context.Context, k string) (sqlcgen.InstanceSetting, error) {
	v, ok := r.settings[k]
	if !ok {
		return sqlcgen.InstanceSetting{}, pgx.ErrNoRows
	}
	return sqlcgen.InstanceSetting{Key: k, Value: v}, nil
}

func phaseIn(v string, phases ...string) bool {
	for _, p := range phases {
		if strings.Contains(v, `"phase":"`+p+`"`) {
			return true
		}
	}
	return false
}

func (r *repinFake) StartIPFSRepinRun(_ context.Context, a sqlcgen.StartIPFSRepinRunParams) (int64, error) {
	if phaseIn(r.settings[a.Key], "running", "paused") {
		return 0, nil
	}
	r.settings[a.Key] = a.Value
	return 1, nil
}

func (r *repinFake) SaveIPFSRepinRun(_ context.Context, a sqlcgen.SaveIPFSRepinRunParams) (int64, error) {
	if !phaseIn(r.settings[a.Key], a.ExpectedPhase) {
		return 0, nil
	}
	r.settings[a.Key] = a.Value
	return 1, nil
}

type repinEnv struct {
	t      *testing.T
	repo   *repinFake
	client *ipfs.FakeIPFSClient
	svc    *Service
	lk     *masterLookups
}

func newRepinEnv(t *testing.T, budget int64) *repinEnv {
	t.Helper()
	repo := &repinFake{fakeRepo: newFakeRepo(), settings: map[string]string{}}
	lk := &masterLookups{fakeLookups: &fakeLookups{videoOK: true, videoPrivacy: "public", videoState: "published", userOK: true, userActive: true}}
	cfg := testConfig()
	cfg.RepoBudgetBytes = budget
	client := ipfs.NewFakeIPFSClient()
	return &repinEnv{t: t, repo: repo, client: client, lk: lk, svc: New(repo, lk, newBlobs(t), client, cfg)}
}

// pinnedBeforeF6 stores a native tree (served files + downloads + vp9.webm) and
// seeds the pinned row the OLD pinDirectory would have left: the root of ALL of it.
func (e *repinEnv) pinnedBeforeF6() (key, oldCID string, oldSize int64) {
	e.t.Helper()
	id := uuid.New()
	gen := media.HLSPrefixForGeneration(id, 1) + "/"
	files := map[string]string{
		"master.m3u8":                            "#EXTM3U master",
		"720p/playlist.m3u8":                     "#EXTM3U playlist",
		"720p/seg_00000.ts":                      "segment-bytes",
		"720p/" + media.HLSMuxedDownloadFilename: strings.Repeat("d", 400),
		media.HLSAudioDownloadFilename:           strings.Repeat("a", 100),
		media.VP9WebMFilename:                    strings.Repeat("w", 300),
	}
	var entries []ipfs.DirEntry
	for rel, data := range files {
		putBlob(e.t, e.svc.blobs, gen+rel, data)
		entries = append(entries, ipfs.DirEntry{Path: rel, Data: strings.NewReader(data)})
	}
	res, err := e.client.AddDirectory(context.Background(), entries)
	if err != nil {
		e.t.Fatal(err)
	}
	key = media.HLSKeyPrefix(id) + "/"
	e.repo.rows[key] = &sqlcgen.MediaIpfsPin{ObjectKey: key, MediaClass: "hls", State: "pinned", Network: "public",
		Cid: res.CID, CarRoot: res.CID, ByteSize: res.Size, VideoID: pgUUID(id)}
	// The fake resolves one video; give it this one's generation.
	e.lk.master, e.lk.hlsTree = gen+"master.m3u8", strings.TrimSuffix(gen, "/")
	return key, res.CID, res.Size
}

func (e *repinEnv) tick() RepinStatus {
	e.t.Helper()
	if err := e.svc.RepinTick(context.Background()); err != nil {
		e.t.Fatalf("RepinTick: %v", err)
	}
	st, err := e.svc.RepinStatus(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *repinEnv) drain() {
	e.t.Helper()
	if _, err := e.svc.DrainDue(context.Background(), 10); err != nil {
		e.t.Fatal(err)
	}
}

func pinned(c *ipfs.FakeIPFSClient, cid string) bool {
	ok, _ := c.IsPinned(context.Background(), cid)
	return ok
}

// A row whose root still holds the downloads is re-pinned to the served-only root,
// the ordinary pin path records it, and the superseded root is unpinned.
func TestRepinMovesASupersetRowToTheServedOnlyRoot(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 0)
	key, oldCID, oldSize := e.pinnedBeforeF6()

	if _, err := e.svc.StartRepin(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.tick(); st.Queued != 1 || st.InFlight != 1 || e.repo.state(key) != "pending" {
		t.Fatalf("not armed: %+v state=%q", st, e.repo.state(key))
	}
	e.drain() // the existing pin path: add the served-only tree, swap-unpin the old root
	row := e.repo.rows[key]
	if row.State != "pinned" || row.CarRoot == oldCID || row.ByteSize >= oldSize {
		t.Fatalf("not re-pinned to a smaller root: %+v (old %s, %d bytes)", row, oldCID, oldSize)
	}
	if pinned(e.client, oldCID) || !pinned(e.client, row.CarRoot) {
		t.Fatalf("old root pinned=%v, new root pinned=%v", pinned(e.client, oldCID), pinned(e.client, row.CarRoot))
	}
	st := e.tick()
	if st.Done != 1 || st.Phase != RepinCompleted || st.BytesSavedEstimate != oldSize-row.ByteSize || st.Failed != 0 {
		t.Fatalf("settle: %+v want saved %d", st, oldSize-row.ByteSize)
	}

	// Self-verifying: a second run finds nothing stale and arms nothing.
	if _, err := e.svc.StartRepin(ctx); err != nil {
		t.Fatal(err)
	}
	e.tick()
	if st := e.tick(); st.Queued != 0 || st.Skipped != 1 || st.Phase != RepinCompleted {
		t.Fatalf("second run re-armed an already playback-only row: %+v", st)
	}
}

// The old root stays pinned while another live row still shares the CID.
func TestRepinKeepsAnOldRootAnotherRowShares(t *testing.T) {
	e := newRepinEnv(t, 0)
	key, oldCID, _ := e.pinnedBeforeF6()
	e.repo.rows["videos/other/original.mp4"] = &sqlcgen.MediaIpfsPin{ObjectKey: "videos/other/original.mp4", MediaClass: "video_original",
		State: "pinned", Network: "public", Cid: oldCID}

	if _, err := e.svc.StartRepin(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.tick()
	e.drain()
	if e.repo.rows[key].CarRoot == oldCID || !pinned(e.client, oldCID) {
		t.Fatalf("shared old root unpinned or row not moved: new=%s oldPinned=%v", e.repo.rows[key].CarRoot, pinned(e.client, oldCID))
	}
}

// An interrupted run resumes from its persisted state in a fresh process: rows
// already armed are settled, not armed twice.
func TestRepinResumesAfterAnInterruption(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 0)
	key, oldCID, _ := e.pinnedBeforeF6()
	if _, err := e.svc.StartRepin(ctx); err != nil {
		t.Fatal(err)
	}
	e.tick() // armed, then the process dies before the pin completes

	restarted := New(e.repo, e.lk, e.svc.blobs, e.client, testConfig())
	e.svc = restarted
	if st := e.tick(); st.Queued != 1 || st.InFlight != 1 || st.Phase != RepinRunning {
		t.Fatalf("restart re-armed or lost the in-flight row: %+v", st)
	}
	e.drain()
	st := e.tick()
	if st.Done != 1 || st.Queued != 1 || st.Phase != RepinCompleted || e.repo.rows[key].CarRoot == oldCID {
		t.Fatalf("did not finish after the restart: %+v", st)
	}
	if e.client.AddCount != 2 { // the fixture's old tree + exactly one served-only add
		t.Fatalf("AddCount = %d, want 2", e.client.AddCount)
	}
}

// While the unmanaged node is at its repo ceiling (F3) nothing is armed and the run
// reports why; when space frees it resumes by itself.
func TestRepinPausesOnCapacityAndResumes(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 1000)
	key, _, _ := e.pinnedBeforeF6()
	e.client.RepoSizeBytes = 1000
	if _, err := e.svc.StartRepin(ctx); err != nil {
		t.Fatal(err)
	}
	if st := e.tick(); st.Phase != RepinPaused || st.PauseReason != reasonBudgetExhausted || st.Queued != 0 || st.Scanned != 0 || e.repo.state(key) != "pinned" {
		t.Fatalf("armed a copy over the ceiling: %+v state=%q", st, e.repo.state(key))
	}
	if _, err := e.svc.StartRepin(ctx); err != ErrRepinRunning {
		t.Fatalf("second start while paused = %v, want ErrRepinRunning", err)
	}
	e.client.RepoSizeBytes = 100
	if st := e.tick(); st.Phase != RepinRunning || st.Queued != 1 || st.PauseReason != "" {
		t.Fatalf("did not resume: %+v", st)
	}
}

// Two starts cannot both win: the second is refused by the SQL compare-and-set, not
// just by this process's mutex, and the first run's cursor is not reset.
func TestRepinStartIsACompareAndSet(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 0)
	e.pinnedBeforeF6()
	if _, err := e.svc.StartRepin(ctx); err != nil {
		t.Fatal(err)
	}
	e.tick()
	before := e.repo.settings[repinSettingKey]
	// A second replica's start passes the load-and-check (it saw "idle" before the
	// first start landed) and reaches the store.
	b := `{"phase":"running","started_at":"2026-01-01T00:00:00Z","cursor":""}`
	if n, _ := e.repo.StartIPFSRepinRun(ctx, sqlcgen.StartIPFSRepinRunParams{Key: repinSettingKey, Value: b}); n != 0 {
		t.Fatal("a second start overwrote a running run")
	}
	if e.repo.settings[repinSettingKey] != before {
		t.Fatal("the running state changed")
	}
}

// Cancel works from running and from paused, stops arming, and lets what is armed finish.
func TestRepinCancel(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 1000)
	key, oldCID, _ := e.pinnedBeforeF6()
	if _, err := e.svc.CancelRepin(ctx); err != ErrRepinNotRunning {
		t.Fatalf("cancel with no run = %v", err)
	}
	e.client.RepoSizeBytes = 1000
	e.svc.StartRepin(ctx)
	if st := e.tick(); st.Phase != RepinPaused {
		t.Fatalf("want paused, got %+v", st)
	}
	if st, err := e.svc.CancelRepin(ctx); err != nil || st.Phase != RepinCancelled {
		t.Fatalf("cancel from paused = %+v %v", st, err)
	}
	e.client.RepoSizeBytes = 0
	if st := e.tick(); st.Queued != 0 || e.repo.state(key) != "pinned" {
		t.Fatalf("armed after cancel: %+v", st)
	}

	// From running: the armed row finishes and is counted, nothing further is armed.
	e.svc.StartRepin(ctx)
	e.tick()
	if st, err := e.svc.CancelRepin(ctx); err != nil || st.InFlight != 1 {
		t.Fatalf("cancel from running = %+v %v", st, err)
	}
	e.drain()
	if st := e.tick(); st.Phase != RepinCancelled || st.Done != 1 || st.InFlight != 0 || e.repo.rows[key].CarRoot == oldCID {
		t.Fatalf("armed row did not finish after cancel: %+v", st)
	}
}

// A re-add that dead-letters falls back to the old root, counted failed; Reconcile
// must not read it as a failed unpin.
func TestRepinFailureKeepsTheOldRoot(t *testing.T) {
	ctx := context.Background()
	e := newRepinEnv(t, 0)
	key, oldCID, _ := e.pinnedBeforeF6()
	e.svc.maxAttempts = 1
	e.svc.StartRepin(ctx)
	e.tick()
	e.client.Down = true
	e.drain()
	e.client.Down = false
	if e.repo.state(key) != "pinned" || e.repo.rows[key].CarRoot != oldCID || e.repo.rows[key].LastError == "" {
		t.Fatalf("not back on the old root: %+v", e.repo.rows[key])
	}
	if st := e.tick(); st.Failed != 1 || st.Done != 0 {
		t.Fatalf("failure not counted: %+v", st)
	}
	if _, err := e.svc.Reconcile(ctx); err != nil || e.repo.state(key) != "pinned" || !pinned(e.client, oldCID) {
		t.Fatalf("reconcile unpinned the good root: state=%q", e.repo.state(key))
	}
}
