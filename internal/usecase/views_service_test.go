package usecase

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

// Mock implementations
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
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.DailyVideoStats), args.Error(1)
}

func (m *MockViewsRepository) GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error) {
	args := m.Called(ctx, userID, startDate, endDate)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.UserEngagementStats), args.Error(1)
}

func (m *MockViewsRepository) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.TrendingVideo), args.Error(1)
}

func (m *MockViewsRepository) GetBatchTrendingStats(ctx context.Context, videoIDs []string) ([]domain.VideoTrendingStats, error) {
	args := m.Called(ctx, videoIDs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
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
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.UserView), args.Error(1)
}

func (m *MockViewsRepository) GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
	VideoID     string  `db:"video_id"`
	TotalViews  int64   `db:"total_views"`
	UniqueViews int64   `db:"unique_views"`
	AvgDuration float64 `db:"avg_duration"`
}, error) {
	args := m.Called(ctx, startDate, endDate, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
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

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

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
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath)
	return args.Error(0)
}

func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID)
	return args.Error(0)
}

func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func TestViewsService_TrackView_NewView(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	userID := uuid.New().String()
	videoID := uuid.New().String()
	sessionID := uuid.New().String()

	// Mock video exists
	video := &domain.Video{
		ID:     videoID,
		Title:  "Test Video",
		Status: domain.StatusCompleted,
	}
	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)

	// Mock no existing view (new view)
	mockViewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, sessionID, videoID).Return(nil, nil)

	// Mock successful view creation
	mockViewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)

	// Mock successful view count increment
	mockViewsRepo.On("IncrementVideoViews", mock.Anything, videoID).Return(nil)

	request := &domain.ViewTrackingRequest{
		VideoID:              videoID,
		SessionID:            sessionID,
		FingerprintHash:      "test_hash",
		WatchDuration:        120,
		VideoDuration:        300,
		CompletionPercentage: 40.0,
		IsCompleted:          false,
		DeviceType:           "mobile",
		CountryCode:          "US",
		TrackingConsent:      true,
	}

	err := service.TrackView(ctx, &userID, request)
	require.NoError(t, err)

	// Wait for worker to process
	service.Close()

	// Verify all mocks were called
	mockVideoRepo.AssertExpectations(t)
	mockViewsRepo.AssertExpectations(t)

	// Verify CreateUserView was called with correct data
	foundCreate := false
	for _, call := range mockViewsRepo.Calls {
		if call.Method == "CreateUserView" {
			createdView := call.Arguments[1].(*domain.UserView)
			assert.Equal(t, videoID, createdView.VideoID)
			assert.Equal(t, &userID, createdView.UserID)
			assert.Equal(t, sessionID, createdView.SessionID)
			assert.Equal(t, 120, createdView.WatchDuration)
			assert.Equal(t, 40.0, createdView.CompletionPercentage)
			foundCreate = true
			break
		}
	}
	assert.True(t, foundCreate, "CreateUserView should have been called")
}

func TestViewsService_TrackView_UpdateExistingView(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	userID := uuid.New().String()
	videoID := uuid.New().String()
	sessionID := uuid.New().String()

	// Mock video exists
	video := &domain.Video{
		ID:     videoID,
		Title:  "Test Video",
		Status: domain.StatusCompleted,
	}
	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)

	// Mock existing view
	existingView := &domain.UserView{
		ID:                   uuid.New().String(),
		VideoID:              videoID,
		UserID:               &userID,
		SessionID:            sessionID,
		WatchDuration:        60,
		CompletionPercentage: 20.0,
		IsCompleted:          false,
		SeekCount:            1,
	}
	mockViewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, sessionID, videoID).Return(existingView, nil)

	// Mock successful view update
	mockViewsRepo.On("UpdateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)

	request := &domain.ViewTrackingRequest{
		VideoID:              videoID,
		SessionID:            sessionID,
		FingerprintHash:      "test_hash",
		WatchDuration:        120, // Updated duration
		VideoDuration:        300,
		CompletionPercentage: 40.0, // Updated completion
		IsCompleted:          false,
		SeekCount:            2, // Updated seek count
	}

	err := service.TrackView(ctx, &userID, request)
	require.NoError(t, err)

	service.Close()

	mockVideoRepo.AssertExpectations(t)
	mockViewsRepo.AssertExpectations(t)

	// Verify UpdateUserView was called and not CreateUserView
	assert.NotContains(t, getMethodNames(mockViewsRepo.Calls), "CreateUserView")
	assert.Contains(t, getMethodNames(mockViewsRepo.Calls), "UpdateUserView")
}

