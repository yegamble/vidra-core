package uploadfinalize

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/upload"
	"github.com/vidra/vidra-core/internal/video"
)

// ---- fakes ----------------------------------------------------------------

// fakeJobRepo is an in-memory upload_finalize_jobs queue. The mutex is not
// decoration: DrainJobs runs its claimed batch on a bounded pool, so -race
// exercises concurrent access.
type fakeJobRepo struct {
	mu      sync.Mutex
	jobs    map[uuid.UUID]sqlcgen.UploadFinalizeJob
	order   []uuid.UUID
	leases  int
	liveErr error
}

func newFakeJobRepo() *fakeJobRepo {
	return &fakeJobRepo{jobs: map[uuid.UUID]sqlcgen.UploadFinalizeJob{}}
}

func (r *fakeJobRepo) EnqueueUploadFinalizeJob(_ context.Context, arg sqlcgen.EnqueueUploadFinalizeJobParams) (sqlcgen.UploadFinalizeJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, id := range r.order {
		if j := r.jobs[id]; j.UploadID == arg.UploadID && (j.State == StatePending || j.State == StateRunning) {
			return sqlcgen.UploadFinalizeJob{}, pgx.ErrNoRows
		}
	}
	j := sqlcgen.UploadFinalizeJob{
		ID: uuid.New(), UploadID: arg.UploadID, VideoID: arg.VideoID,
		Purpose: arg.Purpose, CanManage: arg.CanManage, State: StatePending,
		NextAttemptAt: time.Now(), CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
	return j, nil
}

func (r *fakeJobRepo) GetLatestUploadFinalizeJob(_ context.Context, uploadID uuid.UUID) (sqlcgen.UploadFinalizeJob, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.order) - 1; i >= 0; i-- {
		if j := r.jobs[r.order[i]]; j.UploadID == uploadID {
			return j, nil
		}
	}
	return sqlcgen.UploadFinalizeJob{}, pgx.ErrNoRows
}

func (r *fakeJobRepo) ClaimDueUploadFinalizeJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueUploadFinalizeJobsRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var rows []sqlcgen.ClaimDueUploadFinalizeJobsRow
	for _, id := range r.order {
		j := r.jobs[id]
		if j.State != StatePending || j.NextAttemptAt.After(time.Now()) {
			continue
		}
		j.State = StateRunning
		r.jobs[id] = j
		rows = append(rows, sqlcgen.ClaimDueUploadFinalizeJobsRow{
			ID: j.ID, UploadID: j.UploadID, VideoID: j.VideoID,
			Purpose: j.Purpose, CanManage: j.CanManage, Attempts: j.Attempts,
		})
		if int32(len(rows)) >= limit {
			break
		}
	}
	return rows, nil
}

func (r *fakeJobRepo) HasLiveUploadFinalizeJob(_ context.Context, uploadID uuid.UUID) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.liveErr != nil {
		return false, r.liveErr
	}
	for _, id := range r.order {
		if j := r.jobs[id]; j.UploadID == uploadID && (j.State == StatePending || j.State == StateRunning) {
			return true, nil
		}
	}
	return false, nil
}

func (r *fakeJobRepo) DeleteUploadFinalizeJob(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.jobs, id)
	for i, x := range r.order {
		if x == id {
			r.order = append(r.order[:i], r.order[i+1:]...)
			break
		}
	}
	return nil
}

func (r *fakeJobRepo) RenewUploadFinalizeJobLease(_ context.Context, _ uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.leases++
	return nil
}

func (r *fakeJobRepo) CompleteUploadFinalizeJob(_ context.Context, id uuid.UUID) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[id]
	j.State, j.Error = StateDone, ""
	r.jobs[id] = j
	return nil
}

func (r *fakeJobRepo) RescheduleUploadFinalizeJob(_ context.Context, arg sqlcgen.RescheduleUploadFinalizeJobParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.NextAttemptAt, j.Error = StatePending, j.Attempts+1, arg.NextAttemptAt, arg.Error
	r.jobs[arg.ID] = j
	return nil
}

