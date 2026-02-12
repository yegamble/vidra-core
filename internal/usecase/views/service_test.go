package views

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockViewsRepository is a mock implementation of port.ViewsRepository
type MockViewsRepository struct {
	mock.Mock
}

func (m *MockViewsRepository) CreateUserView(ctx context.Context, view *domain.UserView) error {
	args := m.Called(ctx, view)
	return args.Error(0)
}

func (m *MockViewsRepository) UpdateUserView(ctx context.Context, view *domain.UserView) error {
	args := m.Called(ctx, view)
	return args.Error(0)
}

func (m *MockViewsRepository) GetUserViewBySessionAndVideo(ctx context.Context, sessionID, videoID string) (*domain.UserView, error) {
	args := m.Called(ctx, sessionID, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UserView), args.Error(1)
}

func (m *MockViewsRepository) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ViewAnalyticsResponse), args.Error(1)
}

func (m *MockViewsRepository) GetDailyVideoStats(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error) {
	args := m.Called(ctx, videoID, startDate, endDate)
	return args.Get(0).([]domain.DailyVideoStats), args.Error(1)
}

func (m *MockViewsRepository) GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error) {
	args := m.Called(ctx, userID, startDate, endDate)
	return args.Get(0).([]domain.UserEngagementStats), args.Error(1)
}

func (m *MockViewsRepository) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]domain.TrendingVideo), args.Error(1)
}

func (m *MockViewsRepository) GetBatchTrendingStats(ctx context.Context, videoIDs []string) ([]domain.VideoTrendingStats, error) {
	args := m.Called(ctx, videoIDs)
	return args.Get(0).([]domain.VideoTrendingStats), args.Error(1)
}

func (m *MockViewsRepository) UpdateTrendingVideo(ctx context.Context, trending *domain.TrendingVideo) error {
	args := m.Called(ctx, trending)
	return args.Error(0)
}

func (m *MockViewsRepository) BatchUpdateTrendingVideos(ctx context.Context, videos []*domain.TrendingVideo) error {
	args := m.Called(ctx, videos)
	return args.Error(0)
}

func (m *MockViewsRepository) IncrementVideoViews(ctx context.Context, videoID string) error {
	args := m.Called(ctx, videoID)
	return args.Error(0)
}

func (m *MockViewsRepository) GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error) {
	args := m.Called(ctx, videoID, startDate, endDate)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockViewsRepository) CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error) {
	args := m.Called(ctx, videoID, hoursBack)
	return args.Get(0).(float64), args.Error(1)
}

func (m *MockViewsRepository) AggregateDailyStats(ctx context.Context, date time.Time) error {
	args := m.Called(ctx, date)
	return args.Error(0)
}

func (m *MockViewsRepository) CleanupOldViews(ctx context.Context, daysToKeep int) error {
	args := m.Called(ctx, daysToKeep)
	return args.Error(0)
}

func (m *MockViewsRepository) GetViewsByDateRange(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
	args := m.Called(ctx, filter)
	return args.Get(0).([]domain.UserView), args.Error(1)
}

func (m *MockViewsRepository) GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
	VideoID     string  `db:"video_id"`
	TotalViews  int64   `db:"total_views"`
	UniqueViews int64   `db:"unique_views"`
	AvgDuration float64 `db:"avg_duration"`
}, error) {
	args := m.Called(ctx, startDate, endDate, limit)
	return args.Get(0).([]struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}), args.Error(1)
}

// MockVideoRepository
type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error { return nil }
func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error { return nil }
func (m *MockVideoRepository) Delete(ctx context.Context, id, userID string) error   { return nil }
func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return nil
}
func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return nil
}
func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return nil
}

func TestUpdateTrendingMetrics_Performance(t *testing.T) {
	mockViewsRepo := new(MockViewsRepository)
	mockVideoRepo := new(MockVideoRepository)
	service := NewService(mockViewsRepo, mockVideoRepo)

	videoIDs := []string{"v1", "v2", "v3", "v4", "v5"}

	stats := make([]domain.VideoTrendingStats, len(videoIDs))
	for i, vid := range videoIDs {
		stats[i] = domain.VideoTrendingStats{
			VideoID:       vid,
			ViewsLastHour: 10,
			ViewsLast24h:  100,
			ViewsLast7d:   500,
			Score1h:       5.0,
			Score24h:      20.0,
			Score7d:       100.0,
		}
	}

	// Expect exactly 1 call to fetch all stats
	mockViewsRepo.On("GetBatchTrendingStats", mock.Anything, videoIDs).Return(stats, nil).Times(1)

	// Expect exactly 1 call to update all videos
	mockViewsRepo.On("BatchUpdateTrendingVideos", mock.Anything, mock.MatchedBy(func(videos []*domain.TrendingVideo) bool {
		return len(videos) == 5
	})).Return(nil).Times(1)

	ctx := context.Background()
	err := service.UpdateTrendingMetrics(ctx, videoIDs)
	assert.NoError(t, err)

	// Verify expectations
	mockViewsRepo.AssertExpectations(t)

	// Explicitly verify call counts to demonstrate optimization
	mockViewsRepo.AssertNumberOfCalls(t, "GetBatchTrendingStats", 1)
	mockViewsRepo.AssertNumberOfCalls(t, "BatchUpdateTrendingVideos", 1)

	// Verify that the old N+1 methods are NOT called
	mockViewsRepo.AssertNumberOfCalls(t, "CalculateEngagementScore", 0)
	mockViewsRepo.AssertNumberOfCalls(t, "GetUniqueViews", 0)
	mockViewsRepo.AssertNumberOfCalls(t, "UpdateTrendingVideo", 0)
}
