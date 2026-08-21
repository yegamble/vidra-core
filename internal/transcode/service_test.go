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
	steps      []sqlcgen.UpsertTranscodeStepParams
	sizes      map[string]int64 // storage key -> size_bytes, for the scratch estimate
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{
		jobs:       map[uuid.UUID]*sqlcgen.TranscodeJob{},
		playlists:  map[uuid.UUID]sqlcgen.StreamingPlaylist{},
		renditions: map[uuid.UUID][]sqlcgen.VideoRendition{},
		videoFiles: map[uuid.UUID][]sqlcgen.VideoFile{},
		sizes:      map[string]int64{},
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
		State: "pending", TranscodeType: a.TranscodeType,
		NextAttemptAt: time.Now().Add(-time.Second),
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
				ID: j.ID, VideoID: j.VideoID, SourceKey: j.SourceKey,
				TranscodeType: j.TranscodeType, Attempts: j.Attempts,
			})
		}
	}
	return rows, nil
}

func (f *fakeRepo) UpsertTranscodeStep(_ context.Context, a sqlcgen.UpsertTranscodeStepParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.steps = append(f.steps, a)
	return nil
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

// DeferTranscodeJob returns a job to the queue WITHOUT consuming an attempt --
// the distinction the scratch-space guard depends on.
func (f *fakeRepo) DeferTranscodeJob(_ context.Context, a sqlcgen.DeferTranscodeJobParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	j := f.jobs[a.ID]
	j.State = "pending"
	j.NextAttemptAt = a.NextAttemptAt
	j.LastError = a.LastError
	return nil
}

// GetVideoFileSizeByStorageKey makes the fake a sizeRepository so the per-job
// scratch estimate is exercised. sizes is keyed by storage key; a miss reports
// pgx.ErrNoRows-ish (any error), which the guard treats as "admit".
func (f *fakeRepo) GetVideoFileSizeByStorageKey(_ context.Context, key string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n, ok := f.sizes[key]
	if !ok {
		return 0, errors.New("no such file")
	}
	return n, nil
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

func (f *fakeRepo) HasLiveTranscodeJob(_ context.Context, videoID uuid.UUID) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, j := range f.jobs {
		if j.VideoID == videoID && (j.State == "pending" || j.State == "running") {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeRepo) DeleteStreamingPlaylist(_ context.Context, videoID uuid.UUID) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.playlists, videoID)
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

type fakeTargetTranscoder struct {
	hlsResult  media.HLSResult
	webResult  []media.WebVideoResult
	hlsErr     error
	webErr     error
	probeErr   error
	hlsCalls   []string
	webCalls   []string
	probeCalls []string
}

func (f *fakeTargetTranscoder) Transcode(ctx context.Context, videoID uuid.UUID, sourceKey string) (media.HLSResult, error) {
	return f.TranscodeHLS(ctx, videoID, sourceKey, media.Metadata{}, nil)
}

func (f *fakeTargetTranscoder) Probe(_ context.Context, sourceKey string) (media.Metadata, error) {
	f.probeCalls = append(f.probeCalls, sourceKey)
	if f.probeErr != nil {
		return media.Metadata{}, f.probeErr
	}
	return media.Metadata{DurationSeconds: 120, Width: 1280, Height: 720, FPS: 30}, nil
}

func (f *fakeTargetTranscoder) TranscodeHLS(_ context.Context, _ uuid.UUID, sourceKey string, _ media.Metadata, progress media.ProgressFunc) (media.HLSResult, error) {
	f.hlsCalls = append(f.hlsCalls, sourceKey)
	if progress != nil {
		progress(media.TranscodeProgress{Format: media.TranscodeFormatHLS, Height: 720, Width: 1280, State: media.ProgressRunning, Stage: "encoding", Percent: 42})
	}
	if f.hlsErr != nil {
		if progress != nil {
			progress(media.TranscodeProgress{Format: media.TranscodeFormatHLS, Height: 720, Width: 1280, State: media.ProgressFailed, Stage: "encoding", Percent: 42})
		}
	}
	return f.hlsResult, f.hlsErr
}

func (f *fakeTargetTranscoder) TranscodeWebVideos(_ context.Context, _ uuid.UUID, sourceKey string, _ media.Metadata, progress media.ProgressFunc) ([]media.WebVideoResult, error) {
	f.webCalls = append(f.webCalls, sourceKey)
	if progress != nil {
		progress(media.TranscodeProgress{Format: media.TranscodeFormatWebVideo, Height: 720, Width: 1280, State: media.ProgressRunning, Stage: "encoding", Percent: 63})
	}
	if f.webErr != nil {
		if progress != nil {
			progress(media.TranscodeProgress{Format: media.TranscodeFormatWebVideo, Height: 720, Width: 1280, State: media.ProgressFailed, Stage: "encoding", Percent: 63})
		}
	}
	return f.webResult, f.webErr
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

// TestJobProbesSourceOnce pins the shared-probe contract: an 'all' job runs two
// encode targets against one source and must read its metadata exactly once. On
// a backend with no local paths a probe streams the entire source to a temp
// file, so a per-target probe costs a full extra download of the original.
func TestJobProbesSourceOnce(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	originalKey := "web-videos/" + videoID.String() + ".mp4"
	tc := &fakeTargetTranscoder{
		hlsResult: media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"},
		webResult: []media.WebVideoResult{
			{Height: 720, Width: 1280, StorageKey: "web-videos/" + videoID.String() + "/720p.mp4", SizeBytes: 4096},
		},
	}
	svc := NewService(repo, tc)

	if err := svc.EnqueueTarget(context.Background(), videoID, originalKey, TargetAll); err != nil {
		t.Fatalf("EnqueueTarget: %v", err)
	}
	if n, err := svc.DrainJobs(context.Background(), 1); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	if len(tc.probeCalls) != 1 || tc.probeCalls[0] != originalKey {
		t.Errorf("probe calls = %v, want exactly one against the retained original", tc.probeCalls)
	}
	if len(tc.hlsCalls) != 1 || len(tc.webCalls) != 1 {
		t.Errorf("target calls HLS=%v Web=%v, want each exactly once", tc.hlsCalls, tc.webCalls)
	}
}

// TestProbeFailureFailsJobWithoutEncoding proves an unreadable source is a job
// failure, not a silent encode against zero dimensions.
func TestProbeFailureFailsJobWithoutEncoding(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTargetTranscoder{probeErr: errors.New("source unreadable")}
	svc := NewService(repo, tc)

	if err := svc.EnqueueTarget(context.Background(), videoID, "web-videos/x.mp4", TargetAll); err != nil {
		t.Fatalf("EnqueueTarget: %v", err)
	}
	if n, err := svc.DrainJobs(context.Background(), 1); err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want (0, nil) for a failed job", n, err)
	}
	if len(tc.hlsCalls) != 0 || len(tc.webCalls) != 0 {
		t.Errorf("encoded despite an unreadable source: HLS=%v Web=%v", tc.hlsCalls, tc.webCalls)
	}
}

func TestWebVideoTargetUsesOriginalAndProjectsResolutionProgress(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	originalKey := "web-videos/" + videoID.String() + ".mp4"
	tc := &fakeTargetTranscoder{webResult: []media.WebVideoResult{
		{Height: 720, Width: 1280, StorageKey: "web-videos/" + videoID.String() + "/720p.mp4", SizeBytes: 4096},
	}}
	svc := NewService(repo, tc)

	if err := svc.EnqueueTarget(context.Background(), videoID, originalKey, TargetWebVideo); err != nil {
		t.Fatalf("EnqueueTarget: %v", err)
	}
	if got := repo.job(t, videoID); got.SourceKey != originalKey || got.TranscodeType != TargetWebVideo {
		t.Fatalf("job source/type = %q/%q, want original/%q", got.SourceKey, got.TranscodeType, TargetWebVideo)
	}
	if n, err := svc.DrainJobs(context.Background(), 1); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	if len(tc.hlsCalls) != 0 || len(tc.webCalls) != 1 || tc.webCalls[0] != originalKey {
		t.Fatalf("target calls HLS=%v Web=%v, want Web once with retained original", tc.hlsCalls, tc.webCalls)
	}
	if len(repo.steps) == 0 {
		t.Fatal("no resolution progress was projected")
	}
	for _, step := range repo.steps {
		if step.Format != media.TranscodeFormatWebVideo || step.Height != 720 {
			t.Errorf("step = %+v, want independent Web Video 720p progress", step)
		}
	}
	files := repo.videoFiles[videoID]
	if len(files) != 1 || files[0].Kind != "rendition" || files[0].StorageKey != tc.webResult[0].StorageKey {
		t.Fatalf("stored web videos = %+v, want replacement rendition", files)
	}
}

func TestInvalidateRemovesEveryDerivativeButKeepsOriginal(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	repo.playlists[videoID] = sqlcgen.StreamingPlaylist{VideoID: videoID, State: PlaylistReady}
	repo.renditions[videoID] = []sqlcgen.VideoRendition{{VideoID: videoID, Height: 720}}
	repo.videoFiles[videoID] = []sqlcgen.VideoFile{
		{VideoID: videoID, Kind: "original", StorageKey: "web-videos/original.mp4"},
		{VideoID: videoID, Kind: "thumbnail", StorageKey: "thumbnails/poster.jpg"},
		{VideoID: videoID, Kind: "rendition", StorageKey: "web-videos/720p.mp4"},
		{VideoID: videoID, Kind: "webm", StorageKey: "streaming-playlists/vp9.webm"},
	}

	if err := NewService(repo, nil).Invalidate(context.Background(), videoID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := repo.playlists[videoID]; ok || len(repo.renditions[videoID]) != 0 {
		t.Fatal("HLS derivatives survived invalidation")
	}
	files := repo.videoFiles[videoID]
	if len(files) != 2 || files[0].Kind != "original" || files[1].Kind != "thumbnail" {
		t.Fatalf("files after invalidation = %+v, want original and thumbnail only", files)
	}
}

func TestWebVideoFailureDoesNotReplaceHealthyHLSState(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	repo.playlists[videoID] = sqlcgen.StreamingPlaylist{VideoID: videoID, MasterKey: "master.m3u8", State: PlaylistReady}
	tc := &fakeTargetTranscoder{webErr: errors.New("web encode failed")}
	svc := NewService(repo, tc)
	if err := svc.EnqueueTarget(context.Background(), videoID, "web-videos/original.mp4", TargetWebVideo); err != nil {
		t.Fatalf("EnqueueTarget: %v", err)
	}
	repo.job(t, videoID).Attempts = maxAttempts - 1

	if n, err := svc.DrainJobs(context.Background(), 1); err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want failed job", n, err)
	}
	if got := repo.playlists[videoID]; got.State != PlaylistReady || got.MasterKey != "master.m3u8" {
		t.Fatalf("healthy HLS state was overwritten by Web Video failure: %+v", got)
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

// TestFailureHookFiresOnlyOnDeadLetter (0098): the terminal-failure hook fires
// exactly once, only when a job is permanently dead-lettered (maxAttempts
// reached) — never on an intermediate retry. It is the seam that releases a
// publish-after-transcode hold so a video whose transcode never completes still
// publishes from its playable original.
func TestFailureHookFiresOnlyOnDeadLetter(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{err: errors.New("boom")}
	var got []uuid.UUID
	svc := NewService(repo, tc, WithFailureHook(func(_ context.Context, id uuid.UUID) {
		got = append(got, id)
	}))
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	for i := 0; i < maxAttempts; i++ {
		repo.job(t, videoID).NextAttemptAt = time.Now().Add(-time.Second)
		if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
			t.Fatalf("DrainJobs: %v", err)
		}
		wantFires := 0
		if i == maxAttempts-1 {
			wantFires = 1 // only the final attempt dead-letters
		}
		if len(got) != wantFires {
			t.Fatalf("after attempt %d: failure hook fired %d times, want %d", i+1, len(got), wantFires)
		}
	}
	if len(got) != 1 || got[0] != videoID {
		t.Fatalf("failure hook fired with %v, want [%s] exactly once at dead-letter", got, videoID)
	}
}

// TestFailureHookDoesNotFireOnSuccess proves the terminal-failure hook never
// fires for a job that completes.
func TestFailureHookDoesNotFireOnSuccess(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{res: media.HLSResult{MasterKey: "streaming-playlists/" + videoID.String() + "/master.m3u8"}}
	fired := false
	svc := NewService(repo, tc, WithFailureHook(func(_ context.Context, _ uuid.UUID) { fired = true }))
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(context.Background(), 10); err != nil {
		t.Fatalf("DrainJobs: %v", err)
	}
	if fired {
		t.Error("failure hook fired for a SUCCESSFUL job, want no fire")
	}
}

// errLiveRepo fails the live-job lookup; every other Repository method is the
// embedded nil interface (panics if reached — these tests never do).
type errLiveRepo struct{ Repository }

func (errLiveRepo) HasLiveTranscodeJob(context.Context, uuid.UUID) (bool, error) {
	return false, errors.New("db down")
}

// TestLiveJobSurfacesErrorWhereHasLiveJobFailsBusy pins the two failure
// postures: HasLiveJob reports a lookup error as busy (right for admission —
// replace-conflict, hold entry), while LiveJob surfaces the error so RELEASE
// call sites can treat "undetermined" as releasable instead of suppressing a
// publish-after-transcode release forever.
func TestLiveJobSurfacesErrorWhereHasLiveJobFailsBusy(t *testing.T) {
	svc := NewService(errLiveRepo{}, nil)
	id := uuid.New()
	if !svc.HasLiveJob(context.Background(), id) {
		t.Error("HasLiveJob on lookup error = false, want true (fail-busy admission posture)")
	}
	live, err := svc.LiveJob(context.Background(), id)
	if err == nil {
		t.Fatal("LiveJob swallowed the lookup error, want it surfaced")
	}
	if live {
		t.Error("LiveJob on error reported live=true, want false with the error")
	}

	// And on a healthy repo the two agree.
	okRepo := newFakeRepo()
	okSvc := NewService(okRepo, nil)
	if okSvc.HasLiveJob(context.Background(), id) {
		t.Error("HasLiveJob with no jobs = true, want false")
	}
	if live, err := okSvc.LiveJob(context.Background(), id); err != nil || live {
		t.Errorf("LiveJob with no jobs = (%v, %v), want (false, nil)", live, err)
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

// TestReplaceGenerationPromotionIsAtomic (W14): while a replacement's
// re-transcode job is pending/running, the playlist and renditions keep
// pointing at the OLD generation; only a successful storeResult swaps them to
// the new one, and a failed job leaves the pipeline's usual dead-letter
// behaviour. HasLiveJob reports the in-flight window the replace endpoints
// gate on.
func TestReplaceGenerationPromotionIsAtomic(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	oldPrefix := "streaming-playlists/" + videoID.String()
	newPrefix := oldPrefix + "/r1"

	// Seed the promoted OLD generation (the tree players are streaming).
	if _, err := repo.UpsertStreamingPlaylist(context.Background(), sqlcgen.UpsertStreamingPlaylistParams{
		VideoID: videoID, MasterKey: oldPrefix + "/master.m3u8", State: PlaylistReady,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateVideoRendition(context.Background(), sqlcgen.CreateVideoRenditionParams{
		VideoID: videoID, Height: 480, Width: 854, KeyPrefix: oldPrefix + "/480p",
	}); err != nil {
		t.Fatal(err)
	}

	tc := &fakeTranscoder{res: media.HLSResult{
		MasterKey: newPrefix + "/master.m3u8",
		Renditions: []media.HLSRendition{
			{Height: 720, Width: 1280, KeyPrefix: newPrefix + "/720p"},
		},
	}}
	svc := NewService(repo, tc)

	if svc.HasLiveJob(context.Background(), videoID) {
		t.Fatal("HasLiveJob before enqueue = true, want false")
	}
	if err := svc.Enqueue(context.Background(), videoID, "web-videos/"+videoID.String()+".r1.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !svc.HasLiveJob(context.Background(), videoID) {
		t.Fatal("HasLiveJob with a pending job = false, want true")
	}
	// Mid-flight: the OLD generation still serves.
	if sp, ok := svc.Playlist(context.Background(), videoID); !ok || sp.MasterKey != oldPrefix+"/master.m3u8" {
		t.Errorf("mid-flight playlist = %+v, want the old master", sp)
	}

	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
	// Promotion: playlist + renditions now point at the new generation only.
	sp, ok := svc.Playlist(context.Background(), videoID)
	if !ok || sp.State != PlaylistReady || sp.MasterKey != newPrefix+"/master.m3u8" {
		t.Errorf("post-promotion playlist = (%+v, %v), want the new master", sp, ok)
	}
	rends := svc.Renditions(context.Background(), videoID)
	if len(rends) != 1 || rends[0].KeyPrefix != newPrefix+"/720p" {
		t.Errorf("post-promotion renditions = %+v, want only the new generation's", rends)
	}
	if svc.HasLiveJob(context.Background(), videoID) {
		t.Error("HasLiveJob after completion = true, want false")
	}
}

// TestInvalidateDropsPlaylistAndRenditions (W14): a replacement landing while
// transcoding is unavailable must stop the stale HLS tree from serving.
func TestInvalidateDropsPlaylistAndRenditions(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	prefix := "streaming-playlists/" + videoID.String()
	if _, err := repo.UpsertStreamingPlaylist(context.Background(), sqlcgen.UpsertStreamingPlaylistParams{
		VideoID: videoID, MasterKey: prefix + "/master.m3u8", State: PlaylistReady,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateVideoRendition(context.Background(), sqlcgen.CreateVideoRenditionParams{
		VideoID: videoID, Height: 360, Width: 640, KeyPrefix: prefix + "/360p",
	}); err != nil {
		t.Fatal(err)
	}

	svc := NewService(repo, nil)
	if err := svc.Invalidate(context.Background(), videoID); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, ok := svc.Playlist(context.Background(), videoID); ok {
		t.Error("playlist still present after Invalidate")
	}
	if rends := svc.Renditions(context.Background(), videoID); len(rends) != 0 {
		t.Errorf("renditions after Invalidate = %+v, want none", rends)
	}
}

const gib = 1 << 30

// TestScratchFloorStopsClaiming is the guard that matters most. The scratch
// volume shares a filesystem with the Postgres data directory on the single-disk
// deployment, so a transcode that fills it does not merely fail a job — it stops
// the database accepting writes. Below the floor the worker must claim NOTHING,
// and crucially must leave the jobs in 'pending' rather than flipping them to
// 'running' and failing them for a condition the job did not cause.
func TestScratchFloorStopsClaiming(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 2 * gib, nil }, 10*gib))

	if err := svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	n, err := svc.DrainJobs(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want (0, nil) below the floor", n, err)
	}
	if tc.calls != 0 {
		t.Errorf("transcoder ran %d times below the scratch floor", tc.calls)
	}
	if got := repo.job(t, videoID); got.State != "pending" {
		t.Errorf("job state = %q, want it left pending (not claimed-then-failed)", got.State)
	}
	if got := repo.job(t, videoID); got.Attempts != 0 {
		t.Errorf("attempts = %d; a full disk must not consume the video's retry budget", got.Attempts)
	}
}

// TestScratchFloorAdmitsWhenSpaceIsAvailable is the other half: the guard must
// not be a permanent brake.
func TestScratchFloorAdmitsWhenSpaceIsAvailable(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 500 * gib, nil }, 10*gib))

	_ = svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4")
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil) with space free", n, err)
	}
}

// TestUnmeasurableScratchDoesNotHaltTranscoding pins the fail-open posture. A
// statfs that errors is a broken measurement, not a full disk; halting every
// transcode on the instance because a syscall failed would be a worse outage
// than the one being guarded against.
func TestUnmeasurableScratchDoesNotHaltTranscoding(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 0, errors.New("statfs: permission denied") }, 10*gib))

	_ = svc.Enqueue(context.Background(), videoID, "web-videos/x.mp4")
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil) — an unmeasurable disk must not halt the queue", n, err)
	}
}