func (r *fakeJobRepo) FailUploadFinalizeJob(_ context.Context, arg sqlcgen.FailUploadFinalizeJobParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.Error = StateFailed, j.Attempts+1, arg.Error
	r.jobs[arg.ID] = j
	return nil
}

func (r *fakeJobRepo) get(id uuid.UUID) sqlcgen.UploadFinalizeJob {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[id]
}

func (r *fakeJobRepo) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.order)
}

// fakeSessionRepo is an in-memory upload.Repository — enough of it to open a
// session, land its chunks and drive the state machine.
type fakeSessionRepo struct {
	mu       sync.Mutex
	sessions map[uuid.UUID]sqlcgen.UploadSession
	chunks   map[uuid.UUID]map[int32]int64
}

func newFakeSessionRepo() *fakeSessionRepo {
	return &fakeSessionRepo{
		sessions: map[uuid.UUID]sqlcgen.UploadSession{},
		chunks:   map[uuid.UUID]map[int32]int64{},
	}
}

func (r *fakeSessionRepo) CreateUploadSession(_ context.Context, arg sqlcgen.CreateUploadSessionParams) (sqlcgen.UploadSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := sqlcgen.UploadSession{
		ID: uuid.New(), VideoID: arg.VideoID, UserID: arg.UserID, Filename: arg.Filename,
		TotalSize: arg.TotalSize, ChunkSize: arg.ChunkSize, State: upload.StateActive,
		ExpiresAt: arg.ExpiresAt, FileFingerprint: arg.FileFingerprint, Purpose: arg.Purpose,
	}
	r.sessions[s.ID] = s
	return s, nil
}

func (r *fakeSessionRepo) CountActiveUploadSessionsForUser(context.Context, uuid.UUID) (int64, error) {
	return 0, nil
}

func (r *fakeSessionRepo) HasActiveReplaceSessionForVideo(context.Context, uuid.UUID) (bool, error) {
	return false, nil
}

func (r *fakeSessionRepo) GetUploadSession(_ context.Context, id uuid.UUID) (sqlcgen.UploadSession, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok {
		return sqlcgen.UploadSession{}, pgx.ErrNoRows
	}
	return s, nil
}

func (r *fakeSessionRepo) UpsertUploadChunk(_ context.Context, arg sqlcgen.UpsertUploadChunkParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.chunks[arg.UploadID] == nil {
		r.chunks[arg.UploadID] = map[int32]int64{}
	}
	r.chunks[arg.UploadID][arg.N] = arg.SizeBytes
	return nil
}

func (r *fakeSessionRepo) ListUploadChunks(_ context.Context, uploadID uuid.UUID) ([]sqlcgen.ListUploadChunksRow, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows := make([]sqlcgen.ListUploadChunksRow, 0, len(r.chunks[uploadID]))
	for n, size := range r.chunks[uploadID] {
		rows = append(rows, sqlcgen.ListUploadChunksRow{N: n, SizeBytes: size})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].N < rows[j].N })
	return rows, nil
}

func (r *fakeSessionRepo) ListActiveUploadSessionsForUser(context.Context, sqlcgen.ListActiveUploadSessionsForUserParams) ([]sqlcgen.ListActiveUploadSessionsForUserRow, error) {
	return nil, nil
}

func (r *fakeSessionRepo) SetUploadSessionState(_ context.Context, arg sqlcgen.SetUploadSessionStateParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[arg.ID]
	s.State, s.FailureReason = arg.State, ""
	r.sessions[arg.ID] = s
	return nil
}

// MarkUploadSessionQueued mirrors the SQL's CAS on state = 'active'.
func (r *fakeSessionRepo) MarkUploadSessionQueued(_ context.Context, id uuid.UUID) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[id]
	if !ok || s.State != upload.StateActive {
		return 0, nil
	}
	s.State, s.FailureReason = upload.StateQueued, ""
	r.sessions[id] = s
	return 1, nil
}

func (r *fakeSessionRepo) FailUploadSession(_ context.Context, arg sqlcgen.FailUploadSessionParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sessions[arg.ID]
	s.State, s.FailureReason = upload.StateFailed, arg.FailureReason
	r.sessions[arg.ID] = s
	return nil
}

