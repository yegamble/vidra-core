package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/testutil"
)

func TestViewsRepository_CreateUserView(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test user and video
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	ctx := context.Background()
	now := time.Now()

	view := &domain.UserView{
		VideoID:              video.ID,
		UserID:               &user.ID,
		SessionID:            uuid.New().String(),
		FingerprintHash:      "test_hash_123",
		WatchDuration:        120,
		VideoDuration:        300,
		CompletionPercentage: 40.0,
		IsCompleted:          false,
		SeekCount:            2,
		PauseCount:           1,
		ReplayCount:          0,
		QualityChanges:       1,
		InitialLoadTime:      intPtr(1500),
		BufferEvents:         0,
		ConnectionType:       stringPtrForViews("wifi"),
		VideoQuality:         stringPtrForViews("720p"),
		DeviceType:           "mobile",
		OSName:               "iOS",
		BrowserName:          "Safari",
		ScreenResolution:     "375x667",
		IsMobile:             true,
		CountryCode:          "US",
		RegionCode:           "CA",
		CityName:             "San Francisco",
		Timezone:             "America/Los_Angeles",
		ReferrerURL:          "https://example.com",
		ReferrerType:         "search",
		UTMSource:            "google",
		UTMMedium:            "cpc",
		UTMCampaign:          "summer2024",
		IsAnonymous:          false,
		TrackingConsent:      true,
		GDPRConsent:          boolPtrForViews(true),
		ViewDate:             now.Truncate(24 * time.Hour),
		ViewHour:             now.Hour(),
		Weekday:              int(now.Weekday()),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	err := repo.CreateUserView(ctx, view)
	require.NoError(t, err)
	assert.NotEmpty(t, view.ID)

	// Verify the view was created correctly
	createdView, err := repo.GetUserViewBySessionAndVideo(ctx, view.SessionID, view.VideoID)
	require.NoError(t, err)
	require.NotNil(t, createdView)

	assert.Equal(t, view.VideoID, createdView.VideoID)
	assert.Equal(t, view.SessionID, createdView.SessionID)
	assert.Equal(t, view.WatchDuration, createdView.WatchDuration)
	assert.Equal(t, view.CompletionPercentage, createdView.CompletionPercentage)
	assert.Equal(t, view.DeviceType, createdView.DeviceType)
	assert.Equal(t, view.CountryCode, createdView.CountryCode)
}

func TestViewsRepository_UpdateUserView(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test user, video, and view
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)
	view := createTestUserView(t, testDB, user.ID, video.ID)

	ctx := context.Background()

	// Update view data
	view.WatchDuration = 200
	view.VideoDuration = 300 // Set the video duration for calculation
	view.CompletionPercentage = 66.7
	view.IsCompleted = false // This will be recalculated
	view.SeekCount = 3
	view.PauseCount = 2

	err := repo.UpdateUserView(ctx, view)
	require.NoError(t, err)

	// Verify the update
	updatedView, err := repo.GetUserViewBySessionAndVideo(ctx, view.SessionID, view.VideoID)
	require.NoError(t, err)
	require.NotNil(t, updatedView)

	assert.Equal(t, 200, updatedView.WatchDuration)
	// The completion percentage is recalculated based on watch duration / video duration
	expectedCompletion := (float64(200) / float64(300)) * 100.0
	assert.InDelta(t, expectedCompletion, updatedView.CompletionPercentage, 0.01)
	assert.False(t, updatedView.IsCompleted) // 66.67% is not completed (< 95%)
	assert.Equal(t, 3, updatedView.SeekCount)
	assert.Equal(t, 2, updatedView.PauseCount)
}

