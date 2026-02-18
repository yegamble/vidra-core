package views

import (
	"context"
	"errors"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

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
func (m *MockVideoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
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

func (m *MockVideoRepository) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func newTestService(t *testing.T) (*Service, *MockViewsRepository, *MockVideoRepository) {
	t.Helper()
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)
	t.Cleanup(func() { svc.Close() })
	return svc, viewsRepo, videoRepo
}

func TestNewService(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.viewQueue)
	assert.Equal(t, 1000, cap(svc.viewQueue))
	assert.Same(t, viewsRepo, svc.viewsRepo.(*MockViewsRepository))
	assert.Same(t, videoRepo, svc.videoRepo.(*MockVideoRepository))
}

func TestClose_GracefulShutdown(t *testing.T) {
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)

	done := make(chan struct{})
	go func() {
		svc.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return within timeout")
	}
}

func TestClose_Idempotent(t *testing.T) {
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)

	svc.Close()
	svc.Close()
	svc.Close()
}

func TestTrackView_Success(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)

	video := &domain.Video{ID: "vid-1", Title: "Test Video"}
	videoRepo.On("GetByID", mock.Anything, "vid-1").Return(video, nil)

	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").Return(nil, nil)
	viewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)
	viewsRepo.On("IncrementVideoViews", mock.Anything, "vid-1").Return(nil)

	req := &domain.ViewTrackingRequest{
		VideoID:         "vid-1",
		SessionID:       "sess-1",
		FingerprintHash: "fp-hash",
		WatchDuration:   60,
	}

	err := svc.TrackView(context.Background(), nil, req)
	assert.NoError(t, err)
	videoRepo.AssertExpectations(t)
}

func TestTrackView_VideoNotFound(t *testing.T) {
	svc, _, videoRepo := newTestService(t)

	videoRepo.On("GetByID", mock.Anything, "nonexistent").Return(nil, nil)

	req := &domain.ViewTrackingRequest{
		VideoID:         "nonexistent",
		SessionID:       "sess-1",
		FingerprintHash: "fp-hash",
	}

	err := svc.TrackView(context.Background(), nil, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "video not found")
}

func TestTrackView_VideoRepoError(t *testing.T) {
	svc, _, videoRepo := newTestService(t)

	videoRepo.On("GetByID", mock.Anything, "vid-err").Return(nil, errors.New("db timeout"))

	req := &domain.ViewTrackingRequest{
		VideoID:         "vid-err",
		SessionID:       "sess-1",
		FingerprintHash: "fp-hash",
	}

	err := svc.TrackView(context.Background(), nil, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to verify video exists")
}

func TestTrackView_QueueFull(t *testing.T) {
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := &Service{
		viewsRepo: viewsRepo,
		videoRepo: videoRepo,
		viewQueue: make(chan viewTask, 1),
	}
	video := &domain.Video{ID: "vid-1", Title: "Test Video"}
	videoRepo.On("GetByID", mock.Anything, "vid-1").Return(video, nil)

	req := &domain.ViewTrackingRequest{
		VideoID:         "vid-1",
		SessionID:       "sess-1",
		FingerprintHash: "fp-hash",
	}

	svc.viewQueue <- viewTask{userID: nil, request: req}

	err := svc.TrackView(context.Background(), nil, req)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tracking queue full")

	<-svc.viewQueue
}

func TestProcessViewTask_NewView(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)
	_ = videoRepo

	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").Return(nil, nil)
	viewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)
	viewsRepo.On("IncrementVideoViews", mock.Anything, "vid-1").Return(nil)

	userID := "user-1"
	task := viewTask{
		userID: &userID,
		request: &domain.ViewTrackingRequest{
			VideoID:              "vid-1",
			SessionID:            "sess-1",
			FingerprintHash:      "fp-hash",
			WatchDuration:        120,
			VideoDuration:        300,
			CompletionPercentage: 40.0,
			DeviceType:           "desktop",
			CountryCode:          "US",
		},
	}

	svc.processViewTask(task)

	viewsRepo.AssertExpectations(t)
	viewsRepo.AssertCalled(t, "CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView"))
	viewsRepo.AssertCalled(t, "IncrementVideoViews", mock.Anything, "vid-1")
}

