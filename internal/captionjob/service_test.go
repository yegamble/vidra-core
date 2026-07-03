package captionjob

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
	"github.com/vidra/vidra-core/internal/video"
)

// ---- fake repository ------------------------------------------------------

type fakeRepo struct {
	jobs  map[uuid.UUID]sqlcgen.CaptionJob
	order []uuid.UUID
}

func newFakeRepo() *fakeRepo { return &fakeRepo{jobs: map[uuid.UUID]sqlcgen.CaptionJob{}} }

func (r *fakeRepo) EnqueueCaptionJob(_ context.Context, arg sqlcgen.EnqueueCaptionJobParams) (sqlcgen.CaptionJob, error) {
	for _, id := range r.order {
		if j := r.jobs[id]; j.VideoID == arg.VideoID && (j.State == "pending" || j.State == "running") {
			return sqlcgen.CaptionJob{}, pgx.ErrNoRows
		}
	}
	now := time.Now()
	j := sqlcgen.CaptionJob{ID: uuid.New(), VideoID: arg.VideoID, Language: arg.Language, State: "pending", NextAttemptAt: now, CreatedAt: now, UpdatedAt: now}
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
	return j, nil
}
func (r *fakeRepo) GetLatestCaptionJobByVideo(_ context.Context, videoID uuid.UUID) (sqlcgen.CaptionJob, error) {
	for i := len(r.order) - 1; i >= 0; i-- {
		if j := r.jobs[r.order[i]]; j.VideoID == videoID {
			return j, nil
		}
	}
	return sqlcgen.CaptionJob{}, pgx.ErrNoRows
}
func (r *fakeRepo) ClaimDueCaptionJobs(_ context.Context, limit int32) ([]sqlcgen.ClaimDueCaptionJobsRow, error) {
	var rows []sqlcgen.ClaimDueCaptionJobsRow
	for _, id := range r.order {
		j := r.jobs[id]
		if j.State == "pending" && !j.NextAttemptAt.After(time.Now()) {
			j.State = "running"
			r.jobs[id] = j
			rows = append(rows, sqlcgen.ClaimDueCaptionJobsRow{ID: j.ID, VideoID: j.VideoID, Language: j.Language, Attempts: j.Attempts})
			if int32(len(rows)) >= limit {
				break
			}
		}
	}
	return rows, nil
}
func (r *fakeRepo) CompleteCaptionJob(_ context.Context, id uuid.UUID) error {
	j := r.jobs[id]
	j.State, j.Error = "done", ""
	r.jobs[id] = j
	return nil
}
func (r *fakeRepo) RescheduleCaptionJob(_ context.Context, arg sqlcgen.RescheduleCaptionJobParams) error {
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.NextAttemptAt, j.Error = "pending", j.Attempts+1, arg.NextAttemptAt, arg.Error
	r.jobs[arg.ID] = j
	return nil
}
func (r *fakeRepo) FailCaptionJob(_ context.Context, arg sqlcgen.FailCaptionJobParams) error {
	j := r.jobs[arg.ID]
	j.State, j.Attempts, j.Error = "failed", j.Attempts+1, arg.Error
	r.jobs[arg.ID] = j
	return nil
}

// forceDue rewinds every pending job so the next drain re-claims it (exercising
// the retry ladder without real backoff waits).
func (r *fakeRepo) forceDue() {
	for id, j := range r.jobs {
		if j.State == "pending" {
			j.NextAttemptAt = time.Now().Add(-time.Hour)
			r.jobs[id] = j
		}
	}
}
func (r *fakeRepo) latest(videoID uuid.UUID) sqlcgen.CaptionJob {
	j, _ := r.GetLatestCaptionJobByVideo(context.Background(), videoID)
	return j
}

// ---- fake video store -----------------------------------------------------

type storedCaption struct {
	owner    uuid.UUID
	language string
	vtt      []byte
}

type fakeVideoStore struct {
	owner      uuid.UUID
	origKey    string
	origErr    error
	getErr     error
	captionErr error
	stored     []storedCaption
}

