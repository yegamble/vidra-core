package transcode

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository implementing the queue + bookkeeping
// semantics the service relies on (due filtering, active-job dedupe, upserts).
type fakeRepo struct {
	mu         sync.Mutex // DrainJobs runs jobs on a bounded pool (W10); the fake must be safe under -race
	jobs       map[uuid.UUID]*sqlcgen.TranscodeJob
	playlists  map[uuid.UUID]sqlcgen.StreamingPlaylist
	renditions map[uuid.UUID][]sqlcgen.VideoRendition
	videoFiles map[uuid.UUID][]sqlcgen.VideoFile
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		jobs:       map[uuid.UUID]*sqlcgen.TranscodeJob{},
		playlists:  map[uuid.UUID]sqlcgen.StreamingPlaylist{},
		renditions: map[uuid.UUID][]sqlcgen.VideoRendition{},
		videoFiles: map[uuid.UUID][]sqlcgen.VideoFile{},
	}
}

func (f *fakeRepo) CreateVideoFile(_ context.Context, a sqlcgen.CreateVideoFileParams) (sqlcgen.VideoFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
	}
	f.videoFiles[a.VideoID] = append(f.videoFiles[a.VideoID], vf)
	return vf, nil
}

func (f *fakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	cur := f.videoFiles[a.VideoID]
	out := cur[:0:0]
	for _, vf := range cur {
		if vf.Kind != a.Kind {
			out = append(out, vf)
		}
	}
	f.videoFiles[a.VideoID] = out
	return nil
}

func (f *fakeRepo) EnqueueTranscodeJob(_ context.Context, a sqlcgen.EnqueueTranscodeJobParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.VideoID == a.VideoID && (j.State == "pending" || j.State == "running") {
			return nil // partial unique index + ON CONFLICT DO NOTHING
		}
	}
	id := uuid.New()
	f.jobs[id] = &sqlcgen.TranscodeJob{
		ID: id, VideoID: a.VideoID, SourceKey: a.SourceKey,
		State: "pending", NextAttemptAt: time.Now().Add(-time.Second),
	}
	return nil
}

func (f *fakeRepo) ClaimDueTranscodeJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueTranscodeJobsRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var rows []sqlcgen.ClaimDueTranscodeJobsRow
	for _, j := range f.jobs {
		if int32(len(rows)) >= limit {
			break
		}
		if j.State == "pending" && !j.NextAttemptAt.After(time.Now()) {
			j.State = "running"
			rows = append(rows, sqlcgen.ClaimDueTranscodeJobsRow{
				ID: j.ID, VideoID: j.VideoID, SourceKey: j.SourceKey, Attempts: j.Attempts,
			})
		}
	}
	return rows, nil
}

func (f *fakeRepo) CompleteTranscodeJob(_ context.Context, id uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.jobs[id].State = "done"
	return nil
}

func (f *fakeRepo) RescheduleTranscodeJob(_ context.Context, a sqlcgen.RescheduleTranscodeJobParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[a.ID]
	j.State = "pending"
	j.Attempts++
	j.NextAttemptAt = a.NextAttemptAt
	j.LastError = a.LastError
	return nil
}

func (f *fakeRepo) FailTranscodeJob(_ context.Context, a sqlcgen.FailTranscodeJobParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[a.ID]
	j.State = "failed"
	j.Attempts++
	j.LastError = a.LastError
	return nil
}

func (f *fakeRepo) UpsertStreamingPlaylist(_ context.Context, a sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sp := sqlcgen.StreamingPlaylist{VideoID: a.VideoID, MasterKey: a.MasterKey, State: a.State}
	f.playlists[a.VideoID] = sp
	return sp, nil
}

func (f *fakeRepo) GetStreamingPlaylist(_ context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	sp, ok := f.playlists[videoID]
	if !ok {
		return sqlcgen.StreamingPlaylist{}, errors.New("no rows")
	}
	return sp, nil
}

