package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
)

// TestRateLimitingDoesNotBlockGenuineTraffic ensures that our view tracking system
// doesn't accidentally rate limit legitimate user behavior patterns
func TestRateLimitingDoesNotBlockGenuineTraffic(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsRateLimitUser(t, testDB)
	video := createTestViewsRateLimitVideo(t, testDB, user.ID)

	t.Run("rapid session updates should not be rate limited", func(t *testing.T) {
		// Simulate a user watching a video and frequently updating their progress
		// This is normal behavior (e.g., video player sending heartbeats every 10 seconds)
		sessionID := uuid.New().String()
		fingerprintHash := "heartbeat_user_hash"

		// Create 20 rapid updates (simulating 10-second heartbeats for a 3+ minute watch)
		successCount := 0
		for i := 0; i < 20; i++ {
			request := domain.ViewTrackingRequest{
				SessionID:            sessionID,
				FingerprintHash:      fingerprintHash,
				WatchDuration:        (i + 1) * 10, // Progressive watch time
				VideoDuration:        300,
				CompletionPercentage: float64((i+1)*10) / 3.0, // Progressive completion
				SeekCount:            i / 5,                   // Occasional seeks
				TrackingConsent:      true,
			}

			body, err := json.Marshal(request)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				successCount++
			}

			// Small delay to simulate realistic timing
			time.Sleep(time.Millisecond * 10)
		}

		// All requests should succeed - no rate limiting for same session updates
		assert.Equal(t, 20, successCount, "All session updates should succeed without rate limiting")

		// Verify only one view record exists (same session)
		view, err := viewsRepo.GetUserViewBySessionAndVideo(context.Background(), sessionID, video.ID)
		require.NoError(t, err)
		require.NotNil(t, view)
		assert.Equal(t, 200, view.WatchDuration) // Final watch duration
	})

	t.Run("multiple users viewing same video should not be rate limited", func(t *testing.T) {
		// Simulate multiple different users watching the same popular video simultaneously
		numUsers := 25
		var wg sync.WaitGroup
		successChan := make(chan bool, numUsers)

		// Create multiple users concurrently viewing the same video
		for i := 0; i < numUsers; i++ {
			wg.Add(1)
			go func(userIndex int) {
				defer wg.Done()

				// Each user gets a unique session and fingerprint
				request := domain.ViewTrackingRequest{
					SessionID:            uuid.New().String(),
					FingerprintHash:      fmt.Sprintf("user_%d_hash", userIndex),
					WatchDuration:        60 + userIndex, // Slight variation in watch time
					VideoDuration:        300,
					CompletionPercentage: float64(20 + userIndex%80), // Varied completion
					DeviceType:           stringPtr("mobile"),
					TrackingConsent:      true,
				}

				body, err := json.Marshal(request)
				if err != nil {
					successChan <- false
					return
				}

				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				// Some users are authenticated, some anonymous
				if userIndex%2 == 0 {
					ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
					req = req.WithContext(ctx)
				}

				rr := httptest.NewRecorder()
				router := chi.NewRouter()
				router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
				router.ServeHTTP(rr, req)

				successChan <- rr.Code == http.StatusOK
			}(i)
		}

		wg.Wait()
		close(successChan)

		// Count successful requests
		successCount := 0
		for success := range successChan {
			if success {
				successCount++
			}
		}

		// All users should be able to track their views without rate limiting
		assert.Equal(t, numUsers, successCount, "Multiple users should be able to view the same video simultaneously")
	})

	t.Run("single user viewing multiple videos should not be rate limited", func(t *testing.T) {
		// Simulate a user binge-watching multiple videos (common behavior)
		numVideos := 15
		var videos []*domain.Video

		// Create multiple test videos
		for i := 0; i < numVideos; i++ {
			video := createTestViewsRateLimitVideo(t, testDB, user.ID)
			videos = append(videos, video)
		}

		successCount := 0
		for i, video := range videos {
			request := domain.ViewTrackingRequest{
				SessionID:            uuid.New().String(), // New session for each video
				FingerprintHash:      "binge_watcher_hash",
				WatchDuration:        240 + i*10, // Varied watch duration
				VideoDuration:        300,
				CompletionPercentage: float64(80 + i%20), // High completion rate
				DeviceType:           stringPtr("desktop"),
				TrackingConsent:      true,
			}

			body, err := json.Marshal(request)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				successCount++
			}

			// Small delay between videos (realistic binge-watching)
			time.Sleep(time.Millisecond * 50)
		}

		// All video views should be tracked successfully
		assert.Equal(t, numVideos, successCount, "User should be able to watch multiple videos without rate limiting")
	})

	t.Run("analytics requests should not be rate limited", func(t *testing.T) {
		// Simulate a dashboard or analytics page making multiple requests
		// for different metrics - this is normal admin/creator behavior
		numRequests := 30
		successCount := 0

		// Make multiple analytics requests in quick succession
		for i := 0; i < numRequests; i++ {
			// Vary the request parameters to simulate different analytics queries
			var url string
			switch i % 4 {
			case 0:
				url = fmt.Sprintf("/api/v1/videos/%s/analytics", video.ID)
			case 1:
				url = fmt.Sprintf("/api/v1/videos/%s/analytics?device_type=mobile", video.ID)
			case 2:
				startDate := time.Now().AddDate(0, 0, -7).Format("2006-01-02")
				url = fmt.Sprintf("/api/v1/videos/%s/analytics?start_date=%s", video.ID, startDate)
			case 3:
				url = fmt.Sprintf("/api/v1/videos/%s/stats/daily?days=30", video.ID)
			}

			req := httptest.NewRequest(http.MethodGet, url, nil)
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
			router.Get("/api/v1/videos/{videoId}/stats/daily", handler.GetDailyStats)
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				successCount++
			}

			time.Sleep(time.Millisecond * 20) // Realistic dashboard loading timing
		}

		// Analytics requests should not be rate limited
		assert.Equal(t, numRequests, successCount, "Analytics requests should not be rate limited")
	})
}

