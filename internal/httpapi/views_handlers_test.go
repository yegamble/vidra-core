package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

func TestViewsHandler_TrackView(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	t.Run("successful view tracking - authenticated user", func(t *testing.T) {
		request := domain.ViewTrackingRequest{
			SessionID:            uuid.New().String(),
			FingerprintHash:      "test_hash_123",
			WatchDuration:        120,
			VideoDuration:        300,
			CompletionPercentage: 40.0,
			IsCompleted:          false,
			SeekCount:            2,
			PauseCount:           1,
			DeviceType:           "mobile",
			OSName:               "iOS",
			BrowserName:          "Safari",
			CountryCode:          "US",
			TrackingConsent:      true,
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		// Add user to context (authenticated user)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
		req = req.WithContext(ctx)

		// Set up router with URL parameter
		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.True(t, response["success"].(bool))
		data := response["data"].(map[string]interface{})
		assert.Equal(t, "View tracked successfully", data["message"])
		assert.Equal(t, video.ID, data["video_id"])

		// Verify view was actually created in database
		view, err := viewsRepo.GetUserViewBySessionAndVideo(context.Background(), request.SessionID, video.ID)
		require.NoError(t, err)
		require.NotNil(t, view)
		assert.Equal(t, user.ID, *view.UserID)
		assert.Equal(t, 120, view.WatchDuration)
		assert.Equal(t, 40.0, view.CompletionPercentage)
	})

	t.Run("successful view tracking - anonymous user", func(t *testing.T) {
		request := domain.ViewTrackingRequest{
			SessionID:            uuid.New().String(),
			FingerprintHash:      "anon_hash_456",
			WatchDuration:        90,
			VideoDuration:        300,
			CompletionPercentage: 30.0,
			IsAnonymous:          true,
			TrackingConsent:      true,
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		// No user in context (anonymous)
		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify anonymous view was created
		view, err := viewsRepo.GetUserViewBySessionAndVideo(context.Background(), request.SessionID, video.ID)
		require.NoError(t, err)
		require.NotNil(t, view)
		assert.Nil(t, view.UserID) // Should be nil for anonymous users
		assert.True(t, view.IsAnonymous)
	})

	t.Run("update existing view session", func(t *testing.T) {
		sessionID := uuid.New().String()

		// Create initial view
		initialRequest := domain.ViewTrackingRequest{
			SessionID:            sessionID,
			FingerprintHash:      "update_test_hash",
			WatchDuration:        60,
			VideoDuration:        300,
			CompletionPercentage: 20.0,
			TrackingConsent:      true,
		}

		body, err := json.Marshal(initialRequest)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Update the same session with more watch time
		updateRequest := domain.ViewTrackingRequest{
			SessionID:            sessionID,
			FingerprintHash:      "update_test_hash",
			WatchDuration:        150, // Increased watch duration
			VideoDuration:        300,
			CompletionPercentage: 50.0, // Increased completion
			SeekCount:            1,    // Added some interaction
			TrackingConsent:      true,
		}

		body, err = json.Marshal(updateRequest)
		require.NoError(t, err)

		req = httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr = httptest.NewRecorder()
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify the view was updated, not duplicated
		view, err := viewsRepo.GetUserViewBySessionAndVideo(context.Background(), sessionID, video.ID)
		require.NoError(t, err)
		require.NotNil(t, view)
		assert.Equal(t, 150, view.WatchDuration)         // Should be updated
		assert.Equal(t, 50.0, view.CompletionPercentage) // Should be updated
		assert.Equal(t, 1, view.SeekCount)               // Should be updated
	})

	t.Run("invalid video ID", func(t *testing.T) {
		request := domain.ViewTrackingRequest{
			SessionID:       uuid.New().String(),
			FingerprintHash: "test_hash",
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		nonExistentVideoID := uuid.New().String()
		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", nonExistentVideoID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("invalid JSON payload", func(t *testing.T) {
		invalidJSON := `{"session_id": "test", "invalid_json": }`

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), strings.NewReader(invalidJSON))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("validation errors", func(t *testing.T) {
		request := domain.ViewTrackingRequest{
			SessionID:            "", // Missing required field
			FingerprintHash:      "test_hash",
			CompletionPercentage: 150.0, // Invalid percentage
			WatchDuration:        -10,   // Invalid duration
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestViewsHandler_GetVideoAnalytics(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	// Create some test views
	for i := 0; i < 5; i++ {
		view := createTestViewsUserView(t, testDB, user.ID, video.ID, i)
		_ = view
	}

	t.Run("successful analytics retrieval", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/analytics", video.ID), nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		data := response["data"].(map[string]interface{})
		assert.Greater(t, data["total_views"], float64(0))
		assert.Greater(t, data["unique_views"], float64(0))
		assert.NotNil(t, data["device_breakdown"])
		assert.NotNil(t, data["country_breakdown"])
	})

	t.Run("analytics with date filters", func(t *testing.T) {
		// Use dates that will definitely include the test data
		startDate := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
		endDate := time.Now().AddDate(0, 0, 1).Format("2006-01-02") // Tomorrow to be sure

		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/videos/%s/analytics?start_date=%s&end_date=%s", video.ID, startDate, endDate), nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
		router.ServeHTTP(rr, req)

		// For now, just check that the request is processed without error
		// The date filtering logic is tested in the repository tests
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("analytics with device filter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/videos/%s/analytics?device_type=mobile", video.ID), nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("invalid date format", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("/api/v1/videos/%s/analytics?start_date=invalid-date", video.ID), nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestViewsHandler_GetTrendingVideos(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video1 := createTestViewsVideo(t, testDB, user.ID)
	video2 := createTestViewsVideo(t, testDB, user.ID)

	// Create trending data
	trending1 := &domain.TrendingVideo{
		VideoID:         video1.ID,
		ViewsLastHour:   100,
		ViewsLast24h:    2400,
		EngagementScore: 500.0,
		VelocityScore:   80.0,
		IsTrending:      true,
		LastUpdated:     time.Now(),
	}
	trending2 := &domain.TrendingVideo{
		VideoID:         video2.ID,
		ViewsLastHour:   75,
		ViewsLast24h:    1800,
		EngagementScore: 400.0,
		VelocityScore:   60.0,
		IsTrending:      true,
		LastUpdated:     time.Now(),
	}

	err := viewsRepo.UpdateTrendingVideo(context.Background(), trending1)
	require.NoError(t, err)
	err = viewsRepo.UpdateTrendingVideo(context.Background(), trending2)
	require.NoError(t, err)

	t.Run("get trending videos", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/trending", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/trending", handler.GetTrendingVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		// Extract data from wrapped response
		data := response["data"].(map[string]interface{})

		videos := data["videos"].([]interface{})
		assert.Len(t, videos, 2)
		assert.NotNil(t, data["limit"])
		assert.NotNil(t, data["updated_at"])
	})

	t.Run("trending videos with details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/trending?include_details=true", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/trending", handler.GetTrendingVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &wrapper)
		require.NoError(t, err)

		// Re-marshal the data part to get the proper structure
		dataBytes, err3 := json.Marshal(wrapper["data"])
		require.NoError(t, err3)

		var response domain.TrendingVideosResponse
		err4 := json.Unmarshal(dataBytes, &response)
		require.NoError(t, err4)

		assert.Len(t, response.Videos, 2)
		assert.NotEmpty(t, response.Videos[0].Video.Title)
		assert.NotEmpty(t, response.Videos[1].Video.Title)
	})

	t.Run("trending videos with limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/trending?limit=1", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/trending", handler.GetTrendingVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		// Extract data from wrapped response
		data := response["data"].(map[string]interface{})

		videos := data["videos"].([]interface{})
		assert.Len(t, videos, 1)
	})
}

func TestViewsHandler_GetTopVideos(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data with different view counts
	user := createTestViewsUser(t, testDB)
	video1 := createTestViewsVideo(t, testDB, user.ID)
	video2 := createTestViewsVideo(t, testDB, user.ID)

	// Create more views for video1
	for i := 0; i < 5; i++ {
		createTestViewsUserView(t, testDB, user.ID, video1.ID, i)
	}
	for i := 0; i < 3; i++ {
		createTestViewsUserView(t, testDB, user.ID, video2.ID, i)
	}

	t.Run("get top videos", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/top", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/top", handler.GetTopVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		data := response["data"].(map[string]interface{})
		videos := data["videos"].([]interface{})
		assert.Len(t, videos, 2)

		// Should be ordered by view count
		firstVideo := videos[0].(map[string]interface{})
		assert.Equal(t, video1.ID, firstVideo["video_id"])
		assert.Equal(t, float64(5), firstVideo["total_views"]) // JSON numbers are float64
	})

	t.Run("top videos with custom period and limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/top?days=30&limit=1", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/top", handler.GetTopVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		data := response["data"].(map[string]interface{})
		assert.Equal(t, float64(30), data["period_days"])
		assert.Equal(t, float64(1), data["limit"])

		videos := data["videos"].([]interface{})
		assert.Len(t, videos, 1)
	})

	t.Run("invalid days parameter", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/top?days=invalid", nil)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Get("/api/v1/videos/top", handler.GetTopVideos)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestViewsHandler_GenerateFingerprint(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	t.Run("successful fingerprint generation", func(t *testing.T) {
		request := map[string]string{
			"ip":         "192.168.1.1",
			"user_agent": "Mozilla/5.0 (iPhone; CPU iPhone OS 14_0 like Mac OS X)",
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/views/fingerprint", handler.GenerateFingerprint)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err = json.Unmarshal(rr.Body.Bytes(), &response)
		require.NoError(t, err)

		data := response["data"].(map[string]interface{})
		assert.NotEmpty(t, data["fingerprint_hash"])
		assert.NotNil(t, data["created_at"])

		// Fingerprint should be consistent
		fingerprint1 := data["fingerprint_hash"].(string)

		// Make another request with same data
		req2 := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", bytes.NewReader(body))
		req2.Header.Set("Content-Type", "application/json")

		rr2 := httptest.NewRecorder()
		router.ServeHTTP(rr2, req2)

		var response2 map[string]interface{}
		err = json.Unmarshal(rr2.Body.Bytes(), &response2)
		require.NoError(t, err)

		data2 := response2["data"].(map[string]interface{})
		fingerprint2 := data2["fingerprint_hash"].(string)
		assert.Equal(t, fingerprint1, fingerprint2)
	})

	t.Run("missing fields", func(t *testing.T) {
		request := map[string]string{
			"ip": "192.168.1.1",
			// Missing user_agent
		}

		body, err := json.Marshal(request)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/views/fingerprint", handler.GenerateFingerprint)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestViewsHandler_ConcurrentRequests(t *testing.T) {
	// This test verifies that multiple concurrent requests to the same video
	// are handled correctly without rate limiting legitimate traffic
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	concurrency := 20
	results := make(chan int, concurrency)

	// Launch concurrent view tracking requests
	for i := 0; i < concurrency; i++ {
		go func(index int) {
			request := domain.ViewTrackingRequest{
				SessionID:            uuid.New().String(), // Each request has unique session
				FingerprintHash:      fmt.Sprintf("concurrent_hash_%d", index),
				WatchDuration:        60 + index, // Vary the data
				VideoDuration:        300,
				CompletionPercentage: float64(20 + index%80),
				DeviceType:           "mobile",
				TrackingConsent:      true,
			}

			body, err := json.Marshal(request)
			if err != nil {
				results <- http.StatusInternalServerError
				return
			}

			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			// Add user to context
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
			router.ServeHTTP(rr, req)

			results <- rr.Code
		}(i)
	}

	// Collect all results
	var statusCodes []int
	for i := 0; i < concurrency; i++ {
		statusCodes = append(statusCodes, <-results)
	}

	// All requests should succeed (no rate limiting of legitimate traffic)
	for i, code := range statusCodes {
		assert.Equal(t, http.StatusOK, code, fmt.Sprintf("Request %d failed with status %d", i, code))
	}

	// Verify all views were tracked
	analytics, err := viewsService.GetVideoAnalytics(context.Background(), &domain.ViewAnalyticsFilter{VideoID: video.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(concurrency), analytics.TotalViews)
	assert.Equal(t, int64(concurrency), analytics.UniqueViews) // All unique sessions
}

func TestViewsHandler_BulkViewTracking(t *testing.T) {
	// Test handling many view tracking requests in rapid succession
	// to ensure the system doesn't accidentally rate limit genuine user behavior
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsUser(t, testDB)
	video := createTestViewsVideo(t, testDB, user.ID)

	// Simulate a user watching a video and generating multiple tracking events
	// (seeking, pausing, resuming, etc.) - this is normal user behavior that shouldn't be rate limited
	sessionID := uuid.New().String()
	fingerprintHash := "user_session_hash"

	trackingEvents := []domain.ViewTrackingRequest{
		// Initial view
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 10, CompletionPercentage: 3.3, SeekCount: 0, PauseCount: 0},
		// User seeks forward
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 30, CompletionPercentage: 10.0, SeekCount: 1, PauseCount: 0},
		// User pauses
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 45, CompletionPercentage: 15.0, SeekCount: 1, PauseCount: 1},
		// User resumes
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 90, CompletionPercentage: 30.0, SeekCount: 1, PauseCount: 1},
		// User seeks again
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 150, CompletionPercentage: 50.0, SeekCount: 2, PauseCount: 1},
		// User finishes watching
		{SessionID: sessionID, FingerprintHash: fingerprintHash, WatchDuration: 300, CompletionPercentage: 100.0, SeekCount: 2, PauseCount: 1, IsCompleted: true},
	}

	for i, event := range trackingEvents {
		event.VideoDuration = 300
		event.TrackingConsent = true

		body, err := json.Marshal(event)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		// Add user to context
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code, fmt.Sprintf("Tracking event %d failed", i))

		// Small delay between events to simulate real user behavior
		time.Sleep(10 * time.Millisecond)
	}

	// Verify that only one view record exists (same session should be updated, not duplicated)
	view, err := viewsRepo.GetUserViewBySessionAndVideo(context.Background(), sessionID, video.ID)
	require.NoError(t, err)
	require.NotNil(t, view)

	// Final state should reflect the last tracking event
	assert.Equal(t, 300, view.WatchDuration)
	assert.Equal(t, 100.0, view.CompletionPercentage)
	assert.True(t, view.IsCompleted)
	assert.Equal(t, 2, view.SeekCount)
	assert.Equal(t, 1, view.PauseCount)

	// Total views for the video should only be 1 (despite multiple tracking calls)
	analytics, err := viewsService.GetVideoAnalytics(context.Background(), &domain.ViewAnalyticsFilter{VideoID: video.ID})
	require.NoError(t, err)
	assert.Equal(t, int64(1), analytics.TotalViews)
	assert.Equal(t, int64(1), analytics.UniqueViews)
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
		FileSize:    1024000,
		MimeType:    "video/mp4",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		// Add missing fields with defaults
		OriginalCID:   "",
		ThumbnailCID:  "",
		Tags:          []string{},
		Category:      nil,
		Language:      "en",
		Metadata:      domain.VideoMetadata{},
		ThumbnailPath: "",
		PreviewPath:   "",
		OutputPaths:   map[string]string{},
		ProcessedCIDs: map[string]string{},
	}

	query := `INSERT INTO videos (id, thumbnail_id, title, description, duration, views, privacy, status, 
		upload_date, user_id, file_size, mime_type, created_at, updated_at,
		original_cid, processed_cids, thumbnail_cid, tags, category, language, metadata,
		output_paths, thumbnail_path, preview_path)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22, $23, $24)`

	processedCIDsJSON, _ := json.Marshal(video.ProcessedCIDs)
	metadataJSON, _ := json.Marshal(video.Metadata)
	outputPathsJSON, _ := json.Marshal(video.OutputPaths)

	_, err := testDB.DB.Exec(query, video.ID, video.ThumbnailID, video.Title, video.Description,
		video.Duration, video.Views, video.Privacy, video.Status, video.UploadDate, video.UserID,
		video.FileSize, video.MimeType, video.CreatedAt, video.UpdatedAt,
		video.OriginalCID, processedCIDsJSON, video.ThumbnailCID,
		pq.Array(video.Tags), video.Category, video.Language, metadataJSON,
		outputPathsJSON, video.ThumbnailPath, video.PreviewPath)
	require.NoError(t, err)

	return video
}

func createTestViewsUserView(t *testing.T, testDB *testutil.TestDB, userID, videoID string, index int) *domain.UserView {
	t.Helper()

	now := time.Now()
	view := &domain.UserView{
		ID:                   uuid.New().String(),
		VideoID:              videoID,
		UserID:               &userID,
		SessionID:            uuid.New().String(), // Each view gets unique session
		FingerprintHash:      fmt.Sprintf("test_hash_%d", index),
		WatchDuration:        120 + index*10, // Vary watch duration
		VideoDuration:        300,
		CompletionPercentage: float64(40 + index*5), // Vary completion
		IsCompleted:          false,
		SeekCount:            index % 3,
		PauseCount:           index % 2,
		DeviceType:           "mobile",
		CountryCode:          "US",
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