func (r *fakeSessionRepo) ListSweepableUploadSessions(context.Context, int32) ([]uuid.UUID, error) {
	return nil, nil
}

func (r *fakeSessionRepo) DeleteUploadSession(context.Context, uuid.UUID) error { return nil }

func (r *fakeSessionRepo) state(id uuid.UUID) sqlcgen.UploadSession {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sessions[id]
}

// fakePipeline records what the worker asked the video layer to do. It is a
// stand-in for *video.Service specifically so these tests never touch ffmpeg —
// the real Process shells out to ffprobe, which is build-tagged out of the
// default gate.
type fakePipeline struct {
	mu sync.Mutex

	ownerID uuid.UUID

	attached  []byte // the bytes AttachOriginal was handed
	attachErr error

	processedKey string
	processErr   error

	replaced    []byte
	replaceErr  error
	replaceMgmt bool

	getErr error
}

func (p *fakePipeline) GetByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if p.getErr != nil {
		return sqlcgen.GetVideoByIDRow{}, p.getErr
	}
	return sqlcgen.GetVideoByIDRow{ID: id, OwnerID: p.ownerID, State: "draft"}, nil
}

func (p *fakePipeline) AttachOriginal(_ context.Context, ownerID, videoID uuid.UUID, in video.UploadInput) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	body, _ := io.ReadAll(in.Reader)
	p.mu.Lock()
	p.attached = body
	p.mu.Unlock()
	if p.attachErr != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, p.attachErr
	}
	if ownerID != p.ownerID {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, errors.New("attributed to the wrong owner")
	}
	return sqlcgen.Video{ID: videoID, State: "processing"},
		sqlcgen.VideoFile{VideoID: videoID, Kind: "original", StorageKey: "web-videos/" + videoID.String() + ".mp4"}, nil
}

func (p *fakePipeline) Process(_ context.Context, videoID uuid.UUID, originalKey string) (sqlcgen.Video, error) {
	p.mu.Lock()
	p.processedKey = originalKey
	p.mu.Unlock()
	if p.processErr != nil {
		return sqlcgen.Video{}, p.processErr
	}
	return sqlcgen.Video{ID: videoID, State: "published"}, nil
}

func (p *fakePipeline) ReplaceSource(_ context.Context, _, videoID uuid.UUID, in video.UploadInput, canManage bool) (sqlcgen.Video, sqlcgen.VideoFile, error) {
	body, _ := io.ReadAll(in.Reader)
	p.mu.Lock()
	p.replaced, p.replaceMgmt = body, canManage
	p.mu.Unlock()
	if p.replaceErr != nil {
		return sqlcgen.Video{}, sqlcgen.VideoFile{}, p.replaceErr
	}
	return sqlcgen.Video{ID: videoID, State: "published"},
		sqlcgen.VideoFile{VideoID: videoID, Kind: "original", StorageKey: "web-videos/" + videoID.String() + ".r2.mp4"}, nil
}

// ---- harness ---------------------------------------------------------------

type harness struct {
	svc      *Service
	jobs     *fakeJobRepo
	sessions *fakeSessionRepo
	uploads  *upload.Service
	pipeline *fakePipeline
	blobs    storage.Backend
	owner    uuid.UUID
	video    uuid.UUID
}

func newHarness(t *testing.T, opts ...Option) *harness {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage: %v", err)
	}
	sessions := newFakeSessionRepo()
	uploads := upload.NewService(sessions, blobs, upload.WithChunkSize(4))
	pipeline := &fakePipeline{ownerID: uuid.New()}
	jobs := newFakeJobRepo()
	return &harness{
		svc:      NewService(jobs, uploads, pipeline, opts...),
		jobs:     jobs,
		sessions: sessions,
		uploads:  uploads,
		pipeline: pipeline,
		blobs:    blobs,
		owner:    pipeline.ownerID,
		video:    uuid.New(),
	}
}