func (f *fakeRepo) CreateVideoRendition(_ context.Context, a sqlcgen.CreateVideoRenditionParams) (sqlcgen.VideoRendition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r := sqlcgen.VideoRendition{ID: uuid.New(), VideoID: a.VideoID, Height: a.Height, Width: a.Width, KeyPrefix: a.KeyPrefix}
	f.renditions[a.VideoID] = append(f.renditions[a.VideoID], r)
	return r, nil
}

func (f *fakeRepo) DeleteVideoRenditions(_ context.Context, videoID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.renditions, videoID)
	return nil
}

func (f *fakeRepo) ListVideoRenditions(_ context.Context, videoID uuid.UUID) ([]sqlcgen.VideoRendition, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.renditions[videoID], nil
}

// job returns the single job for a video (fails the test on 0 or >1).
func (f *fakeRepo) job(t *testing.T, videoID uuid.UUID) *sqlcgen.TranscodeJob {
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Helper()
	var found *sqlcgen.TranscodeJob
	for _, j := range f.jobs {
		if j.VideoID == videoID {
			if found != nil {
				t.Fatalf("multiple jobs for video %s", videoID)
			}
			found = j
		}
	}
	if found == nil {
		t.Fatalf("no job for video %s", videoID)
	}
	return found
}

// fakeTranscoder scripts the per-call outcome. It is safe for concurrent use
// (DrainJobs runs jobs on a bounded pool, W10); block, when set, stalls each
// call until released so tests can observe true concurrency.
type fakeTranscoder struct {
	res media.HLSResult
	err error

	mu    sync.Mutex
	calls int

	block chan struct{} // nil = never block
}

func (f *fakeTranscoder) Transcode(_ context.Context, _ uuid.UUID, _ string) (media.HLSResult, error) {
	f.mu.Lock()
	f.calls++
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	return f.res, f.err
}

func (f *fakeTranscoder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func TestDrainJobsSuccessStoresPlaylistAndRenditions(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{res: media.HLSResult{
		MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8",
		Renditions: []media.HLSRendition{
			{Height: 480, Width: 854, KeyPrefix: "streaming-playlists/" + videoID.String() + "/480p"},
			{Height: 360, Width: 640, KeyPrefix: "streaming-playlists/" + videoID.String() + "/360p"},
		},
	}}
	svc := NewService(repo, tc)

	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	// Enqueue is idempotent while a job is live.
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("re-Enqueue: %v", err)
	}

	n, err := svc.DrainJobs(context.Background(), 10)
	if err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	if tc.calls != 1 {
		t.Errorf("transcoder ran %d times, want 1 (enqueue must dedupe)", tc.calls)
	}
	if got := repo.job(t, videoID).State; got != "done" {
		t.Errorf("job state = %q, want done", got)
	}
	sp, ok := svc.Playlist(context.Background(), videoID)
	if !ok || sp.State != PlaylistReady || sp.MasterKey != tc.res.MasterKey {
		t.Errorf("playlist = (%+v, %v), want ready with master key", sp, ok)
	}
	rends := svc.Renditions(context.Background(), videoID)
	if len(rends) != 2 || rends[0].Height != 480 || rends[1].Width != 640 {
		t.Errorf("renditions = %+v, want the two stored rungs", rends)
	}
	// Nothing left due.
	if n, _ := svc.DrainJobs(context.Background(), 10); n != 0 {
		t.Errorf("second drain completed %d jobs, want 0", n)
	}
}

func TestDrainJobsStoresVP9WebMAlternate(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	webmKey := "streaming-playlists/" + videoID.String() + "/vp9.webm"
	tc := &fakeTranscoder{res: media.HLSResult{
		MasterKey:  "streaming-playlists/" + videoID.String() + "/master.m3u8",
		Renditions: []media.HLSRendition{{Height: 360, Width: 640, KeyPrefix: "streaming-playlists/" + videoID.String() + "/360p"}},
		WebMKey:    webmKey,
		WebMHeight: 360,
		WebMWidth:  640,
		WebMBytes:  4096,
	}}
	svc := NewService(repo, tc)
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	files := repo.videoFiles[videoID]
	if len(files) != 1 {
		t.Fatalf("stored %d video files, want 1 (the webm alternate)", len(files))
	}
	if got := files[0]; got.Kind != "webm" || got.StorageKey != webmKey || got.ContentType != media.WebMContentType || got.SizeBytes != 4096 {
		t.Errorf("webm video_file = %+v, want kind=webm key=%q ct=%q size=4096", got, webmKey, media.WebMContentType)
	}
}

