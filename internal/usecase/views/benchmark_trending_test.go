package views

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func BenchmarkGetTrendingVideosWithDetails(b *testing.B) {
	mockViewsRepo := new(MockViewsRepository)
	mockVideoRepo := new(MockVideoRepository)
	service := NewService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	limit := 50

	// Create trending videos
	trendingVideos := make([]domain.TrendingVideo, limit)
	expectedVideos := make([]*domain.Video, limit)

	for i := 0; i < limit; i++ {
		vid := uuid.New().String()
		trendingVideos[i] = domain.TrendingVideo{
			VideoID:         vid,
			EngagementScore: float64(1000 - i),
			IsTrending:      true,
		}
		expectedVideos[i] = &domain.Video{ID: vid, Title: "Video Title"}
	}

	// Mock getting trending list
	mockViewsRepo.On("GetTrendingVideos", ctx, limit).Return(trendingVideos, nil)

	// Mock batch retrieval
	// Simulating 2ms for batch call (vs 50 * 1ms = 50ms)
	mockVideoRepo.On("GetByIDs", ctx, mock.MatchedBy(func(ids []string) bool {
		return len(ids) == limit
	})).Run(func(args mock.Arguments) {
		time.Sleep(2 * time.Millisecond)
	}).Return(expectedVideos, nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = service.GetTrendingVideosWithDetails(ctx, limit)
	}
	b.StopTimer()
}