func TestViewsRepository_GetUserViewBySessionAndVideo_Deduplication(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test user and video
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	ctx := context.Background()
	sessionID := uuid.New().String()

	// Create first view
	view1 := createTestUserViewWithSession(t, testDB, user.ID, video.ID, sessionID)

	// Try to get view by session and video - should find the first view
	foundView, err := repo.GetUserViewBySessionAndVideo(ctx, sessionID, video.ID)
	require.NoError(t, err)
	require.NotNil(t, foundView)
	assert.Equal(t, view1.ID, foundView.ID)

	// Non-existent session should return nil
	nonExistentSession := uuid.New().String()
	notFound, err := repo.GetUserViewBySessionAndVideo(ctx, nonExistentSession, video.ID)
	require.NoError(t, err)
	assert.Nil(t, notFound)
}

func TestViewsRepository_GetVideoAnalytics(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user1 := createTestViewsUser(t, testDB)
	user2 := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user1.ID)

	// Create multiple views with different characteristics
	views := []*domain.UserView{
		createTestUserViewWithDetails(t, testDB, user1.ID, video.ID, 120, 40.0, "mobile", "US"),
		createTestUserViewWithDetails(t, testDB, user2.ID, video.ID, 180, 60.0, "desktop", "CA"),
		createTestUserViewWithDetails(t, testDB, user1.ID, video.ID, 300, 100.0, "mobile", "US"), // Completed view
	}

	ctx := context.Background()
	filter := &domain.ViewAnalyticsFilter{
		VideoID: video.ID,
	}

	analytics, err := repo.GetVideoAnalytics(ctx, filter)
	require.NoError(t, err)
	require.NotNil(t, analytics)

	assert.Equal(t, int64(3), analytics.TotalViews)
	assert.Equal(t, int64(3), analytics.UniqueViews) // Different sessions
	assert.Equal(t, int64(1), analytics.CompletedViews)
	assert.Equal(t, int64(600), analytics.TotalWatchTime)     // 120+180+300
	assert.InDelta(t, 200.0, analytics.AvgWatchDuration, 0.1) // (120+180+300)/3
	assert.InDelta(t, 66.7, analytics.AvgCompletionRate, 0.1) // (40+60+100)/3

	// Check device breakdown
	assert.Contains(t, analytics.DeviceBreakdown, "mobile")
	assert.Contains(t, analytics.DeviceBreakdown, "desktop")
	assert.Equal(t, int64(2), analytics.DeviceBreakdown["mobile"])
	assert.Equal(t, int64(1), analytics.DeviceBreakdown["desktop"])

	// Check country breakdown
	assert.Contains(t, analytics.CountryBreakdown, "US")
	assert.Contains(t, analytics.CountryBreakdown, "CA")
	assert.Equal(t, int64(2), analytics.CountryBreakdown["US"])
	assert.Equal(t, int64(1), analytics.CountryBreakdown["CA"])

	_ = views // Avoid unused variable warning
}

func TestViewsRepository_GetVideoAnalytics_WithFilters(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	// Create views with different device types
	createTestUserViewWithDetails(t, testDB, user.ID, video.ID, 120, 40.0, "mobile", "US")
	createTestUserViewWithDetails(t, testDB, user.ID, video.ID, 180, 60.0, "desktop", "CA")

	ctx := context.Background()

	// Test device type filter
	filter := &domain.ViewAnalyticsFilter{
		VideoID:    video.ID,
		DeviceType: "mobile",
	}

	analytics, err := repo.GetVideoAnalytics(ctx, filter)
	require.NoError(t, err)
	require.NotNil(t, analytics)

	assert.Equal(t, int64(1), analytics.TotalViews)
	assert.Equal(t, int64(120), analytics.TotalWatchTime)

	// Test country filter
	filter.DeviceType = ""
	filter.CountryCode = "CA"

	analytics, err = repo.GetVideoAnalytics(ctx, filter)
	require.NoError(t, err)

	assert.Equal(t, int64(1), analytics.TotalViews)
	assert.Equal(t, int64(180), analytics.TotalWatchTime)
}

