package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockFederationRepository struct {
	ListJobsFunc    func(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error)
	GetJobFunc      func(ctx context.Context, id string) (*domain.FederationJob, error)
	RetryJobFunc    func(ctx context.Context, id string, when time.Time) error
	DeleteJobFunc   func(ctx context.Context, id string) error
	ListActorsFunc  func(ctx context.Context, limit, offset int) ([]repository.FederationActor, int, error)
	UpsertActorFunc func(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error
	UpdateActorFunc func(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error
	DeleteActorFunc func(ctx context.Context, actor string) error
}

func (m *mockFederationRepository) ListJobs(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error) {
	if m.ListJobsFunc != nil {
		return m.ListJobsFunc(ctx, status, limit, offset)
	}
	return nil, 0, nil
}

func (m *mockFederationRepository) GetJob(ctx context.Context, id string) (*domain.FederationJob, error) {
	if m.GetJobFunc != nil {
		return m.GetJobFunc(ctx, id)
	}
	return nil, nil
}

func (m *mockFederationRepository) RetryJob(ctx context.Context, id string, when time.Time) error {
	if m.RetryJobFunc != nil {
		return m.RetryJobFunc(ctx, id, when)
	}
	return nil
}

func (m *mockFederationRepository) DeleteJob(ctx context.Context, id string) error {
	if m.DeleteJobFunc != nil {
		return m.DeleteJobFunc(ctx, id)
	}
	return nil
}

func (m *mockFederationRepository) ListActors(ctx context.Context, limit, offset int) ([]repository.FederationActor, int, error) {
	if m.ListActorsFunc != nil {
		return m.ListActorsFunc(ctx, limit, offset)
	}
	return nil, 0, nil
}

func (m *mockFederationRepository) UpsertActor(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error {
	if m.UpsertActorFunc != nil {
		return m.UpsertActorFunc(ctx, actor, enabled, rateLimitSeconds)
	}
	return nil
}

func (m *mockFederationRepository) UpdateActor(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error {
	if m.UpdateActorFunc != nil {
		return m.UpdateActorFunc(ctx, actor, enabled, rateLimitSeconds, cursor, nextAt, attempts)
	}
	return nil
}

func (m *mockFederationRepository) DeleteActor(ctx context.Context, actor string) error {
	if m.DeleteActorFunc != nil {
		return m.DeleteActorFunc(ctx, actor)
	}
	return nil
}

func requestWithAdmin(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserRoleKey, string(domain.RoleAdmin)))
	rec := httptest.NewRecorder()
	return req, rec
}

func requestWithoutAdmin(method, path string, body string) (*http.Request, *httptest.ResponseRecorder) {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserRoleKey, string(domain.RoleUser)))
	rec := httptest.NewRecorder()
	return req, rec
}