func TestHighVolumeViewTracking(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsRateLimitUser(t, testDB)
	video := createTestViewsRateLimitVideo(t, testDB, user.ID)

	t.Run("handle burst of legitimate view tracking requests", func(t *testing.T) {
		// Simulate a popular video going viral - many users watching simultaneously
		concurrency := 100 // High but realistic concurrent viewers
		var wg sync.WaitGroup
		results := make(chan int, concurrency)

		start := time.Now()

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				// Each viewer has unique session and device characteristics
				request := domain.ViewTrackingRequest{
					SessionID:            uuid.New().String(),
					FingerprintHash:      fmt.Sprintf("viral_viewer_%d", index),
					WatchDuration:        30 + index%240, // Realistic varied watch times
					VideoDuration:        300,
					CompletionPercentage: float64(10 + index%90), // Realistic completion spread
					DeviceType:           []string{"mobile", "desktop", "tablet"}[index%3],
					OSName:               []string{"iOS", "Android", "Windows", "macOS"}[index%4],
					CountryCode:          []string{"US", "CA", "GB", "DE", "FR"}[index%5],
					TrackingConsent:      true,
				}

				body, err := json.Marshal(request)
				if err != nil {
					results <- http.StatusInternalServerError
					return
				}

				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				// Mix of authenticated and anonymous users
				if index%3 == 0 { // 1/3 authenticated
					ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
					req = req.WithContext(ctx)
				}

				rr := httptest.NewRecorder()
				router := chi.NewRouter()
				router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
				router.ServeHTTP(rr, req)

				results <- rr.Code
			}(i)
		}

		wg.Wait()
		close(results)
		duration := time.Since(start)

		// Collect results
		statusCodes := make(map[int]int)
		for code := range results {
			statusCodes[code]++
		}

		// Performance assertions
		t.Logf("Processed %d concurrent requests in %v", concurrency, duration)
		t.Logf("Status codes: %+v", statusCodes)

		// All requests should complete successfully within reasonable time
		assert.Equal(t, concurrency, statusCodes[http.StatusOK], "All concurrent view tracking requests should succeed")
		assert.Zero(t, statusCodes[http.StatusTooManyRequests], "No requests should be rate limited")
		assert.Less(t, duration, 10*time.Second, "High volume requests should complete within reasonable time")

		// Verify all views were tracked in database
		analytics, err := viewsService.GetVideoAnalytics(context.Background(), &domain.ViewAnalyticsFilter{VideoID: &video.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(concurrency), analytics.TotalViews)
		assert.Equal(t, int64(concurrency), analytics.UniqueViews) // All unique sessions
	})

	t.Run("sustained load over time should remain stable", func(t *testing.T) {
		// Simulate sustained traffic over a longer period
		duration := 10 * time.Second
		requestsPerSecond := 10 // Moderate sustained load
		totalRequests := int(duration.Seconds()) * requestsPerSecond

		var wg sync.WaitGroup
		results := make(chan int, totalRequests)
		ticker := time.NewTicker(time.Second / time.Duration(requestsPerSecond))
		defer ticker.Stop()

		start := time.Now()
		requestCount := 0

		for time.Since(start) < duration && requestCount < totalRequests {
			select {
			case <-ticker.C:
				wg.Add(1)
				go func(index int) {
					defer wg.Done()

					request := domain.ViewTrackingRequest{
						SessionID:            uuid.New().String(),
						FingerprintHash:      fmt.Sprintf("sustained_user_%d", index),
						WatchDuration:        60 + index%180,
						VideoDuration:        300,
						CompletionPercentage: float64(20 + index%60),
						DeviceType:           stringPtr("mobile"),
						TrackingConsent:      true,
					}

					body, _ := json.Marshal(request)
					req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
					req.Header.Set("Content-Type", "application/json")

					rr := httptest.NewRecorder()
					router := chi.NewRouter()
					router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
					router.ServeHTTP(rr, req)

					results <- rr.Code
				}(requestCount)

				requestCount++
			}
		}

		wg.Wait()
		close(results)

		// Collect results
		statusCodes := make(map[int]int)
		for code := range results {
			statusCodes[code]++
		}

		t.Logf("Sustained load test: %d requests over %v", requestCount, duration)
		t.Logf("Status codes: %+v", statusCodes)

		// System should handle sustained load without rate limiting
		assert.Greater(t, statusCodes[http.StatusOK], requestCount/2, "Majority of sustained requests should succeed")
		assert.Zero(t, statusCodes[http.StatusTooManyRequests], "Sustained load should not trigger rate limiting")
	})
}

