package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"database/sql"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
	"vidra-core/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func withAdmin(r *http.Request) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserRoleKey, string(domain.RoleAdmin)))
}

func withAdminAndParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	ctx := context.WithValue(r.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, middleware.UserRoleKey, string(domain.RoleAdmin))
	return r.WithContext(ctx)
}

type mockHardeningRepo struct {
	getConfigFn         func(ctx context.Context) (*domain.FederationSecurityConfig, error)
	getHealthSummaryFn  func(ctx context.Context) ([]domain.FederationHealthSummary, error)
	refreshHealthFn     func(ctx context.Context) error
	getMetricsFn        func(ctx context.Context, metricType string, since time.Time, limit int) ([]domain.FederationMetric, error)
	recordMetricFn      func(ctx context.Context, metric *domain.FederationMetric) error
	getDLQJobsFn        func(ctx context.Context, limit int, canRetryOnly bool) ([]domain.DeadLetterJob, error)
	retryDLQJobFn       func(ctx context.Context, dlqID string) error
	getInstanceBlocksFn func(ctx context.Context) ([]domain.InstanceBlock, error)
	addInstanceBlockFn  func(ctx context.Context, block *domain.InstanceBlock) error
	removeInstanceBlock func(ctx context.Context, d string) error
	isInstanceBlockedFn func(ctx context.Context, d string) (bool, error)
	addActorBlockFn     func(ctx context.Context, block *domain.ActorBlock) error
	isActorBlockedFn    func(ctx context.Context, did, handle string) (bool, error)
	getAbuseReportsFn   func(ctx context.Context, status string, limit int) ([]domain.FederationAbuseReport, error)
	createAbuseReportFn func(ctx context.Context, report *domain.FederationAbuseReport) error
	updateAbuseReportFn func(ctx context.Context, id, status, resolution, resolvedBy string) error
	cleanupFn           func(ctx context.Context) error

	checkIdempotencyFn     func(ctx context.Context, key string) (*domain.IdempotencyRecord, error)
	recordIdempotencyFn    func(ctx context.Context, record *domain.IdempotencyRecord) error
	updateJobWithBackoffFn func(ctx context.Context, jobID string, attempts int, lastError string) error
	moveToDLQFn            func(ctx context.Context, job *domain.FederationJob, errorMsg string) error
	checkRequestSigFn      func(ctx context.Context, signatureHash string) (bool, error)
	recordRequestSigFn     func(ctx context.Context, sig *domain.RequestSignature) error
	checkRateLimitFn       func(ctx context.Context, id string, limit int, window time.Duration) (bool, error)
}