func TestProcessViewTask_UpdateExistingView(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	existingView := &domain.UserView{
		ID:            "view-1",
		VideoID:       "vid-1",
		SessionID:     "sess-1",
		WatchDuration: 30,
	}
	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").Return(existingView, nil)
	viewsRepo.On("UpdateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)

	task := viewTask{
		userID: nil,
		request: &domain.ViewTrackingRequest{
			VideoID:              "vid-1",
			SessionID:            "sess-1",
			FingerprintHash:      "fp-hash",
			WatchDuration:        120,
			CompletionPercentage: 80.0,
			IsCompleted:          false,
			SeekCount:            2,
			PauseCount:           1,
		},
	}

	svc.processViewTask(task)

	viewsRepo.AssertExpectations(t)
	assert.Equal(t, 120, existingView.WatchDuration)
	assert.Equal(t, 80.0, existingView.CompletionPercentage)
	assert.Equal(t, 2, existingView.SeekCount)
	assert.Equal(t, 1, existingView.PauseCount)
}

func TestProcessViewTask_LookupError(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-err", "vid-1").Return(nil, errors.New("db error"))

	task := viewTask{
		userID: nil,
		request: &domain.ViewTrackingRequest{
			VideoID:   "vid-1",
			SessionID: "sess-err",
		},
	}

	svc.processViewTask(task)

	viewsRepo.AssertNotCalled(t, "CreateUserView", mock.Anything, mock.Anything)
	viewsRepo.AssertNotCalled(t, "UpdateUserView", mock.Anything, mock.Anything)
}

func TestProcessViewTask_CreateError(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").Return(nil, nil)
	viewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(errors.New("insert failed"))

	task := viewTask{
		userID: nil,
		request: &domain.ViewTrackingRequest{
			VideoID:         "vid-1",
			SessionID:       "sess-1",
			FingerprintHash: "fp-hash",
		},
	}

	svc.processViewTask(task)

	viewsRepo.AssertNotCalled(t, "IncrementVideoViews", mock.Anything, mock.Anything)
}

func TestTrackViewAndWorkerProcesses(t *testing.T) {
	viewsRepo := new(MockViewsRepository)
	videoRepo := new(MockVideoRepository)
	svc := NewService(viewsRepo, videoRepo)

	video := &domain.Video{ID: "vid-1", Title: "Test"}
	videoRepo.On("GetByID", mock.Anything, "vid-1").Return(video, nil)
	viewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, "sess-1", "vid-1").Return(nil, nil)
	viewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)
	viewsRepo.On("IncrementVideoViews", mock.Anything, "vid-1").Return(nil)

	req := &domain.ViewTrackingRequest{
		VideoID:         "vid-1",
		SessionID:       "sess-1",
		FingerprintHash: "fp-hash",
	}

	err := svc.TrackView(context.Background(), nil, req)
	require.NoError(t, err)

	svc.Close()

	viewsRepo.AssertCalled(t, "CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView"))
	viewsRepo.AssertCalled(t, "IncrementVideoViews", mock.Anything, "vid-1")
}

func TestGetVideoAnalytics_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	expected := &domain.ViewAnalyticsResponse{
		TotalViews:  1000,
		UniqueViews: 500,
		AvgDuration: 120.5,
	}
	filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}
	viewsRepo.On("GetVideoAnalytics", mock.Anything, filter).Return(expected, nil)

	result, err := svc.GetVideoAnalytics(context.Background(), filter)
	assert.NoError(t, err)
	assert.Equal(t, int64(1000), result.TotalViews)
	assert.Equal(t, int64(500), result.UniqueViews)
}

func TestGetVideoAnalytics_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	filter := &domain.ViewAnalyticsFilter{VideoID: "vid-err"}
	viewsRepo.On("GetVideoAnalytics", mock.Anything, filter).Return(nil, errors.New("query failed"))

	result, err := svc.GetVideoAnalytics(context.Background(), filter)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetDailyStats_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	stats := []domain.DailyVideoStats{
		{VideoID: "vid-1", TotalViews: 100},
		{VideoID: "vid-1", TotalViews: 150},
	}
	viewsRepo.On("GetDailyVideoStats", mock.Anything, "vid-1", mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return(stats, nil)

	result, err := svc.GetDailyStats(context.Background(), "vid-1", 7)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetDailyStats_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetDailyVideoStats", mock.Anything, "vid-err", mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return([]domain.DailyVideoStats{}, errors.New("db error"))

	_, err := svc.GetDailyStats(context.Background(), "vid-err", 30)
	assert.Error(t, err)
}

