package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// MockFederationService
// ---------------------------------------------------------------------------

type MockFederationSvc struct {
	mock.Mock
}

func (m *MockFederationSvc) ProcessNext(ctx context.Context) (bool, error) {
	args := m.Called(ctx)
	return args.Bool(0), args.Error(1)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newTestHardeningService(
	repo *MockHardeningRepository,
	fedSvc *MockFederationSvc,
	secCfg *domain.FederationSecurityConfig,
) *FederationHardeningService {
	cfg := &config.Config{JWTSecret: "test-secret-key-for-hmac-1234567890"}
	svc := NewFederationHardeningService(repo, fedSvc, cfg)
	svc.config = secCfg
	return svc
}

func defaultSecurityConfig() *domain.FederationSecurityConfig {
	return &domain.FederationSecurityConfig{
		MaxRequestSize:         1024 * 1024, // 1 MB
		SignatureWindowSeconds: 300,
		RateLimitRequests:      100,
		RateLimitWindow:        time.Minute,
		EnableAbuseReporting:   true,
		MetricsEnabled:         true,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestFederationHardening_NewFederationHardeningService(t *testing.T) {
	repo := new(MockHardeningRepository)
	fedSvc := new(MockFederationSvc)
	cfg := &config.Config{JWTSecret: "secret"}

	svc := NewFederationHardeningService(repo, fedSvc, cfg)

	require.NotNil(t, svc)
	assert.Nil(t, svc.Config(), "config should be nil before Initialize")
}

func TestFederationHardening_Initialize(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		cfg := &config.Config{JWTSecret: "secret"}
		secCfg := defaultSecurityConfig()

		repo.On("GetFederationConfig", mock.Anything).Return(secCfg, nil)

		svc := NewFederationHardeningService(repo, fedSvc, cfg)
		err := svc.Initialize(context.Background())

		require.NoError(t, err)
		assert.Equal(t, secCfg, svc.Config())
		repo.AssertExpectations(t)
	})

	t.Run("repo error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		cfg := &config.Config{JWTSecret: "secret"}

		repo.On("GetFederationConfig", mock.Anything).Return((*domain.FederationSecurityConfig)(nil), errors.New("db error"))

		svc := NewFederationHardeningService(repo, fedSvc, cfg)
		err := svc.Initialize(context.Background())

		require.Error(t, err)
		assert.Contains(t, err.Error(), "load federation config")
	})
}

func TestFederationHardening_Config(t *testing.T) {
	secCfg := defaultSecurityConfig()
	repo := new(MockHardeningRepository)
	fedSvc := new(MockFederationSvc)
	svc := newTestHardeningService(repo, fedSvc, secCfg)

	assert.Equal(t, secCfg, svc.Config())
}