func (m *mockHardeningRepo) GetFederationConfig(ctx context.Context) (*domain.FederationSecurityConfig, error) {
	if m.getConfigFn != nil {
		return m.getConfigFn(ctx)
	}
	return &domain.FederationSecurityConfig{MetricsEnabled: true, EnableAbuseReporting: true}, nil
}
func (m *mockHardeningRepo) CheckIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	if m.checkIdempotencyFn != nil {
		return m.checkIdempotencyFn(ctx, key)
	}
	return nil, nil
}
func (m *mockHardeningRepo) RecordIdempotency(ctx context.Context, record *domain.IdempotencyRecord) error {
	if m.recordIdempotencyFn != nil {
		return m.recordIdempotencyFn(ctx, record)
	}
	return nil
}
func (m *mockHardeningRepo) UpdateJobWithBackoff(ctx context.Context, jobID string, attempts int, lastError string) error {
	if m.updateJobWithBackoffFn != nil {
		return m.updateJobWithBackoffFn(ctx, jobID, attempts, lastError)
	}
	return nil
}
func (m *mockHardeningRepo) MoveToDLQ(ctx context.Context, job *domain.FederationJob, errorMsg string) error {
	if m.moveToDLQFn != nil {
		return m.moveToDLQFn(ctx, job, errorMsg)
	}
	return nil
}
func (m *mockHardeningRepo) GetDLQJobs(ctx context.Context, limit int, canRetryOnly bool) ([]domain.DeadLetterJob, error) {
	if m.getDLQJobsFn != nil {
		return m.getDLQJobsFn(ctx, limit, canRetryOnly)
	}
	return []domain.DeadLetterJob{}, nil
}
func (m *mockHardeningRepo) RetryDLQJob(ctx context.Context, dlqID string) error {
	if m.retryDLQJobFn != nil {
		return m.retryDLQJobFn(ctx, dlqID)
	}
	return nil
}
func (m *mockHardeningRepo) CheckRequestSignature(ctx context.Context, signatureHash string) (bool, error) {
	if m.checkRequestSigFn != nil {
		return m.checkRequestSigFn(ctx, signatureHash)
	}
	return false, nil
}
func (m *mockHardeningRepo) RecordRequestSignature(ctx context.Context, sig *domain.RequestSignature) error {
	if m.recordRequestSigFn != nil {
		return m.recordRequestSigFn(ctx, sig)
	}
	return nil
}
func (m *mockHardeningRepo) CheckRateLimit(ctx context.Context, id string, limit int, window time.Duration) (bool, error) {
	if m.checkRateLimitFn != nil {
		return m.checkRateLimitFn(ctx, id, limit, window)
	}
	return true, nil
}
func (m *mockHardeningRepo) AddInstanceBlock(ctx context.Context, block *domain.InstanceBlock) error {
	if m.addInstanceBlockFn != nil {
		return m.addInstanceBlockFn(ctx, block)
	}
	return nil
}
func (m *mockHardeningRepo) RemoveInstanceBlock(ctx context.Context, d string) error {
	if m.removeInstanceBlock != nil {
		return m.removeInstanceBlock(ctx, d)
	}
	return nil
}
func (m *mockHardeningRepo) IsInstanceBlocked(ctx context.Context, d string) (bool, error) {
	if m.isInstanceBlockedFn != nil {
		return m.isInstanceBlockedFn(ctx, d)
	}
	return false, nil
}
func (m *mockHardeningRepo) GetInstanceBlocks(ctx context.Context) ([]domain.InstanceBlock, error) {
	if m.getInstanceBlocksFn != nil {
		return m.getInstanceBlocksFn(ctx)
	}
	return []domain.InstanceBlock{}, nil
}
func (m *mockHardeningRepo) AddActorBlock(ctx context.Context, block *domain.ActorBlock) error {
	if m.addActorBlockFn != nil {
		return m.addActorBlockFn(ctx, block)
	}
	return nil
}
func (m *mockHardeningRepo) IsActorBlocked(ctx context.Context, did, handle string) (bool, error) {
	if m.isActorBlockedFn != nil {
		return m.isActorBlockedFn(ctx, did, handle)
	}
	return false, nil
}
func (m *mockHardeningRepo) CreateAbuseReport(ctx context.Context, report *domain.FederationAbuseReport) error {
	if m.createAbuseReportFn != nil {
		return m.createAbuseReportFn(ctx, report)
	}
	return nil
}
func (m *mockHardeningRepo) GetAbuseReports(ctx context.Context, status string, limit int) ([]domain.FederationAbuseReport, error) {
	if m.getAbuseReportsFn != nil {
		return m.getAbuseReportsFn(ctx, status, limit)
	}
	return []domain.FederationAbuseReport{}, nil
}
func (m *mockHardeningRepo) UpdateAbuseReport(ctx context.Context, id, status, resolution, resolvedBy string) error {
	if m.updateAbuseReportFn != nil {
		return m.updateAbuseReportFn(ctx, id, status, resolution, resolvedBy)
	}
	return nil
}
func (m *mockHardeningRepo) RefreshHealthSummary(ctx context.Context) error {
	if m.refreshHealthFn != nil {
		return m.refreshHealthFn(ctx)
	}
	return nil
}
func (m *mockHardeningRepo) GetHealthSummary(ctx context.Context) ([]domain.FederationHealthSummary, error) {
	if m.getHealthSummaryFn != nil {
		return m.getHealthSummaryFn(ctx)
	}
	return []domain.FederationHealthSummary{}, nil
}
func (m *mockHardeningRepo) GetMetrics(ctx context.Context, metricType string, since time.Time, limit int) ([]domain.FederationMetric, error) {
	if m.getMetricsFn != nil {
		return m.getMetricsFn(ctx, metricType, since, limit)
	}
	return []domain.FederationMetric{}, nil
}
func (m *mockHardeningRepo) RecordMetric(ctx context.Context, metric *domain.FederationMetric) error {
	if m.recordMetricFn != nil {
		return m.recordMetricFn(ctx, metric)
	}
	return nil
}
func (m *mockHardeningRepo) CleanupExpired(ctx context.Context) error {
	if m.cleanupFn != nil {
		return m.cleanupFn(ctx)
	}
	return nil
}

