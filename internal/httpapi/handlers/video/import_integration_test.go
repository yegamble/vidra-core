//go:build integration

package video

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/importer"
	"vidra-core/internal/repository"
	importuc "vidra-core/internal/usecase/import"

	"github.com/go-chi/chi/v5"
	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestImportIntegration_EndToEnd tests the complete import flow with real database
// Run with: go test -v -tags=integration ./internal/httpapi -run TestImportIntegration
func TestImportIntegration_EndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test database connection
	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://test_user:test_password@localhost:5433/vidra_test?sslmode=disable"
	}

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err, "Failed to connect to test database")
	defer db.Close()

	// Create test storage directory
	storageDir := "/tmp/vidra-integration-test"
	require.NoError(t, os.MkdirAll(storageDir+"/imports", 0750))
	defer os.RemoveAll(storageDir)

	// Initialize repositories
	importRepo := repository.NewImportRepository(db)
	videoRepo := repository.NewVideoRepository(db)
	encodingRepo := repository.NewEncodingRepository(db)

	// Initialize yt-dlp wrapper (will use mock or skip if yt-dlp not available)
	ytdlp := importer.NewYtDlp("yt-dlp", storageDir+"/imports")

	// Check if yt-dlp is available
	if err := importer.CheckAvailability("yt-dlp"); err != nil {
		t.Skip("yt-dlp not available, skipping integration test")
	}

	// Create config
	cfg := &config.Config{
		StorageDir: storageDir,
	}

	// Initialize service
	importService := importuc.NewService(
		importRepo,
		videoRepo,
		encodingRepo,
		ytdlp,
		cfg,
		storageDir,
	)

	// Initialize handlers
	handlers := NewImportHandlers(importService)

	// Create test user and channel
	userID := "test-user-" + time.Now().Format("20060102150405")
	channelID := "test-channel-" + time.Now().Format("20060102150405")

	// Clean up test data at the end
	defer func() {
		_, _ = db.Exec("DELETE FROM video_imports WHERE user_id = $1", userID)
		_, _ = db.Exec("DELETE FROM videos WHERE user_id = $1", userID)
	}()

	t.Run("Create import successfully", func(t *testing.T) {
		// Note: Using a test URL that won't actually download
		// In a real integration test, you'd use a small, fast test video
		reqBody := CreateImportRequest{
			SourceURL:     "https://www.youtube.com/watch?v=jNQXAC9IVRw", // "Me at the zoo" - first YouTube video, very short
			ChannelID:     &channelID,
			TargetPrivacy: "private",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))
		w := httptest.NewRecorder()

		handlers.CreateImport(w, req)

		require.Equal(t, http.StatusCreated, w.Code)

		var resp ImportResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)

		assert.NotEmpty(t, resp.ID)
		assert.Equal(t, reqBody.SourceURL, resp.SourceURL)
		assert.Equal(t, "pending", resp.Status)
		assert.Equal(t, "private", resp.TargetPrivacy)
		assert.Equal(t, "youtube", resp.SourcePlatform)

		importID := resp.ID

		// Wait a moment for background processing to potentially start
		time.Sleep(100 * time.Millisecond)

		t.Run("Get import status", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/videos/imports/"+importID, nil)
			req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", importID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			handlers.GetImport(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var getResp ImportResponse
			err := json.NewDecoder(w.Body).Decode(&getResp)
			require.NoError(t, err)

			assert.Equal(t, importID, getResp.ID)
			assert.Equal(t, reqBody.SourceURL, getResp.SourceURL)
			assert.NotEmpty(t, getResp.Status)
		})

		t.Run("List user imports", func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/v1/videos/imports", nil)
			req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))
			w := httptest.NewRecorder()

			handlers.ListImports(w, req)

			require.Equal(t, http.StatusOK, w.Code)

			var listResp ImportListResponse
			err := json.NewDecoder(w.Body).Decode(&listResp)
			require.NoError(t, err)

			assert.GreaterOrEqual(t, len(listResp.Imports), 1)
			assert.GreaterOrEqual(t, listResp.TotalCount, 1)

			// Find our import
			found := false
			for _, imp := range listResp.Imports {
				if imp.ID == importID {
					found = true
					break
				}
			}
			assert.True(t, found, "Created import should be in list")
		})

		t.Run("Cancel import", func(t *testing.T) {
			req := httptest.NewRequest("DELETE", "/api/v1/videos/imports/"+importID, nil)
			req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", importID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			handlers.CancelImport(w, req)

			// Should succeed unless already in terminal state
			if w.Code != http.StatusNoContent && w.Code != http.StatusInternalServerError {
				t.Errorf("Expected 204 or 500, got %d", w.Code)
			}

			// Verify status changed (if not already completed)
			if w.Code == http.StatusNoContent {
				req := httptest.NewRequest("GET", "/api/v1/videos/imports/"+importID, nil)
				req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))

				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("id", importID)
				req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

				w := httptest.NewRecorder()
				handlers.GetImport(w, req)

				if w.Code == http.StatusOK {
					var getResp ImportResponse
					_ = json.NewDecoder(w.Body).Decode(&getResp)
					assert.Contains(t, []string{"cancelled", "completed", "failed"}, getResp.Status)
				}
			}
		})
	})

	t.Run("Quota enforcement", func(t *testing.T) {
		// Create imports up to the daily quota
		testUserID := userID + "-quota"
		defer func() {
			_, _ = db.Exec("DELETE FROM video_imports WHERE user_id = $1", testUserID)
		}()

		// Manually insert imports to reach quota
		for i := 0; i < 100; i++ {
			_, err := db.Exec(`
				INSERT INTO video_imports (user_id, source_url, status, target_privacy, created_at, updated_at)
				VALUES ($1, $2, $3, $4, NOW(), NOW())
			`, testUserID, fmt.Sprintf("https://example.com/video%d", i), domain.ImportStatusCompleted, "private")
			require.NoError(t, err)
		}

		// Try to create one more - should fail with quota exceeded
		reqBody := CreateImportRequest{
			SourceURL:     "https://www.youtube.com/watch?v=test",
			TargetPrivacy: "private",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), "user_id", testUserID))
		w := httptest.NewRecorder()

		handlers.CreateImport(w, req)

		assert.Equal(t, http.StatusTooManyRequests, w.Code)

		var errResp ErrorResponse
		err := json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "quota exceeded")
	})

	t.Run("Concurrent rate limiting", func(t *testing.T) {
		testUserID := userID + "-ratelimit"
		defer func() {
			_, _ = db.Exec("DELETE FROM video_imports WHERE user_id = $1", testUserID)
		}()

		// Create 5 imports in downloading state
		for i := 0; i < 5; i++ {
			_, err := db.Exec(`
				INSERT INTO video_imports (user_id, source_url, status, target_privacy, created_at, updated_at)
				VALUES ($1, $2, $3, $4, NOW(), NOW())
			`, testUserID, fmt.Sprintf("https://example.com/video%d", i), domain.ImportStatusDownloading, "private")
			require.NoError(t, err)
		}

		// Try to create one more - should fail with rate limit
		reqBody := CreateImportRequest{
			SourceURL:     "https://www.youtube.com/watch?v=test",
			TargetPrivacy: "private",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), "user_id", testUserID))
		w := httptest.NewRecorder()

		handlers.CreateImport(w, req)

		assert.Equal(t, http.StatusTooManyRequests, w.Code)

		var errResp ErrorResponse
		err := json.NewDecoder(w.Body).Decode(&errResp)
		require.NoError(t, err)
		assert.Contains(t, errResp.Message, "concurrent imports")
	})

	t.Run("Unauthorized access", func(t *testing.T) {
		// Create import for one user
		reqBody := CreateImportRequest{
			SourceURL:     "https://www.youtube.com/watch?v=test",
			TargetPrivacy: "private",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest("POST", "/api/v1/videos/imports", bytes.NewReader(body))
		req = req.WithContext(context.WithValue(req.Context(), "user_id", userID))
		w := httptest.NewRecorder()

		handlers.CreateImport(w, req)
		require.Equal(t, http.StatusCreated, w.Code)

		var resp ImportResponse
		err := json.NewDecoder(w.Body).Decode(&resp)
		require.NoError(t, err)
		importID := resp.ID

		// Try to access with different user
		otherUserID := userID + "-other"
		req = httptest.NewRequest("GET", "/api/v1/videos/imports/"+importID, nil)
		req = req.WithContext(context.WithValue(req.Context(), "user_id", otherUserID))

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", importID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w = httptest.NewRecorder()
		handlers.GetImport(w, req)

		// Should fail with unauthorized or not found
		assert.NotEqual(t, http.StatusOK, w.Code)
	})
}