// TestOversizedSourceIsDeferredNotFailed covers the per-job estimate. The
// distinction between DEFER and RESCHEDULE is the whole point: rescheduling
// increments attempts, so five full-disk ticks would dead-letter a perfectly
// good video permanently.
func TestOversizedSourceIsDeferredNotFailed(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	const key = "web-videos/huge.mp4"
	// 40 GiB source needs ~120 GiB of scratch; only 50 GiB is free.
	repo.sizes[key] = 40 * gib
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 50 * gib, nil }, 10*gib))

	_ = svc.Enqueue(context.Background(), videoID, key)
	n, err := svc.DrainJobs(context.Background(), 10)
	if err != nil || n != 0 {
		t.Fatalf("DrainJobs = (%d, %v), want (0, nil) for an oversized source", n, err)
	}
	if tc.calls != 0 {
		t.Errorf("transcoder ran %d times for a source that cannot fit", tc.calls)
	}
	job := repo.job(t, videoID)
	if job.State != "pending" {
		t.Errorf("job state = %q, want pending", job.State)
	}
	if job.Attempts != 0 {
		t.Errorf("attempts = %d, want 0 — a deferral must not burn the retry budget, "+
			"or five full-disk ticks would dead-letter the video", job.Attempts)
	}
	if !strings.Contains(job.LastError, "scratch space") {
		t.Errorf("last_error = %q, want it to name the reason so an operator can act", job.LastError)
	}
	if !job.NextAttemptAt.After(time.Now()) {
		t.Error("deferred job is due immediately; it would spin against a full disk")
	}
}