type mockFedSvc struct{}

func (m *mockFedSvc) ProcessNext(context.Context) (bool, error) { return false, nil }

func newHardeningHandler(repo *mockHardeningRepo) *FederationHardeningHandler {
	cfg := &config.Config{JWTSecret: "test-secret"}
	svc := usecase.NewFederationHardeningService(repo, &mockFedSvc{}, cfg)
	_ = svc.Initialize(context.Background())
	return NewFederationHardeningHandler(svc)
}

type mockUserRepo struct {
	countFn func(ctx context.Context) (int64, error)
}

func (m *mockUserRepo) Create(context.Context, *domain.User, string) error          { return nil }
func (m *mockUserRepo) GetByID(context.Context, string) (*domain.User, error)       { return nil, nil }
func (m *mockUserRepo) GetByEmail(context.Context, string) (*domain.User, error)    { return nil, nil }
func (m *mockUserRepo) GetByUsername(context.Context, string) (*domain.User, error) { return nil, nil }
func (m *mockUserRepo) Update(context.Context, *domain.User) error                  { return nil }
func (m *mockUserRepo) Delete(context.Context, string) error                        { return nil }
func (m *mockUserRepo) GetPasswordHash(context.Context, string) (string, error)     { return "", nil }
func (m *mockUserRepo) UpdatePassword(context.Context, string, string) error        { return nil }
func (m *mockUserRepo) List(context.Context, int, int) ([]*domain.User, error)      { return nil, nil }
func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	if m.countFn != nil {
		return m.countFn(ctx)
	}
	return 0, nil
}
func (m *mockUserRepo) SetAvatarFields(context.Context, string, sql.NullString, sql.NullString) error {
	return nil
}
func (m *mockUserRepo) MarkEmailAsVerified(context.Context, string) error { return nil }
func (m *mockUserRepo) Anonymize(_ context.Context, _ string) error       { return nil }

type mockVideoRepo struct {
	countFn func(ctx context.Context) (int64, error)
}

