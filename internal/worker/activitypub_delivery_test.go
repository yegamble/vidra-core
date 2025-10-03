package worker

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
)

// MockAPRepository mocks the ActivityPub repository
type MockAPRepository struct {
	mock.Mock
}

func (m *MockAPRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*domain.APDeliveryQueue, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.APDeliveryQueue), args.Error(1)
}

func (m *MockAPRepository) GetActivity(ctx context.Context, activityID string) (*domain.APActivity, error) {
	args := m.Called(ctx, activityID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.APActivity), args.Error(1)
}

func (m *MockAPRepository) UpdateDeliveryStatus(ctx context.Context, deliveryID, status string, attempts int, lastError *string, nextAttempt time.Time) error {
	args := m.Called(ctx, deliveryID, status, attempts, lastError, nextAttempt)
	return args.Error(0)
}

// MockAPService mocks the ActivityPub service
type MockAPService struct {
	mock.Mock
}

func (m *MockAPService) DeliverActivity(ctx context.Context, actorID, inboxURL string, activity interface{}) error {
	args := m.Called(ctx, actorID, inboxURL, activity)
	return args.Error(0)
}

func TestNewActivityPubDeliveryWorker(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryWorkers: 3,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	assert.NotNil(t, worker)
	assert.Equal(t, mockRepo, worker.apRepo)
	assert.Equal(t, mockService, worker.service)
	assert.Equal(t, cfg, worker.cfg)
	assert.NotNil(t, worker.stopCh)
}

func TestProcessDeliveriesSuccess(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryRetries:    10,
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	activityJSON := json.RawMessage(`{"type":"Follow","actor":"https://example.com/users/alice"}`)
	activity := &domain.APActivity{
		ID:           "activity-1",
		ActorID:      "local-user-1",
		Type:         "Follow",
		ActivityJSON: activityJSON,
	}

	delivery := &domain.APDeliveryQueue{
		ID:          "delivery-1",
		ActivityID:  "activity-1",
		InboxURL:    "https://mastodon.example/inbox",
		ActorID:     "local-user-1",
		Attempts:    0,
		MaxAttempts: 10,
		NextAttempt: time.Now(),
		Status:      "pending",
	}

	deliveries := []*domain.APDeliveryQueue{delivery}

	t.Run("Successful delivery", func(t *testing.T) {
		mockRepo.On("GetPendingDeliveries", ctx, 10).Return(deliveries, nil).Once()
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "processing", 0, (*string)(nil), mock.Anything).Return(nil).Once()
		mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(activity, nil).Once()
		mockService.On("DeliverActivity", ctx, delivery.ActorID, delivery.InboxURL, mock.Anything).Return(nil).Once()
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "completed", 1, (*string)(nil), mock.Anything).Return(nil).Once()

		worker.processDeliveries(ctx, 0)

		mockRepo.AssertExpectations(t)
		mockService.AssertExpectations(t)
	})
}

func TestProcessDeliveriesRetry(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryRetries:    10,
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	activityJSON := json.RawMessage(`{"type":"Follow"}`)
	activity := &domain.APActivity{
		ID:           "activity-2",
		ActorID:      "local-user-1",
		ActivityJSON: activityJSON,
	}

	delivery := &domain.APDeliveryQueue{
		ID:          "delivery-2",
		ActivityID:  "activity-2",
		InboxURL:    "https://mastodon.example/inbox",
		ActorID:     "local-user-1",
		Attempts:    0,
		MaxAttempts: 10,
		NextAttempt: time.Now(),
		Status:      "pending",
	}

	deliveries := []*domain.APDeliveryQueue{delivery}

	t.Run("Failed delivery with retry", func(t *testing.T) {
		deliveryError := errors.New("connection timeout")

		mockRepo.On("GetPendingDeliveries", ctx, 10).Return(deliveries, nil).Once()
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "processing", 0, (*string)(nil), mock.Anything).Return(nil).Once()
		mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(activity, nil).Once()
		mockService.On("DeliverActivity", ctx, delivery.ActorID, delivery.InboxURL, mock.Anything).Return(deliveryError).Once()

		// Should be rescheduled with error message
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "pending", 1, mock.MatchedBy(func(err *string) bool {
			return err != nil && *err == deliveryError.Error()
		}), mock.MatchedBy(func(t time.Time) bool {
			return t.After(time.Now())
		})).Return(nil).Once()

		worker.processDeliveries(ctx, 0)

		mockRepo.AssertExpectations(t)
		mockService.AssertExpectations(t)
	})
}

