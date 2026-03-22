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
		return s.runner, nil
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
