package usecase

import (
	"context"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/mock"
)

// MockHardeningRepository for testing
type MockHardeningRepository struct {
	mock.Mock
}

func (m *MockHardeningRepository) MoveToDLQ(ctx context.Context, job *domain.FederationJob, errorMsg string) error {
	args := m.Called(ctx, job, errorMsg)
	return args.Error(0)
}

func (m *MockHardeningRepository) GetDLQJobs(ctx context.Context, limit int, canRetryOnly bool) ([]domain.DeadLetterJob, error) {
	args := m.Called(ctx, limit, canRetryOnly)
	return args.Get(0).([]domain.DeadLetterJob), args.Error(1)
}

func (m *MockHardeningRepository) RetryDLQJob(ctx context.Context, dlqID string) error {
	args := m.Called(ctx, dlqID)
	return args.Error(0)
}

func (m *MockHardeningRepository) AddInstanceBlock(ctx context.Context, block *domain.InstanceBlock) error {
	args := m.Called(ctx, block)
	return args.Error(0)
}

func (m *MockHardeningRepository) RemoveInstanceBlock(ctx context.Context, domain string) error {
	args := m.Called(ctx, domain)
	return args.Error(0)
}

func (m *MockHardeningRepository) IsInstanceBlocked(ctx context.Context, domain string) (bool, error) {
	args := m.Called(ctx, domain)
	return args.Bool(0), args.Error(1)
}

func (m *MockHardeningRepository) GetInstanceBlocks(ctx context.Context) ([]domain.InstanceBlock, error) {
	args := m.Called(ctx)
	return args.Get(0).([]domain.InstanceBlock), args.Error(1)
}

func (m *MockHardeningRepository) AddActorBlock(ctx context.Context, block *domain.ActorBlock) error {
	args := m.Called(ctx, block)
	return args.Error(0)
}

func (m *MockHardeningRepository) IsActorBlocked(ctx context.Context, did, handle string) (bool, error) {
	args := m.Called(ctx, did, handle)
	return args.Bool(0), args.Error(1)
}

func (m *MockHardeningRepository) RecordMetric(ctx context.Context, metric *domain.FederationMetric) error {
	args := m.Called(ctx, metric)
	return args.Error(0)
}

func (m *MockHardeningRepository) GetMetrics(ctx context.Context, metricType string, since time.Time, limit int) ([]domain.FederationMetric, error) {
	args := m.Called(ctx, metricType, since, limit)
	return args.Get(0).([]domain.FederationMetric), args.Error(1)
}

func (m *MockHardeningRepository) GetHealthSummary(ctx context.Context) ([]domain.FederationHealthSummary, error) {
	args := m.Called(ctx)
	return args.Get(0).([]domain.FederationHealthSummary), args.Error(1)
}

func (m *MockHardeningRepository) RefreshHealthSummary(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockHardeningRepository) CheckIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.IdempotencyRecord), args.Error(1)
}

func (m *MockHardeningRepository) RecordIdempotency(ctx context.Context, record *domain.IdempotencyRecord) error {
	args := m.Called(ctx, record)
	return args.Error(0)
}

func (m *MockHardeningRepository) CheckRequestSignature(ctx context.Context, signatureHash string) (bool, error) {
	args := m.Called(ctx, signatureHash)
	return args.Bool(0), args.Error(1)
}

func (m *MockHardeningRepository) RecordRequestSignature(ctx context.Context, sig *domain.RequestSignature) error {
	args := m.Called(ctx, sig)
	return args.Error(0)
}

func (m *MockHardeningRepository) CheckRateLimit(ctx context.Context, id string, limit int, window time.Duration) (bool, error) {
	args := m.Called(ctx, id, limit, window)
	return args.Bool(0), args.Error(1)
}

func (m *MockHardeningRepository) CreateAbuseReport(ctx context.Context, report *domain.FederationAbuseReport) error {
	args := m.Called(ctx, report)
	return args.Error(0)
}

func (m *MockHardeningRepository) GetAbuseReports(ctx context.Context, status string, limit int) ([]domain.FederationAbuseReport, error) {
	args := m.Called(ctx, status, limit)
	return args.Get(0).([]domain.FederationAbuseReport), args.Error(1)
}

func (m *MockHardeningRepository) UpdateAbuseReport(ctx context.Context, id, status, resolution, resolvedBy string) error {
	args := m.Called(ctx, id, status, resolution, resolvedBy)
	return args.Error(0)
}

