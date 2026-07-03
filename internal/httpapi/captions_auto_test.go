package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// captionJobFakeRepo is an in-memory captionjob.Repository for the handler
// tests: it enforces the single-active-job-per-video conflict and records the
// enqueued rows so status reads work. The worker paths (claim/complete/…) are
// present for interface satisfaction but unused here.
type captionJobFakeRepo struct {
	jobs  map[uuid.UUID]sqlcgen.CaptionJob
	order []uuid.UUID
}

func newCaptionJobFakeRepo() *captionJobFakeRepo {
	return &captionJobFakeRepo{jobs: map[uuid.UUID]sqlcgen.CaptionJob{}}
}

func (r *captionJobFakeRepo) EnqueueCaptionJob(_ context.Context, arg sqlcgen.EnqueueCaptionJobParams) (sqlcgen.CaptionJob, error) {
	for _, id := range r.order {
		if j := r.jobs[id]; j.VideoID == arg.VideoID && (j.State == "pending" || j.State == "running") {
			return sqlcgen.CaptionJob{}, pgx.ErrNoRows // active job exists
		}
	}
	now := time.Now()
	j := sqlcgen.CaptionJob{
		ID: uuid.New(), VideoID: arg.VideoID, Language: arg.Language, State: "pending",
		NextAttemptAt: now, CreatedAt: now, UpdatedAt: now,
	}
	r.jobs[j.ID] = j
	r.order = append(r.order, j.ID)
	return j, nil
}

func (r *captionJobFakeRepo) GetLatestCaptionJobByVideo(_ context.Context, videoID uuid.UUID) (sqlcgen.CaptionJob, error) {
	for i := len(r.order) - 1; i >= 0; i-- {
		if j := r.jobs[r.order[i]]; j.VideoID == videoID {
			return j, nil
		}
	}
	return sqlcgen.CaptionJob{}, pgx.ErrNoRows
}

func (r *captionJobFakeRepo) ClaimDueCaptionJobs(context.Context, int32) ([]sqlcgen.ClaimDueCaptionJobsRow, error) {
	return nil, nil
}
func (r *captionJobFakeRepo) CompleteCaptionJob(context.Context, uuid.UUID) error { return nil }
func (r *captionJobFakeRepo) RescheduleCaptionJob(context.Context, sqlcgen.RescheduleCaptionJobParams) error {
	return nil
}
func (r *captionJobFakeRepo) FailCaptionJob(context.Context, sqlcgen.FailCaptionJobParams) error {
	return nil
}

// captionJobBody is the { caption_job: {...} } response shape.
type captionJobBody struct {
	CaptionJob struct {
		ID       string `json:"id"`
		VideoID  string `json:"video_id"`
		Language string `json:"language"`
		State    string `json:"state"`
	} `json:"caption_job"`
}

func requestAutoCaption(srv *Server, videoID, body, token string) *httptest.ResponseRecorder {
	return postJSONAuth(srv, "/api/v1/videos/"+videoID+"/captions/auto", body, token)
}

func getAutoCaption(srv *Server, videoID, token string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/captions/auto", nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// whisperEnabledConfig is testConfig with auto-captioning turned on, so the
// request endpoint enqueues (202) instead of answering 503.
func whisperEnabledConfig() *config.Config {
	cfg := testConfig()
	cfg.WhisperEnabled = true
	return cfg
}

// TestAutoCaptionRequestAndStatus: an owner enqueues an auto-caption (202,
// default language) and then reads its status (200, pending).
func TestAutoCaptionRequestAndStatus(t *testing.T) {
	srv := videoServerCfg(t, whisperEnabledConfig())
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := requestAutoCaption(srv, vid, `{}`, owner)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got captionJobBody
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.CaptionJob.State != "pending" || got.CaptionJob.Language != "en" || got.CaptionJob.VideoID != vid {
		t.Fatalf("job = %+v, want pending/en for video %s", got.CaptionJob, vid)
	}

	// Status reflects the same job.
	rec = getAutoCaption(srv, vid, owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var status captionJobBody
	_ = json.Unmarshal(rec.Body.Bytes(), &status)
	if status.CaptionJob.ID != got.CaptionJob.ID {
		t.Errorf("status id = %s, want %s", status.CaptionJob.ID, got.CaptionJob.ID)
	}
}

// TestAutoCaptionRespectsLanguage: an explicit language is stored on the job.
func TestAutoCaptionRespectsLanguage(t *testing.T) {
	srv := videoServerCfg(t, whisperEnabledConfig())
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := requestAutoCaption(srv, vid, `{"language":"pt-BR"}`, owner)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("request = %d; body=%s", rec.Code, rec.Body.String())
	}
	var got captionJobBody
	_ = json.Unmarshal(rec.Body.Bytes(), &got)
	if got.CaptionJob.Language != "pt-BR" {
		t.Errorf("language = %q, want pt-BR", got.CaptionJob.Language)
	}
}

// TestAutoCaptionConflict: a second request while one is pending is 409.
func TestAutoCaptionConflict(t *testing.T) {
	srv := videoServerCfg(t, whisperEnabledConfig())
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)

	if rec := requestAutoCaption(srv, vid, `{}`, owner); rec.Code != http.StatusAccepted {
		t.Fatalf("first request = %d; body=%s", rec.Code, rec.Body.String())
	}
	rec := requestAutoCaption(srv, vid, `{}`, owner)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second request = %d, want 409; body=%s", rec.Code, rec.Body.String())
	}
}

// TestAutoCaptionDisabled: with auto-captioning off, the request endpoint is 503.
func TestAutoCaptionDisabled(t *testing.T) {
	srv := videoServer(t) // testConfig: WhisperEnabled=false
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	vid := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)

	rec := requestAutoCaption(srv, vid, `{}`, owner)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("disabled request = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	var env ErrorResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	if env.Error.Code != "service_unavailable" {
		t.Errorf("error code = %q, want service_unavailable", env.Error.Code)
	}
}

// TestAutoCaptionAuthAndOwnership covers the auth/ownership/validation matrix.
func TestAutoCaptionAuthAndOwnership(t *testing.T) {
	srv := videoServerCfg(t, whisperEnabledConfig())
	owner := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	other := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	vid := createPublishedVideo(t, srv, owner, "ada", `{"title":"Clip","privacy":"public"}`)

	// Anonymous → 401.
	if rec := requestAutoCaption(srv, vid, `{}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon request = %d, want 401", rec.Code)
	}
	// Non-owner → 404 (existence not leaked).
	if rec := requestAutoCaption(srv, vid, `{}`, other); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner request = %d, want 404", rec.Code)
	}
	// Unknown video → 404.
	if rec := requestAutoCaption(srv, uuid.New().String(), `{}`, owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown-video request = %d, want 404", rec.Code)
	}
	// Bad language tag → 422.
	if rec := requestAutoCaption(srv, vid, `{"language":"not a tag!"}`, owner); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad-language request = %d, want 422", rec.Code)
	}
	// Status with no job yet → 404.
	if rec := getAutoCaption(srv, vid, owner); rec.Code != http.StatusNotFound {
		t.Errorf("status with no job = %d, want 404", rec.Code)
	}
	// Status by non-owner → 404.
	if rec := getAutoCaption(srv, vid, other); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner status = %d, want 404", rec.Code)
	}
}