func TestDrainJobsNoWebMWhenAbsent(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{res: media.HLSResult{
		MasterKey:  "streaming-playlists/" + videoID.String() + "/master.m3u8",
		Renditions: []media.HLSRendition{{Height: 360, Width: 640, KeyPrefix: "streaming-playlists/" + videoID.String() + "/360p"}},
	}}
	svc := NewService(repo, tc)
	_ = svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4")
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if len(repo.videoFiles[videoID]) != 0 {
		t.Errorf("stored webm video_file when VP9 was off: %+v", repo.videoFiles[videoID])
	}
}

func TestDrainJobsFailureReschedulesWithBackoff(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{err: errors.New("boom " + strings.Repeat("x", 600))}
	svc := NewService(repo, tc)
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want (0, nil)", n, err)
	}
	j := repo.job(t, videoID)
	if j.State != "pending" || j.Attempts != 1 {
		t.Errorf("job = state %q attempts %d, want pending/1 (rescheduled)", j.State, j.Attempts)
	}
	if !j.NextAttemptAt.After(time.Now()) {
		t.Error("next attempt should be scheduled in the future (backoff)")
	}
	if j.LastError == "" || len(j.LastError) > 500 {
		t.Errorf("last_error should be recorded and bounded, got %d bytes", len(j.LastError))
	}
	// Not yet due → a second drain does not re-run it.
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if tc.calls != 1 {
		t.Errorf("transcoder ran %d times, want 1 (backoff must delay the retry)", tc.calls)
	}
	// No playlist while retries remain.
	if _, ok := svc.Playlist(context.Background(), videoID); ok {
		t.Error("no playlist row should exist while the job is still retrying")
	}
}

func TestDrainJobsDeadLettersAfterMaxAttempts(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{err: errors.New("boom")}
	svc := NewService(repo, tc)
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Force each retry due immediately, then drain until the cap.
	for i := 0; i < maxAttempts; i++ {
		repo.job(t, videoID).NextAttemptAt = time.Now().Add(-time.Second)
		if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
			t.Fatalf("DrainJobs: %v", err)
		}
	}
	j := repo.job(t, videoID)
	if j.State != "failed" || j.Attempts != maxAttempts {
		t.Errorf("job = state %q attempts %d, want failed/%d (dead-letter)", j.State, j.Attempts, maxAttempts)
	}
	if tc.calls != maxAttempts {
		t.Errorf("transcoder ran %d times, want %d", tc.calls, maxAttempts)
	}
	sp, ok := svc.Playlist(context.Background(), videoID)
	if !ok || sp.State != PlaylistFailed {
		t.Errorf("playlist = (%+v, %v), want a failed marker after dead-letter", sp, ok)
	}
	// A dead-lettered job is no longer live: a new upload can re-enqueue.
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x2.mp4"); err != nil {
		t.Fatalf("Enqueue after dead-letter: %v", err)
	}
	found := false
	for _, j := range repo.jobs {
		if j.VideoID == videoID && j.State == "pending" {
			found = true
		}
	}
	if !found {
		t.Error("a fresh job should be enqueueable after dead-letter")
	}
}