func TestFederationHardening_ProcessJobWithRetry(t *testing.T) {
	ctx := context.Background()

	t.Run("already processed (idempotent)", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		existing := &domain.IdempotencyRecord{
			IdempotencyKey: "job_abc",
			Status:         domain.IdempotencyStatusSuccess,
		}
		repo.On("CheckIdempotency", mock.Anything, "job_abc").Return(existing, nil)

		err := svc.ProcessJobWithRetry(ctx, "abc")
		require.NoError(t, err)
		fedSvc.AssertNotCalled(t, "ProcessNext")
		repo.AssertExpectations(t)
	})

	t.Run("successful processing", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckIdempotency", mock.Anything, "job_j1").Return(nil, nil)
		repo.On("RecordIdempotency", mock.Anything, mock.AnythingOfType("*domain.IdempotencyRecord")).Return(nil)
		fedSvc.On("ProcessNext", mock.Anything).Return(true, nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.ProcessJobWithRetry(ctx, "j1")
		require.NoError(t, err)
		fedSvc.AssertCalled(t, "ProcessNext", mock.Anything)
	})

	t.Run("processing failure triggers backoff", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckIdempotency", mock.Anything, "job_fail1").Return(nil, nil)
		repo.On("RecordIdempotency", mock.Anything, mock.AnythingOfType("*domain.IdempotencyRecord")).Return(nil)
		fedSvc.On("ProcessNext", mock.Anything).Return(false, errors.New("process error"))
		repo.On("UpdateJobWithBackoff", mock.Anything, "fail1", 2, "process error").Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.ProcessJobWithRetry(ctx, "fail1")
		require.Error(t, err)
		assert.Equal(t, "process error", err.Error())
	})

	t.Run("idempotency check error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckIdempotency", mock.Anything, "job_err").Return(nil, errors.New("db error"))

		err := svc.ProcessJobWithRetry(ctx, "err")
		require.Error(t, err)
		assert.Equal(t, "db error", err.Error())
	})

	t.Run("processed false does not record success metric", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckIdempotency", mock.Anything, "job_nop").Return(nil, nil)
		repo.On("RecordIdempotency", mock.Anything, mock.AnythingOfType("*domain.IdempotencyRecord")).Return(nil)
		fedSvc.On("ProcessNext", mock.Anything).Return(false, nil)

		err := svc.ProcessJobWithRetry(ctx, "nop")
		require.NoError(t, err)
		// RecordMetric should NOT be called for job_success when processed=false
		repo.AssertNotCalled(t, "RecordMetric", mock.Anything, mock.MatchedBy(func(m *domain.FederationMetric) bool {
			return m.MetricType == domain.MetricTypeJobSuccess
		}))
	})
}

func TestFederationHardening_ValidateRequestSignature(t *testing.T) {
	ctx := context.Background()

	t.Run("missing signature", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		err := svc.ValidateRequestSignature(ctx, "", "example.com", "/inbox", nil, "")
		require.Error(t, err)
		assert.Equal(t, "missing signature", err.Error())
	})

	t.Run("duplicate signature (replay attack)", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(true, nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		ts := strconv.FormatInt(time.Now().Unix(), 10)
		err := svc.ValidateRequestSignature(ctx, "sig123", "example.com", "/inbox", []byte("body"), ts)
		require.Error(t, err)
		assert.Equal(t, "duplicate request signature", err.Error())
	})

	t.Run("expired timestamp", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)

		oldTs := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
		err := svc.ValidateRequestSignature(ctx, "sig-old", "example.com", "/inbox", []byte("body"), oldTs)
		require.Error(t, err)
		assert.Equal(t, "signature timestamp expired", err.Error())
	})

	t.Run("missing timestamp", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)

		err := svc.ValidateRequestSignature(ctx, "sig-nots", "example.com", "/inbox", []byte("body"), "")
		require.Error(t, err)
		assert.Equal(t, "missing signature timestamp", err.Error())
	})

	t.Run("invalid timestamp format", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)

		err := svc.ValidateRequestSignature(ctx, "sig-bad", "example.com", "/inbox", []byte("body"), "not-a-timestamp")
		require.Error(t, err)
		assert.Equal(t, "invalid signature timestamp", err.Error())
	})

	t.Run("timestamp too far in future", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)

		futureTs := strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)
		err := svc.ValidateRequestSignature(ctx, "sig-future", "example.com", "/inbox", []byte("body"), futureTs)
		require.Error(t, err)
		assert.Equal(t, "signature timestamp too far in future", err.Error())
	})

	t.Run("valid signature with unix timestamp", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)
		repo.On("RecordRequestSignature", mock.Anything, mock.AnythingOfType("*domain.RequestSignature")).Return(nil)

		ts := strconv.FormatInt(time.Now().Unix(), 10)
		err := svc.ValidateRequestSignature(ctx, "sig-ok", "example.com", "/inbox", []byte("body"), ts)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("valid signature with RFC3339 timestamp", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)
		repo.On("RecordRequestSignature", mock.Anything, mock.AnythingOfType("*domain.RequestSignature")).Return(nil)

		ts := time.Now().Format(time.RFC3339)
		err := svc.ValidateRequestSignature(ctx, "sig-rfc", "example.com", "/inbox", []byte("body"), ts)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("repo CheckRequestSignature error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, errors.New("db err"))

		ts := strconv.FormatInt(time.Now().Unix(), 10)
		err := svc.ValidateRequestSignature(ctx, "sig-err", "example.com", "/inbox", []byte("body"), ts)
		require.Error(t, err)
		assert.Equal(t, "db err", err.Error())
	})
}

