package videoimport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// ---- fakes ----------------------------------------------------------------

type fakeRepo struct {
	jobs     map[uuid.UUID]sqlcgen.ImportJob
	order    []uuid.UUID
	stageLog []string // every stage written (progress ordering assertions)
}

func newFakeRepo() *fakeRepo { return &fakeRepo{jobs: map[uuid.UUID]sqlcgen.ImportJob{}} }

func (r *fakeRepo) EnqueueImportJob(_ context.Context, arg sqlcgen.EnqueueImportJobParams) (sqlcgen.ImportJob, error) {
	for _, id := range r.order {
		j := r.jobs[id]
		if j.VideoID == arg.VideoID && (j.State == "pending" || j.State == "running") {
			return sqlcgen.ImportJob{}, pgx.ErrNoRows
		}
	}
	resolver := arg.Resolver
	if resolver == "" {
		resolver = "direct"
	}
	j := sqlcgen.ImportJob{ID: uuid.New(), VideoID: arg.VideoID, Url: arg.Url, State: "pending", Resolver: resolver, NextAttemptAt: time.Now(), CreatedAt: time.Now()}
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
	return j, nil
}
func (r *fakeRepo) SetImportJobStage(_ context.Context, arg sqlcgen.SetImportJobStageParams) error {
	j := r.jobs[arg.ID]
	j.Stage = arg.Stage
	r.jobs[arg.ID] = j
	r.stageLog = append(r.stageLog, arg.Stage)
	return nil
}
func (r *fakeRepo) SetImportJobResolver(_ context.Context, arg sqlcgen.SetImportJobResolverParams) error {
	j := r.jobs[arg.ID]
	j.Resolver, j.Stage = arg.Resolver, arg.Stage
	r.jobs[arg.ID] = j
	r.stageLog = append(r.stageLog, arg.Stage)
	return nil
}
func (r *fakeRepo) GetLatestImportJobByVideo(_ context.Context, videoID uuid.UUID) (sqlcgen.ImportJob, error) {
	for i := len(r.order) - 1; i >= 0; i-- {
		if j := r.jobs[r.order[i]]; j.VideoID == videoID {
			return j, nil
		}
	}
	return sqlcgen.ImportJob{}, pgx.ErrNoRows
}
func (r *fakeRepo) ClaimDueImportJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueImportJobsRow, error) {
	var rows []sqlcgen.ClaimDueImportJobsRow
	for _, id := range r.order {
		j := r.jobs[id]
		if j.State == "pending" && !j.NextAttemptAt.After(time.Now()) {
			j.State = "running"
			r.jobs[id] = j
			rows = append(rows, sqlcgen.ClaimDueImportJobsRow{ID: j.ID, VideoID: j.VideoID, Url: j.Url, Attempts: j.Attempts, Resolver: j.Resolver, Stage: j.Stage})
			if int32(len(rows)) >= limit {
				break
			}
		}
	}
	return rows, nil
}
func (r *fakeRepo) CompleteImportJob(_ context.Context, id uuid.UUID) error {
	j := r.jobs[id]
	j.State, j.Error, j.Stage = "done", "", ""
	r.jobs[id] = j
	return nil
}
func (r *fakeRepo) RescheduleImportJob(_ context.Context, arg sqlcgen.RescheduleImportJobParams) error {
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.NextAttemptAt, j.Error, j.Stage = "pending", j.Attempts+1, arg.NextAttemptAt, arg.Error, ""
	r.jobs[arg.ID] = j
	return nil
}
func (r *fakeRepo) FailImportJob(_ context.Context, arg sqlcgen.FailImportJobParams) error {
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.Error, j.Stage = "failed", j.Attempts+1, arg.Error, ""
	r.jobs[arg.ID] = j
	return nil
}

// forceDue resets every pending job's next_attempt_at to the past so the next
// drain re-claims it (exercising the retry ladder without real backoff waits).
func (r *fakeRepo) forceDue() {
	for id, j := range r.jobs {
		if j.State == "pending" {
			j.NextAttemptAt = time.Now().Add(-time.Hour)
			r.jobs[id] = j
		}
	}
}

func (r *fakeRepo) latest(videoID uuid.UUID) sqlcgen.ImportJob {
	j, _ := r.GetLatestImportJobByVideo(context.Background(), videoID)
	return j
}