// openAndFill opens a session for payload and lands every chunk, leaving it
// exactly where the request handler finds it.
func (h *harness) openAndFill(t *testing.T, payload string, purpose string) sqlcgen.UploadSession {
	t.Helper()
	ctx := context.Background()
	user := uuid.New()
	sess, err := h.uploads.CreateSession(ctx, h.video, user, "clip.mp4", int64(len(payload)), "", purpose)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	for n := 0; n*4 < len(payload); n++ {
		end := (n + 1) * 4
		if end > len(payload) {
			end = len(payload)
		}
		if _, err := h.uploads.PutChunk(ctx, sess.ID, user, n, bytes.NewReader([]byte(payload[n*4:end]))); err != nil {
			t.Fatalf("put chunk %d: %v", n, err)
		}
	}
	return sess
}

// ---- tests -----------------------------------------------------------------

// TestFinalizeRunsThePipeline is the happy path the request used to run inline:
// the worker assembles the chunks in order, hands them to AttachOriginal
// attributed to the channel OWNER, runs Process (which is where the transcode
// enqueue lives), and settles both the job and the session.
func TestFinalizeRunsThePipeline(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCDEFGHIJ", upload.PurposeUpload) // 4/4/2

	job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if got := h.sessions.state(sess.ID).State; got != upload.StateQueued {
		t.Fatalf("session after enqueue = %q, want queued", got)
	}

	done, err := h.svc.DrainJobs(ctx, 5)
	if err != nil || done != 1 {
		t.Fatalf("drain = %d, %v; want 1, nil", done, err)
	}
	if got := string(h.pipeline.attached); got != "ABCDEFGHIJ" {
		t.Errorf("assembled bytes = %q, want the whole file in chunk order", got)
	}
	if h.pipeline.processedKey == "" {
		t.Error("Process was never called — the transcode enqueue lives behind it")
	}
	if got := h.jobs.get(job.ID).State; got != StateDone {
		t.Errorf("job state = %q, want done", got)
	}
	s := h.sessions.state(sess.ID)
	if s.State != upload.StateCompleted || s.FailureReason != "" {
		t.Errorf("session = %q/%q, want completed with no reason", s.State, s.FailureReason)
	}
	// The consumed chunk blobs are gone.
	if ex, _ := h.blobs.Exists(ctx, "uploads/"+sess.ID.String()+"/0"); ex {
		t.Error("chunk blob survived a successful finalize")
	}
}

// TestEnqueueIsIdempotent: a client retrying a completion it never saw the
// response to must not queue the pipeline twice.
func TestEnqueueIsIdempotent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)

	first, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	second, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("re-enqueue: %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("re-enqueue produced job %s, want the in-flight %s", second.ID, first.ID)
	}
	if n := h.jobs.count(); n != 1 {
		t.Errorf("queued jobs = %d, want 1", n)
	}
	if done, _ := h.svc.DrainJobs(ctx, 5); done != 1 {
		t.Errorf("drain completed %d jobs, want 1", done)
	}
}

// TestFinalizeFailureRetriesThenMarksTheSessionFailed: a pipeline failure is
// retried with backoff (the session stays 'processing' — the work is still in
// flight), and on dead-letter the session carries a client-visible reason,
// because a poller has nowhere else to read one.
func TestFinalizeFailureRetriesThenMarksTheSessionFailed(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.pipeline.attachErr = video.ErrUnsupportedMedia
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)

	job, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	for attempt := 1; attempt < 5; attempt++ {
		if done, _ := h.svc.DrainJobs(ctx, 5); done != 0 {
			t.Fatalf("attempt %d completed %d jobs, want 0", attempt, done)
		}
		j := h.jobs.get(job.ID)
		if j.State != StatePending || int(j.Attempts) != attempt {
			t.Fatalf("attempt %d: job = %q/%d, want pending/%d", attempt, j.State, j.Attempts, attempt)
		}
		if !j.NextAttemptAt.After(time.Now()) {
			t.Errorf("attempt %d scheduled no backoff", attempt)
		}
		if got := h.sessions.state(sess.ID).State; got != upload.StateProcessing {
			t.Errorf("attempt %d: session = %q, want processing between attempts", attempt, got)
		}
		// Make the retry due so the loop can drive it without waiting.
		h.jobs.mu.Lock()
		row := h.jobs.jobs[job.ID]
		row.NextAttemptAt = time.Now().Add(-time.Second)
		h.jobs.jobs[job.ID] = row
		h.jobs.mu.Unlock()
	}

	// The fifth attempt dead-letters.
	if done, _ := h.svc.DrainJobs(ctx, 5); done != 0 {
		t.Fatal("the dead-lettering attempt reported a completion")
	}
	if got := h.jobs.get(job.ID).State; got != StateFailed {
		t.Errorf("job state = %q, want failed", got)
	}
	s := h.sessions.state(sess.ID)
	if s.State != upload.StateFailed {
		t.Fatalf("session = %q, want failed", s.State)
	}
	if s.FailureReason == "" {
		t.Error("session failure_reason is empty — the poller has nothing true to show")
	}
	if got := s.FailureReason; got == video.ErrUnsupportedMedia.Error() {
		t.Errorf("failure_reason = %q — it must be the safe sentence, not the raw error", got)
	}
}