// TestSourceThatFitsRuns proves the estimate admits normal work.
func TestSourceThatFitsRuns(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	const key = "web-videos/normal.mp4"
	repo.sizes[key] = 2 * gib // needs ~6 GiB, 500 GiB free
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 500 * gib, nil }, 10*gib))

	_ = svc.Enqueue(context.Background(), videoID, key)
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil)", n, err)
	}
}

// TestUnknownSourceSizeAdmits pins the other fail-open: a missing size row (an
// imported video, a hand-inserted job) must not block transcoding. The floor has
// already passed at this point, so admitting is the safe default.
func TestUnknownSourceSizeAdmits(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc, WithScratchGuard(
		func() (uint64, error) { return 50 * gib, nil }, 10*gib))

	_ = svc.Enqueue(context.Background(), videoID, "web-videos/unknown.mp4")
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil) when the source size is unknown", n, err)
	}
}

// TestNoScratchGuardKeepsPreGuardBehaviour proves the option is genuinely
// optional — a service without it behaves exactly as before.
func TestNoScratchGuardKeepsPreGuardBehaviour(t *testing.T) {
	repo := newFakeRepo()
	videoID := uuid.New()
	repo.sizes["web-videos/huge.mp4"] = 500 * gib
	tc := &fakeTranscoder{}
	svc := NewService(repo, tc)

	_ = svc.Enqueue(context.Background(), videoID, "web-videos/huge.mp4")
	if n, err := svc.DrainJobs(context.Background(), 10); err != nil || n != 1 {
		t.Fatalf("DrainJobs = (%d, %v), want (1, nil) with no guard wired", n, err)
	}
}

// TestScratchGuardDefaultsTheFloor proves a zero minFree does not mean "no
// floor" — that would silently disable the guard for anyone who passes an unset
// config value.
func TestScratchGuardDefaultsTheFloor(t *testing.T) {
	svc := NewService(newFakeRepo(), &fakeTranscoder{}, WithScratchGuard(
		func() (uint64, error) { return 0, nil }, 0))
	if svc.minFreeScratch != DefaultMinFreeScratchBytes {
		t.Errorf("minFreeScratch = %d, want the default %d", svc.minFreeScratch, DefaultMinFreeScratchBytes)
	}
}