// TestImportIntegration_DatabaseOperations tests database-level operations
func TestImportIntegration_DatabaseOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	dbURL := os.Getenv("TEST_DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://test_user:test_password@localhost:5433/vidra_test?sslmode=disable"
	}

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer db.Close()

	importRepo := repository.NewImportRepository(db)
	ctx := context.Background()

	userID := "db-test-user-" + time.Now().Format("20060102150405")
	defer func() {
		_, _ = db.Exec("DELETE FROM video_imports WHERE user_id = $1", userID)
	}()

	t.Run("Full CRUD lifecycle", func(t *testing.T) {
		// Create
		imp := &domain.VideoImport{
			UserID:        userID,
			SourceURL:     "https://youtube.com/watch?v=test",
			Status:        domain.ImportStatusPending,
			TargetPrivacy: "private",
		}

		err := importRepo.Create(ctx, imp)
		require.NoError(t, err)
		assert.NotEmpty(t, imp.ID)

		importID := imp.ID

		// Read
		retrieved, err := importRepo.GetByID(ctx, importID)
		require.NoError(t, err)
		assert.Equal(t, userID, retrieved.UserID)
		assert.Equal(t, domain.ImportStatusPending, retrieved.Status)

		// Update progress
		err = importRepo.UpdateProgress(ctx, importID, 50, 50000000)
		require.NoError(t, err)

		retrieved, err = importRepo.GetByID(ctx, importID)
		require.NoError(t, err)
		assert.Equal(t, 50, retrieved.Progress)
		assert.Equal(t, int64(50000000), retrieved.DownloadedBytes)

		// Mark completed
		err = importRepo.MarkCompleted(ctx, importID, "video-123")
		require.NoError(t, err)

		retrieved, err = importRepo.GetByID(ctx, importID)
		require.NoError(t, err)
		assert.Equal(t, domain.ImportStatusCompleted, retrieved.Status)
		assert.Equal(t, "video-123", *retrieved.VideoID)

		// List by user
		imports, err := importRepo.GetByUserID(ctx, userID, 10, 0)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(imports), 1)

		// Count
		count, err := importRepo.CountByUserID(ctx, userID)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, count, 1)
	})

	t.Run("Quota checks", func(t *testing.T) {
		testUserID := userID + "-quota-check"
		defer func() {
			_, _ = db.Exec("DELETE FROM video_imports WHERE user_id = $1", testUserID)
		}()

		// Create some imports today
		for i := 0; i < 5; i++ {
			imp := &domain.VideoImport{
				UserID:        testUserID,
				SourceURL:     fmt.Sprintf("https://example.com/%d", i),
				Status:        domain.ImportStatusCompleted,
				TargetPrivacy: "private",
			}
			err := importRepo.Create(ctx, imp)
			require.NoError(t, err)
		}

		// Check today's count
		todayCount, err := importRepo.CountByUserIDToday(ctx, testUserID)
		require.NoError(t, err)
		assert.Equal(t, 5, todayCount)

		// Check status count
		statusCount, err := importRepo.CountByUserIDAndStatus(ctx, testUserID, domain.ImportStatusCompleted)
		require.NoError(t, err)
		assert.Equal(t, 5, statusCount)
	})
}
