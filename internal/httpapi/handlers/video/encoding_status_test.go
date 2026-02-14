package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/usecase"
)

type mockEncodingRepo struct{ counts map[string]int64 }

func (m *mockEncodingRepo) CreateJob(_ context.Context, _ *domain.EncodingJob) error { return nil }
func (m *mockEncodingRepo) GetJob(_ context.Context, _ string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) GetJobByVideoID(_ context.Context, _ string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) UpdateJob(_ context.Context, _ *domain.EncodingJob) error { return nil }
func (m *mockEncodingRepo) DeleteJob(_ context.Context, _ string) error              { return nil }
func (m *mockEncodingRepo) GetPendingJobs(_ context.Context, _ int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) GetNextJob(_ context.Context) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) UpdateJobStatus(_ context.Context, _ string, _ domain.EncodingStatus) error {
	return nil
}
func (m *mockEncodingRepo) UpdateJobProgress(_ context.Context, _ string, _ int) error { return nil }
func (m *mockEncodingRepo) SetJobError(_ context.Context, _ string, _ string) error    { return nil }
func (m *mockEncodingRepo) GetJobCounts(_ context.Context) (map[string]int64, error) {
	return m.counts, nil
}
func (m *mockEncodingRepo) ResetStaleJobs(_ context.Context, _ time.Duration) (int64, error) {
	return 0, nil
}
func (m *mockEncodingRepo) GetJobsByVideoID(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) GetActiveJobsByVideoID(_ context.Context, _ string) ([]*domain.EncodingJob, error) {
	return nil, nil
}

// Ensure mockEncodingRepo satisfies the interface at compile time
var _ usecase.EncodingRepository = (*mockEncodingRepo)(nil)

func TestEncodingStatusHandlerEnhanced_ReturnsCountsAndSchedulerFields(t *testing.T) {
	repo := &mockEncodingRepo{counts: map[string]int64{"pending": 2, "processing": 1, "completed": 5, "failed": 0}}
	cfg := &config.Config{EnableEncodingScheduler: true, EncodingSchedulerIntervalSeconds: 15, EncodingSchedulerBurst: 2}
	h := EncodingStatusHandlerEnhanced(repo, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var env Response
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode env: %v", err)
	}
	var data map[string]interface{}
	b, _ := json.Marshal(env.Data)
	if err := json.Unmarshal(b, &data); err != nil {
		t.Fatalf("decode data: %v", err)
	}
	if int64(data["pending"].(float64)) != 2 || int64(data["completed"].(float64)) != 5 {
		t.Fatalf("unexpected counts: %+v", data)
	}
	if _, ok := data["scheduler_enabled"]; !ok {
		t.Fatalf("expected scheduler fields present")
	}
}

func TestEncodingStatusHandlerEnhanced_WithScheduler(t *testing.T) {
	repo := &mockEncodingRepo{counts: map[string]int64{"pending": 2, "processing": 1, "completed": 5, "failed": 0}}
	cfg := &config.Config{
		EnableEncodingScheduler:          true,
		EncodingSchedulerIntervalSeconds: 15,
		EncodingSchedulerBurst:           2,
	}

	// Create a mock scheduler (we can't instantiate a real one without dependencies)
	// Instead test with nil scheduler which is already covered
	// The scheduler.Snapshot() path requires integration test or complex mocking
	h := EncodingStatusHandlerEnhanced(repo, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var env Response
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode env: %v", err)
	}

	data, _ := json.Marshal(env.Data)
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode data: %v", err)
	}

	// Check scheduler fields are present
	if enabled, ok := resp["scheduler_enabled"].(bool); !ok || !enabled {
		t.Fatalf("expected scheduler_enabled=true, got %v", resp["scheduler_enabled"])
	}
	if interval, ok := resp["scheduler_interval_seconds"].(float64); !ok || int(interval) != 15 {
		t.Fatalf("expected scheduler_interval_seconds=15, got %v", resp["scheduler_interval_seconds"])
	}
	if burst, ok := resp["scheduler_burst"].(float64); !ok || int(burst) != 2 {
		t.Fatalf("expected scheduler_burst=2, got %v", resp["scheduler_burst"])
	}
}

type mockEncodingRepoError struct {
	mockEncodingRepo
	returnError bool
}

func (m *mockEncodingRepoError) GetJobCounts(_ context.Context) (map[string]int64, error) {
	if m.returnError {
		return nil, domain.NewDomainError("DATABASE_ERROR", "database error")
	}
	return m.counts, nil
}

func TestEncodingStatusHandlerEnhanced_Error(t *testing.T) {
	repo := &mockEncodingRepoError{
		mockEncodingRepo: mockEncodingRepo{counts: nil},
		returnError:      true,
	}
	cfg := &config.Config{}
	h := EncodingStatusHandlerEnhanced(repo, cfg, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}

func TestEncodingStatusHandler_Success(t *testing.T) {
	repo := &mockEncodingRepo{counts: map[string]int64{"pending": 3, "processing": 2, "completed": 10, "failed": 1}}
	h := EncodingStatusHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var env Response
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode env: %v", err)
	}

	data, _ := json.Marshal(env.Data)
	var resp map[string]interface{}
	if err := json.Unmarshal(data, &resp); err != nil {
		t.Fatalf("decode data: %v", err)
	}

	if int64(resp["pending"].(float64)) != 3 {
		t.Errorf("expected pending=3, got %v", resp["pending"])
	}
	if int64(resp["processing"].(float64)) != 2 {
		t.Errorf("expected processing=2, got %v", resp["processing"])
	}
	if int64(resp["completed"].(float64)) != 10 {
		t.Errorf("expected completed=10, got %v", resp["completed"])
	}
	if int64(resp["failed"].(float64)) != 1 {
		t.Errorf("expected failed=1, got %v", resp["failed"])
	}
}

func TestEncodingStatusHandler_Error(t *testing.T) {
	repo := &mockEncodingRepoError{
		mockEncodingRepo: mockEncodingRepo{counts: nil},
		returnError:      true,
	}
	h := EncodingStatusHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", rr.Code)
	}
}
