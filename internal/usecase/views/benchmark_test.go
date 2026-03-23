package views

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/mock"
)

// MockCacheRepository implements CacheRepository for testing
type MockCacheRepository struct {
	data map[string]string
}

func (m *MockCacheRepository) Get(ctx context.Context, key string) (string, error) {
	if val, ok := m.data[key]; ok {
		return val, nil
	}
	return "", nil // miss
}

func (m *MockCacheRepository) Set(ctx context.Context, key string, value interface{}, ttl time.Duration) error {
	if b, ok := value.([]byte); ok {
		m.data[key] = string(b)
	}
	return nil
}

func (m *MockCacheRepository) Del(ctx context.Context, key string) error {
	delete(m.data, key)
	return nil
}

func BenchmarkProcessViewTask_NoCache(b *testing.B) {
	// Setup mock with delay
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)
	b.Cleanup(func() { svc.Close() })

	// Simulate DB delay for reading view
	dbDelay := 1 * time.Millisecond

	// Configure mock behavior
	// Return an existing view to simulate repeated updates
	existingView := &domain.UserView{
		ID:        "view-1",
		VideoID:   "vid-1",
		SessionID: "sess-1",
	}

	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").
		Run(func(args mock.Arguments) {
			time.Sleep(dbDelay)
		}).
		Return(existingView, nil)

	viewsRepo.On("UpdateUserView", mock.Anything, mock.Anything).Return(nil)

	task := viewTask{
		userID: nil,
		request: &domain.ViewTrackingRequest{
			VideoID:         "vid-1",
			SessionID:       "sess-1",
			FingerprintHash: "fp-hash",
			WatchDuration:   10,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.processViewTask(task)
	}
}

func BenchmarkProcessViewTask_WithCache(b *testing.B) {
	// Setup mock with delay
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)
	b.Cleanup(func() { svc.Close() })

	// Setup mock cache
	cacheRepo := &MockCacheRepository{data: make(map[string]string)}
	svc.SetCacheRepository(cacheRepo)

	// Simulate DB delay for reading view
	dbDelay := 1 * time.Millisecond

	// Existing view
	existingView := &domain.UserView{
		ID:        "view-1",
		VideoID:   "vid-1",
		SessionID: "sess-1",
	}
	// Pre-populate cache to simulate hit
	data, _ := json.Marshal(existingView)
	cacheRepo.data["view:sess-1:vid-1"] = string(data)

	// DB Read should NOT be called if cache works
	// But if called, apply delay so we see the difference
	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").
		Run(func(args mock.Arguments) {
			time.Sleep(dbDelay)
		}).
		Return(existingView, nil).Maybe()

	viewsRepo.On("UpdateUserView", mock.Anything, mock.Anything).Return(nil)

	task := viewTask{
		userID: nil,
		request: &domain.ViewTrackingRequest{
			VideoID:         "vid-1",
			SessionID:       "sess-1",
			FingerprintHash: "fp-hash",
			WatchDuration:   10,
		},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		svc.processViewTask(task)
	}
}
