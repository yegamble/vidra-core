package runner

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"

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
func (s *stubRunnerRepo) RegisterRunner(_ context.Context, registrationToken, name, description string) (*domain.RemoteRunner, error) {
	if registrationToken != "reg-token" {
		return nil, domain.ErrNotFound
	}
	s.runner = &domain.RemoteRunner{
		ID:          uuid.New(),
		Name:        name,
		Description: description,
		Token:       "runner-token",
		Status:      domain.RemoteRunnerStatusRegistered,
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
func (s *stubRunnerRepo) ListAssignments(context.Context) ([]*domain.RemoteRunnerJobAssignment, error) {
	items := []*domain.RemoteRunnerJobAssignment{}
	for _, assignment := range s.assignments {
		items = append(items, assignment)
	}
	return items, nil
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