// fakePipeline mimics the video ingest seam. AttachOriginal reads the reader
// (so the size/quota caps surface), gates on the filename/Content-Type, and
// records the stored size; Process marks the video published.
type fakePipeline struct {
	owners     map[uuid.UUID]uuid.UUID
	storedSize map[uuid.UUID]int64
	published  map[uuid.UUID]bool
	prefill    map[uuid.UUID][2]string // videoID → {title, description}
}

func newFakePipeline() *fakePipeline {
	return &fakePipeline{owners: map[uuid.UUID]uuid.UUID{}, storedSize: map[uuid.UUID]int64{}, published: map[uuid.UUID]bool{}}
}

func (p *fakePipeline) GetByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	owner, ok := p.owners[id]
	if !ok {
		return sqlcgen.GetVideoByIDRow{}, video.ErrNotFound
	}
	return sqlcgen.GetVideoByIDRow{ID: id, OwnerID: owner}, nil
}
func (p *fakePipeline) AttachOriginal(_ context.Context, _, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	data, err := io.ReadAll(in.Reader)
	if err != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, err // errImportTooLarge / errImportOverQuota
	}
	_, okExt := video.AcceptedVideoExt(in.Filename)
	okType := strings.HasPrefix(strings.ToLower(in.ContentType), "video/")
	if !okExt && !okType {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, video.ErrUnsupportedMedia
	}
	p.storedSize[videoID] = int64(len(data))
	return sqlcgen.Video{ID: videoID}, sqlcgen.VideoFile{StorageKey: "web-videos/" + videoID.String(), SizeBytes: int64(len(data))}, nil
}
func (p *fakePipeline) Process(_ context.Context, videoID uuid.UUID, _ string) (sqlcgen.Video, error) {
	p.published[videoID] = true
	return sqlcgen.Video{ID: videoID, State: "published"}, nil
}
func (p *fakePipeline) PrefillMetadata(_ context.Context, videoID uuid.UUID, title, description string) error {
	if p.prefill == nil {
		p.prefill = map[uuid.UUID][2]string{}
	}
	p.prefill[videoID] = [2]string{title, description}
	return nil
}

type fakeQuota struct {
	remaining int64
	limited   bool
}

func (q fakeQuota) Remaining(_ context.Context, _ uuid.UUID) (int64, bool, error) {
	return q.remaining, q.limited, nil
}

// originServer serves the import fixtures on loopback.
func originServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clip.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("tiny-video-bytes"))
		case "/notavideo.txt":
			_, _ = w.Write([]byte("not a video"))
		case "/sized.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(make([]byte, 64)) // Content-Length: 64
		case "/stream": // no extension, but a video content-type (auto → direct)
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("tiny-video-bytes"))
		case "/page": // no extension, HTML (auto → ytdlp when enabled)
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte("<html>watch page</html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func loopbackHost(u string) string { return strings.Replace(u, "127.0.0.1", "localhost", 1) }

// seedVideo registers a video with the given owner in the pipeline and returns
// its id.
func seedVideo(p *fakePipeline, owner uuid.UUID) uuid.UUID {
	id := uuid.New()
	p.owners[id] = owner
	return id
}

// ---- tests ----------------------------------------------------------------

// TestEnqueueValidatesAndIsIdempotent covers URL validation and the single
// active import per video.
func TestEnqueueValidatesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, "ftp://example.com/x.mp4", ResolverDirect); err != ErrInvalidURL {
		t.Errorf("ftp enqueue err = %v, want ErrInvalidURL", err)
	}
	first, err := svc.Enqueue(ctx, vid, "https://example.com/a.mp4", ResolverDirect)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	second, err := svc.Enqueue(ctx, vid, "https://example.com/b.mp4", ResolverDirect)
	if err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if first.ID != second.ID {
		t.Errorf("second enqueue = %s, want the in-flight job %s", second.ID, first.ID)
	}
}