func TestViewsRepository_IncrementVideoViews(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test user and video
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	ctx := context.Background()

	// Get initial view count
	initialViews := getVideoViewCount(t, testDB, video.ID)

	// Increment views
	err := repo.IncrementVideoViews(ctx, video.ID)
	require.NoError(t, err)

	// Verify increment
	newViews := getVideoViewCount(t, testDB, video.ID)
	assert.Equal(t, initialViews+1, newViews)

	// Increment again
	err = repo.IncrementVideoViews(ctx, video.ID)
	require.NoError(t, err)

	finalViews := getVideoViewCount(t, testDB, video.ID)
	assert.Equal(t, initialViews+2, finalViews)
}

func TestViewsRepository_GetUniqueViews(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	// Create multiple views with same and different sessions
	sessionID1 := uuid.New().String()
	sessionID2 := uuid.New().String()

	createTestUserViewWithSession(t, testDB, user.ID, video.ID, sessionID1)
	createTestUserViewWithSession(t, testDB, user.ID, video.ID, sessionID1) // Same session - should not count as unique
	createTestUserViewWithSession(t, testDB, user.ID, video.ID, sessionID2) // Different session - should count

	ctx := context.Background()
	startTime := time.Now().Add(-1 * time.Hour)
	endTime := time.Now()

	uniqueViews, err := repo.GetUniqueViews(ctx, video.ID, startTime, endTime)
	require.NoError(t, err)
	assert.Equal(t, int64(2), uniqueViews) // 2 unique sessions
}

func TestViewsRepository_CalculateEngagementScore(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	// Create views with high engagement (high completion percentage)
	createTestUserViewWithDetails(t, testDB, user.ID, video.ID, 300, 100.0, "mobile", "US") // Completed
	createTestUserViewWithDetails(t, testDB, user.ID, video.ID, 240, 80.0, "desktop", "CA") // High completion

	ctx := context.Background()

	score, err := repo.CalculateEngagementScore(ctx, video.ID, 24)
	require.NoError(t, err)
	assert.Greater(t, score, 0.0)

	// Test with no views (should return 0)
	emptyVideo := createTestViewsVideo(t, testDB, user.ID)
	emptyScore, err := repo.CalculateEngagementScore(ctx, emptyVideo.ID, 24)
	require.NoError(t, err)
	assert.Equal(t, 0.0, emptyScore)
}

func TestViewsRepository_UpdateTrendingVideo(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test video
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	ctx := context.Background()

	trending := &domain.TrendingVideo{
		VideoID:         video.ID,
		ViewsLastHour:   50,
		ViewsLast24h:    1200,
		ViewsLast7d:     8500,
		EngagementScore: 245.67,
		VelocityScore:   89.34,
		HourlyRank:      intPtr(1),
		DailyRank:       intPtr(3),
		WeeklyRank:      intPtr(5),
		IsTrending:      true,
		LastUpdated:     time.Now(),
	}

	// First insert
	err := repo.UpdateTrendingVideo(ctx, trending)
	require.NoError(t, err)

	// Update with new values
	trending.ViewsLastHour = 75
	trending.EngagementScore = 300.45
	trending.HourlyRank = intPtr(2)

	err = repo.UpdateTrendingVideo(ctx, trending)
	require.NoError(t, err)

	// Verify the update
	trendingVideos, err := repo.GetTrendingVideos(ctx, 10)
	require.NoError(t, err)
	require.Len(t, trendingVideos, 1)

	updated := trendingVideos[0]
	assert.Equal(t, video.ID, updated.VideoID)
	assert.Equal(t, int64(75), updated.ViewsLastHour)
	assert.Equal(t, 300.45, updated.EngagementScore)
	assert.Equal(t, 2, *updated.HourlyRank)
}