func TestGetUserEngagement_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	stats := []domain.UserEngagementStats{
		{UserID: "user-1", VideosWatched: 10},
	}
	viewsRepo.On("GetUserEngagementStats", mock.Anything, "user-1", mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return(stats, nil)

	result, err := svc.GetUserEngagement(context.Background(), "user-1", 30)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(10), result[0].VideosWatched)
}

func TestGetUserEngagement_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetUserEngagementStats", mock.Anything, "user-err", mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time")).Return([]domain.UserEngagementStats{}, errors.New("not found"))

	_, err := svc.GetUserEngagement(context.Background(), "user-err", 7)
	assert.Error(t, err)
}

func TestGetTrendingVideos_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	trending := []domain.TrendingVideo{
		{VideoID: "vid-1", ViewsLastHour: 100, IsTrending: true},
		{VideoID: "vid-2", ViewsLastHour: 80, IsTrending: true},
	}
	viewsRepo.On("GetTrendingVideos", mock.Anything, 50).Return(trending, nil)

	result, err := svc.GetTrendingVideos(context.Background(), 50)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetTrendingVideos_DefaultLimit(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetTrendingVideos", mock.Anything, 50).Return([]domain.TrendingVideo{}, nil)

	result, err := svc.GetTrendingVideos(context.Background(), 0)
	assert.NoError(t, err)
	assert.Empty(t, result)

	_, err = svc.GetTrendingVideos(context.Background(), 200)
	assert.NoError(t, err)
}

func TestGetTrendingVideos_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetTrendingVideos", mock.Anything, 10).Return([]domain.TrendingVideo{}, errors.New("query failed"))

	_, err := svc.GetTrendingVideos(context.Background(), 10)
	assert.Error(t, err)
}

func TestGetTrendingVideosWithDetails_Success(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)

	trending := []domain.TrendingVideo{
		{VideoID: "vid-1", EngagementScore: 50.0, VelocityScore: 10.0},
		{VideoID: "vid-2", EngagementScore: 30.0, VelocityScore: 5.0},
	}
	viewsRepo.On("GetTrendingVideos", mock.Anything, 10).Return(trending, nil)

	videos := []*domain.Video{
		{ID: "vid-1", Title: "Video One", Duration: 300, Views: 1000, ThumbnailPath: "/thumb/1.jpg"},
		{ID: "vid-2", Title: "Video Two", Duration: 600, Views: 500, ThumbnailPath: "/thumb/2.jpg"},
	}
	videoRepo.On("GetByIDs", mock.Anything, []string{"vid-1", "vid-2"}).Return(videos, nil)

	result, err := svc.GetTrendingVideosWithDetails(context.Background(), 10)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Videos, 2)
	assert.Equal(t, "Video One", result.Videos[0].Title)
	assert.Equal(t, 50.0, result.Videos[0].EngagementScore)
	assert.Equal(t, "Video Two", result.Videos[1].Title)
}

func TestGetTrendingVideosWithDetails_MissingVideoSkipped(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)

	trending := []domain.TrendingVideo{
		{VideoID: "vid-1", EngagementScore: 50.0},
		{VideoID: "vid-deleted", EngagementScore: 30.0},
	}
	viewsRepo.On("GetTrendingVideos", mock.Anything, 50).Return(trending, nil)

	videos := []*domain.Video{
		{ID: "vid-1", Title: "Video One"},
	}
	videoRepo.On("GetByIDs", mock.Anything, []string{"vid-1", "vid-deleted"}).Return(videos, nil)

	result, err := svc.GetTrendingVideosWithDetails(context.Background(), 0)
	assert.NoError(t, err)
	require.NotNil(t, result)
	assert.Len(t, result.Videos, 1)
	assert.Equal(t, "vid-1", result.Videos[0].VideoID)
}