func TestEdgeCaseScenarios(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	viewsRepo := repository.NewViewsRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	viewsService := usecase.NewViewsService(viewsRepo, videoRepo)
	handler := NewViewsHandler(viewsService)

	// Create test data
	user := createTestViewsRateLimitUser(t, testDB)
	video := createTestViewsRateLimitVideo(t, testDB, user.ID)

	t.Run("rapid session switching should not be blocked", func(t *testing.T) {
		// Simulate user with multiple tabs or devices switching between sessions
		numSessions := 10
		successCount := 0

		for i := 0; i < numSessions; i++ {
			request := domain.ViewTrackingRequest{
				SessionID:            uuid.New().String(), // Each tab/device gets new session
				FingerprintHash:      "multi_tab_user",
				WatchDuration:        30 + i*5,
				VideoDuration:        300,
				CompletionPercentage: float64(10 + i*5),
				DeviceType:           stringPtr("desktop"),
				TrackingConsent:      true,
			}

			body, err := json.Marshal(request)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, user.ID)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				successCount++
			}
		}

		assert.Equal(t, numSessions, successCount, "Multiple sessions should not be rate limited")

		// Verify each session created a separate view record
		analytics, err := viewsService.GetVideoAnalytics(context.Background(), &domain.ViewAnalyticsFilter{VideoID: &video.ID})
		require.NoError(t, err)
		assert.Equal(t, int64(numSessions), analytics.UniqueViews)
	})

	t.Run("fingerprint generation should not be rate limited", func(t *testing.T) {
		// Simulate privacy-conscious users frequently regenerating fingerprints
		numRequests := 50
		successCount := 0

		for i := 0; i < numRequests; i++ {
			request := map[string]string{
				"ip":         fmt.Sprintf("192.168.1.%d", i%255),
				"user_agent": fmt.Sprintf("Mozilla/5.0 (TestBrowser %d)", i),
			}

			body, err := json.Marshal(request)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")

			rr := httptest.NewRecorder()
			router := chi.NewRouter()
			router.Post("/api/v1/views/fingerprint", handler.GenerateFingerprint)
			router.ServeHTTP(rr, req)

			if rr.Code == http.StatusOK {
				successCount++
			}
		}

		assert.Equal(t, numRequests, successCount, "Fingerprint generation should not be rate limited")
	})

	t.Run("mixed request types should not interfere", func(t *testing.T) {
		// Simulate realistic mixed traffic: view tracking + analytics + fingerprints
		var wg sync.WaitGroup
		results := make(chan bool, 60) // 20 requests of each type

		// Launch different types of requests concurrently
		for i := 0; i < 20; i++ {
			// View tracking requests
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				request := domain.ViewTrackingRequest{
					SessionID:       uuid.New().String(),
					FingerprintHash: fmt.Sprintf("mixed_hash_%d", index),
					WatchDuration:   60,
					VideoDuration:   300,
					TrackingConsent: true,
				}

				body, _ := json.Marshal(request)
				req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/v1/videos/%s/views", video.ID), bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				rr := httptest.NewRecorder()
				router := chi.NewRouter()
				router.Post("/api/v1/videos/{videoId}/views", handler.TrackView)
				router.ServeHTTP(rr, req)

				results <- rr.Code == http.StatusOK
			}(i)

			// Analytics requests
			wg.Add(1)
			go func() {
				defer wg.Done()

				req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/videos/%s/analytics", video.ID), nil)
				rr := httptest.NewRecorder()
				router := chi.NewRouter()
				router.Get("/api/v1/videos/{videoId}/analytics", handler.GetVideoAnalytics)
				router.ServeHTTP(rr, req)

				results <- rr.Code == http.StatusOK
			}()

			// Fingerprint requests
			wg.Add(1)
			go func(index int) {
				defer wg.Done()

				request := map[string]string{
					"ip":         "192.168.1.100",
					"user_agent": fmt.Sprintf("Browser %d", index),
				}

				body, _ := json.Marshal(request)
				req := httptest.NewRequest(http.MethodPost, "/api/v1/views/fingerprint", bytes.NewReader(body))
				req.Header.Set("Content-Type", "application/json")

				rr := httptest.NewRecorder()
				router := chi.NewRouter()
				router.Post("/api/v1/views/fingerprint", handler.GenerateFingerprint)
				router.ServeHTTP(rr, req)

				results <- rr.Code == http.StatusOK
			}(i)
		}

		wg.Wait()
		close(results)

		// Count successes
		successCount := 0
		for success := range results {
			if success {
				successCount++
			}
		}

		// Mixed traffic should not cause rate limiting issues
		assert.Greater(t, successCount, 55, "Mixed request types should not interfere with each other") // Allow small margin for test timing
	})
}

// Helper functions (reuse from views_handlers_test.go)

func createTestViewsRateLimitUser(t *testing.T, testDB *testutil.TestDB) *domain.User {
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

func createTestViewsRateLimitVideo(t *testing.T, testDB *testutil.TestDB, userID string) *domain.Video {
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
	}

	query := `INSERT INTO videos (id, thumbnail_id, title, description, duration, views, privacy, status, 
		upload_date, user_id, file_size, mime_type, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`

	_, err := testDB.DB.Exec(query, video.ID, video.ThumbnailID, video.Title, video.Description,
		video.Duration, video.Views, video.Privacy, video.Status, video.UploadDate, video.UserID,
		video.FileSize, video.MimeType, video.CreatedAt, video.UpdatedAt)
	require.NoError(t, err)

	return video
}

func stringPtrRL(s string) *string {
	return &s
}
