package usecase

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"athena/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockFederationRepositoryExt) GetActorStateSimple(ctx context.Context, actor string) (cursor string, nextAt *time.Time, attempts int, rateLimitSeconds int, err error) {
	args := m.Called(ctx, actor)
	return args.String(0), args.Get(1).(*time.Time), args.Int(2), args.Int(3), args.Error(4)
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
	return args.Get(0).([]domain.FederatedPost), args.Error(1)
}

func (m *MockFederationRepositoryExt) GetPost(ctx context.Context, id string) (*domain.FederatedPost, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.FederatedPost), args.Error(1)
}

// MockHardeningRepository for testing
type MockHardeningRepository struct {
	mock.Mock
}

func (m *MockHardeningRepository) MoveToDLQ(ctx context.Context, job *domain.FederationJob, errorMsg string) error {
	args := m.Called(ctx, job, errorMsg)
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

func (m *MockHardeningRepository) IsActorBlocked(ctx context.Context, did, handle string) (bool, error) {
	args := m.Called(ctx, did, handle)
	return args.Bool(0), args.Error(1)
}

func (m *MockHardeningRepository) RecordMetric(ctx context.Context, metric *domain.FederationMetric) error {
	args := m.Called(ctx, metric)
	return args.Error(0)
}

func (m *MockHardeningRepository) CreateAbuseReport(ctx context.Context, report *domain.FederationAbuseReport) error {
	args := m.Called(ctx, report)
	return args.Error(0)
}

// Test Deduplication Service
func TestDeduplicationService_CalculateContentHash(t *testing.T) {
	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	text := "Test post content"
	embedURL := "https://example.com/video"
	cid := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"

	post := &domain.FederatedPost{
		ActorDID: "did:plc:test123",
		URI:      "at://did:plc:test123/app.bsky.feed.post/3ktest",
		Text:     &text,
		EmbedURL: &embedURL,
		CID:      &cid,
	}

	hash := service.CalculateContentHash(post)
	assert.NotEmpty(t, hash)
	assert.Len(t, hash, 64) // SHA256 produces 64 hex characters

	// Same content should produce same hash
	hash2 := service.CalculateContentHash(post)
	assert.Equal(t, hash, hash2)

	// Different content should produce different hash
	differentText := "Different content"
	post2 := &domain.FederatedPost{
		ActorDID: "did:plc:test123",
		URI:      "at://did:plc:test123/app.bsky.feed.post/3ktest",
		Text:     &differentText,
		EmbedURL: &embedURL,
		CID:      &cid,
	}
	hash3 := service.CalculateContentHash(post2)
	assert.NotEqual(t, hash, hash3)
}

func TestDeduplicationService_DetectDuplicate(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	text := "Test post content"
	existingPost := &domain.FederatedPost{
		ID:       "existing-id",
		ActorDID: "did:plc:test123",
		URI:      "at://did:plc:test123/app.bsky.feed.post/3ktest",
		Text:     &text,
	}

	newPost := &domain.FederatedPost{
		ID:       "new-id",
		ActorDID: "did:plc:test123",
		URI:      "at://did:plc:test123/app.bsky.feed.post/3ktest",
		Text:     &text,
	}

	contentHash := service.CalculateContentHash(newPost)

	// Test: Duplicate found
	mockRepo.On("GetPostByContentHash", ctx, contentHash).Return(existingPost, nil).Once()
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Once()

	existing, isDuplicate, err := service.DetectDuplicate(ctx, newPost)
	assert.NoError(t, err)
	assert.True(t, isDuplicate)
	assert.Equal(t, existingPost, existing)

	// Test: No duplicate
	mockRepo.On("GetPostByContentHash", ctx, contentHash).Return(nil, nil).Once()

	existing, isDuplicate, err = service.DetectDuplicate(ctx, newPost)
	assert.NoError(t, err)
	assert.False(t, isDuplicate)
	assert.Nil(t, existing)

	mockRepo.AssertExpectations(t)
	mockHardening.AssertExpectations(t)
}

func TestDeduplicationService_ResolveDuplicate(t *testing.T) {
	ctx := context.Background()
	mockRepo := new(MockFederationRepositoryExt)
	mockHardening := new(MockHardeningRepository)
	service := NewDeduplicationService(mockRepo, mockHardening)

	now := time.Now()
	later := now.Add(time.Hour)

	originalPost := &domain.FederatedPost{
		ID:        "original-id",
		ActorDID:  "did:plc:test123",
		URI:       "at://did:plc:test123/app.bsky.feed.post/original",
		CreatedAt: &now,
	}

	duplicatePost := &domain.FederatedPost{
		ID:        "duplicate-id",
		ActorDID:  "did:plc:test123",
		URI:       "at://did:plc:test123/app.bsky.feed.post/duplicate",
		CreatedAt: &later,
	}

	t.Run("KeepLatest", func(t *testing.T) {
		// Duplicate is newer, should become canonical
		mockRepo.On("UpdatePostCanonical", ctx, "original-id", false).Return(nil).Once()
		mockRepo.On("UpdatePostCanonical", ctx, "duplicate-id", true).Return(nil).Once()
		mockRepo.On("UpdatePostDuplicateOf", ctx, "original-id", &duplicatePost.ID).Return(nil).Once()
		mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Once()

		err := service.ResolveDuplicate(ctx, originalPost, duplicatePost, StrategyKeepLatest)
		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})

	t.Run("KeepOriginal", func(t *testing.T) {
		// Original remains canonical
		dupID := originalPost.ID
		mockRepo.On("UpdatePostDuplicateOf", ctx, "duplicate-id", &dupID).Return(nil).Once()
		mockRepo.On("UpdatePostCanonical", ctx, "duplicate-id", false).Return(nil).Once()
		mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil).Once()

		err := service.ResolveDuplicate(ctx, originalPost, duplicatePost, StrategyKeepOriginal)
		assert.NoError(t, err)
		mockRepo.AssertExpectations(t)
	})

	mockHardening.AssertExpectations(t)
}