func TestFederationHardening_CheckRateLimit(t *testing.T) {
	ctx := context.Background()

	t.Run("nil config allows all", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, nil)

		err := svc.CheckRateLimit(ctx, "example.com")
		require.NoError(t, err)
	})

	t.Run("allowed", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		repo.On("CheckRateLimit", mock.Anything, "example.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(true, nil)

		err := svc.CheckRateLimit(ctx, "example.com")
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("rate limited", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		repo.On("CheckRateLimit", mock.Anything, "spam.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(false, nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.CheckRateLimit(ctx, "spam.com")
		require.Error(t, err)
		assert.Equal(t, "rate limit exceeded", err.Error())
	})

	t.Run("repo error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		repo.On("CheckRateLimit", mock.Anything, "err.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(false, errors.New("redis err"))

		err := svc.CheckRateLimit(ctx, "err.com")
		require.Error(t, err)
		assert.Equal(t, "redis err", err.Error())
	})
}

func TestFederationHardening_ValidateRequestSize(t *testing.T) {
	t.Run("nil config allows all", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, nil)

		err := svc.ValidateRequestSize(999999999)
		require.NoError(t, err)
	})

	t.Run("within limits", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		err := svc.ValidateRequestSize(512)
		require.NoError(t, err)
	})

	t.Run("exceeds limits", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		err := svc.ValidateRequestSize(2 * 1024 * 1024) // 2 MB
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})
}

func TestFederationHardening_BlockInstance(t *testing.T) {
	ctx := context.Background()

	t.Run("block with duration", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("AddInstanceBlock", mock.Anything, mock.MatchedBy(func(b *domain.InstanceBlock) bool {
			return b.InstanceDomain == "bad.com" &&
				b.Severity == domain.BlockSeverityBlock &&
				b.ExpiresAt != nil
		})).Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.BlockInstance(ctx, "bad.com", "spam", domain.BlockSeverityBlock, "admin", 24*time.Hour)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("block without duration (permanent)", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("AddInstanceBlock", mock.Anything, mock.MatchedBy(func(b *domain.InstanceBlock) bool {
			return b.InstanceDomain == "evil.com" && b.ExpiresAt == nil
		})).Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.BlockInstance(ctx, "evil.com", "illegal", domain.BlockSeverityBlock, "admin", 0)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("repo error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("AddInstanceBlock", mock.Anything, mock.Anything).Return(errors.New("db err"))

		err := svc.BlockInstance(ctx, "fail.com", "reason", domain.BlockSeverityBlock, "admin", 0)
		require.Error(t, err)
		assert.Equal(t, "db err", err.Error())
	})
}

func TestFederationHardening_UnblockInstance(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("RemoveInstanceBlock", mock.Anything, "bad.com").Return(nil)

		err := svc.UnblockInstance(ctx, "bad.com")
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("RemoveInstanceBlock", mock.Anything, "bad.com").Return(errors.New("not found"))

		err := svc.UnblockInstance(ctx, "bad.com")
		require.Error(t, err)
	})
}

func TestFederationHardening_IsInstanceBlocked(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name    string
		domain  string
		blocked bool
		repoErr error
		wantErr bool
	}{
		{"blocked instance", "bad.com", true, nil, false},
		{"allowed instance", "good.com", false, nil, false},
		{"repo error", "err.com", false, errors.New("db err"), true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			repo := new(MockHardeningRepository)
			fedSvc := new(MockFederationSvc)
			svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

			repo.On("IsInstanceBlocked", mock.Anything, tc.domain).Return(tc.blocked, tc.repoErr)

			blocked, err := svc.IsInstanceBlocked(ctx, tc.domain)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.blocked, blocked)
			}
		})
	}
}