func (m *MockHardeningRepository) CleanupExpired(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

func (m *MockHardeningRepository) GetFederationConfig(ctx context.Context) (*domain.FederationSecurityConfig, error) {
	args := m.Called(ctx)
	return args.Get(0).(*domain.FederationSecurityConfig), args.Error(1)
}

func (m *MockHardeningRepository) UpdateJobWithBackoff(ctx context.Context, jobID string, attempts int, lastError string) error {
	args := m.Called(ctx, jobID, attempts, lastError)
	return args.Error(0)
}

func (m *MockHardeningRepository) GetJobsForProcessing(ctx context.Context, limit int) ([]domain.FederationJob, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]domain.FederationJob), args.Error(1)
}

// MockFederationRepositoryExt for testing
type MockFederationRepositoryExt struct {
	mock.Mock
}

func (m *MockFederationRepositoryExt) EnqueueJob(ctx context.Context, jobType string, payload any, runAt time.Time) (string, error) {
	args := m.Called(ctx, jobType, payload, runAt)
	return args.String(0), args.Error(1)
}

func (m *MockFederationRepositoryExt) GetNextJob(ctx context.Context) (*domain.FederationJob, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.FederationJob), args.Error(1)
}

func (m *MockFederationRepositoryExt) CompleteJob(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) RescheduleJob(ctx context.Context, id string, lastErr string, backoff time.Duration) error {
	args := m.Called(ctx, id, lastErr, backoff)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) UpsertPost(ctx context.Context, p *domain.FederatedPost) error {
	args := m.Called(ctx, p)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) ListEnabledActors(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockFederationRepositoryExt) GetActorStateSimple(ctx context.Context, actor string) (cursor string, nextAt *time.Time, attempts int, rateLimitSeconds int, err error) {
	args := m.Called(ctx, actor)
	var nextAtPtr *time.Time
	if args.Get(1) != nil {
		nextAtPtr = args.Get(1).(*time.Time)
	}
	return args.String(0), nextAtPtr, args.Int(2), args.Int(3), args.Error(4)
}

func (m *MockFederationRepositoryExt) SetActorCursor(ctx context.Context, actor string, cursor string) error {
	args := m.Called(ctx, actor, cursor)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) SetActorNextAt(ctx context.Context, actor string, t time.Time) error {
	args := m.Called(ctx, actor, t)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) SetActorAttempts(ctx context.Context, actor string, n int) error {
	args := m.Called(ctx, actor, n)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) GetPostByContentHash(ctx context.Context, hash string) (*domain.FederatedPost, error) {
	args := m.Called(ctx, hash)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.FederatedPost), args.Error(1)
}

func (m *MockFederationRepositoryExt) UpdatePostCanonical(ctx context.Context, id string, canonical bool) error {
	args := m.Called(ctx, id, canonical)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) UpdatePostDuplicateOf(ctx context.Context, id string, duplicateOf *string) error {
	args := m.Called(ctx, id, duplicateOf)
	return args.Error(0)
}

func (m *MockFederationRepositoryExt) GetPostDuplicates(ctx context.Context, postID string) ([]domain.FederatedPost, error) {
	args := m.Called(ctx, postID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.FederatedPost), args.Error(1)
}

func (m *MockFederationRepositoryExt) GetPost(ctx context.Context, id string) (*domain.FederatedPost, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.FederatedPost), args.Error(1)
}

// MockInstanceConfigReader for testing
type MockInstanceConfigReader struct {
	mock.Mock
}

func (m *MockInstanceConfigReader) GetInstanceConfig(ctx context.Context, key string) (*domain.InstanceConfig, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.InstanceConfig), args.Error(1)
}

func (m *MockInstanceConfigReader) GetInstanceConfigAsString(ctx context.Context, key string) (string, error) {
	args := m.Called(ctx, key)
	return args.String(0), args.Error(1)
}

func (m *MockInstanceConfigReader) GetInstanceConfigAsInt(ctx context.Context, key string) (int, error) {
	args := m.Called(ctx, key)
	return args.Int(0), args.Error(1)
}

func (m *MockInstanceConfigReader) GetInstanceConfigAsBool(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockInstanceConfigReader) GetInstanceConfigAsJSON(ctx context.Context, key string, target interface{}) error {
	args := m.Called(ctx, key, target)
	return args.Error(0)
}

func (m *MockInstanceConfigReader) UpdateInstanceConfig(ctx context.Context, key string, value []byte, isPublic bool) error {
	args := m.Called(ctx, key, value, isPublic)
	return args.Error(0)
}

// Helper function
func strPtr(s string) *string {
	return &s
}