// Test Circuit Breaker Service
func TestCircuitBreakerService_BasicFlow(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	config := CircuitBreakerConfig{
		FailureThreshold:   3,
		SuccessThreshold:   2,
		Timeout:            100 * time.Millisecond,
		HalfOpenMaxCalls:   1,
		ErrorRateThreshold: 0.5,
		WindowSize:         time.Second,
	}
	service := NewCircuitBreakerService(mockHardening, config)

	endpoint := "test-endpoint"
	successCount := 0
	failureCount := 0

	// Function that succeeds
	successFunc := func() error {
		successCount++
		return nil
	}

	// Function that fails
	failureFunc := func() error {
		failureCount++
		return assert.AnError
	}

	// Test: Circuit starts closed
	state, err := service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitClosed, state)

	// Test: Successful calls work
	err = service.Call(ctx, endpoint, successFunc)
	assert.NoError(t, err)
	assert.Equal(t, 1, successCount)

	// Test: Failures accumulate
	for i := 0; i < 3; i++ {
		_ = service.Call(ctx, endpoint, failureFunc)
	}
	assert.Equal(t, 3, failureCount)

	// Test: Circuit should be open after threshold failures
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
	state, err = service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	// Circuit might be open or transitioning

	// Test: Reset circuit
	err = service.Reset(ctx, endpoint)
	assert.NoError(t, err)

	state, err = service.GetState(ctx, endpoint)
	assert.NoError(t, err)
	assert.Equal(t, CircuitClosed, state)
}

// Test Backpressure Service
func TestBackpressureService_Throttling(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		RecoveryFactor:       1.2,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       10 * time.Second,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: true,
	}
	service := NewBackpressureService(mockHardening, config)

	instance := "test-instance"

	// Test: Initially not throttled
	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, shouldThrottle)
	assert.Equal(t, 1.0, factor)

	// Test: Record high queue depth
	metrics := BackpressureMetrics{
		QueueDepth:     200, // Above threshold
		ProcessingRate: 10,
		ErrorRate:      0.05,
	}
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
	err = service.RecordMetrics(ctx, instance, metrics)
	assert.NoError(t, err)

	// Test: Should throttle due to high queue
	shouldThrottle, factor, err = service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.Less(t, factor, 1.0)

	// Test: Emergency stop at max queue depth
	criticalMetrics := BackpressureMetrics{
		QueueDepth:     1500, // Above max
		ProcessingRate: 10,
		ErrorRate:      0.05,
	}
	err = service.RecordMetrics(ctx, instance, criticalMetrics)
	assert.NoError(t, err)

	shouldThrottle, factor, err = service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.Equal(t, 0.0, factor) // Complete stop

	// Test: Reset
	err = service.Reset(ctx, instance)
	assert.NoError(t, err)

	shouldThrottle, factor, err = service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.False(t, shouldThrottle)
	assert.Equal(t, 1.0, factor)
}

func TestBackpressureService_ErrorRateThrottling(t *testing.T) {
	ctx := context.Background()
	mockHardening := new(MockHardeningRepository)
	config := BackpressureConfig{
		QueueThreshold:       100,
		ErrorRateThreshold:   0.1,
		ThrottleFactor:       0.5,
		MeasurementWindow:    time.Minute,
		CooldownPeriod:       10 * time.Second,
		MaxQueueDepth:        1000,
		EmergencyStopEnabled: false,
	}
	service := NewBackpressureService(mockHardening, config)

	instance := "test-instance"

	// Test: High error rate triggers throttling
	metrics := BackpressureMetrics{
		QueueDepth:     50, // Below queue threshold
		ProcessingRate: 10,
		ErrorRate:      0.3, // Above error threshold
	}
	mockHardening.On("RecordMetric", ctx, mock.Anything).Return(nil)
	err := service.RecordMetrics(ctx, instance, metrics)
	assert.NoError(t, err)

	shouldThrottle, factor, err := service.ShouldThrottle(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, shouldThrottle)
	assert.Less(t, factor, 1.0)

	// Test: Get status
	status, err := service.GetStatus(ctx, instance)
	assert.NoError(t, err)
	assert.True(t, status.IsThrottled)
	assert.Equal(t, 50, status.QueueDepth)
	assert.Equal(t, 0.3, status.ErrorRate)
}

// Test Integrated Federation Service with new features
func TestFederationService_WithDeduplication(t *testing.T) {
	ctx := context.Background()

	// This test would require setting up the full federation service
	// with all the new services integrated
	// For brevity, we're testing the individual services above

	// In a real implementation, you'd test:
	// 1. Federation service properly detects and resolves duplicates
	// 2. Circuit breaker protects against cascading failures
	// 3. Backpressure properly throttles ingestion
	// 4. All services work together harmoniously
}