func TestGetTrendingVideosWithDetails_GetTrendingError(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetTrendingVideos", mock.Anything, 50).Return([]domain.TrendingVideo{}, errors.New("trending query failed"))

	result, err := svc.GetTrendingVideosWithDetails(context.Background(), 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetTrendingVideosWithDetails_GetByIDsError(t *testing.T) {
	svc, viewsRepo, videoRepo := newTestService(t)

	trending := []domain.TrendingVideo{
		{VideoID: "vid-1"},
	}
	viewsRepo.On("GetTrendingVideos", mock.Anything, 50).Return(trending, nil)
	videoRepo.On("GetByIDs", mock.Anything, []string{"vid-1"}).Return(nil, errors.New("video query failed"))

	result, err := svc.GetTrendingVideosWithDetails(context.Background(), 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestUpdateTrendingMetrics_EmptyVideoIDs(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetBatchTrendingStats", mock.Anything, []string{}).Return([]domain.VideoTrendingStats{}, nil)

	err := svc.UpdateTrendingMetrics(context.Background(), []string{})
	assert.NoError(t, err)
	viewsRepo.AssertNotCalled(t, "BatchUpdateTrendingVideos", mock.Anything, mock.Anything)
}

func TestUpdateTrendingMetrics_GetStatsError(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("GetBatchTrendingStats", mock.Anything, []string{"vid-1"}).Return([]domain.VideoTrendingStats{}, errors.New("stats error"))

	err := svc.UpdateTrendingMetrics(context.Background(), []string{"vid-1"})
	assert.Error(t, err)
}

func TestUpdateTrendingMetrics_IsTrendingCalculation(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	stats := []domain.VideoTrendingStats{
		{VideoID: "trending-24h", Score24h: 150.0, Score1h: 10.0, Score7d: 50.0, ViewsLastHour: 5, ViewsLast24h: 50, ViewsLast7d: 200},
		{VideoID: "trending-velocity", Score24h: 50.0, Score1h: 60.0, Score7d: 50.0, ViewsLastHour: 20, ViewsLast24h: 100, ViewsLast7d: 400},
		{VideoID: "not-trending", Score24h: 10.0, Score1h: 5.0, Score7d: 20.0, ViewsLastHour: 1, ViewsLast24h: 10, ViewsLast7d: 50},
	}
	videoIDs := []string{"trending-24h", "trending-velocity", "not-trending"}

	viewsRepo.On("GetBatchTrendingStats", mock.Anything, videoIDs).Return(stats, nil)
	viewsRepo.On("BatchUpdateTrendingVideos", mock.Anything, mock.MatchedBy(func(videos []*domain.TrendingVideo) bool {
		if len(videos) != 3 {
			return false
		}
		trendingMap := make(map[string]bool)
		for _, v := range videos {
			trendingMap[v.VideoID] = v.IsTrending
		}
		return trendingMap["trending-24h"] == true && trendingMap["not-trending"] == false
	})).Return(nil)

	err := svc.UpdateTrendingMetrics(context.Background(), videoIDs)
	assert.NoError(t, err)
	viewsRepo.AssertExpectations(t)
}

func TestGetTopVideos_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	dbResults := []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}{
		{VideoID: "vid-1", TotalViews: 100, UniqueViews: 80, AvgDuration: 120.0},
		{VideoID: "vid-2", TotalViews: 50, UniqueViews: 40, AvgDuration: 60.0},
	}
	viewsRepo.On("GetTopVideos", mock.Anything, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time"), 20).Return(dbResults, nil)

	result, err := svc.GetTopVideos(context.Background(), 7, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
	assert.Equal(t, "vid-1", result[0].VideoID)
	assert.Equal(t, int64(100), result[0].TotalViews)
}

func TestGetTopVideos_LimitClamped(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	dbResults := []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}{}
	viewsRepo.On("GetTopVideos", mock.Anything, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time"), 20).Return(dbResults, nil)

	_, err := svc.GetTopVideos(context.Background(), 30, 200)
	assert.NoError(t, err)
}

func TestGetTopVideos_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	dbResults := []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}{}
	viewsRepo.On("GetTopVideos", mock.Anything, mock.AnythingOfType("time.Time"), mock.AnythingOfType("time.Time"), 10).Return(dbResults, errors.New("query error"))

	_, err := svc.GetTopVideos(context.Background(), 7, 10)
	assert.Error(t, err)
}

func TestGetViewHistory_Success(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	views := []domain.UserView{
		{ID: "v1", VideoID: "vid-1"},
		{ID: "v2", VideoID: "vid-2"},
	}
	viewsRepo.On("GetViewsByDateRange", mock.Anything, mock.AnythingOfType("*domain.ViewAnalyticsFilter")).Return(views, nil)

	filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1", Limit: 50}
	result, err := svc.GetViewHistory(context.Background(), filter)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetViewHistory_LimitDefault(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	views := []domain.UserView{}
	viewsRepo.On("GetViewsByDateRange", mock.Anything, mock.MatchedBy(func(f *domain.ViewAnalyticsFilter) bool {
		return f.Limit == 100
	})).Return(views, nil)

	filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1", Limit: 0}
	_, err := svc.GetViewHistory(context.Background(), filter)
	assert.NoError(t, err)
}

func TestGetViewHistory_LimitCapped(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	views := []domain.UserView{}
	viewsRepo.On("GetViewsByDateRange", mock.Anything, mock.MatchedBy(func(f *domain.ViewAnalyticsFilter) bool {
		return f.Limit == 100
	})).Return(views, nil)

	filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1", Limit: 5000}
	_, err := svc.GetViewHistory(context.Background(), filter)
	assert.NoError(t, err)
}

func TestAggregateStats_WithDate(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	date := time.Date(2025, 1, 15, 0, 0, 0, 0, time.UTC)
	viewsRepo.On("AggregateDailyStats", mock.Anything, date).Return(nil)

	err := svc.AggregateStats(context.Background(), &date)
	assert.NoError(t, err)
	viewsRepo.AssertExpectations(t)
}

func TestAggregateStats_NilDate(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("AggregateDailyStats", mock.Anything, mock.AnythingOfType("time.Time")).Return(nil)

	err := svc.AggregateStats(context.Background(), nil)
	assert.NoError(t, err)
	viewsRepo.AssertExpectations(t)
}

func TestAggregateStats_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("AggregateDailyStats", mock.Anything, mock.AnythingOfType("time.Time")).Return(errors.New("aggregate failed"))

	err := svc.AggregateStats(context.Background(), nil)
	assert.Error(t, err)
}

func TestCleanupOldData_CustomDays(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("CleanupOldViews", mock.Anything, 90).Return(nil)

	err := svc.CleanupOldData(context.Background(), 90)
	assert.NoError(t, err)
	viewsRepo.AssertExpectations(t)
}

func TestCleanupOldData_DefaultDays(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("CleanupOldViews", mock.Anything, 365).Return(nil)

	err := svc.CleanupOldData(context.Background(), 0)
	assert.NoError(t, err)
	viewsRepo.AssertExpectations(t)
}

func TestCleanupOldData_Error(t *testing.T) {
	svc, viewsRepo, _ := newTestService(t)

	viewsRepo.On("CleanupOldViews", mock.Anything, 30).Return(errors.New("cleanup failed"))

	err := svc.CleanupOldData(context.Background(), 30)
	assert.Error(t, err)
}

func TestGenerateFingerprint(t *testing.T) {
	fp1 := GenerateFingerprint("192.168.1.1", "Mozilla/5.0")
	fp2 := GenerateFingerprint("192.168.1.1", "Mozilla/5.0")
	fp3 := GenerateFingerprint("10.0.0.1", "Chrome/100")

	assert.Equal(t, fp1, fp2, "Same input should produce same fingerprint")
	assert.NotEqual(t, fp1, fp3, "Different input should produce different fingerprint")
	assert.Len(t, fp1, 32, "Fingerprint should be 32 hex chars (16 bytes)")
}

func TestValidateTrackingRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *domain.ViewTrackingRequest
		wantErr string
	}{
		{
			name: "valid request",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
			},
			wantErr: "",
		},
		{
			name: "missing video_id",
			req: &domain.ViewTrackingRequest{
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
			},
			wantErr: "video_id is required",
		},
		{
			name: "missing session_id",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				FingerprintHash: "fp-hash",
			},
			wantErr: "session_id is required",
		},
		{
			name: "missing fingerprint_hash",
			req: &domain.ViewTrackingRequest{
				VideoID:   "vid-1",
				SessionID: "sess-1",
			},
			wantErr: "fingerprint_hash is required",
		},
		{
			name: "negative completion_percentage",
			req: &domain.ViewTrackingRequest{
				VideoID:              "vid-1",
				SessionID:            "sess-1",
				FingerprintHash:      "fp-hash",
				CompletionPercentage: -10.0,
			},
			wantErr: "completion_percentage must be between 0 and 100",
		},
		{
			name: "completion_percentage over 100",
			req: &domain.ViewTrackingRequest{
				VideoID:              "vid-1",
				SessionID:            "sess-1",
				FingerprintHash:      "fp-hash",
				CompletionPercentage: 150.0,
			},
			wantErr: "completion_percentage must be between 0 and 100",
		},
		{
			name: "negative watch_duration",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
				WatchDuration:   -5,
			},
			wantErr: "watch_duration must be non-negative",
		},
		{
			name: "negative video_duration",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
				VideoDuration:   -1,
			},
			wantErr: "video_duration must be non-negative",
		},
		{
			name: "negative seek_count",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
				SeekCount:       -1,
			},
			wantErr: "interaction counts must be non-negative",
		},
		{
			name: "negative pause_count",
			req: &domain.ViewTrackingRequest{
				VideoID:         "vid-1",
				SessionID:       "sess-1",
				FingerprintHash: "fp-hash",
				PauseCount:      -1,
			},
			wantErr: "interaction counts must be non-negative",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTrackingRequest(tt.req)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			}
		})
	}
}