func TestFederationHardening_GetInstanceBlocks(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		blocks := []domain.InstanceBlock{
			{InstanceDomain: "bad.com"},
			{InstanceDomain: "evil.com"},
		}
		repo.On("GetInstanceBlocks", mock.Anything).Return(blocks, nil)

		result, err := svc.GetInstanceBlocks(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})
}

func TestFederationHardening_BlockActor(t *testing.T) {
	ctx := context.Background()

	t.Run("block with duration", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("AddActorBlock", mock.Anything, mock.MatchedBy(func(b *domain.ActorBlock) bool {
			return *b.ActorDID == "did:plc:abc" && b.ExpiresAt != nil
		})).Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.BlockActor(ctx, "did:plc:abc", "@user.bsky.social", "spam", domain.BlockSeverityBlock, "admin", time.Hour)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("block without duration", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("AddActorBlock", mock.Anything, mock.MatchedBy(func(b *domain.ActorBlock) bool {
			return b.ExpiresAt == nil
		})).Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.BlockActor(ctx, "did:plc:xyz", "@evil.bsky.social", "abuse", domain.BlockSeverityShadowban, "admin", 0)
		require.NoError(t, err)
	})
}

func TestFederationHardening_IsActorBlocked(t *testing.T) {
	ctx := context.Background()

	t.Run("blocked", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("IsActorBlocked", mock.Anything, "did:plc:abc", "@user.bsky.social").Return(true, nil)

		blocked, err := svc.IsActorBlocked(ctx, "did:plc:abc", "@user.bsky.social")
		require.NoError(t, err)
		assert.True(t, blocked)
	})

	t.Run("not blocked", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("IsActorBlocked", mock.Anything, "did:plc:good", "@good.bsky.social").Return(false, nil)

		blocked, err := svc.IsActorBlocked(ctx, "did:plc:good", "@good.bsky.social")
		require.NoError(t, err)
		assert.False(t, blocked)
	})
}

func TestFederationHardening_ReportAbuse(t *testing.T) {
	ctx := context.Background()

	t.Run("reporting disabled", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		secCfg.EnableAbuseReporting = false
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		err := svc.ReportAbuse(ctx, "did:plc:reporter", "spam", "at://post/1", "did:plc:bad", "desc", json.RawMessage("{}"))
		require.Error(t, err)
		assert.Equal(t, "abuse reporting is disabled", err.Error())
	})

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CreateAbuseReport", mock.Anything, mock.MatchedBy(func(r *domain.FederationAbuseReport) bool {
			return r.ReportType == "spam" && r.Status == domain.FederationAbuseReportStatusPending
		})).Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.ReportAbuse(ctx, "did:plc:reporter", "spam", "at://post/1", "did:plc:bad", "desc", json.RawMessage("{}"))
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("repo error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CreateAbuseReport", mock.Anything, mock.Anything).Return(errors.New("db error"))

		err := svc.ReportAbuse(ctx, "did:plc:reporter", "spam", "at://post/1", "did:plc:bad", "desc", nil)
		require.Error(t, err)
		assert.Equal(t, "db error", err.Error())
	})
}

func TestFederationHardening_GetPendingAbuseReports(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		reports := []domain.FederationAbuseReport{
			{ID: "r1", ReportType: "spam", Status: domain.FederationAbuseReportStatusPending},
		}
		repo.On("GetAbuseReports", mock.Anything, domain.FederationAbuseReportStatusPending, 10).Return(reports, nil)

		result, err := svc.GetPendingAbuseReports(ctx, 10)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, "r1", result[0].ID)
	})
}

