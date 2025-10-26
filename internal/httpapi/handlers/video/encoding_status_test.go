package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