func TestViewsService_TrackView_VideoNotFound(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	userID := uuid.New().String()
	videoID := uuid.New().String()

	// Mock video not found
	mockVideoRepo.On("GetByID", ctx, videoID).Return(nil, nil)

	request := &domain.ViewTrackingRequest{
		VideoID:         videoID,
		SessionID:       uuid.New().String(),
		FingerprintHash: "test_hash",
	}

	err := service.TrackView(ctx, &userID, request)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "video not found")

	mockVideoRepo.AssertExpectations(t)
	// ViewsRepository should not be called if video doesn't exist
	mockViewsRepo.AssertNotCalled(t, "GetUserViewBySessionAndVideo")
}

func TestViewsService_TrackView_AnonymousUser(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	videoID := uuid.New().String()
	sessionID := uuid.New().String()

	// Mock video exists
	video := &domain.Video{ID: videoID, Title: "Test Video"}
	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)

	// Mock no existing view
	mockViewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, sessionID, videoID).Return(nil, nil)

	// Mock successful view creation
	mockViewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil)

	// Mock successful view count increment
	mockViewsRepo.On("IncrementVideoViews", mock.Anything, videoID).Return(nil)

	request := &domain.ViewTrackingRequest{
		VideoID:         videoID,
		SessionID:       sessionID,
		FingerprintHash: "test_hash",
		IsAnonymous:     true,
	}

	// Pass nil for userID (anonymous)
	err := service.TrackView(ctx, nil, request)
	require.NoError(t, err)

	service.Close()

	// Verify CreateUserView was called with nil user_id
	foundCreate := false
	for _, call := range mockViewsRepo.Calls {
		if call.Method == "CreateUserView" {
			createdView := call.Arguments[1].(*domain.UserView)
			assert.Nil(t, createdView.UserID)
			assert.True(t, createdView.IsAnonymous)
			foundCreate = true
			break
		}
	}
	assert.True(t, foundCreate, "CreateUserView should have been called")
}