func TestFederationHardening_ResolveAbuseReport(t *testing.T) {
	ctx := context.Background()

	t.Run("take action (resolved)", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("UpdateAbuseReport", mock.Anything, "r1", domain.FederationAbuseReportStatusResolved, "banned actor", "admin1").Return(nil)

		err := svc.ResolveAbuseReport(ctx, "r1", "banned actor", "admin1", true)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})

	t.Run("reject report", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("UpdateAbuseReport", mock.Anything, "r2", domain.FederationAbuseReportStatusRejected, "not abuse", "admin2").Return(nil)

		err := svc.ResolveAbuseReport(ctx, "r2", "not abuse", "admin2", false)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})
}

func TestFederationHardening_GetHealthMetrics(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		summaries := []domain.FederationHealthSummary{
			{MetricType: "job_success", EventCount: 42},
		}
		repo.On("RefreshHealthSummary", mock.Anything).Return(nil)
		repo.On("GetHealthSummary", mock.Anything).Return(summaries, nil)

		result, err := svc.GetHealthMetrics(ctx)
		require.NoError(t, err)
		assert.Len(t, result, 1)
		assert.Equal(t, int64(42), result[0].EventCount)
	})
}

func TestFederationHardening_GetDashboardData(t *testing.T) {
	ctx := context.Background()

	t.Run("success with metrics", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("GetHealthSummary", mock.Anything).Return([]domain.FederationHealthSummary{}, nil)
		repo.On("GetDLQJobs", mock.Anything, 10, false).Return([]domain.DeadLetterJob{}, nil)
		repo.On("GetInstanceBlocks", mock.Anything).Return([]domain.InstanceBlock{}, nil)
		repo.On("GetAbuseReports", mock.Anything, domain.FederationAbuseReportStatusPending, 10).Return([]domain.FederationAbuseReport{}, nil)
		// Success and failure metrics with matching matchers
		repo.On("GetMetrics", mock.Anything, domain.MetricTypeJobSuccess, mock.AnythingOfType("time.Time"), 100).Return(
			[]domain.FederationMetric{{}, {}, {}}, nil,
		)
		repo.On("GetMetrics", mock.Anything, domain.MetricTypeJobFailure, mock.AnythingOfType("time.Time"), 100).Return(
			[]domain.FederationMetric{{}}, nil,
		)

		data, err := svc.GetDashboardData(ctx)
		require.NoError(t, err)

		assert.Equal(t, 0, data["dlq_count"])
		assert.Equal(t, 4, data["total_jobs_24h"])
		assert.Equal(t, 75.0, data["success_rate_24h"])
		assert.Equal(t, true, data["metrics_enabled"])
	})

	t.Run("zero total jobs yields zero success rate", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("GetHealthSummary", mock.Anything).Return([]domain.FederationHealthSummary{}, nil)
		repo.On("GetDLQJobs", mock.Anything, 10, false).Return([]domain.DeadLetterJob{}, nil)
		repo.On("GetInstanceBlocks", mock.Anything).Return([]domain.InstanceBlock{}, nil)
		repo.On("GetAbuseReports", mock.Anything, domain.FederationAbuseReportStatusPending, 10).Return([]domain.FederationAbuseReport{}, nil)
		repo.On("GetMetrics", mock.Anything, domain.MetricTypeJobSuccess, mock.AnythingOfType("time.Time"), 100).Return(
			[]domain.FederationMetric{}, nil,
		)
		repo.On("GetMetrics", mock.Anything, domain.MetricTypeJobFailure, mock.AnythingOfType("time.Time"), 100).Return(
			[]domain.FederationMetric{}, nil,
		)

		data, err := svc.GetDashboardData(ctx)
		require.NoError(t, err)
		assert.Equal(t, float64(0), data["success_rate_24h"])
		assert.Equal(t, 0, data["total_jobs_24h"])
	})
}