func TestListJobs_Success(t *testing.T) {
	mockRepo := &mockFederationRepository{
		ListJobsFunc: func(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error) {
			return []domain.FederationJob{
				{ID: "job1", JobType: "test_type", Status: "pending"},
				{ID: "job2", JobType: "test_type2", Status: "completed"},
			}, 2, nil
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)
	req, rec := requestWithAdmin("GET", "/admin/federation/jobs?status=pending&page=1&pageSize=20", "")

	handler.ListJobs(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	response, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(2), response["total"])
	assert.Equal(t, float64(1), response["page"])
	assert.Equal(t, float64(20), response["pageSize"])
	data, ok := response["data"].([]interface{})
	require.True(t, ok)
	assert.Len(t, data, 2)
}

func TestListJobs_Unauthorized(t *testing.T) {
	mockRepo := &mockFederationRepository{}
	handler := NewAdminFederationHandlers(mockRepo)
	req, rec := requestWithoutAdmin("GET", "/admin/federation/jobs", "")

	handler.ListJobs(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestListJobs_RepositoryError(t *testing.T) {
	mockRepo := &mockFederationRepository{
		ListJobsFunc: func(ctx context.Context, status string, limit, offset int) ([]domain.FederationJob, int, error) {
			return nil, 0, domain.NewDomainError("DB_ERROR", "database error")
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)
	req, rec := requestWithAdmin("GET", "/admin/federation/jobs", "")

	handler.ListJobs(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetJob_Success(t *testing.T) {
	mockRepo := &mockFederationRepository{
		GetJobFunc: func(ctx context.Context, id string) (*domain.FederationJob, error) {
			if id == "job1" {
				return &domain.FederationJob{
					ID:      "job1",
					JobType: "test_type",
					Status:  "pending",
				}, nil
			}
			return nil, domain.NewDomainError("NOT_FOUND", "job not found")
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "job1")

	req, rec := requestWithAdmin("GET", "/admin/federation/jobs/job1", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetJob(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	jobData, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "job1", jobData["id"])
}

func TestGetJob_NotFound(t *testing.T) {
	mockRepo := &mockFederationRepository{
		GetJobFunc: func(ctx context.Context, id string) (*domain.FederationJob, error) {
			return nil, domain.NewDomainError("NOT_FOUND", "job not found")
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")

	req, rec := requestWithAdmin("GET", "/admin/federation/jobs/nonexistent", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetJob(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestRetryJob_Success(t *testing.T) {
	var capturedID string
	var capturedWhen time.Time

	mockRepo := &mockFederationRepository{
		RetryJobFunc: func(ctx context.Context, id string, when time.Time) error {
			capturedID = id
			capturedWhen = when
			return nil
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "job1")

	req, rec := requestWithAdmin("POST", "/admin/federation/jobs/job1/retry", `{"delaySeconds": 60}`)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	beforeCall := time.Now()
	handler.RetryJob(rec, req)
	afterCall := time.Now()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "job1", capturedID)
	assert.True(t, capturedWhen.After(beforeCall.Add(50*time.Second)))
	assert.True(t, capturedWhen.Before(afterCall.Add(70*time.Second)))
}

func TestRetryJob_DefaultDelay(t *testing.T) {
	var capturedWhen time.Time

	mockRepo := &mockFederationRepository{
		RetryJobFunc: func(ctx context.Context, id string, when time.Time) error {
			capturedWhen = when
			return nil
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "job1")

	req, rec := requestWithAdmin("POST", "/admin/federation/jobs/job1/retry", `{}`)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	beforeCall := time.Now()
	handler.RetryJob(rec, req)
	afterCall := time.Now()

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, capturedWhen.After(beforeCall.Add(25*time.Second)))
	assert.True(t, capturedWhen.Before(afterCall.Add(35*time.Second)))
}

func TestDeleteJob_Success(t *testing.T) {
	var capturedID string

	mockRepo := &mockFederationRepository{
		DeleteJobFunc: func(ctx context.Context, id string) error {
			capturedID = id
			return nil
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "job1")

	req, rec := requestWithAdmin("DELETE", "/admin/federation/jobs/job1", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.DeleteJob(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "job1", capturedID)
}

func TestDeleteJob_NotFound(t *testing.T) {
	mockRepo := &mockFederationRepository{
		DeleteJobFunc: func(ctx context.Context, id string) error {
			return domain.NewDomainError("NOT_FOUND", "job not found")
		},
	}

	handler := NewAdminFederationHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "nonexistent")

	req, rec := requestWithAdmin("DELETE", "/admin/federation/jobs/nonexistent", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.DeleteJob(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListActors_Success(t *testing.T) {
	mockRepo := &mockFederationRepository{
		ListActorsFunc: func(ctx context.Context, limit, offset int) ([]repository.FederationActor, int, error) {
			return []repository.FederationActor{
				{Actor: "actor1@example.com", Enabled: true, RateLimitSeconds: 60},
				{Actor: "actor2@example.com", Enabled: false, RateLimitSeconds: 120},
			}, 2, nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)
	req, rec := requestWithAdmin("GET", "/admin/federation/actors?page=1&pageSize=50", "")

	handler.ListActors(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper map[string]interface{}
	err := json.Unmarshal(rec.Body.Bytes(), &wrapper)
	require.NoError(t, err)

	response, ok := wrapper["data"].(map[string]interface{})
	require.True(t, ok)

	assert.Equal(t, float64(2), response["total"])
	assert.Equal(t, float64(1), response["page"])
	assert.Equal(t, float64(50), response["pageSize"])
}

func TestListActors_Pagination(t *testing.T) {
	var capturedLimit, capturedOffset int

	mockRepo := &mockFederationRepository{
		ListActorsFunc: func(ctx context.Context, limit, offset int) ([]repository.FederationActor, int, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []repository.FederationActor{}, 0, nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)
	req, rec := requestWithAdmin("GET", "/admin/federation/actors?page=3&pageSize=25", "")

	handler.ListActors(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 25, capturedLimit)
	assert.Equal(t, 50, capturedOffset)
}

func TestUpsertActor_Success(t *testing.T) {
	var capturedActor string
	var capturedEnabled bool
	var capturedRateLimit int

	mockRepo := &mockFederationRepository{
		UpsertActorFunc: func(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error {
			capturedActor = actor
			capturedEnabled = enabled
			capturedRateLimit = rateLimitSeconds
			return nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)
	body := `{"actor": "test@example.com", "enabled": true, "rate_limit_seconds": 120}`
	req, rec := requestWithAdmin("POST", "/admin/federation/actors", body)

	handler.UpsertActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test@example.com", capturedActor)
	assert.True(t, capturedEnabled)
	assert.Equal(t, 120, capturedRateLimit)
}

func TestUpsertActor_DefaultRateLimit(t *testing.T) {
	var capturedRateLimit int

	mockRepo := &mockFederationRepository{
		UpsertActorFunc: func(ctx context.Context, actor string, enabled bool, rateLimitSeconds int) error {
			capturedRateLimit = rateLimitSeconds
			return nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)
	body := `{"actor": "test@example.com", "enabled": true}`
	req, rec := requestWithAdmin("POST", "/admin/federation/actors", body)

	handler.UpsertActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 60, capturedRateLimit)
}

func TestUpsertActor_InvalidBody(t *testing.T) {
	mockRepo := &mockFederationRepository{}
	handler := NewAdminFederationActorsHandlers(mockRepo)

	tests := []struct {
		name string
		body string
	}{
		{"invalid JSON", `{invalid json}`},
		{"missing actor", `{"enabled": true}`},
		{"empty actor", `{"actor": "", "enabled": true}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, rec := requestWithAdmin("POST", "/admin/federation/actors", tt.body)
			handler.UpsertActor(rec, req)
			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestUpdateActor_Success(t *testing.T) {
	enabled := true
	rateLimit := 90

	var capturedActor string
	var capturedEnabled *bool
	var capturedRateLimit *int

	mockRepo := &mockFederationRepository{
		UpdateActorFunc: func(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error {
			capturedActor = actor
			capturedEnabled = enabled
			capturedRateLimit = rateLimitSeconds
			return nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("actor", "test@example.com")

	body := `{"enabled": true, "rate_limit_seconds": 90}`
	req, rec := requestWithAdmin("PATCH", "/admin/federation/actors/test@example.com", body)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.UpdateActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test@example.com", capturedActor)
	require.NotNil(t, capturedEnabled)
	assert.Equal(t, enabled, *capturedEnabled)
	require.NotNil(t, capturedRateLimit)
	assert.Equal(t, rateLimit, *capturedRateLimit)
}

func TestUpdateActor_WithNextAt(t *testing.T) {
	var capturedNextAt *time.Time

	mockRepo := &mockFederationRepository{
		UpdateActorFunc: func(ctx context.Context, actor string, enabled *bool, rateLimitSeconds *int, cursor *string, nextAt *time.Time, attempts *int) error {
			capturedNextAt = nextAt
			return nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("actor", "test@example.com")

	body := `{"next_at": "2024-01-01T12:00:00Z"}`
	req, rec := requestWithAdmin("PATCH", "/admin/federation/actors/test@example.com", body)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.UpdateActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedNextAt)
	expected, _ := time.Parse(time.RFC3339, "2024-01-01T12:00:00Z")
	assert.Equal(t, expected.UTC(), capturedNextAt.UTC())
}

func TestDeleteActor_Success(t *testing.T) {
	var capturedActor string

	mockRepo := &mockFederationRepository{
		DeleteActorFunc: func(ctx context.Context, actor string) error {
			capturedActor = actor
			return nil
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("actor", "test@example.com")

	req, rec := requestWithAdmin("DELETE", "/admin/federation/actors/test@example.com", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.DeleteActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "test@example.com", capturedActor)
}

func TestDeleteActor_NotFound(t *testing.T) {
	mockRepo := &mockFederationRepository{
		DeleteActorFunc: func(ctx context.Context, actor string) error {
			return domain.NewDomainError("NOT_FOUND", "actor not found")
		},
	}

	handler := NewAdminFederationActorsHandlers(mockRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("actor", "nonexistent@example.com")

	req, rec := requestWithAdmin("DELETE", "/admin/federation/actors/nonexistent@example.com", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.DeleteActor(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}
