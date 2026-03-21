package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
)

// mockJobRepo satisfies JobRepository for tests.
type mockJobRepo struct {
	jobs []*domain.EncodingJob
	err  error
}

func (m *mockJobRepo) ListJobsByStatus(_ context.Context, status string) ([]*domain.EncodingJob, error) {
	if m.err != nil {
		return nil, m.err
	}
	var result []*domain.EncodingJob
	for _, j := range m.jobs {
		if string(j.Status) == status {
			result = append(result, j)
		}
	}
	return result, nil
}

// mockJobScheduler satisfies JobScheduler for tests.
type mockJobScheduler struct {
	paused  bool
	resumed bool
}

func (m *mockJobScheduler) Pause()  { m.paused = true }
func (m *mockJobScheduler) Resume() { m.resumed = true }

func TestListJobs_OK(t *testing.T) {
	repo := &mockJobRepo{
		jobs: []*domain.EncodingJob{
			{ID: "job-1", Status: domain.EncodingStatusPending},
			{ID: "job-2", Status: domain.EncodingStatusCompleted},
		},
	}
	h := NewJobHandlers(repo, nil)

	r := chi.NewRouter()
	r.Get("/jobs/{state}", h.ListJobs)

	req := httptest.NewRequest(http.MethodGet, "/jobs/pending", nil)
	req = withAdminContext(req, "admin-1")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool                    `json:"success"`
		Data    []*domain.EncodingJob   `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 pending job, got %d", len(resp.Data))
	}
}

func TestPauseJobs_OK(t *testing.T) {
	sched := &mockJobScheduler{}
	h := NewJobHandlers(nil, sched)

	req := httptest.NewRequest(http.MethodPost, "/jobs/pause", nil)
	req = withAdminContext(req, "admin-1")
	rr := httptest.NewRecorder()
	h.PauseJobs(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !sched.paused {
		t.Fatal("expected scheduler to be paused")
	}
}

func TestResumeJobs_OK(t *testing.T) {
	sched := &mockJobScheduler{}
	h := NewJobHandlers(nil, sched)

	req := httptest.NewRequest(http.MethodPost, "/jobs/resume", nil)
	req = withAdminContext(req, "admin-1")
	rr := httptest.NewRecorder()
	h.ResumeJobs(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !sched.resumed {
		t.Fatal("expected scheduler to be resumed")
	}
}

func TestPauseJobs_NoScheduler(t *testing.T) {
	h := NewJobHandlers(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/jobs/pause", nil)
	req = withAdminContext(req, "admin-1")
	rr := httptest.NewRecorder()
	h.PauseJobs(rr, req)

	// Returns 503 when scheduler not configured
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", rr.Code)
	}
}
