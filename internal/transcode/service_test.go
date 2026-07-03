package transcode

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository implementing the queue + bookkeeping
// semantics the service relies on (due filtering, active-job dedupe, upserts).
type fakeRepo struct {
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
	vf := sqlcgen.VideoFile{
		ID: uuid.New(), VideoID: a.VideoID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, OriginalName: a.OriginalName, SizeBytes: a.SizeBytes,
	}
	f.videoFiles[a.VideoID] = append(f.videoFiles[a.VideoID], vf)
	return vf, nil
}

func (f *fakeRepo) DeleteVideoFilesByVideoAndKind(_ context.Context, a sqlcgen.DeleteVideoFilesByVideoAndKindParams) error {
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
	f.jobs[id].State = "done"
	return nil
}

func (f *fakeRepo) RescheduleTranscodeJob(_ context.Context, a sqlcgen.RescheduleTranscodeJobParams) error {
	j := f.jobs[a.ID]
	j.State = "pending"
	j.Attempts++
	j.NextAttemptAt = a.NextAttemptAt
	j.LastError = a.LastError
	return nil
}

func (f *fakeRepo) FailTranscodeJob(_ context.Context, a sqlcgen.FailTranscodeJobParams) error {
	j := f.jobs[a.ID]
	j.State = "failed"
	j.Attempts++
	j.LastError = a.LastError
	return nil
}

func (f *fakeRepo) UpsertStreamingPlaylist(_ context.Context, a sqlcgen.UpsertStreamingPlaylistParams) (sqlcgen.StreamingPlaylist, error) {
	sp := sqlcgen.StreamingPlaylist{VideoID: a.VideoID, MasterKey: a.MasterKey, State: a.State}
	f.playlists[a.VideoID] = sp
	return sp, nil
}

func (f *fakeRepo) GetStreamingPlaylist(_ context.Context, videoID uuid.UUID) (sqlcgen.StreamingPlaylist, error) {
	sp, ok := f.playlists[videoID]
	if !ok {
		return sqlcgen.StreamingPlaylist{}, errors.New("no rows")
	}
	return sp, nil
}

func (f *fakeRepo) CreateVideoRendition(_ context.Context, a sqlcgen.CreateVideoRenditionParams) (sqlcgen.VideoRendition, error) {
	r := sqlcgen.VideoRendition{ID: uuid.New(), VideoID: a.VideoID, Height: a.Height, Width: a.Width, KeyPrefix: a.KeyPrefix}
	f.renditions[a.VideoID] = append(f.renditions[a.VideoID], r)
	return r, nil
}

func (f *fakeRepo) DeleteVideoRenditions(_ context.Context, videoID uuid.UUID) error {
	delete(f.renditions, videoID)
	return nil
}

func (f *fakeRepo) ListVideoRenditions(_ context.Context, videoID uuid.UUID) ([]sqlcgen.VideoRendition, error) {
	return f.renditions[videoID], nil
}

// job returns the single job for a video (fails the test on 0 or >1).
func (f *fakeRepo) job(t *testing.T, videoID uuid.UUID) *sqlcgen.TranscodeJob {
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

// fakeTranscoder scripts the per-call outcome.
type fakeTranscoder struct {
	res   media.HLSResult
	err   error
	calls int
}

func (f *fakeTranscoder) Transcode(_ context.Context, _ uuid.UUID, _ string) (media.HLSResult, error) {
	f.calls++
	return f.res, f.err
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