func (m *mockVideoRepo) Create(context.Context, *domain.Video) error { return nil }
func (m *mockVideoRepo) GetByID(context.Context, string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByIDs(context.Context, []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByUserID(context.Context, string, int, int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Update(context.Context, *domain.Video) error  { return nil }
func (m *mockVideoRepo) Delete(context.Context, string, string) error { return nil }
func (m *mockVideoRepo) List(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Search(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) UpdateProcessingInfo(context.Context, port.VideoProcessingParams) error {
	return nil
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(context.Context, port.VideoProcessingWithCIDsParams) error {
	return nil
}
func (m *mockVideoRepo) Count(ctx context.Context) (int64, error) {
	if m.countFn != nil {
		return m.countFn(ctx)
	}
	return 0, nil
}
func (m *mockVideoRepo) GetVideosForMigration(context.Context, int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByRemoteURI(context.Context, string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) CreateRemoteVideo(context.Context, *domain.Video) error { return nil }
func (m *mockVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVideoRepo) AppendOutputPath(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func TestRequireAdmin_Unit(t *testing.T) {
	t.Run("no role in context returns forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rr := httptest.NewRecorder()
		ok := requireAdmin(rr, req)
		assert.False(t, ok)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("non-admin role returns forbidden", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserRoleKey, "user"))
		rr := httptest.NewRecorder()
		ok := requireAdmin(rr, req)
		assert.False(t, ok)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("admin role succeeds", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = withAdmin(req)
		rr := httptest.NewRecorder()
		ok := requireAdmin(rr, req)
		assert.True(t, ok)
	})
}

func TestAdminFederationHandlers_Forbidden_Unit(t *testing.T) {
	handlers := NewAdminFederationHandlers(nil)

	endpoints := []struct {
		name   string
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{"ListJobs", http.MethodGet, "/admin/federation/jobs", handlers.ListJobs},
		{"GetJob", http.MethodGet, "/admin/federation/jobs/abc", handlers.GetJob},
		{"RetryJob", http.MethodPost, "/admin/federation/jobs/abc/retry", handlers.RetryJob},
		{"DeleteJob", http.MethodDelete, "/admin/federation/jobs/abc", handlers.DeleteJob},
	}

	for _, ep := range endpoints {
		t.Run(ep.name+" forbidden", func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rr := httptest.NewRecorder()
			ep.fn(rr, req)
			require.Equal(t, http.StatusForbidden, rr.Code)
		})
	}
}

func TestAdminFederationActorsHandlers_Forbidden_Unit(t *testing.T) {
	handlers := NewAdminFederationActorsHandlers(nil)

	endpoints := []struct {
		name   string
		method string
		path   string
		fn     http.HandlerFunc
	}{
		{"ListActors", http.MethodGet, "/admin/federation/actors", handlers.ListActors},
		{"UpsertActor", http.MethodPost, "/admin/federation/actors", handlers.UpsertActor},
		{"UpdateActor", http.MethodPut, "/admin/federation/actors/test", handlers.UpdateActor},
		{"DeleteActor", http.MethodDelete, "/admin/federation/actors/test", handlers.DeleteActor},
	}

	for _, ep := range endpoints {
		t.Run(ep.name+" forbidden", func(t *testing.T) {
			req := httptest.NewRequest(ep.method, ep.path, nil)
			rr := httptest.NewRecorder()
			ep.fn(rr, req)
			require.Equal(t, http.StatusForbidden, rr.Code)
		})
	}
}

func TestUpsertActor_BadRequest_Unit(t *testing.T) {
	handlers := NewAdminFederationActorsHandlers(nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/federation/actors", strings.NewReader("{bad"))
		req = withAdmin(req)
		rr := httptest.NewRecorder()
		handlers.UpsertActor(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("empty actor", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/admin/federation/actors", strings.NewReader(`{"actor":"","enabled":true}`))
		req = withAdmin(req)
		rr := httptest.NewRecorder()
		handlers.UpsertActor(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestUpdateActor_BadRequest_Unit(t *testing.T) {
	handlers := NewAdminFederationActorsHandlers(nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/admin/federation/actors/test", strings.NewReader("{bad"))
		req = withAdminAndParam(req, "actor", "test")
		rr := httptest.NewRecorder()
		handlers.UpdateActor(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestHardeningHandler_GetDashboard_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/dashboard", nil)
		rr := httptest.NewRecorder()
		h.GetDashboard(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("health summary error still succeeds (dashboard ignores errors)", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getHealthSummaryFn: func(context.Context) ([]domain.FederationHealthSummary, error) {
				return nil, errors.New("db down")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/dashboard", nil)
		rr := httptest.NewRecorder()
		h.GetDashboard(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestHardeningHandler_GetHealthMetrics_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getHealthSummaryFn: func(context.Context) ([]domain.FederationHealthSummary, error) {
				return []domain.FederationHealthSummary{{MetricType: "job_success"}}, nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/health", nil)
		rr := httptest.NewRecorder()
		h.GetHealthMetrics(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getHealthSummaryFn: func(context.Context) ([]domain.FederationHealthSummary, error) {
				return nil, errors.New("fail")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/health", nil)
		rr := httptest.NewRecorder()
		h.GetHealthMetrics(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_GetDLQJobs_Unit(t *testing.T) {
	t.Run("success with defaults", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/dlq", nil)
		rr := httptest.NewRecorder()
		h.GetDLQJobs(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("success with custom limit", func(t *testing.T) {
		var capturedLimit int
		repo := &mockHardeningRepo{
			getDLQJobsFn: func(_ context.Context, limit int, _ bool) ([]domain.DeadLetterJob, error) {
				capturedLimit = limit
				return []domain.DeadLetterJob{}, nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/dlq?limit=10&can_retry=true", nil)
		rr := httptest.NewRecorder()
		h.GetDLQJobs(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, 10, capturedLimit)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getDLQJobsFn: func(context.Context, int, bool) ([]domain.DeadLetterJob, error) {
				return nil, errors.New("dlq error")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/dlq", nil)
		rr := httptest.NewRecorder()
		h.GetDLQJobs(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_RetryDLQJob_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/dlq/job-1/retry", nil)
		req = withURLParam(req, "id", "job-1")
		rr := httptest.NewRecorder()
		h.RetryDLQJob(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			retryDLQJobFn: func(context.Context, string) error {
				return errors.New("retry failed")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/dlq/job-1/retry", nil)
		req = withURLParam(req, "id", "job-1")
		rr := httptest.NewRecorder()
		h.RetryDLQJob(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_BlockInstance_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"instance_domain":"evil.example","reason":"spam","severity":"full","blocked_by":"admin","duration":"24h"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/instances", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockInstance(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/instances", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.BlockInstance(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid duration", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"instance_domain":"evil.example","reason":"spam","severity":"full","blocked_by":"admin","duration":"not-a-duration"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/instances", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockInstance(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			addInstanceBlockFn: func(context.Context, *domain.InstanceBlock) error {
				return errors.New("db error")
			},
		}
		h := newHardeningHandler(repo)

		body := `{"instance_domain":"evil.example","reason":"spam","severity":"full","blocked_by":"admin"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/instances", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockInstance(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_UnblockInstance_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/federation/hardening/blocklist/instances/evil.example", nil)
		req = withURLParam(req, "domain", "evil.example")
		rr := httptest.NewRecorder()
		h.UnblockInstance(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			removeInstanceBlock: func(context.Context, string) error {
				return errors.New("not found")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/federation/hardening/blocklist/instances/evil.example", nil)
		req = withURLParam(req, "domain", "evil.example")
		rr := httptest.NewRecorder()
		h.UnblockInstance(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_GetInstanceBlocks_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getInstanceBlocksFn: func(context.Context) ([]domain.InstanceBlock, error) {
				return []domain.InstanceBlock{{InstanceDomain: "evil.example"}}, nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/blocklist/instances", nil)
		rr := httptest.NewRecorder()
		h.GetInstanceBlocks(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getInstanceBlocksFn: func(context.Context) ([]domain.InstanceBlock, error) {
				return nil, errors.New("fail")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/blocklist/instances", nil)
		rr := httptest.NewRecorder()
		h.GetInstanceBlocks(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_BlockActor_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"actor_did":"did:plc:bad","actor_handle":"bad.bsky","reason":"harassment","severity":"full","blocked_by":"admin"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/actors", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockActor(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/actors", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.BlockActor(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("invalid duration", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"actor_did":"did:plc:bad","actor_handle":"bad.bsky","reason":"spam","severity":"full","blocked_by":"admin","duration":"xyz"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/actors", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockActor(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			addActorBlockFn: func(context.Context, *domain.ActorBlock) error {
				return errors.New("db error")
			},
		}
		h := newHardeningHandler(repo)

		body := `{"actor_did":"did:plc:bad","actor_handle":"bad.bsky","reason":"spam","severity":"full","blocked_by":"admin"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/blocklist/actors", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.BlockActor(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_CheckBlocked_Unit(t *testing.T) {
	t.Run("nothing blocked", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/blocklist/check", nil)
		rr := httptest.NewRecorder()
		h.CheckBlocked(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		var result map[string]bool
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &struct{ Data *map[string]bool }{Data: &result}))
		assert.False(t, result["instance_blocked"])
		assert.False(t, result["actor_blocked"])
	})

	t.Run("instance blocked", func(t *testing.T) {
		repo := &mockHardeningRepo{
			isInstanceBlockedFn: func(_ context.Context, d string) (bool, error) {
				return d == "evil.example", nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/blocklist/check?instance=evil.example", nil)
		rr := httptest.NewRecorder()
		h.CheckBlocked(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("actor blocked", func(t *testing.T) {
		repo := &mockHardeningRepo{
			isActorBlockedFn: func(_ context.Context, did, handle string) (bool, error) {
				return did == "did:plc:bad", nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/blocklist/check?actor_did=did:plc:bad", nil)
		rr := httptest.NewRecorder()
		h.CheckBlocked(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})
}

func TestHardeningHandler_ReportAbuse_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"reporter_did":"did:plc:reporter","report_type":"spam","content_uri":"at://did:plc:bad/post/1","description":"spamming"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/report", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.ReportAbuse(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/report", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.ReportAbuse(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			createAbuseReportFn: func(context.Context, *domain.FederationAbuseReport) error {
				return errors.New("db error")
			},
		}
		h := newHardeningHandler(repo)

		body := `{"reporter_did":"did:plc:reporter","report_type":"spam","description":"spamming"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/report", strings.NewReader(body))
		rr := httptest.NewRecorder()
		h.ReportAbuse(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_GetAbuseReports_Unit(t *testing.T) {
	t.Run("success with default limit", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/abuse/reports", nil)
		rr := httptest.NewRecorder()
		h.GetAbuseReports(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("success with custom limit", func(t *testing.T) {
		var capturedLimit int
		repo := &mockHardeningRepo{
			getAbuseReportsFn: func(_ context.Context, _ string, limit int) ([]domain.FederationAbuseReport, error) {
				capturedLimit = limit
				return []domain.FederationAbuseReport{}, nil
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/abuse/reports?limit=5", nil)
		rr := httptest.NewRecorder()
		h.GetAbuseReports(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
		assert.Equal(t, 5, capturedLimit)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			getAbuseReportsFn: func(context.Context, string, int) ([]domain.FederationAbuseReport, error) {
				return nil, errors.New("fail")
			},
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/federation/hardening/abuse/reports", nil)
		rr := httptest.NewRecorder()
		h.GetAbuseReports(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_ResolveAbuseReport_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		body := `{"resolution":"dismissed","resolved_by":"admin","take_action":false}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/reports/rpt-1/resolve", strings.NewReader(body))
		req = withURLParam(req, "id", "rpt-1")
		rr := httptest.NewRecorder()
		h.ResolveAbuseReport(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid json", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/reports/rpt-1/resolve", strings.NewReader("{bad"))
		req = withURLParam(req, "id", "rpt-1")
		rr := httptest.NewRecorder()
		h.ResolveAbuseReport(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			updateAbuseReportFn: func(context.Context, string, string, string, string) error {
				return errors.New("db error")
			},
		}
		h := newHardeningHandler(repo)

		body := `{"resolution":"banned","resolved_by":"admin","take_action":true}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/abuse/reports/rpt-1/resolve", strings.NewReader(body))
		req = withURLParam(req, "id", "rpt-1")
		rr := httptest.NewRecorder()
		h.ResolveAbuseReport(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestHardeningHandler_RunCleanup_Unit(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := &mockHardeningRepo{}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/cleanup", nil)
		rr := httptest.NewRecorder()
		h.RunCleanup(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("error", func(t *testing.T) {
		repo := &mockHardeningRepo{
			cleanupFn: func(context.Context) error { return errors.New("fail") },
		}
		h := newHardeningHandler(repo)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/federation/hardening/cleanup", nil)
		rr := httptest.NewRecorder()
		h.RunCleanup(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})
}

func TestFederationMiddleware_Unit(t *testing.T) {
	repo := &mockHardeningRepo{
		isInstanceBlockedFn: func(_ context.Context, d string) (bool, error) {
			return d == "blocked.example", nil
		},
	}
	cfg := &config.Config{JWTSecret: "test-secret"}
	svc := usecase.NewFederationHardeningService(repo, &mockFedSvc{}, cfg)
	_ = svc.Initialize(context.Background())

	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusOK)
	})

	mw := FederationMiddleware(svc)

	t.Run("blocked instance returns forbidden", func(t *testing.T) {
		nextCalled = false
		req := httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(`{}`))
		req.Header.Set("X-Federation-Instance", "blocked.example")
		req.Header.Set("X-Federation-Signature", "some-sig")
		rr := httptest.NewRecorder()
		mw(next).ServeHTTP(rr, req)
		require.Equal(t, http.StatusForbidden, rr.Code)
		assert.False(t, nextCalled)
	})
}

func TestActivityPub_NodeInfo20_WithRepos_Unit(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:                  "https://video.example",
		ActivityPubInstanceDescription: "Test instance",
	}

	t.Run("with real counts", func(t *testing.T) {
		uRepo := &mockUserRepo{
			countFn: func(context.Context) (int64, error) { return 42, nil },
		}
		vRepo := &mockVideoRepo{
			countFn: func(context.Context) (int64, error) { return 100, nil },
		}
		handlers := NewActivityPubHandlers(nil, cfg, uRepo, vRepo)

		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rr := httptest.NewRecorder()
		handlers.NodeInfo20(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var nodeInfo domain.NodeInfo
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &nodeInfo))
		assert.Equal(t, 42, nodeInfo.Usage.Users.Total)
		assert.Equal(t, 100, nodeInfo.Usage.LocalPosts)
	})

	t.Run("with repo errors falls back to zero", func(t *testing.T) {
		uRepo := &mockUserRepo{
			countFn: func(context.Context) (int64, error) { return 0, errors.New("db fail") },
		}
		vRepo := &mockVideoRepo{
			countFn: func(context.Context) (int64, error) { return 0, errors.New("db fail") },
		}
		handlers := NewActivityPubHandlers(nil, cfg, uRepo, vRepo)

		req := httptest.NewRequest(http.MethodGet, "/nodeinfo/2.0", nil)
		rr := httptest.NewRecorder()
		handlers.NodeInfo20(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var nodeInfo domain.NodeInfo
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &nodeInfo))
		assert.Equal(t, 0, nodeInfo.Usage.Users.Total)
		assert.Equal(t, 0, nodeInfo.Usage.LocalPosts)
	})
}

func TestActivityPub_GetFollowing_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	t.Run("collection overview", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users/alice/following", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		rr := httptest.NewRecorder()

		mockSvc.On("GetFollowingCount", req.Context(), "alice").Return(3, nil).Once()
		handlers.GetFollowing(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var collection domain.OrderedCollection
		require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &collection))
		assert.Equal(t, 3, collection.TotalItems)
		assert.Contains(t, collection.ID, "following")

		mockSvc.AssertExpectations(t)
	})

	t.Run("paginated", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users/alice/following?page=0", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		rr := httptest.NewRecorder()

		page := &domain.OrderedCollectionPage{
			Context:      domain.ActivityStreamsContext,
			Type:         domain.ObjectTypeOrderedCollectionPage,
			TotalItems:   3,
			OrderedItems: []interface{}{"https://other.example/users/bob"},
		}
		mockSvc.On("GetFollowing", req.Context(), "alice", 0, 20).Return(page, nil).Once()
		handlers.GetFollowing(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		mockSvc.AssertExpectations(t)
	})
}

func TestActivityPub_PostSharedInbox_Errors_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{PublicBaseURL: "https://video.example"}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader("not json"))
		rr := httptest.NewRecorder()
		handlers.PostSharedInbox(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("service error", func(t *testing.T) {
		activity := map[string]interface{}{"type": "Create"}
		body, _ := json.Marshal(activity)
		req := httptest.NewRequest(http.MethodPost, "/inbox", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		mockSvc.On("HandleInboxActivity", req.Context(), activity, req).Return(errors.New("fail")).Once()
		handlers.PostSharedInbox(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
		mockSvc.AssertExpectations(t)
	})
}

func TestActivityPub_GetActor_MissingUsername_Unit(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest(http.MethodGet, "/users/", nil)
	rr := httptest.NewRecorder()
	handlers.GetActor(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_GetOutbox_MissingUsername_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/", nil)
	rr := httptest.NewRecorder()
	handlers.GetOutbox(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_GetFollowers_MissingUsername_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/", nil)
	rr := httptest.NewRecorder()
	handlers.GetFollowers(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_GetFollowing_MissingUsername_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/", nil)
	rr := httptest.NewRecorder()
	handlers.GetFollowing(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_WebFinger_InvalidAcctFormat_Unit(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}
	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger?resource=acct:justname", nil)
	rr := httptest.NewRecorder()
	handlers.WebFinger(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_WebFinger_HTTPResource_InvalidPath_Unit(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}
	handlers := &ActivityPubHandlers{cfg: cfg}

	req := httptest.NewRequest(http.MethodGet, "/.well-known/webfinger?resource=https://video.example/profiles/alice", nil)
	rr := httptest.NewRecorder()
	handlers.WebFinger(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_PostInbox_EmptyBody_Unit(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest(http.MethodPost, "/users/alice/inbox", strings.NewReader(""))
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", "alice")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	handlers.PostInbox(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestActivityPub_GetOutbox_CountError_Unit(t *testing.T) {
	mockSvc := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}
	handlers := NewActivityPubHandlers(mockSvc, cfg, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/users/alice/outbox", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", "alice")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()

	mockSvc.On("GetOutboxCount", req.Context(), "alice").Return(0, errors.New("user not found")).Once()
	handlers.GetOutbox(rr, req)

	require.Equal(t, http.StatusNotFound, rr.Code)
	mockSvc.AssertExpectations(t)
}

func TestHardeningHandler_RegisterRoutes_Unit(t *testing.T) {
	repo := &mockHardeningRepo{}
	h := newHardeningHandler(repo)

	r := chi.NewRouter()
	h.RegisterRoutes(r)
}

func TestNewFederationHandlers_Unit(t *testing.T) {
	h := NewFederationHandlers(nil)
	require.NotNil(t, h)
}

func TestNewAdminFederationHandlers_Unit(t *testing.T) {
	h := NewAdminFederationHandlers(nil)
	require.NotNil(t, h)
}

func TestNewAdminFederationActorsHandlers_Unit(t *testing.T) {
	h := NewAdminFederationActorsHandlers(nil)
	require.NotNil(t, h)
}

func TestActivityPub_GetInbox_ReturnsEmptyOrderedCollection_Unit(t *testing.T) {
	handlers := &ActivityPubHandlers{}

	req := httptest.NewRequest(http.MethodGet, "/users/alice/inbox", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("username", "alice")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rr := httptest.NewRecorder()
	handlers.GetInbox(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)

	contentType := rr.Header().Get("Content-Type")
	require.Contains(t, contentType, "application/activity+json")

	var body map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&body)
	require.NoError(t, err)

	require.Equal(t, "OrderedCollection", body["type"])
	require.EqualValues(t, 0, body["totalItems"])
}

func TestNewRedundancyHandler_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)
	require.NotNil(t, h)
}

func TestRedundancyHandler_RegisterRoutes_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)
	r := chi.NewRouter()
	h.RegisterRoutes(r, "test-secret")
}

func TestRedundancyHandler_RegisterInstancePeer_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/instances", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.RegisterInstancePeer(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing instance_url", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/instances", strings.NewReader(`{"instance_url":""}`))
		rr := httptest.NewRecorder()
		h.RegisterInstancePeer(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestRedundancyHandler_UpdateInstancePeer_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/redundancy/instances/abc", strings.NewReader("{bad"))
		req = withURLParam(req, "id", "abc")
		rr := httptest.NewRecorder()
		h.UpdateInstancePeer(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestRedundancyHandler_DiscoverInstance_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/instances/discover", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.DiscoverInstance(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing instance_url", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/instances/discover", strings.NewReader(`{"instance_url":""}`))
		rr := httptest.NewRecorder()
		h.DiscoverInstance(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestRedundancyHandler_CreateRedundancy_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	t.Run("invalid json", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/create", strings.NewReader("{bad"))
		rr := httptest.NewRecorder()
		h.CreateRedundancy(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing video_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/create", strings.NewReader(`{"video_id":"","instance_id":""}`))
		rr := httptest.NewRecorder()
		h.CreateRedundancy(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing instance_id", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/create", strings.NewReader(`{"video_id":"v1","instance_id":""}`))
		rr := httptest.NewRecorder()
		h.CreateRedundancy(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestRedundancyHandler_CreatePolicy_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/redundancy/policies", strings.NewReader("{bad"))
	rr := httptest.NewRecorder()
	h.CreatePolicy(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestRedundancyHandler_UpdatePolicy_BadRequest_Unit(t *testing.T) {
	h := NewRedundancyHandler(nil, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/redundancy/policies/p1", strings.NewReader("{bad"))
	req = withURLParam(req, "id", "p1")
	rr := httptest.NewRecorder()
	h.UpdatePolicy(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