// TestFinalizeReplaceSwapsTheSource: a replace-purpose session runs
// ReplaceSource with the authorisation the REQUEST decided (can_manage travels
// on the job row — a worker has no principal), and fires the re-transcode hook.
func TestFinalizeReplaceSwapsTheSource(t *testing.T) {
	ctx := context.Background()
	var hookedVideo uuid.UUID
	var hookedKey string
	h := newHarness(t, WithReplaceHook(func(_ context.Context, videoID uuid.UUID, sourceKey string) {
		hookedVideo, hookedKey = videoID, sourceKey
	}))
	sess := h.openAndFill(t, "NEWBYTES", upload.PurposeReplace)

	if _, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeReplace, true); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if done, err := h.svc.DrainJobs(ctx, 5); err != nil || done != 1 {
		t.Fatalf("drain = %d, %v; want 1, nil", done, err)
	}
	if got := string(h.pipeline.replaced); got != "NEWBYTES" {
		t.Errorf("replacement bytes = %q, want NEWBYTES", got)
	}
	if !h.pipeline.replaceMgmt {
		t.Error("can_manage did not reach ReplaceSource — a staff/collaborator replacement would be refused")
	}
	if h.pipeline.attached != nil {
		t.Error("a replace session must not run the attach-and-publish path")
	}
	if hookedVideo != h.video || hookedKey == "" {
		t.Errorf("replace hook got %s/%q, want the video + its new source key", hookedVideo, hookedKey)
	}
}

// TestFinalizeRejectedReplaceSurfacesItsReason: video.ReplaceSource's own
// rejection (a failed scan or an unplayable file) is what the creator reads, not
// a generic failure.
func TestFinalizeRejectedReplaceSurfacesItsReason(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	h.pipeline.replaceErr = &video.ReplaceRejectedError{Reason: "the file is not a playable video"}
	sess := h.openAndFill(t, "BAD", upload.PurposeReplace)

	if _, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeReplace, false); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	// Drive it straight to the dead-letter.
	for i := 0; i < 5; i++ {
		_, _ = h.svc.DrainJobs(ctx, 5)
		h.jobs.mu.Lock()
		for id, j := range h.jobs.jobs {
			j.NextAttemptAt = time.Now().Add(-time.Second)
			h.jobs.jobs[id] = j
		}
		h.jobs.mu.Unlock()
	}
	if got := h.sessions.state(sess.ID).FailureReason; got != "the file is not a playable video" {
		t.Errorf("failure_reason = %q, want the rejection's own reason", got)
	}
}