func TestFederationHardening_GetDLQJobs(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		jobs := []domain.DeadLetterJob{
			{ID: "dlq1", JobType: "publish_post"},
		}
		repo.On("GetDLQJobs", mock.Anything, 20, true).Return(jobs, nil)

		result, err := svc.GetDLQJobs(ctx, 20, true)
		require.NoError(t, err)
		assert.Len(t, result, 1)
	})
}

func TestFederationHardening_RetryDLQJob(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("RetryDLQJob", mock.Anything, "dlq1").Return(nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.RetryDLQJob(ctx, "dlq1")
		require.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("RetryDLQJob", mock.Anything, "dlq-bad").Return(errors.New("not found"))

		err := svc.RetryDLQJob(ctx, "dlq-bad")
		require.Error(t, err)
		assert.Equal(t, "not found", err.Error())
	})
}

func TestFederationHardening_RunCleanup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CleanupExpired", mock.Anything).Return(nil)

		err := svc.RunCleanup(ctx)
		require.NoError(t, err)
	})

	t.Run("error", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("CleanupExpired", mock.Anything).Return(errors.New("cleanup failed"))

		err := svc.RunCleanup(ctx)
		require.Error(t, err)
	})
}

func TestFederationHardening_ValidateFederationRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("blocked instance", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("IsInstanceBlocked", mock.Anything, "blocked.com").Return(true, nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.ValidateFederationRequest(ctx, "blocked.com", "sig", "/inbox", []byte("body"), "")
		require.Error(t, err)
		assert.Equal(t, "instance is blocked", err.Error())
	})

	t.Run("request size exceeded", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		svc := newTestHardeningService(repo, fedSvc, defaultSecurityConfig())

		repo.On("IsInstanceBlocked", mock.Anything, "big.com").Return(false, nil)

		bigBody := make([]byte, 2*1024*1024) // 2 MB
		err := svc.ValidateFederationRequest(ctx, "big.com", "sig", "/inbox", bigBody, "")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds maximum")
	})

	t.Run("rate limited", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		repo.On("IsInstanceBlocked", mock.Anything, "spammy.com").Return(false, nil)
		repo.On("CheckRateLimit", mock.Anything, "spammy.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(false, nil)
		repo.On("RecordMetric", mock.Anything, mock.Anything).Return(nil)

		err := svc.ValidateFederationRequest(ctx, "spammy.com", "sig", "/inbox", []byte("ok"), "")
		require.Error(t, err)
		assert.Equal(t, "rate limit exceeded", err.Error())
	})

	t.Run("invalid signature", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		repo.On("IsInstanceBlocked", mock.Anything, "notsigned.com").Return(false, nil)
		repo.On("CheckRateLimit", mock.Anything, "notsigned.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(true, nil)

		err := svc.ValidateFederationRequest(ctx, "notsigned.com", "", "/inbox", []byte("body"), "")
		require.Error(t, err)
		assert.Equal(t, "missing signature", err.Error())
	})

	t.Run("full validation success", func(t *testing.T) {
		repo := new(MockHardeningRepository)
		fedSvc := new(MockFederationSvc)
		secCfg := defaultSecurityConfig()
		svc := newTestHardeningService(repo, fedSvc, secCfg)

		ts := fmt.Sprintf("%d", time.Now().Unix())

		repo.On("IsInstanceBlocked", mock.Anything, "good.com").Return(false, nil)
		repo.On("CheckRateLimit", mock.Anything, "good.com", secCfg.RateLimitRequests, secCfg.RateLimitWindow).Return(true, nil)
		repo.On("CheckRequestSignature", mock.Anything, mock.AnythingOfType("string")).Return(false, nil)
		repo.On("RecordRequestSignature", mock.Anything, mock.AnythingOfType("*domain.RequestSignature")).Return(nil)

		err := svc.ValidateFederationRequest(ctx, "good.com", "valid-sig", "/inbox", []byte("body"), ts)
		require.NoError(t, err)
		repo.AssertExpectations(t)
	})
}
