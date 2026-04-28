package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type stubRunnerRepo struct {
	createdToken *domain.RemoteRunnerRegistrationToken
	runner       *domain.RemoteRunner
	assignments  map[string]*domain.RemoteRunnerJobAssignment
}

func (s *stubRunnerRepo) ListRunners(context.Context) ([]*domain.RemoteRunner, error) {
	if s.runner == nil {
		return []*domain.RemoteRunner{}, nil
	}
	return []*domain.RemoteRunner{s.runner}, nil
}
func (s *stubRunnerRepo) GetRunner(_ context.Context, runnerID uuid.UUID) (*domain.RemoteRunner, error) {
	if s.runner != nil && s.runner.ID == runnerID {
		return s.runner, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubRunnerRepo) GetRunnerByToken(_ context.Context, token string) (*domain.RemoteRunner, error) {
	if s.runner != nil && s.runner.Token == token {
		// Return a copy to prevent authenticateRunner's `runner.Token = ""` from
		// mutating the stored runner and breaking subsequent auth calls.
		copy := *s.runner
		return &copy, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubRunnerRepo) TouchRunner(context.Context, uuid.UUID) error { return nil }
func (s *stubRunnerRepo) CreateRegistrationToken(_ context.Context, _ *uuid.UUID, _ *time.Time) (*domain.RemoteRunnerRegistrationToken, error) {
	s.createdToken = &domain.RemoteRunnerRegistrationToken{ID: uuid.New(), Token: "reg-token"}
	return s.createdToken, nil
}
func (s *stubRunnerRepo) ListRegistrationTokens(context.Context) ([]*domain.RemoteRunnerRegistrationToken, error) {
	if s.createdToken == nil {
		return []*domain.RemoteRunnerRegistrationToken{}, nil
	}
	return []*domain.RemoteRunnerRegistrationToken{s.createdToken}, nil
}
func (s *stubRunnerRepo) DeleteRegistrationToken(context.Context, uuid.UUID) error { return nil }
func (s *stubRunnerRepo) RegisterRunner(_ context.Context, input domain.RegisterRunnerInput) (*domain.RemoteRunner, error) {
	if input.RegistrationToken != "reg-token" {
		return nil, domain.ErrNotFound
	}
	s.runner = &domain.RemoteRunner{
		ID:            uuid.New(),
		Name:          input.Name,
		Description:   input.Description,
		Token:         "runner-token",
		Status:        domain.RemoteRunnerStatusRegistered,
		RunnerVersion: input.RunnerVersion,
		IPAddress:     input.IPAddress,
		Capabilities:  input.Capabilities,
	}
	return s.runner, nil
}
func (s *stubRunnerRepo) UnregisterRunnerByToken(context.Context, string) error { return nil }
func (s *stubRunnerRepo) DeleteRunner(context.Context, uuid.UUID) error         { return nil }
func (s *stubRunnerRepo) CreateAssignment(_ context.Context, runnerID uuid.UUID, encodingJobID string) (*domain.RemoteRunnerJobAssignment, error) {
	assignment := &domain.RemoteRunnerJobAssignment{ID: uuid.New(), RunnerID: runnerID, EncodingJob: encodingJobID}
	s.assignments[encodingJobID] = assignment
	return assignment, nil
}
func (s *stubRunnerRepo) GetAssignmentByJob(_ context.Context, jobID string) (*domain.RemoteRunnerJobAssignment, error) {
	if assignment, ok := s.assignments[jobID]; ok {
		return assignment, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubRunnerRepo) GetAssignmentForRunnerAndJob(_ context.Context, runnerID uuid.UUID, jobID string) (*domain.RemoteRunnerJobAssignment, error) {
	if assignment, ok := s.assignments[jobID]; ok && assignment.RunnerID == runnerID {
		return assignment, nil
	}
	return nil, domain.ErrNotFound
}
func (s *stubRunnerRepo) ListAssignments(_ context.Context, opts domain.ListAssignmentsOpts) ([]*domain.RemoteRunnerJobAssignment, int, error) {
	items := []*domain.RemoteRunnerJobAssignment{}
	for _, assignment := range s.assignments {
		if opts.RunnerID != nil && assignment.RunnerID != *opts.RunnerID {
			continue
		}
		if len(opts.State) > 0 {
			match := false
			for _, st := range opts.State {
				if assignment.State == st {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}
		items = append(items, assignment)
	}
	total := len(items)
	if opts.Count > 0 {
		end := opts.Start + opts.Count
		if opts.Start > total {
			items = []*domain.RemoteRunnerJobAssignment{}
		} else if end > total {
			items = items[opts.Start:]
		} else {
			items = items[opts.Start:end]
		}
	}
	return items, total, nil
}

func (s *stubRunnerRepo) HealthMetrics(context.Context) (*domain.RemoteRunnerHealth, error) {
	h := &domain.RemoteRunnerHealth{}
	if s.runner != nil {
		h.TotalRunners = 1
		if s.runner.LastSeenAt != nil && time.Since(*s.runner.LastSeenAt) <= 5*time.Minute {
			h.OnlineRunners = 1
		} else {
			h.OfflineRunners = 1
		}
	}
	for _, a := range s.assignments {
		switch a.State {
		case domain.RemoteRunnerJobStateAccepted, domain.RemoteRunnerJobStateRunning:
			h.JobsInFlight++
		case domain.RemoteRunnerJobStateFailed, domain.RemoteRunnerJobStateAborted:
			h.JobsFailed24h++
		}
	}
	return h, nil
}
func (s *stubRunnerRepo) UpdateAssignment(_ context.Context, assignment *domain.RemoteRunnerJobAssignment) error {
	s.assignments[assignment.EncodingJob] = assignment
	return nil
}
func (s *stubRunnerRepo) RecordFileReceipt(_ context.Context, assignment *domain.RemoteRunnerJobAssignment, fileKey string, details map[string]any) error {
	if assignment.Metadata == nil {
		assignment.Metadata = map[string]any{}
	}
	assignment.Metadata[fileKey] = details
	s.assignments[assignment.EncodingJob] = assignment
	return nil
}
func (s *stubRunnerRepo) DeleteAssignment(_ context.Context, jobID string) error {
	delete(s.assignments, jobID)
	return nil
}

type stubEncodingRepo struct {
	job *domain.EncodingJob
}

func (s *stubEncodingRepo) CreateJob(context.Context, *domain.EncodingJob) error { return nil }
func (s *stubEncodingRepo) GetJob(_ context.Context, jobID string) (*domain.EncodingJob, error) {
	if s.job != nil && s.job.ID == jobID {
		return s.job, nil
	}
	return nil, domain.NewDomainError("JOB_NOT_FOUND", "job not found")
}
func (s *stubEncodingRepo) GetJobByVideoID(context.Context, string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (s *stubEncodingRepo) UpdateJob(context.Context, *domain.EncodingJob) error { return nil }
func (s *stubEncodingRepo) DeleteJob(context.Context, string) error              { return nil }
func (s *stubEncodingRepo) GetPendingJobs(context.Context, int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (s *stubEncodingRepo) GetNextJob(_ context.Context) (*domain.EncodingJob, error) {
	return s.job, nil
}
func (s *stubEncodingRepo) ResetStaleJobs(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (s *stubEncodingRepo) UpdateJobStatus(context.Context, string, domain.EncodingStatus) error {
	return nil
}
func (s *stubEncodingRepo) UpdateJobProgress(context.Context, string, int) error { return nil }
func (s *stubEncodingRepo) SetJobError(context.Context, string, string) error    { return nil }
func (s *stubEncodingRepo) GetJobCounts(context.Context) (map[string]int64, error) {
	return map[string]int64{}, nil
}
func (s *stubEncodingRepo) GetJobsByVideoID(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (s *stubEncodingRepo) GetActiveJobsByVideoID(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (s *stubEncodingRepo) ListJobsByStatus(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}

func newHandlers() (*Handlers, *stubRunnerRepo, *stubEncodingRepo) {
	repo := &stubRunnerRepo{assignments: map[string]*domain.RemoteRunnerJobAssignment{}}
	enc := &stubEncodingRepo{}
	return NewHandlers(repo, enc), repo, enc
}

// listBody is the inner object returned by list handlers: {total, data}.
type listBody struct {
	Total int              `json:"total"`
	Data  []map[string]any `json:"data"`
}

// listEnvelope wraps the shared response envelope around a listBody.
type listEnvelope struct {
	Success bool     `json:"success"`
	Data    listBody `json:"data"`
}

func TestHandlers_ListRunners_Empty(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners", nil)
	rr := httptest.NewRecorder()
	h.ListRunners(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var env listEnvelope
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 0, env.Data.Total)
	require.Empty(t, env.Data.Data)
}

func TestHandlers_ListRunners_WithRunner(t *testing.T) {
	h, repo, _ := newHandlers()
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Name: "test-runner", Token: "secret"}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners", nil)
	rr := httptest.NewRecorder()
	h.ListRunners(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var env listEnvelope
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 1, env.Data.Total)
	// Token must be redacted
	require.Empty(t, env.Data.Data[0]["token"])
}

func TestHandlers_ListRegistrationTokens_Empty(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/registration-tokens", nil)
	rr := httptest.NewRecorder()
	h.ListRegistrationTokens(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var env listEnvelope
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 0, env.Data.Total)
	require.NotNil(t, env.Data.Data)
	require.Empty(t, env.Data.Data)
}

func TestHandlers_CreateRegistrationToken(t *testing.T) {
	h, repo, _ := newHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/registration-tokens/generate", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.CreateRegistrationToken(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code)
	require.NotNil(t, repo.createdToken)
}

func TestHandlers_CreateRegistrationToken_WithExpiry(t *testing.T) {
	h, _, _ := newHandlers()
	expiry := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)
	body := `{"expiresAt":"` + expiry + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/registration-tokens/generate", strings.NewReader(body))
	rr := httptest.NewRecorder()
	h.CreateRegistrationToken(rr, req)
	require.Equal(t, http.StatusCreated, rr.Code)
}

func TestHandlers_DeleteRegistrationToken(t *testing.T) {
	h, _, _ := newHandlers()

	tokenID := uuid.New()
	req := newChiRequest(http.MethodDelete, "/api/v1/runners/registration-tokens/"+tokenID.String(), nil, map[string]string{"id": tokenID.String()})
	rr := httptest.NewRecorder()
	h.DeleteRegistrationToken(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandlers_DeleteRegistrationToken_InvalidID(t *testing.T) {
	h, _, _ := newHandlers()
	req := newChiRequest(http.MethodDelete, "/api/v1/runners/registration-tokens/not-a-uuid", nil, map[string]string{"id": "not-a-uuid"})
	rr := httptest.NewRecorder()
	h.DeleteRegistrationToken(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlers_UnregisterRunner(t *testing.T) {
	h, repo, _ := newHandlers()
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Token: "runner-token"}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/unregister", strings.NewReader(`{"runnerToken":"runner-token"}`))
	rr := httptest.NewRecorder()
	h.UnregisterRunner(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandlers_UnregisterRunner_MissingToken(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/unregister", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.UnregisterRunner(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestHandlers_DeleteRunner(t *testing.T) {
	h, _, _ := newHandlers()
	runnerID := uuid.New()
	req := newChiRequest(http.MethodDelete, "/api/v1/runners/"+runnerID.String(), nil, map[string]string{"runnerId": runnerID.String()})
	rr := httptest.NewRecorder()
	h.DeleteRunner(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandlers_ListJobs_Empty(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs", nil)
	rr := httptest.NewRecorder()
	h.ListJobs(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)
	var env listEnvelope
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 0, env.Data.Total)
}

// runnerLifecycleSetup creates a registered runner with an assigned job ready for lifecycle tests.
func runnerLifecycleSetup(t *testing.T) (*Handlers, *stubRunnerRepo, string, string) {
	t.Helper()
	repo := &stubRunnerRepo{assignments: map[string]*domain.RemoteRunnerJobAssignment{}}
	jobID := "job-lifecycle-" + uuid.New().String()
	enc := &stubEncodingRepo{
		job: &domain.EncodingJob{ID: jobID, VideoID: "video-1", Status: domain.EncodingStatusPending},
	}
	h := NewHandlers(repo, enc)

	// Register runner
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Name: "lifecycle-runner", Token: "lifecycle-token", Status: domain.RemoteRunnerStatusRegistered}

	// Request job to create assignment
	reqJob := httptest.NewRequest(http.MethodPost, "/api/v1/runners/jobs/request", strings.NewReader(`{"runnerToken":"lifecycle-token"}`))
	rrJob := httptest.NewRecorder()
	h.RequestJob(rrJob, reqJob)
	require.Equal(t, http.StatusOK, rrJob.Code, rrJob.Body.String())

	return h, repo, jobID, "lifecycle-token"
}

func TestHandlers_JobLifecycle_AcceptUpdateSuccess(t *testing.T) {
	h, _, jobID, token := runnerLifecycleSetup(t)

	// Accept
	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/accept",
		strings.NewReader(`{"runnerToken":"`+token+`"}`), map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.AcceptJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())

	// Update progress
	req = newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/update",
		strings.NewReader(`{"runnerToken":"`+token+`","progress":50}`), map[string]string{"jobUUID": jobID})
	rr = httptest.NewRecorder()
	h.UpdateJob(rr, req)
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	// Success
	req = newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/success",
		strings.NewReader(`{"runnerToken":"`+token+`"}`), map[string]string{"jobUUID": jobID})
	rr = httptest.NewRecorder()
	h.SuccessJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_JobLifecycle_AcceptError(t *testing.T) {
	h, _, jobID, token := runnerLifecycleSetup(t)

	// Accept
	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/accept",
		strings.NewReader(`{"runnerToken":"`+token+`"}`), map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.AcceptJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Error
	req = newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/error",
		strings.NewReader(`{"runnerToken":"`+token+`","error":"ffmpeg crashed"}`), map[string]string{"jobUUID": jobID})
	rr = httptest.NewRecorder()
	h.ErrorJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_JobLifecycle_Abort(t *testing.T) {
	h, _, jobID, token := runnerLifecycleSetup(t)

	// Accept
	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/accept",
		strings.NewReader(`{"runnerToken":"`+token+`"}`), map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.AcceptJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)

	// Abort
	req = newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/abort",
		strings.NewReader(`{"runnerToken":"`+token+`"}`), map[string]string{"jobUUID": jobID})
	rr = httptest.NewRecorder()
	h.AbortJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_UploadJobFile(t *testing.T) {
	h, _, jobID, token := runnerLifecycleSetup(t)

	videoID := "video-upload-test"
	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/files/videos/"+videoID+"/max-quality",
		strings.NewReader("fake-video-data"), map[string]string{"jobUUID": jobID, "videoId": videoID})
	req.Header.Set("X-Runner-Token", token)
	rr := httptest.NewRecorder()
	h.UploadJobFile(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_CancelJob(t *testing.T) {
	h, _, jobID, _ := runnerLifecycleSetup(t)

	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/cancel", nil, map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.CancelJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_CancelJob_NotFound(t *testing.T) {
	h, _, _, _ := runnerLifecycleSetup(t)

	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/missing/cancel", nil, map[string]string{"jobUUID": "missing"})
	rr := httptest.NewRecorder()
	h.CancelJob(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}

func TestHandlers_DeleteJob(t *testing.T) {
	h, _, jobID, _ := runnerLifecycleSetup(t)

	req := newChiRequest(http.MethodDelete, "/api/v1/runners/jobs/"+jobID, nil, map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.DeleteJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
}

func TestHandlers_RequestJob_NoToken(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/jobs/request", strings.NewReader(`{}`))
	rr := httptest.NewRecorder()
	h.RequestJob(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestHandlers_RequestJob_NoJobAvailable(t *testing.T) {
	h, repo, _ := newHandlers()
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Token: "token-x", Status: domain.RemoteRunnerStatusRegistered}
	// No job in encoding repo
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/jobs/request", strings.NewReader(`{"runnerToken":"token-x"}`))
	rr := httptest.NewRecorder()
	h.RequestJob(rr, req)
	require.Equal(t, http.StatusNoContent, rr.Code)
}

func TestHandlers_AcceptJob_InvalidToken(t *testing.T) {
	h, _, jobID, _ := runnerLifecycleSetup(t)
	req := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/accept",
		strings.NewReader(`{"runnerToken":"wrong-token"}`), map[string]string{"jobUUID": jobID})
	rr := httptest.NewRecorder()
	h.AcceptJob(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)
}

// newChiRequest creates an HTTP request with chi URL params set.
func newChiRequest(method, url string, body *strings.Reader, params map[string]string) *http.Request {
	var req *http.Request
	if body != nil {
		req = httptest.NewRequest(method, url, body)
	} else {
		req = httptest.NewRequest(method, url, nil)
	}
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestHandlers_RegisterRunner(t *testing.T) {
	repo := &stubRunnerRepo{assignments: map[string]*domain.RemoteRunnerJobAssignment{}}
	handler := NewHandlers(repo, &stubEncodingRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/register", strings.NewReader(`{"registrationToken":"reg-token","name":"runner-a","description":"worker"}`))
	rr := httptest.NewRecorder()

	handler.RegisterRunner(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	require.Equal(t, "runner-a", envelope.Data["name"])
	require.Equal(t, "runner-token", envelope.Data["runnerToken"])
}

func TestHandlers_RequestJob(t *testing.T) {
	repo := &stubRunnerRepo{
		runner:      &domain.RemoteRunner{ID: uuid.New(), Token: "runner-token", Status: domain.RemoteRunnerStatusRegistered},
		assignments: map[string]*domain.RemoteRunnerJobAssignment{},
	}
	encoding := &stubEncodingRepo{
		job: &domain.EncodingJob{ID: "job-1", VideoID: "video-1", Status: domain.EncodingStatusProcessing},
	}
	handler := NewHandlers(repo, encoding)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/jobs/request", strings.NewReader(`{"runnerToken":"runner-token"}`))
	rr := httptest.NewRecorder()

	handler.RequestJob(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var envelope struct {
		Data domain.RemoteRunnerJobAssignment `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &envelope))
	require.Equal(t, "job-1", envelope.Data.EncodingJob)
}

// TestHandlers_FullLifecycle_RegisterToCompletion proves the complete runner lifecycle
// in a single coherent test:
//
//	CreateRegistrationToken → RegisterRunner → RequestJob → AcceptJob →
//	UpdateJob (progress) → UploadJobFile (real binary) → SuccessJob → verify completed
func TestHandlers_FullLifecycle_RegisterToCompletion(t *testing.T) {
	const regTokenValue = "reg-token"
	jobID := "full-lifecycle-job-" + uuid.New().String()

	repo := &stubRunnerRepo{assignments: map[string]*domain.RemoteRunnerJobAssignment{}}
	enc := &stubEncodingRepo{
		job: &domain.EncodingJob{ID: jobID, VideoID: "video-lifecycle", Status: domain.EncodingStatusPending},
	}
	h := NewHandlers(repo, enc)

	// ── Step 1: Admin creates a registration token ────────────────────────────
	regTokenReq := httptest.NewRequest(http.MethodPost, "/api/v1/runners/registration-tokens",
		strings.NewReader(`{}`))
	regTokenRR := httptest.NewRecorder()
	h.CreateRegistrationToken(regTokenRR, regTokenReq)
	require.Equal(t, http.StatusCreated, regTokenRR.Code, regTokenRR.Body.String())
	require.NotNil(t, repo.createdToken, "registration token must be stored in repo")
	require.Equal(t, regTokenValue, repo.createdToken.Token)

	// ── Step 2: Runner registers using the token ──────────────────────────────
	regBody := `{"registrationToken":"` + regTokenValue + `","name":"lifecycle-runner","description":"integration test runner"}`
	registerReq := httptest.NewRequest(http.MethodPost, "/api/v1/runners/register", strings.NewReader(regBody))
	registerRR := httptest.NewRecorder()
	h.RegisterRunner(registerRR, registerReq)
	require.Equal(t, http.StatusCreated, registerRR.Code, registerRR.Body.String())

	var registerEnv struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(registerRR.Body.Bytes(), &registerEnv))
	runnerToken, _ := registerEnv.Data["runnerToken"].(string)
	require.NotEmpty(t, runnerToken, "runner token must be returned on registration")
	require.Equal(t, "lifecycle-runner", registerEnv.Data["name"])

	// ── Step 3: Runner requests a job ─────────────────────────────────────────
	requestReq := httptest.NewRequest(http.MethodPost, "/api/v1/runners/jobs/request",
		strings.NewReader(`{"runnerToken":"`+runnerToken+`"}`))
	requestRR := httptest.NewRecorder()
	h.RequestJob(requestRR, requestReq)
	require.Equal(t, http.StatusOK, requestRR.Code, requestRR.Body.String())

	var requestEnv struct {
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(requestRR.Body.Bytes(), &requestEnv))
	require.NotNil(t, requestEnv.Data, "assignment must be returned")
	assignedJobID, _ := requestEnv.Data["jobUUID"].(string)
	require.Equal(t, jobID, assignedJobID)

	// ── Step 4: Runner accepts the job ────────────────────────────────────────
	acceptReq := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/accept",
		strings.NewReader(`{"runnerToken":"`+runnerToken+`"}`), map[string]string{"jobUUID": jobID})
	acceptRR := httptest.NewRecorder()
	h.AcceptJob(acceptRR, acceptReq)
	require.Equal(t, http.StatusNoContent, acceptRR.Code, acceptRR.Body.String())

	// Verify assignment state is now accepted
	assignment, ok := repo.assignments[jobID]
	require.True(t, ok, "assignment must exist in repo after accept")
	require.Equal(t, domain.RemoteRunnerJobStateAccepted, assignment.State)
	require.NotNil(t, assignment.AcceptedAt, "accepted_at must be set")

	// ── Step 5: Runner updates progress ──────────────────────────────────────
	updateReq := newChiRequest(http.MethodPut, "/api/v1/runners/jobs/"+jobID,
		strings.NewReader(`{"runnerToken":"`+runnerToken+`","progress":50}`), map[string]string{"jobUUID": jobID})
	updateRR := httptest.NewRecorder()
	h.UpdateJob(updateRR, updateReq)
	require.Equal(t, http.StatusOK, updateRR.Code, updateRR.Body.String())

	assignment = repo.assignments[jobID]
	require.Equal(t, 50, assignment.Progress)

	// ── Step 6: Runner uploads result file (real binary data) ─────────────────
	// Simulate a minimal MP4 FTYP box — real binary data, not just a string.
	fakeMp4 := []byte("\x00\x00\x00\x1cftypisom\x00\x00\x00\x00isomiso2avc1mp41")
	fileURL := "/api/v1/runners/jobs/" + jobID + "/files/videos/" + jobID + "/max-quality.mp4"

	uploadReq := httptest.NewRequest(http.MethodPost, fileURL, bytes.NewReader(fakeMp4))
	uploadReq.Header.Set("X-Runner-Token", runnerToken)
	uploadReq.Header.Set("Content-Type", "video/mp4")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobUUID", jobID)
	uploadReq = uploadReq.WithContext(context.WithValue(uploadReq.Context(), chi.RouteCtxKey, rctx))
	uploadRR := httptest.NewRecorder()
	h.UploadJobFile(uploadRR, uploadReq)
	require.Equal(t, http.StatusNoContent, uploadRR.Code, uploadRR.Body.String())

	// Verify file receipt was recorded with correct content length
	assignment = repo.assignments[jobID]
	require.NotNil(t, assignment.Metadata, "metadata must be set after file upload")
	receipt, hasReceipt := assignment.Metadata[fileURL]
	require.True(t, hasReceipt, "file receipt must be recorded under the file URL key")
	receiptMap, _ := receipt.(map[string]any)
	require.NotNil(t, receiptMap, "file receipt must be a map")
	contentLength, _ := receiptMap["contentLength"].(int64)
	require.Equal(t, int64(len(fakeMp4)), contentLength, "recorded content length must match actual upload size")

	// ── Step 7: Runner marks job as successful ────────────────────────────────
	successReq := newChiRequest(http.MethodPost, "/api/v1/runners/jobs/"+jobID+"/success",
		strings.NewReader(`{"runnerToken":"`+runnerToken+`","metadata":{"output":"video.mp4"}}`),
		map[string]string{"jobUUID": jobID})
	successRR := httptest.NewRecorder()
	h.SuccessJob(successRR, successReq)
	require.Equal(t, http.StatusNoContent, successRR.Code, successRR.Body.String())

	// ── Step 8: Verify final completed state ──────────────────────────────────
	assignment = repo.assignments[jobID]
	require.Equal(t, domain.RemoteRunnerJobStateCompleted, assignment.State,
		"assignment state must be completed after success")
	require.Equal(t, 100, assignment.Progress, "progress must be 100 after success")
	require.NotNil(t, assignment.CompletedAt, "completed_at must be set")
}

// ─── Phase 12: ListJobs query-param filtering ────────────────────────────────

func TestParseListAssignmentsOpts_Defaults(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs", nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.Equal(t, 0, opts.Start)
	require.Equal(t, 0, opts.Count)
	require.Empty(t, opts.State)
	require.Nil(t, opts.RunnerID)
}

func TestParseListAssignmentsOpts_StartCount(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?start=20&count=10", nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.Equal(t, 20, opts.Start)
	require.Equal(t, 10, opts.Count)
}

func TestParseListAssignmentsOpts_CountClamp(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?count=9999", nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.Equal(t, 500, opts.Count, "count must clamp to 500")
}

func TestParseListAssignmentsOpts_NegativeStart(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?start=-1", nil)
	_, err := parseListAssignmentsOpts(req)
	require.Error(t, err)
}

func TestParseListAssignmentsOpts_StateMultiSelect(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?state=failed&state=aborted", nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.Equal(t, []domain.RemoteRunnerJobState{
		domain.RemoteRunnerJobStateFailed,
		domain.RemoteRunnerJobStateAborted,
	}, opts.State)
}

func TestParseListAssignmentsOpts_UnknownStateDropped(t *testing.T) {
	// A typo'd filter chip should produce an empty result, not a 400. The
	// handler decides how to render no-matches; rejecting at parse-time is the
	// wrong layer.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?state=bogus&state=running", nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.Equal(t, []domain.RemoteRunnerJobState{
		domain.RemoteRunnerJobStateRunning,
	}, opts.State)
}

func TestParseListAssignmentsOpts_RunnerIDValid(t *testing.T) {
	id := uuid.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?runnerId="+id.String(), nil)
	opts, err := parseListAssignmentsOpts(req)
	require.NoError(t, err)
	require.NotNil(t, opts.RunnerID)
	require.Equal(t, id, *opts.RunnerID)
}

func TestParseListAssignmentsOpts_RunnerIDInvalid(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?runnerId=not-a-uuid", nil)
	_, err := parseListAssignmentsOpts(req)
	require.Error(t, err)
}

func TestHandlers_ListJobs_FilterRespectsTotal(t *testing.T) {
	// Two assignments — one running, one completed. Filter for running should
	// yield total=1 in the response envelope, not the unfiltered count.
	h, repo, _ := newHandlers()
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Token: "t"}
	repo.assignments["a"] = &domain.RemoteRunnerJobAssignment{ID: uuid.New(), EncodingJob: "a", State: domain.RemoteRunnerJobStateRunning}
	repo.assignments["b"] = &domain.RemoteRunnerJobAssignment{ID: uuid.New(), EncodingJob: "b", State: domain.RemoteRunnerJobStateCompleted}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/jobs?state=running", nil)
	rr := httptest.NewRecorder()
	h.ListJobs(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var env listEnvelope
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 1, env.Data.Total)
	require.Len(t, env.Data.Data, 1)
}

// ─── Phase 12: RunnerHealth handler ───────────────────────────────────────────

func TestHandlers_RunnerHealth_Empty(t *testing.T) {
	h, _, _ := newHandlers()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/health", nil)
	rr := httptest.NewRecorder()
	h.RunnerHealth(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var env struct {
		Data domain.RemoteRunnerHealth `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 0, env.Data.TotalRunners)
	require.Equal(t, 0, env.Data.JobsInFlight)
}

func TestHandlers_RunnerHealth_WithRunningJobs(t *testing.T) {
	h, repo, _ := newHandlers()
	now := time.Now().UTC()
	repo.runner = &domain.RemoteRunner{ID: uuid.New(), Token: "t", LastSeenAt: &now}
	repo.assignments["a"] = &domain.RemoteRunnerJobAssignment{ID: uuid.New(), EncodingJob: "a", State: domain.RemoteRunnerJobStateRunning}
	repo.assignments["b"] = &domain.RemoteRunnerJobAssignment{ID: uuid.New(), EncodingJob: "b", State: domain.RemoteRunnerJobStateFailed}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/runners/health", nil)
	rr := httptest.NewRecorder()
	h.RunnerHealth(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	var env struct {
		Data domain.RemoteRunnerHealth `json:"data"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &env))
	require.Equal(t, 1, env.Data.TotalRunners)
	require.Equal(t, 1, env.Data.OnlineRunners)
	require.Equal(t, 1, env.Data.JobsInFlight)
	require.Equal(t, 1, env.Data.JobsFailed24h)
}

// ─── Phase 12: clientIPFromRequest ────────────────────────────────────────────

func TestClientIPFromRequest_XForwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-Forwarded-For", "203.0.113.42, 10.0.0.1")
	req.RemoteAddr = "10.0.0.1:54321"
	require.Equal(t, "203.0.113.42", clientIPFromRequest(req))
}

func TestClientIPFromRequest_RemoteAddr(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = "192.0.2.5:8080"
	require.Equal(t, "192.0.2.5", clientIPFromRequest(req))
}

func TestClientIPFromRequest_Empty(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.RemoteAddr = ""
	require.Equal(t, "", clientIPFromRequest(req))
}

// ─── Phase 12: RegisterRunner captures new fields ─────────────────────────────

func TestHandlers_RegisterRunner_CapturesNewFields(t *testing.T) {
	repo := &stubRunnerRepo{assignments: map[string]*domain.RemoteRunnerJobAssignment{}}
	handler := NewHandlers(repo, &stubEncodingRepo{})

	body := `{"registrationToken":"reg-token","name":"runner-x","runnerVersion":"1.4.2","capabilities":{"gpu":"nvidia","ffmpeg":"6.0"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/runners/register", strings.NewReader(body))
	req.Header.Set("X-Forwarded-For", "198.51.100.7, 10.0.0.1")
	rr := httptest.NewRecorder()

	handler.RegisterRunner(rr, req)

	require.Equal(t, http.StatusCreated, rr.Code, rr.Body.String())
	require.NotNil(t, repo.runner)
	require.Equal(t, "1.4.2", repo.runner.RunnerVersion)
	require.Equal(t, "198.51.100.7", repo.runner.IPAddress)
	require.Equal(t, "nvidia", repo.runner.Capabilities["gpu"])
	require.Equal(t, "6.0", repo.runner.Capabilities["ffmpeg"])
}