func (s *fakeVideoStore) GetByID(_ context.Context, id uuid.UUID) (sqlcgen.GetVideoByIDRow, error) {
	if s.getErr != nil {
		return sqlcgen.GetVideoByIDRow{}, s.getErr
	}
	return sqlcgen.GetVideoByIDRow{ID: id, OwnerID: s.owner}, nil
}
func (s *fakeVideoStore) OriginalFileKey(_ context.Context, _ uuid.UUID) (string, error) {
	if s.origErr != nil {
		return "", s.origErr
	}
	key := s.origKey
	if key == "" {
		key = "web-videos/x.mp4"
	}
	return key, nil
}
func (s *fakeVideoStore) AddCaption(_ context.Context, ownerID, _ uuid.UUID, in video.CaptionInput) (sqlcgen.Caption, error) {
	if s.captionErr != nil {
		return sqlcgen.Caption{}, s.captionErr
	}
	data, _ := io.ReadAll(in.Reader)
	s.stored = append(s.stored, storedCaption{owner: ownerID, language: in.Language, vtt: data})
	return sqlcgen.Caption{Language: in.Language, Label: in.Label}, nil
}

// ---- fake notifier --------------------------------------------------------

type fakeNotifier struct {
	calls int
	last  uuid.UUID
}

func (n *fakeNotifier) NotifyCaptionReady(_ context.Context, recipientID, _ uuid.UUID) error {
	n.calls++
	n.last = recipientID
	return nil
}

// ---- helpers --------------------------------------------------------------

// stubExtractor writes a tiny placeholder WAV to a temp file — the fake Whisper
// server ignores the bytes, so no ffmpeg is needed.
func stubExtractor(_ context.Context, _ string) (string, func(), error) {
	f, err := os.CreateTemp("", "captionjob-stub-*.wav")
	if err != nil {
		return "", func() {}, err
	}
	_, _ = f.WriteString("RIFF----WAVE")
	name := f.Name()
	_ = f.Close()
	return name, func() { _ = os.Remove(name) }, nil
}

// fakeWhisperServer serves the whisper.cpp /inference contract; success returns
// canned verbose_json, and a non-2xx when fail is true.
func fakeWhisperServer(t *testing.T, fail bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"segments":[{"start":0.0,"end":1.5,"text":" hello world"}]}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newWhisperTranscriber(endpoint string) Transcriber {
	return media.NewWhisperClient(nil, endpoint,
		media.WithAudioExtractor(stubExtractor),
		media.WithWhisperHTTPClient(&http.Client{}),
	)
}

// ---- tests ----------------------------------------------------------------

// TestEnqueueGating covers the disabled / language / conflict rules.
func TestEnqueueGating(t *testing.T) {
	ctx := context.Background()
	vid := uuid.New()

	// Disabled instance.
	off := NewService(newFakeRepo(), &fakeVideoStore{}, nil)
	if _, err := off.Enqueue(ctx, vid, ""); err != ErrDisabled {
		t.Errorf("disabled enqueue err = %v, want ErrDisabled", err)
	}

	repo := newFakeRepo()
	svc := NewService(repo, &fakeVideoStore{owner: uuid.New()}, nil, WithEnabled(true), WithDefaultLanguage("en"))

	// Bad language.
	if _, err := svc.Enqueue(ctx, vid, "not a tag!"); err != ErrInvalidLanguage {
		t.Errorf("bad-language enqueue err = %v, want ErrInvalidLanguage", err)
	}
	// Default language applied when omitted.
	job, err := svc.Enqueue(ctx, vid, "")
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.Language != "en" || job.State != StatePending {
		t.Errorf("job = %+v, want en/pending", job)
	}
	// Second request while active → conflict.
	if _, err := svc.Enqueue(ctx, vid, "en"); err != ErrJobActive {
		t.Errorf("second enqueue err = %v, want ErrJobActive", err)
	}
}