func TestViewsRepository_GetTopVideos(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video1 := createTestViewsVideo(t, testDB, user.ID)
	video2 := createTestViewsVideo(t, testDB, user.ID)
	video3 := createTestViewsVideo(t, testDB, user.ID)

	// Create views for videos (video1 has most views)
	for i := 0; i < 5; i++ {
		createTestUserView(t, testDB, user.ID, video1.ID) // 5 views
	}
	for i := 0; i < 3; i++ {
		createTestUserView(t, testDB, user.ID, video2.ID) // 3 views
	}
	createTestUserView(t, testDB, user.ID, video3.ID) // 1 view

	ctx := context.Background()
	startDate := time.Now().AddDate(0, 0, -7)
	endDate := time.Now()

	topVideos, err := repo.GetTopVideos(ctx, startDate, endDate, 10)
	require.NoError(t, err)
	require.Len(t, topVideos, 3)

	// Should be ordered by view count descending
	assert.Equal(t, video1.ID, topVideos[0].VideoID)
	assert.Equal(t, int64(5), topVideos[0].TotalViews)
	assert.Equal(t, int64(5), topVideos[0].UniqueViews) // Each view has unique session

	assert.Equal(t, video2.ID, topVideos[1].VideoID)
	assert.Equal(t, int64(3), topVideos[1].TotalViews)

	assert.Equal(t, video3.ID, topVideos[2].VideoID)
	assert.Equal(t, int64(1), topVideos[2].TotalViews)
}

func TestViewsRepository_RateLimitingConcurrency(t *testing.T) {
	// This test ensures that multiple concurrent view tracking requests
	// don't accidentally get rate limited or cause database conflicts
	testDB := testutil.SetupTestDB(t)
	repo := NewViewsRepository(testDB.DB)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	ctx := context.Background()
	concurrency := 50 // Simulate 50 concurrent view tracking requests

	// Create a channel to collect results
	results := make(chan error, concurrency)

	// Launch concurrent view tracking operations
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			view := &domain.UserView{
				VideoID:              video.ID,
				UserID:               &user.ID,
				SessionID:            uuid.New().String(), // Each request gets unique session
				FingerprintHash:      fmt.Sprintf("hash_%d", index),
				WatchDuration:        60 + index, // Vary the data slightly
				VideoDuration:        300,
				CompletionPercentage: float64(20 + index%80),
				SeekCount:            index % 5,
				PauseCount:           index % 3,
				DeviceType:           "mobile",
				ViewDate:             time.Now().Truncate(24 * time.Hour),
				ViewHour:             time.Now().Hour(),
				Weekday:              int(time.Now().Weekday()),
				CreatedAt:            time.Now(),
				UpdatedAt:            time.Now(),
			}

			err := repo.CreateUserView(ctx, view)
			results <- err
		}(i)
	}

	// Collect all results
	var errors []error
	for i := 0; i < concurrency; i++ {
		if err := <-results; err != nil {
			errors = append(errors, err)
		}
	}

	// All operations should succeed without rate limiting or conflicts
	assert.Empty(t, errors, "Concurrent view tracking should not produce errors")

	// Verify all views were created
	analytics, err := repo.GetVideoAnalytics(ctx, &domain.ViewAnalyticsFilter{VideoID: video.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(concurrency), analytics.TotalViews)
	assert.Equal(t, int64(concurrency), analytics.UniqueViews) // All unique sessions
}

// Helper functions

func createTestViewsUser(t *testing.T, testDB *testutil.TestDB) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser_" + uuid.New().String()[:8],
		Email:       "test_" + uuid.New().String()[:8] + "@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	query := `INSERT INTO users (id, username, email, display_name, role, password_hash, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`

	_, err := testDB.DB.Exec(query, user.ID, user.Username, user.Email, user.DisplayName,
		user.Role, "hashed_password", user.IsActive, user.CreatedAt, user.UpdatedAt)
	require.NoError(t, err)

	return user
}