// TestCompletionHookFiresOnlyOnSuccess (P19.4): the completion hook fires exactly
// once per SUCCESSFUL job (with the video id — the seam the IPFS mirror uses to
// pin the finalized HLS tree) and never fires for a job that fails.
func TestCompletionHookFiresOnlyOnSuccess(t *testing.T) {
	// Success ⇒ hook fires once with the video id.
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{res: media.HLSResult{
		MasterKey:  "streaming-playlists/" + videoID.String() + "/master.m3u8",
		Renditions: []media.HLSRendition{{Height: 360, Width: 640, KeyPrefix: "streaming-playlists/" + videoID.String() + "/360p"}},
	}}
	var got []uuid.UUID
	svc := NewService(repo, tc, WithCompletionHook(func(_ context.Context, id uuid.UUID) {
		got = append(got, id)
	}))
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if len(got) != 1 || got[0] != videoID {
		t.Fatalf("completion hook fired with %v, want [%s] exactly once", got, videoID)
	}

	// Failure ⇒ hook does NOT fire (the job did not complete).
	repo2 := newFakeRepo()
	failID := uuid.New()
	fired := false
	svc2 := NewService(repo2, &fakeTranscoder{err: errors.New("boom")}, WithCompletionHook(func(_ context.Context, _ uuid.UUID) {
		fired = true
	}))
	_ = svc2.Enqueue(context.Background(), failID, "web-videos/y.mp4")
	if _, err := svc2.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs (failure): %v", err)
	}
	if fired {
		t.Error("completion hook fired for a FAILED job, want no fire")
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Minute}, {2, 2 * time.Minute}, {3, 4 * time.Minute}, {4, 8 * time.Minute}, {10, time.Hour},
	}
	for _, c := range cases {
		if got := backoff(c.attempts); got != c.want {
			t.Errorf("backoff(%d) = %v, want %v", c.attempts, got, c.want)
		}
	}
}

func TestDrainJobsWithoutTranscoder(t *testing.T) {
	svc := NewService(newFakeRepo(), nil)
	if _, err := svc.DrainJobs(context.Background(), 10); !errors.Is(err, ErrNoTranscoder) {
		t.Errorf("DrainJobs on a read-only service = %v, want ErrNoTranscoder", err)
	}
}

// --- config-parity W10: runtime gate + bounded worker pool ---

// TestEnqueueAndDrainHonorRuntimeGate proves the transcoding_enabled seam is
// consulted per call, never at construction: with ONE service instance (no
// restart), flipping the gate changes both enqueue and pickup behavior.
func TestEnqueueAndDrainHonorRuntimeGate(t *testing.T) {
	repo := newFakeRepo()
	tc := &fakeTranscoder{res: media.HLSResult{MasterKey: "streaming-playlists/x/master.m3u8"}}
	enabled := false
	svc := NewService(repo, tc, WithEnabledFunc(func() bool { return enabled }))

	// Gate off: enqueue is a silent no-op (the video keeps serving its
	// retained original), and nothing is claimed.
	off := uuid.New()
	if err := svc.Enqueue(context.Background(), off, "web-videos/off.mp4"); err != nil {
		t.Fatalf("Enqueue while disabled: %v", err)
	}
	if len(repo.jobs) != 0 {
		t.Fatalf("enqueue while disabled wrote %d job rows, want 0", len(repo.jobs))
	}

	// Runtime flip — same service instance, no reconstruction.
	enabled = true
	on := uuid.New()
	if err := svc.Enqueue(context.Background(), on, "web-videos/on.mp4"); err != nil {
		t.Fatalf("Enqueue while enabled: %v", err)
	}
	if len(repo.jobs) != 1 {
		t.Fatalf("enqueue while enabled wrote %d job rows, want 1", len(repo.jobs))
	}

	// Gate off again: the pending job is NOT claimed (it waits, it is not failed).
	enabled = false
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 0 {
		t.Fatalf("DrainJobs while disabled = (%d, %v), want (0, nil)", n, err)
	}
	if tc.callCount() != 0 {
		t.Fatalf("transcoder ran while disabled")
	}
	if got := repo.job(t, on).State; got != "pending" {
		t.Fatalf("job state while disabled = %q, want pending (not failed)", got)
	}

	// Flip back on: the same pending job now completes.
	enabled = true
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs after re-enable = (%d, %v), want (1, nil)", n, err)
	}
	if got := repo.job(t, on).State; got != "done" {
		t.Fatalf("job state after re-enable = %q, want done", got)
	}
}