// TestDrainHappyPath fetches a real loopback origin (plain injected client) and
// finalises the video.
func TestDrainHappyPath(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/clip.mp4", ResolverDirect); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainJobs(ctx, 5)
	if err != nil || n != 1 {
		t.Fatalf("drain = (%d,%v), want (1,nil)", n, err)
	}
	if j := repo.latest(vid); j.State != "done" {
		t.Fatalf("job state = %q (err=%q), want done", j.State, j.Error)
	}
	if !pipe.published[vid] || pipe.storedSize[vid] != int64(len("tiny-video-bytes")) {
		t.Errorf("pipeline not finalised: published=%v size=%d", pipe.published[vid], pipe.storedSize[vid])
	}
}

// TestDrainNonVideoIsSafeFailure: a non-video fetch fails with a safe reason
// that never leaks the URL.
func TestDrainNonVideoIsSafeFailure(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	url := loopbackHost(origin.URL) + "/notavideo.txt"
	if _, err := svc.Enqueue(ctx, vid, url, ResolverDirect); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State == "done" {
		t.Fatalf("non-video job = done, want a failure")
	}
	if j.Error == "" || strings.Contains(j.Error, origin.URL) {
		t.Errorf("job error = %q, want a safe reason that does not leak the URL", j.Error)
	}
}

// TestDrainSSRFGuardBlocksLoopback: with the DEFAULT (guarded) client and
// AllowPrivate off, the worker refuses to fetch a loopback origin — proving the
// SSRF guard is wired into the async path.
func TestDrainSSRFGuardBlocksLoopback(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20) // no injected client → SSRF-guarded client
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/clip.mp4", ResolverDirect); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if j := repo.latest(vid); j.State == "done" || pipe.published[vid] {
		t.Fatalf("loopback fetch via guarded client succeeded; want it refused")
	}
}

// TestDrainAllowPrivateConfig: with AllowPrivateFetch on, the guarded client is
// allowed to reach a literal-127.0.0.1 loopback origin — the dev/test knob flow.
func TestDrainAllowPrivateConfig(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithAllowPrivateFetch(true)) // real guard, private allowed
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, origin.URL+"/clip.mp4", ResolverDirect); err != nil { // literal 127.0.0.1
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	if j := repo.latest(vid); j.State != "done" {
		t.Fatalf("allow-private import state = %q (err=%q), want done", j.State, j.Error)
	}
}

// TestDrainQuotaExceeded: a fetch that would exceed the owner's remaining quota
// fails with a quota reason and nothing is stored.
func TestDrainQuotaExceeded(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}), WithQuota(fakeQuota{remaining: 10, limited: true}))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/sized.mp4", ResolverDirect); err != nil { // 64 bytes > 10
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State == "done" || !strings.Contains(j.Error, "quota") {
		t.Fatalf("quota job state=%q error=%q, want a failure with a quota reason", j.State, j.Error)
	}
	if _, stored := pipe.storedSize[vid]; stored {
		t.Errorf("bytes were stored despite the quota failure")
	}
}

// TestRetryThenDeadLetter: a persistently failing import is retried
// (rescheduled, attempts climbing) and dead-lettered (state failed) after
// maxAttempts.
func TestRetryThenDeadLetter(t *testing.T) {
	ctx := context.Background()
	repo, pipe := newFakeRepo(), newFakePipeline()
	svc := NewService(repo, pipe, 1<<20, WithHTTPClient(&http.Client{}))
	origin := originServer(t)
	vid := seedVideo(pipe, uuid.New())

	// A non-video URL fails every attempt.
	if _, err := svc.Enqueue(ctx, vid, loopbackHost(origin.URL)+"/notavideo.txt", ResolverDirect); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	// Attempts 1..maxAttempts-1 reschedule (state pending, attempts climbing).
	for i := 1; i < maxAttempts; i++ {
		if _, err := svc.DrainJobs(ctx, 5); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
		j := repo.latest(vid)
		if j.State != "pending" || int(j.Attempts) != i {
			t.Fatalf("after attempt %d: state=%q attempts=%d, want pending/%d", i, j.State, j.Attempts, i)
		}
		repo.forceDue() // skip the backoff wait
	}
	// The final attempt dead-letters.
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("final drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State != "failed" || int(j.Attempts) != maxAttempts {
		t.Fatalf("dead-letter: state=%q attempts=%d, want failed/%d", j.State, j.Attempts, maxAttempts)
	}
	if j.Error == "" {
		t.Errorf("dead-lettered job has empty error, want a safe reason")
	}
}