func createTestViewsVideo(t *testing.T, testDB *testutil.TestDB, userID string) *domain.Video {
	t.Helper()

	video := &domain.Video{
		ID:          uuid.New().String(),
		ThumbnailID: uuid.New().String(),
		Title:       "Test Video " + uuid.New().String()[:8],
		Description: "Test Description",
		Duration:    300,
		Views:       0,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
		UploadDate:  time.Now(),
		UserID:      userID,
		CategoryID:  nil, // Set to nil to avoid foreign key constraint violation
		FileSize:    1024000,
		MimeType:    "video/mp4",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	query := `INSERT INTO videos (id, thumbnail_id, title, description, duration, views, privacy, status, 
		upload_date, user_id, category_id, file_size, mime_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	_, err := testDB.DB.Exec(query, video.ID, video.ThumbnailID, video.Title, video.Description,
		video.Duration, video.Views, video.Privacy, video.Status, video.UploadDate, video.UserID,
		video.CategoryID, video.FileSize, video.MimeType, video.CreatedAt, video.UpdatedAt)
	require.NoError(t, err)

	return video
}

func createTestUserView(t *testing.T, testDB *testutil.TestDB, userID, videoID string) *domain.UserView {
	t.Helper()
	return createTestUserViewWithSession(t, testDB, userID, videoID, uuid.New().String())
}

func createTestUserViewWithSession(t *testing.T, testDB *testutil.TestDB, userID, videoID, sessionID string) *domain.UserView {
	t.Helper()
	return createTestUserViewWithDetails(t, testDB, userID, videoID, 120, 40.0, "mobile", "US", sessionID)
}

func createTestUserViewWithDetails(t *testing.T, testDB *testutil.TestDB, userID, videoID string, watchDuration int,
	completionPercentage float64, deviceType, countryCode string, sessionID ...string) *domain.UserView {
	t.Helper()

	var sessID string
	if len(sessionID) > 0 {
		sessID = sessionID[0]
	} else {
		sessID = uuid.New().String()
	}

	now := time.Now()
	view := &domain.UserView{
		ID:                   uuid.New().String(),
		VideoID:              videoID,
		UserID:               &userID,
		SessionID:            sessID,
		FingerprintHash:      "test_hash_" + uuid.New().String()[:8],
		WatchDuration:        watchDuration,
		VideoDuration:        300,
		CompletionPercentage: completionPercentage,
		IsCompleted:          completionPercentage >= 95.0,
		SeekCount:            1,
		PauseCount:           0,
		DeviceType:           deviceType,
		CountryCode:          countryCode,
		ViewDate:             now.Truncate(24 * time.Hour),
		ViewHour:             now.Hour(),
		Weekday:              int(now.Weekday()),
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	query := `INSERT INTO user_views (id, video_id, user_id, session_id, fingerprint_hash, watch_duration, 
		video_duration, completion_percentage, is_completed, seek_count, pause_count, device_type, 
		country_code, view_date, view_hour, weekday, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18)`

	_, err := testDB.DB.Exec(query, view.ID, view.VideoID, view.UserID, view.SessionID,
		view.FingerprintHash, view.WatchDuration, view.VideoDuration, view.CompletionPercentage,
		view.IsCompleted, view.SeekCount, view.PauseCount, view.DeviceType, view.CountryCode,
		view.ViewDate, view.ViewHour, view.Weekday, view.CreatedAt, view.UpdatedAt)
	require.NoError(t, err)

	return view
}

func getVideoViewCount(t *testing.T, testDB *testutil.TestDB, videoID string) int64 {
	t.Helper()

	var viewCount int64
	query := `SELECT views FROM videos WHERE id = $1`
	err := testDB.DB.QueryRow(query, videoID).Scan(&viewCount)
	require.NoError(t, err)

	return viewCount
}

func intPtr(i int) *int {
	return &i
}

func stringPtrForViews(s string) *string {
	return &s
}

func boolPtrForViews(b bool) *bool {
	return &b
}