// TestDrainJobsBoundedConcurrency proves the concurrency provider is resolved
// per drain call and actually bounds/parallelises the batch: with the fake
// transcoder blocking, exactly `concurrency` jobs start, and releasing them
// completes the whole batch.
func TestDrainJobsBoundedConcurrency(t *testing.T) {
	repo := newFakeRepo()
	block := make(chan struct{})
	tc := &fakeTranscoder{res: media.HLSResult{MasterKey: "m"}, block: block}
	concurrency := int64(3)
	svc := NewService(repo, tc, WithConcurrencyFunc(func() int64 { return concurrency }))

	const jobs = 6
	for i := 0; i < jobs; i++ {
		if err := svc.Enqueue(context.Background(), uuid.New(), "web-videos/x.mp4"); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}

	done := make(chan int, 1)
	go func() {
		n, _ := svc.DrainJobs(context.Background(), jobs)
		done <- n
	}()

	// With 3 workers over 6 blocked jobs, exactly 3 must be in flight.
	waitFor(t, func() bool { return tc.callCount() == 3 })
	select {
	case <-done:
		t.Fatal("DrainJobs returned while jobs were still blocked")
	default:
	}
	close(block) // release everything
	if n := <-done; n != jobs {
		t.Fatalf("DrainJobs completed %d, want %d", n, jobs)
	}
	if tc.callCount() != jobs {
		t.Fatalf("transcoder ran %d times, want %d", tc.callCount(), jobs)
	}

	// The provider is re-read on the next call (runtime change, no restart):
	// concurrency 1 degenerates to the sequential pre-W10 path.
	concurrency = 1
	if got := svc.Concurrency(); got != 1 {
		t.Fatalf("Concurrency() after change = %d, want 1", got)
	}
}

// TestDrainJobsConcurrentRetrySemantics proves per-job retry/dead-letter
// bookkeeping is unchanged under a parallel pool: failing jobs reschedule with
// attempts+1 while succeeding jobs complete, in one mixed batch.
func TestDrainJobsConcurrentRetrySemantics(t *testing.T) {
	repo := newFakeRepo()
	tc := &fakeTranscoder{err: errors.New("boom")}
	svc := NewService(repo, tc, WithConcurrencyFunc(func() int64 { return 4 }))

	ids := make([]uuid.UUID, 5)
	for i := range ids {
		ids[i] = uuid.New()
		if err := svc.Enqueue(context.Background(), ids[i], "web-videos/x.mp4"); err != nil {
			t.Fatalf("Enqueue: %v", err)
		}
	}
	if n, err := svc.DrainJobs(context.Background(), len(ids)); err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want (0, nil) — all jobs fail", n, err)
	}
	for _, id := range ids {
		j := repo.job(t, id)
		if j.State != "pending" || j.Attempts != 1 || j.LastError == "" {
			t.Errorf("job %s = state %q attempts %d, want pending/1 with a recorded error", id, j.State, j.Attempts)
		}
	}
}

// TestConcurrencyClampsDefensively: out-of-range provider values (only
// reachable via a manual DB edit) clamp instead of stalling or stampeding.
func TestConcurrencyClampsDefensively(t *testing.T) {
	for in, want := range map[int64]int{0: 1, -3: 1, 1: 1, 16: 16, 400: 16} {
		svc := NewService(newFakeRepo(), nil, WithConcurrencyFunc(func() int64 { return in }))
		if got := svc.Concurrency(); got != want {
			t.Errorf("Concurrency(provider=%d) = %d, want %d", in, got, want)
		}
	}
	if got := NewService(newFakeRepo(), nil).Concurrency(); got != 1 {
		t.Errorf("Concurrency without provider = %d, want 1", got)
	}
}

// TestEnabledAndCapableDefaults: nil providers preserve pre-W10 behavior.
func TestEnabledAndCapableDefaults(t *testing.T) {
	if svc := NewService(newFakeRepo(), nil); !svc.Enabled() || svc.Capable() {
		t.Errorf("nil-wired service: Enabled=%v Capable=%v, want true/false", svc.Enabled(), svc.Capable())
	}
	if svc := NewService(newFakeRepo(), &fakeTranscoder{}); !svc.Capable() {
		t.Error("service with a transcoder must be Capable")
	}
}

// waitFor polls cond until it holds or the deadline passes.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not reached in time")
}