func TestProcessDeliveriesPermanentFailure(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryRetries:    3,
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	activityJSON := json.RawMessage(`{"type":"Follow"}`)
	activity := &domain.APActivity{
		ID:           "activity-3",
		ActorID:      "local-user-1",
		ActivityJSON: activityJSON,
	}

	delivery := &domain.APDeliveryQueue{
		ID:          "delivery-3",
		ActivityID:  "activity-3",
		InboxURL:    "https://mastodon.example/inbox",
		ActorID:     "local-user-1",
		Attempts:    2, // Already failed twice
		MaxAttempts: 3,
		NextAttempt: time.Now(),
		Status:      "pending",
	}

	deliveries := []*domain.APDeliveryQueue{delivery}

	t.Run("Permanent failure after max attempts", func(t *testing.T) {
		deliveryError := errors.New("recipient not found")

		mockRepo.On("GetPendingDeliveries", ctx, 10).Return(deliveries, nil).Once()
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "processing", 2, (*string)(nil), mock.Anything).Return(nil).Once()
		mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(activity, nil).Once()
		mockService.On("DeliverActivity", ctx, delivery.ActorID, delivery.InboxURL, mock.Anything).Return(deliveryError).Once()

		// Should be marked as permanently failed
		mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "failed", 3, mock.MatchedBy(func(err *string) bool {
			return err != nil && *err == deliveryError.Error()
		}), mock.Anything).Return(nil).Once()

		worker.processDeliveries(ctx, 0)

		mockRepo.AssertExpectations(t)
		mockService.AssertExpectations(t)
	})
}

func TestCalculateNextAttempt(t *testing.T) {
	cfg := &config.Config{
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(nil, nil, cfg)

	tests := []struct {
		name            string
		attempts        int
		expectedMinWait time.Duration
		expectedMaxWait time.Duration
	}{
		{
			name:            "First retry",
			attempts:        0,
			expectedMinWait: 60 * time.Second,
			expectedMaxWait: 120 * time.Second,
		},
		{
			name:            "Second retry",
			attempts:        1,
			expectedMinWait: 120 * time.Second,
			expectedMaxWait: 240 * time.Second,
		},
		{
			name:            "Third retry",
			attempts:        2,
			expectedMinWait: 240 * time.Second,
			expectedMaxWait: 480 * time.Second,
		},
		{
			name:            "Very long delay should be capped",
			attempts:        20,
			expectedMinWait: 23 * time.Hour,
			expectedMaxWait: 24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			now := time.Now()
			nextAttempt := worker.calculateNextAttempt(tt.attempts)

			waitTime := nextAttempt.Sub(now)

			// For attempts >= 15, delay should be capped at 24 hours
			if tt.attempts >= 15 {
				assert.True(t, waitTime >= tt.expectedMinWait && waitTime <= tt.expectedMaxWait,
					"Expected wait time between %v and %v, got %v", tt.expectedMinWait, tt.expectedMaxWait, waitTime)
			} else {
				assert.True(t, waitTime >= tt.expectedMinWait,
					"Expected wait time >= %v, got %v", tt.expectedMinWait, waitTime)
			}
		})
	}
}

func TestProcessDeliveriesNoDeliveries(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	t.Run("No pending deliveries", func(t *testing.T) {
		emptyDeliveries := []*domain.APDeliveryQueue{}
		mockRepo.On("GetPendingDeliveries", ctx, 10).Return(emptyDeliveries, nil).Once()

		// Should return without calling any other methods
		worker.processDeliveries(ctx, 0)

		mockRepo.AssertExpectations(t)
		// Service should not be called
		mockService.AssertNotCalled(t, "DeliverActivity")
	})
}