func TestViewsService_GetVideoAnalytics(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	videoID := uuid.New().String()

	filter := &domain.ViewAnalyticsFilter{
		VideoID: videoID,
	}

	expectedAnalytics := &domain.ViewAnalyticsResponse{
		TotalViews:        1000,
		UniqueViews:       850,
		AvgWatchDuration:  180.5,
		AvgCompletionRate: 65.2,
		CompletedViews:    520,
		TotalWatchTime:    180500,
		DeviceBreakdown:   map[string]int64{"mobile": 600, "desktop": 400},
		CountryBreakdown:  map[string]int64{"US": 500, "CA": 300, "UK": 200},
	}

	mockViewsRepo.On("GetVideoAnalytics", ctx, filter).Return(expectedAnalytics, nil)

	result, err := service.GetVideoAnalytics(ctx, filter)
	require.NoError(t, err)
	assert.Equal(t, expectedAnalytics, result)

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_GetTrendingVideos(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	limit := 25

	expectedTrending := []domain.TrendingVideo{
		{
			VideoID:         uuid.New().String(),
			ViewsLastHour:   100,
			ViewsLast24h:    2400,
			ViewsLast7d:     15000,
			EngagementScore: 456.78,
			VelocityScore:   89.12,
			IsTrending:      true,
		},
		{
			VideoID:         uuid.New().String(),
			ViewsLastHour:   75,
			ViewsLast24h:    1800,
			ViewsLast7d:     12000,
			EngagementScore: 389.45,
			VelocityScore:   67.89,
			IsTrending:      true,
		},
	}

	mockViewsRepo.On("GetTrendingVideos", ctx, limit).Return(expectedTrending, nil)

	result, err := service.GetTrendingVideos(ctx, limit)
	require.NoError(t, err)
	assert.Equal(t, expectedTrending, result)

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_GetTrendingVideos_LimitValidation(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()

	testCases := []struct {
		inputLimit    int
		expectedLimit int
	}{
		{0, 50},   // Zero should default to 50
		{-1, 50},  // Negative should default to 50
		{150, 50}, // Over 100 should default to 50
		{25, 25},  // Valid limit should be preserved
	}

	for _, tc := range testCases {
		mockViewsRepo.On("GetTrendingVideos", ctx, tc.expectedLimit).Return([]domain.TrendingVideo{}, nil).Once()

		_, err := service.GetTrendingVideos(ctx, tc.inputLimit)
		require.NoError(t, err)
	}

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_GetTrendingVideosWithDetails(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	limit := 10

	videoID1 := uuid.New().String()
	videoID2 := uuid.New().String()

	trendingVideos := []domain.TrendingVideo{
		{VideoID: videoID1, EngagementScore: 500.0, IsTrending: true},
		{VideoID: videoID2, EngagementScore: 400.0, IsTrending: true},
	}

	video1 := &domain.Video{ID: videoID1, Title: "Trending Video 1"}
	video2 := &domain.Video{ID: videoID2, Title: "Trending Video 2"}

	mockViewsRepo.On("GetTrendingVideos", ctx, limit).Return(trendingVideos, nil)
	mockVideoRepo.On("GetByIDs", ctx, []string{videoID1, videoID2}).Return([]*domain.Video{video1, video2}, nil)

	result, err := service.GetTrendingVideosWithDetails(ctx, limit)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Videos, 2)

	assert.Equal(t, videoID1, result.Videos[0].VideoID)
	assert.Equal(t, "Trending Video 1", result.Videos[0].Video.Title)
	assert.Equal(t, videoID2, result.Videos[1].VideoID)
	assert.Equal(t, "Trending Video 2", result.Videos[1].Video.Title)

	mockViewsRepo.AssertExpectations(t)
	mockVideoRepo.AssertExpectations(t)
}

func TestViewsService_UpdateTrendingMetrics(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	videoIDs := []string{uuid.New().String(), uuid.New().String()}

	// Mock batch trending stats retrieval
	stats := []domain.VideoTrendingStats{
		{
			VideoID:       videoIDs[0],
			ViewsLastHour: 25,
			ViewsLast24h:  600,
			ViewsLast7d:   3500,
			Score1h:       50.0,
			Score24h:      120.0,
			Score7d:       800.0,
		},
		{
			VideoID:       videoIDs[1],
			ViewsLastHour: 25,
			ViewsLast24h:  600,
			ViewsLast7d:   3500,
			Score1h:       50.0,
			Score24h:      120.0,
			Score7d:       800.0,
		},
	}

	mockViewsRepo.On("GetBatchTrendingStats", ctx, videoIDs).Return(stats, nil)

	// Mock batch update of trending videos
	mockViewsRepo.On("BatchUpdateTrendingVideos", ctx, mock.MatchedBy(func(videos []*domain.TrendingVideo) bool {
		if len(videos) != 2 {
			return false
		}
		for _, tv := range videos {
			if tv.EngagementScore != 120.0 {
				return false
			}
		}
		return true
	})).Return(nil)

	err := service.UpdateTrendingMetrics(ctx, videoIDs)
	require.NoError(t, err)

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_GetTopVideos(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	days := 7
	limit := 10

	// Repository returns data with db tags
	repoResults := []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}{
		{VideoID: uuid.New().String(), TotalViews: 5000, UniqueViews: 4200, AvgDuration: 150.5},
		{VideoID: uuid.New().String(), TotalViews: 3500, UniqueViews: 2800, AvgDuration: 180.2},
	}

	// Service returns data with json tags (what we expect back)
	expectedTopVideos := []struct {
		VideoID     string `json:"video_id"`
		TotalViews  int64  `json:"total_views"`
		UniqueViews int64  `json:"unique_views"`
	}{
		{VideoID: repoResults[0].VideoID, TotalViews: repoResults[0].TotalViews, UniqueViews: repoResults[0].UniqueViews},
		{VideoID: repoResults[1].VideoID, TotalViews: repoResults[1].TotalViews, UniqueViews: repoResults[1].UniqueViews},
	}

	// Calculate expected date range
	endDate := time.Now()
	startDate := endDate.AddDate(0, 0, -days)

	mockViewsRepo.On("GetTopVideos", ctx,
		mock.MatchedBy(func(t time.Time) bool { return t.Day() == startDate.Day() }),
		mock.MatchedBy(func(t time.Time) bool { return t.Day() == endDate.Day() }),
		limit).Return(repoResults, nil)

	result, err := service.GetTopVideos(ctx, days, limit)
	require.NoError(t, err)
	assert.Equal(t, expectedTopVideos, result)

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_AggregateStats(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()

	// Test with default date (yesterday)
	mockViewsRepo.On("AggregateDailyStats", ctx,
		mock.MatchedBy(func(t time.Time) bool {
			yesterday := time.Now().AddDate(0, 0, -1)
			return t.Day() == yesterday.Day()
		})).Return(nil)

	err := service.AggregateStats(ctx, nil)
	require.NoError(t, err)

	// Test with specific date
	specificDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	mockViewsRepo.On("AggregateDailyStats", ctx, specificDate).Return(nil)

	err = service.AggregateStats(ctx, &specificDate)
	require.NoError(t, err)

	mockViewsRepo.AssertExpectations(t)
}

func TestViewsService_CleanupOldData(t *testing.T) {
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()

	testCases := []struct {
		inputDays    int
		expectedDays int
	}{
		{0, 365},     // Zero should default to 365
		{-1, 365},    // Negative should default to 365
		{90, 90},     // Valid input should be preserved
		{1000, 1000}, // Large valid input should be preserved
	}

	for _, tc := range testCases {
		mockViewsRepo.On("CleanupOldViews", ctx, tc.expectedDays).Return(nil).Once()

		err := service.CleanupOldData(ctx, tc.inputDays)
		require.NoError(t, err)
	}

	mockViewsRepo.AssertExpectations(t)
}

func TestGenerateFingerprint(t *testing.T) {
	// Test basic fingerprint generation
	ip := "192.168.1.1"
	userAgent := "Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X)"

	fingerprint1 := GenerateFingerprint(ip, userAgent)
	fingerprint2 := GenerateFingerprint(ip, userAgent)

	// Same input should generate same fingerprint
	assert.Equal(t, fingerprint1, fingerprint2)
	assert.NotEmpty(t, fingerprint1)
	assert.Equal(t, 32, len(fingerprint1)) // 16 bytes * 2 hex chars per byte

	// Different input should generate different fingerprint
	differentFingerprint := GenerateFingerprint("192.168.1.2", userAgent)
	assert.NotEqual(t, fingerprint1, differentFingerprint)

	// Test with different user agent
	differentUserAgent := "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
	differentFingerprint2 := GenerateFingerprint(ip, differentUserAgent)
	assert.NotEqual(t, fingerprint1, differentFingerprint2)
}

func TestValidateTrackingRequest(t *testing.T) {
	validRequest := &domain.ViewTrackingRequest{
		VideoID:              uuid.New().String(),
		SessionID:            uuid.New().String(),
		FingerprintHash:      "test_hash",
		WatchDuration:        120,
		VideoDuration:        300,
		CompletionPercentage: 40.0,
		SeekCount:            2,
		PauseCount:           1,
		ReplayCount:          0,
		QualityChanges:       1,
		BufferEvents:         0,
	}

	// Valid request should pass
	err := ValidateTrackingRequest(validRequest)
	assert.NoError(t, err)

	// Test missing required fields
	testCases := []struct {
		name    string
		modify  func(*domain.ViewTrackingRequest)
		wantErr string
	}{
		{
			name:    "missing video_id",
			modify:  func(r *domain.ViewTrackingRequest) { r.VideoID = "" },
			wantErr: "video_id is required",
		},
		{
			name:    "missing session_id",
			modify:  func(r *domain.ViewTrackingRequest) { r.SessionID = "" },
			wantErr: "session_id is required",
		},
		{
			name:    "missing fingerprint_hash",
			modify:  func(r *domain.ViewTrackingRequest) { r.FingerprintHash = "" },
			wantErr: "fingerprint_hash is required",
		},
		{
			name:    "negative completion_percentage",
			modify:  func(r *domain.ViewTrackingRequest) { r.CompletionPercentage = -1.0 },
			wantErr: "completion_percentage must be between 0 and 100",
		},
		{
			name:    "completion_percentage over 100",
			modify:  func(r *domain.ViewTrackingRequest) { r.CompletionPercentage = 101.0 },
			wantErr: "completion_percentage must be between 0 and 100",
		},
		{
			name:    "negative watch_duration",
			modify:  func(r *domain.ViewTrackingRequest) { r.WatchDuration = -1 },
			wantErr: "watch_duration must be non-negative",
		},
		{
			name:    "negative video_duration",
			modify:  func(r *domain.ViewTrackingRequest) { r.VideoDuration = -1 },
			wantErr: "video_duration must be non-negative",
		},
		{
			name:    "negative seek_count",
			modify:  func(r *domain.ViewTrackingRequest) { r.SeekCount = -1 },
			wantErr: "interaction counts must be non-negative",
		},
		{
			name:    "negative pause_count",
			modify:  func(r *domain.ViewTrackingRequest) { r.PauseCount = -1 },
			wantErr: "interaction counts must be non-negative",
		},
		{
			name:    "negative buffer_events",
			modify:  func(r *domain.ViewTrackingRequest) { r.BufferEvents = -1 },
			wantErr: "interaction counts must be non-negative",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create a copy of the valid request
			request := *validRequest
			tc.modify(&request)

			err := ValidateTrackingRequest(&request)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
		})
	}
}

func TestViewsService_ConcurrentViewTracking(t *testing.T) {
	// This test verifies that the service can handle multiple concurrent
	// view tracking requests without rate limiting legitimate traffic
	mockViewsRepo := &MockViewsRepository{}
	mockVideoRepo := &MockVideoRepository{}
	service := NewViewsService(mockViewsRepo, mockVideoRepo)
	defer service.Close()

	ctx := context.Background()
	videoID := uuid.New().String()
	userID := uuid.New().String()

	// Mock video exists
	video := &domain.Video{ID: videoID, Title: "Test Video"}
	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil).Times(50)

	// Mock no existing views (all are new)
	mockViewsRepo.On("GetUserViewBySessionAndVideo", mock.Anything, mock.AnythingOfType("string"), videoID).Return(nil, nil).Times(50)

	// Mock successful view creations
	mockViewsRepo.On("CreateUserView", mock.Anything, mock.AnythingOfType("*domain.UserView")).Return(nil).Times(50)

	// Mock successful view count increments
	mockViewsRepo.On("IncrementVideoViews", mock.Anything, videoID).Return(nil).Times(50)

	concurrency := 50
	results := make(chan error, concurrency)

	// Launch concurrent operations
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			request := &domain.ViewTrackingRequest{
				VideoID:              videoID,
				SessionID:            uuid.New().String(), // Unique session per request
				FingerprintHash:      fmt.Sprintf("hash_%d", index),
				WatchDuration:        60 + index, // Vary the data
				VideoDuration:        300,
				CompletionPercentage: float64(20 + index%80),
				DeviceType:           "mobile",
				TrackingConsent:      true,
			}

			err := service.TrackView(ctx, &userID, request)
			results <- err
		}(i)
	}

	// Collect results
	var errors []error
	for i := 0; i < concurrency; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		}
	}

	// All concurrent operations should succeed
	assert.Empty(t, errors, "Concurrent view tracking should not fail")

	service.Close()

	// Verify all mocks were satisfied
	mockVideoRepo.AssertExpectations(t)
	mockViewsRepo.AssertExpectations(t)
}

// Helper functions

func getMethodNames(calls []mock.Call) []string {
	var names []string
	for _, call := range calls {
		names = append(names, call.Method)
	}
	return names
}

func stringPtr(s string) *string {
	return &s
}