func TestStringPtrIfNotEmpty(t *testing.T) {
	result := stringPtrIfNotEmpty("")
	assert.Nil(t, result)

	result = stringPtrIfNotEmpty("1080p")
	require.NotNil(t, result)
	assert.Equal(t, "1080p", *result)
}

func TestCalculateVelocityScore(t *testing.T) {
	tests := []struct {
		name        string
		hourly      int64
		daily       int64
		weekly      int64
		expectZero  bool
		expectMax   bool
		expectRange [2]float64
	}{
		{
			name:       "zero weekly views returns 0",
			hourly:     10,
			daily:      100,
			weekly:     0,
			expectZero: true,
		},
		{
			name:        "normal velocity",
			hourly:      10,
			daily:       100,
			weekly:      500,
			expectRange: [2]float64{0, 1000},
		},
		{
			name:        "high hourly spike",
			hourly:      1000,
			daily:       100,
			weekly:      700,
			expectRange: [2]float64{0, 1000},
		},
		{
			name:        "steady growth",
			hourly:      5,
			daily:       100,
			weekly:      350,
			expectRange: [2]float64{0, 1000},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := calculateVelocityScore(tt.hourly, tt.daily, tt.weekly)
			if tt.expectZero {
				assert.Equal(t, 0.0, score)
			} else {
				assert.GreaterOrEqual(t, score, tt.expectRange[0])
				assert.LessOrEqual(t, score, tt.expectRange[1])
			}
		})
	}
}

func TestCalculateVelocityScore_CappedAt1000(t *testing.T) {
	score := calculateVelocityScore(100000, 10000, 7)
	assert.Equal(t, 1000.0, score)
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

	mockViewsRepo.On("GetBatchTrendingStats", mock.Anything, videoIDs).Return(stats, nil).Times(1)

	mockViewsRepo.On("BatchUpdateTrendingVideos", mock.Anything, mock.MatchedBy(func(videos []*domain.TrendingVideo) bool {
		return len(videos) == 5
	})).Return(nil).Times(1)

	ctx := context.Background()
	err := service.UpdateTrendingMetrics(ctx, videoIDs)
	assert.NoError(t, err)

	mockViewsRepo.AssertExpectations(t)

	mockViewsRepo.AssertNumberOfCalls(t, "GetBatchTrendingStats", 1)
	mockViewsRepo.AssertNumberOfCalls(t, "BatchUpdateTrendingVideos", 1)

	mockViewsRepo.AssertNumberOfCalls(t, "CalculateEngagementScore", 0)
	mockViewsRepo.AssertNumberOfCalls(t, "GetUniqueViews", 0)
	mockViewsRepo.AssertNumberOfCalls(t, "UpdateTrendingVideo", 0)
}