func TestProcessDeliveriesMultiple(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryRetries:    10,
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	activityJSON := json.RawMessage(`{"type":"Follow"}`)
	activity := &domain.APActivity{
		ID:           "activity-1",
		ActorID:      "local-user-1",
		ActivityJSON: activityJSON,
	}

	deliveries := []*domain.APDeliveryQueue{
		{
			ID:          "delivery-1",
			ActivityID:  "activity-1",
			InboxURL:    "https://mastodon1.example/inbox",
			ActorID:     "local-user-1",
			Attempts:    0,
			MaxAttempts: 10,
			Status:      "pending",
		},
		{
			ID:          "delivery-2",
			ActivityID:  "activity-1",
			InboxURL:    "https://mastodon2.example/inbox",
			ActorID:     "local-user-1",
			Attempts:    0,
			MaxAttempts: 10,
			Status:      "pending",
		},
		{
			ID:          "delivery-3",
			ActivityID:  "activity-1",
			InboxURL:    "https://mastodon3.example/inbox",
			ActorID:     "local-user-1",
			Attempts:    0,
			MaxAttempts: 10,
			Status:      "pending",
		},
	}

	t.Run("Process multiple deliveries", func(t *testing.T) {
		mockRepo.On("GetPendingDeliveries", ctx, 10).Return(deliveries, nil).Once()

		// Each delivery should be processed
		for _, delivery := range deliveries {
			mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "processing", 0, (*string)(nil), mock.Anything).Return(nil).Once()
			mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(activity, nil).Once()
			mockService.On("DeliverActivity", ctx, delivery.ActorID, delivery.InboxURL, mock.Anything).Return(nil).Once()
			mockRepo.On("UpdateDeliveryStatus", ctx, delivery.ID, "completed", 1, (*string)(nil), mock.Anything).Return(nil).Once()
		}

		worker.processDeliveries(ctx, 0)

		mockRepo.AssertExpectations(t)
		mockService.AssertExpectations(t)
	})
}

func TestAttemptDeliveryActivityNotFound(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	delivery := &domain.APDeliveryQueue{
		ID:         "delivery-1",
		ActivityID: "nonexistent-activity",
		InboxURL:   "https://mastodon.example/inbox",
		ActorID:    "local-user-1",
	}

	t.Run("Activity not found", func(t *testing.T) {
		mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(nil, errors.New("not found")).Once()

		err := worker.attemptDelivery(ctx, delivery, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get activity")

		mockRepo.AssertExpectations(t)
		mockService.AssertNotCalled(t, "DeliverActivity")
	})
}

func TestAttemptDeliveryInvalidJSON(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx := context.Background()

	activity := &domain.APActivity{
		ID:           "activity-1",
		ActorID:      "local-user-1",
		ActivityJSON: json.RawMessage(`invalid json`),
	}

	delivery := &domain.APDeliveryQueue{
		ID:         "delivery-1",
		ActivityID: "activity-1",
		InboxURL:   "https://mastodon.example/inbox",
		ActorID:    "local-user-1",
	}

	t.Run("Invalid activity JSON", func(t *testing.T) {
		mockRepo.On("GetActivity", ctx, delivery.ActivityID).Return(activity, nil).Once()

		err := worker.attemptDelivery(ctx, delivery, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse activity")

		mockRepo.AssertExpectations(t)
		mockService.AssertNotCalled(t, "DeliverActivity")
	})
}

func TestStartAndStopWorker(t *testing.T) {
	mockRepo := new(MockAPRepository)
	mockService := new(MockAPService)
	cfg := &config.Config{
		ActivityPubDeliveryWorkers: 2,
	}

	worker := NewActivityPubDeliveryWorker(mockRepo, mockService, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	t.Run("Start and stop worker", func(t *testing.T) {
		err := worker.Start(ctx)
		require.NoError(t, err)

		// Give workers time to start
		time.Sleep(100 * time.Millisecond)

		err = worker.Stop()
		require.NoError(t, err)

		// Give workers time to stop
		time.Sleep(100 * time.Millisecond)
	})
}

func TestExponentialBackoff(t *testing.T) {
	cfg := &config.Config{
		ActivityPubDeliveryRetryDelay: 60,
	}

	worker := NewActivityPubDeliveryWorker(nil, nil, cfg)

	t.Run("Exponential backoff increases correctly", func(t *testing.T) {
		now := time.Now()

		// Test exponential growth
		delay0 := worker.calculateNextAttempt(0).Sub(now)
		delay1 := worker.calculateNextAttempt(1).Sub(now)
		delay2 := worker.calculateNextAttempt(2).Sub(now)

		// Each delay should be roughly double the previous
		assert.True(t, delay1 > delay0)
		assert.True(t, delay2 > delay1)

		// Verify approximate doubling (with some tolerance)
		ratio1 := float64(delay1) / float64(delay0)
		ratio2 := float64(delay2) / float64(delay1)

		assert.InDelta(t, 2.0, ratio1, 0.1)
		assert.InDelta(t, 2.0, ratio2, 0.1)
	})

	t.Run("Delay capped at 24 hours", func(t *testing.T) {
		now := time.Now()

		// Very high attempt number should still cap at 24h
		delay := worker.calculateNextAttempt(100).Sub(now)

		assert.True(t, delay <= 24*time.Hour)
		assert.True(t, delay >= 23*time.Hour) // Allow some tolerance
	})
}
