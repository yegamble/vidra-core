package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase/ipfs_streaming"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mockEncodingRepo implementation
type mockEncodingRepo struct{}

func (m *mockEncodingRepo) CreateJob(ctx context.Context, job *domain.EncodingJob) error { return nil }
func (m *mockEncodingRepo) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) UpdateJob(ctx context.Context, job *domain.EncodingJob) error { return nil }
func (m *mockEncodingRepo) DeleteJob(ctx context.Context, jobID string) error             { return nil }
func (m *mockEncodingRepo) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *mockEncodingRepo) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	return nil
}
func (m *mockEncodingRepo) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	return nil
}
func (m *mockEncodingRepo) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	return nil
}
func (m *mockEncodingRepo) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	return map[string]int64{
		"pending":    0,
		"processing": 0,
		"completed":  0,
		"failed":     0,
	}, nil
}

func TestUnauthenticatedEndpoints(t *testing.T) {
	// Setup
	jwtSecret := "test-secret"
	cfg := &config.Config{
		JWTSecret:           jwtSecret,
		EnableIPFSStreaming: true,
		RateLimitDuration:   time.Minute,
		RateLimitRequests:   100,
	}

	// Mocks
	userRepo := newMockUserRepo()
	encodingRepo := &mockEncodingRepo{}
	ipfsService := ipfs_streaming.NewService(cfg)

	deps := &shared.HandlerDependencies{
		UserRepo:             userRepo,
		EncodingRepo:         encodingRepo,
		IPFSStreamingService: ipfsService,
		JWTSecret:            jwtSecret,
		RedisPingTimeout:     time.Second,
	}

	r := chi.NewRouter()
	rlManager := middleware.NewRateLimiterManager()
	RegisterRoutesWithDependencies(r, cfg, rlManager, deps)

	// Create tokens
	adminToken := generateTestJWT(jwtSecret, uuid.NewString(), "admin")
	userToken := generateTestJWT(jwtSecret, uuid.NewString(), "user")

	// Test cases
	tests := []struct {
		name      string
		path      string
		method    string
		token     string
		wantCode  int
	}{
		// Unauthenticated
		{
			name:     "IPFS Metrics Unauth",
			path:     "/api/v1/ipfs/metrics",
			method:   "GET",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "IPFS Gateways Unauth",
			path:     "/api/v1/ipfs/gateways",
			method:   "GET",
			wantCode: http.StatusUnauthorized,
		},
		{
			name:     "Encoding Status Unauth",
			path:     "/api/v1/encoding/status",
			method:   "GET",
			wantCode: http.StatusUnauthorized,
		},

		// Authenticated as User (should be Forbidden)
		{
			name:     "IPFS Metrics User",
			path:     "/api/v1/ipfs/metrics",
			method:   "GET",
			token:    userToken,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "IPFS Gateways User",
			path:     "/api/v1/ipfs/gateways",
			method:   "GET",
			token:    userToken,
			wantCode: http.StatusForbidden,
		},
		{
			name:     "Encoding Status User",
			path:     "/api/v1/encoding/status",
			method:   "GET",
			token:    userToken,
			wantCode: http.StatusForbidden,
		},

		// Authenticated as Admin (should be OK)
		{
			name:     "IPFS Metrics Admin",
			path:     "/api/v1/ipfs/metrics",
			method:   "GET",
			token:    adminToken,
			wantCode: http.StatusOK,
		},
		{
			name:     "IPFS Gateways Admin",
			path:     "/api/v1/ipfs/gateways",
			method:   "GET",
			token:    adminToken,
			wantCode: http.StatusOK,
		},
		{
			name:     "Encoding Status Admin",
			path:     "/api/v1/encoding/status",
			method:   "GET",
			token:    adminToken,
			wantCode: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			rr := httptest.NewRecorder()
			r.ServeHTTP(rr, req)

			if rr.Code != tt.wantCode {
				t.Errorf("Path %s: wanted code %d, got %d. Body: %s", tt.path, tt.wantCode, rr.Code, rr.Body.String())
			}
		})
	}
}