// TestCancelLandingMidEnqueueDoesNotResurrectTheSession drives the window the
// state CAS exists for.
//
// A DELETE /uploads/{id} can land between the completion's ValidateComplete and
// its Enqueue. Cancel flips the session to 'cancelled' and deletes the chunk
// BLOBS — but the chunk LEDGER survives, so every "are all the chunks here?"
// check still passes. Writing 'queued' unconditionally would therefore undo the
// user's cancel AND hand the worker a job whose bytes are gone, which it would
// only discover after burning all five attempts (~15 minutes of backoff).
//
// So: the session stays cancelled, the job is dropped rather than dead-lettered,
// Enqueue reports ErrNotActive (the handler's 409), and a drain finds nothing.
func TestCancelLandingMidEnqueueDoesNotResurrectTheSession(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)

	// The request validates while the session is still active...
	if _, err := h.uploads.ValidateComplete(ctx, sess.ID, sess.UserID); err != nil {
		t.Fatalf("validate complete: %v", err)
	}
	// ...and the DELETE lands before the enqueue.
	if err := h.uploads.Cancel(ctx, sess.ID, sess.UserID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	_, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false)
	if !errors.Is(err, upload.ErrNotActive) {
		t.Fatalf("enqueue after a racing cancel = %v, want ErrNotActive (the 409 path)", err)
	}
	if got := h.sessions.state(sess.ID).State; got != upload.StateCancelled {
		t.Errorf("session state = %q, want cancelled — the user's cancel must stand", got)
	}
	if n := h.jobs.count(); n != 0 {
		t.Errorf("finalize jobs = %d, want 0 (the job must be dropped, not dead-lettered)", n)
	}
	if done, err := h.svc.DrainJobs(ctx, 5); err != nil || done != 0 {
		t.Fatalf("drain = %d, %v; want 0, nil — there is nothing to run", done, err)
	}
	// And the chunk blobs Cancel removed stay removed.
	if ex, _ := h.blobs.Exists(ctx, "uploads/"+sess.ID.String()+"/0"); ex {
		t.Error("a chunk blob came back after the cancel")
	}
}

// TestEnqueueRefusesASessionThatIsNoLongerActive is the same guard from the
// other side: a session already cancelled (or completed) never queues work, even
// if a caller reaches Enqueue directly.
func TestEnqueueRefusesASessionThatIsNoLongerActive(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)
	if err := h.uploads.Cancel(ctx, sess.ID, sess.UserID); err != nil {
		t.Fatalf("cancel: %v", err)
	}

	if _, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false); !errors.Is(err, upload.ErrNotActive) {
		t.Fatalf("enqueue on a cancelled session = %v, want ErrNotActive", err)
	}
	if n := h.jobs.count(); n != 0 {
		t.Errorf("finalize jobs = %d, want 0", n)
	}
}

// TestHasLiveJobReportsFalseOnLookupError pins the failure posture of the
// reporting seam. It is the opposite of transcode.HasLiveJob's fail-busy default,
// and deliberately so: this answer only rewrites a REPORTED state, so answering
// true on an error would tell a client its upload is queued when nothing is
// queued — a claim no later poll could correct. Answering false merely leaves
// the raw state alone.
func TestHasLiveJobReportsFalseOnLookupError(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)
	if _, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !h.svc.HasLiveJob(ctx, sess.ID) {
		t.Fatal("a freshly enqueued job should read as live")
	}

	h.jobs.mu.Lock()
	h.jobs.liveErr = errors.New("connection reset")
	h.jobs.mu.Unlock()
	if h.svc.HasLiveJob(ctx, sess.ID) {
		t.Error("a failed lookup reported a live job — it must not invent one")
	}
}

// TestHasLiveJobFollowsTheJobLifecycle: only a pending/running job counts, so a
// settled session stops being coalesced and reports its real terminal state.
func TestHasLiveJobFollowsTheJobLifecycle(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	sess := h.openAndFill(t, "ABCD", upload.PurposeUpload)

	if h.svc.HasLiveJob(ctx, sess.ID) {
		t.Error("no completion requested yet, so there is no live job")
	}
	if _, err := h.svc.Enqueue(ctx, sess.ID, h.video, upload.PurposeUpload, false); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if !h.svc.HasLiveJob(ctx, sess.ID) {
		t.Error("a queued job is live")
	}
	if done, err := h.svc.DrainJobs(ctx, 5); err != nil || done != 1 {
		t.Fatalf("drain = %d, %v; want 1, nil", done, err)
	}
	if h.svc.HasLiveJob(ctx, sess.ID) {
		t.Error("a finished job is not live — the session reports its own terminal state")
	}
}