// TestDrainHappyPath: the worker transcribes against a fake Whisper server,
// stores the caption, and notifies the owner.
func TestDrainHappyPath(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	vid := uuid.New()
	repo := newFakeRepo()
	store := &fakeVideoStore{owner: owner}
	notifier := &fakeNotifier{}
	whisper := fakeWhisperServer(t, false)
	svc := NewService(repo, store, newWhisperTranscriber(whisper.URL),
		WithEnabled(true), WithNotifier(notifier))

	if _, err := svc.Enqueue(ctx, vid, "en"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	n, err := svc.DrainJobs(ctx, 5)
	if err != nil || n != 1 {
		t.Fatalf("drain = (%d,%v), want (1,nil)", n, err)
	}
	if j := repo.latest(vid); j.State != StateDone {
		t.Fatalf("job state = %q (err=%q), want done", j.State, j.Error)
	}
	if len(store.stored) != 1 {
		t.Fatalf("stored captions = %d, want 1", len(store.stored))
	}
	got := store.stored[0]
	if got.owner != owner || got.language != "en" {
		t.Errorf("caption owner/lang = %s/%s, want %s/en", got.owner, got.language, owner)
	}
	if !strings.HasPrefix(string(got.vtt), "WEBVTT") || !strings.Contains(string(got.vtt), "hello world") {
		t.Errorf("stored VTT unexpected:\n%s", got.vtt)
	}
	if notifier.calls != 1 || notifier.last != owner {
		t.Errorf("notifier calls=%d last=%s, want 1/%s", notifier.calls, notifier.last, owner)
	}
}

// TestDrainRetriesThenDeadLetters: a persistently failing transcription is
// retried (rescheduled, attempts climbing) and dead-lettered after maxAttempts,
// with a safe error that never leaks the endpoint URL, and no caption stored.
func TestDrainRetriesThenDeadLetters(t *testing.T) {
	ctx := context.Background()
	vid := uuid.New()
	repo := newFakeRepo()
	store := &fakeVideoStore{owner: uuid.New()}
	whisper := fakeWhisperServer(t, true) // 500 every time
	svc := NewService(repo, store, newWhisperTranscriber(whisper.URL), WithEnabled(true))

	if _, err := svc.Enqueue(ctx, vid, "en"); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for i := 1; i < maxAttempts; i++ {
		if _, err := svc.DrainJobs(ctx, 5); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
		j := repo.latest(vid)
		if j.State != StatePending || int(j.Attempts) != i {
			t.Fatalf("after attempt %d: state=%q attempts=%d, want pending/%d", i, j.State, j.Attempts, i)
		}
		repo.forceDue()
	}
	if _, err := svc.DrainJobs(ctx, 5); err != nil {
		t.Fatalf("final drain: %v", err)
	}
	j := repo.latest(vid)
	if j.State != StateFailed || int(j.Attempts) != maxAttempts {
		t.Fatalf("dead-letter: state=%q attempts=%d, want failed/%d", j.State, j.Attempts, maxAttempts)
	}
	if j.Error == "" || strings.Contains(j.Error, whisper.URL) {
		t.Errorf("job error = %q, want a safe reason that does not leak the endpoint URL", j.Error)
	}
	if len(store.stored) != 0 {
		t.Errorf("a caption was stored despite the transcription failing")
	}
}

// TestDrainDisabledIsNoop: DrainJobs does nothing when auto-captioning is off.
func TestDrainDisabledIsNoop(t *testing.T) {
	svc := NewService(newFakeRepo(), &fakeVideoStore{}, nil)
	if n, err := svc.DrainJobs(context.Background(), 5); err != nil || n != 0 {
		t.Errorf("disabled drain = (%d,%v), want (0,nil)", n, err)
	}
}

// TestLatestForVideoNotFound: a video that never requested captioning → ErrNotFound.
func TestLatestForVideoNotFound(t *testing.T) {
	svc := NewService(newFakeRepo(), &fakeVideoStore{}, nil, WithEnabled(true))
	if _, err := svc.LatestForVideo(context.Background(), uuid.New()); err != ErrNotFound {
		t.Errorf("LatestForVideo err = %v, want ErrNotFound", err)
	}
}
