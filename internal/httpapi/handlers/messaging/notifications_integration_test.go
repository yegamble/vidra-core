package messaging

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	jwt "github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/usecase"
)

// generateTestJWT creates a JWT token for testing
func generateTestJWT(secret string, userID string) string {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": userID,
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString([]byte(secret))
	return tokenString
}

func setupTestNotificationEnvironment(t *testing.T) (*sqlx.DB, *chi.Mux, *config.Config) {
	// Skip in short mode (CI load tests)
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	// Setup test database - use environment variable if available (for CI)
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable"
	}

	cfg := &config.Config{
		DatabaseURL: dbURL,
		JWTSecret:   "test-secret-key",
	}

	db, err := sqlx.Connect("postgres", cfg.DatabaseURL)
	if err != nil {
		t.Skipf("Skipping test: Postgres not available (%v)", err)
		return nil, nil, nil
	}

	// Clean up test data
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM notifications")
		_, _ = db.Exec("DELETE FROM subscriptions")
		_, _ = db.Exec("DELETE FROM videos")
		_, _ = db.Exec("DELETE FROM users")
		db.Close()
	})

	// Setup router
	r := chi.NewRouter()

	return db, r, cfg
}

func TestNotificationWorkflow(t *testing.T) {
	db, r, cfg := setupTestNotificationEnvironment(t)

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	videoRepo := repository.NewVideoRepository(db)
	subRepo := repository.NewSubscriptionRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)

	// Initialize services
	notificationService := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)

	// Setup routes
	r.Route("/api/v1/notifications", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		handlers := NewNotificationHandlers(notificationService)
		r.Get("/", handlers.GetNotifications)
		r.Get("/unread-count", handlers.GetUnreadCount)
		r.Put("/{id}/read", handlers.MarkAsRead)
		r.Delete("/{id}", handlers.DeleteNotification)
	})

	// Create test users
	ctx := context.Background()

	// User 1: Channel owner who uploads videos
	channel := &domain.User{
		ID:          uuid.New().String(),
		Username:    "channel_owner",
		Email:       "channel@test.com",
		DisplayName: "Channel Owner",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx" // bcrypt hash
	err := userRepo.Create(ctx, channel, passwordHash)
	require.NoError(t, err)

	// User 2: Subscriber
	subscriber := &domain.User{
		ID:          uuid.New().String(),
		Username:    "subscriber",
		Email:       "subscriber@test.com",
		DisplayName: "Subscriber User",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	err = userRepo.Create(ctx, subscriber, passwordHash)
	require.NoError(t, err)

	// Create subscription
	subscriberUUID, _ := uuid.Parse(subscriber.ID)

	err = subRepo.Subscribe(ctx, subscriber.ID, channel.ID)
	require.NoError(t, err)

	t.Run("Upload video and notify subscriber", func(t *testing.T) {
		// Create a video (simulating upload completion)
		video := &domain.Video{
			ID:            uuid.New().String(),
			ThumbnailID:   uuid.New().String(),
			Title:         "Test Video",
			Description:   "This is a test video",
			Privacy:       domain.PrivacyPublic,
			Status:        domain.StatusCompleted,
			UserID:        channel.ID,
			ThumbnailCID:  "QmTestThumbnailCID",
			FileSize:      1024 * 1024, // 1MB
			ProcessedCIDs: map[string]string{},
			Tags:          []string{"test"},
			Metadata:      domain.VideoMetadata{},
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			UploadDate:    time.Now(),
		}
		err := videoRepo.Create(ctx, video)
		require.NoError(t, err)

		// Trigger notification creation (normally done by encoding service)
		err = notificationService.CreateVideoNotificationForSubscribers(ctx, video, channel.Username)
		require.NoError(t, err)

		// Check subscriber's notifications
		notifications, err := notificationService.GetUserNotifications(ctx, subscriberUUID, domain.NotificationFilter{
			UserID: subscriberUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1)

		notification := notifications[0]
		assert.Equal(t, domain.NotificationNewVideo, notification.Type)
		assert.Equal(t, fmt.Sprintf("New video from %s", channel.Username), notification.Title)
		assert.Contains(t, notification.Message, video.Title)
		assert.False(t, notification.Read)

		// Verify notification data
		assert.Equal(t, video.ID, notification.Data["video_id"])
		assert.Equal(t, channel.ID, notification.Data["channel_id"])
		assert.Equal(t, channel.Username, notification.Data["channel_name"])
		assert.Equal(t, video.Title, notification.Data["video_title"])
		assert.Equal(t, video.ThumbnailCID, notification.Data["thumbnail_cid"])

		// Test API endpoint to get notifications
		token := generateTestJWT(cfg.JWTSecret, subscriber.ID)
		req := httptest.NewRequest("GET", "/api/v1/notifications", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var apiNotifications []domain.Notification
		err = json.Unmarshal(w.Body.Bytes(), &apiNotifications)
		require.NoError(t, err)
		assert.Len(t, apiNotifications, 1)

		// Test unread count
		req = httptest.NewRequest("GET", "/api/v1/notifications/unread-count", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var unreadResponse map[string]int
		err = json.Unmarshal(w.Body.Bytes(), &unreadResponse)
		require.NoError(t, err)
		assert.Equal(t, 1, unreadResponse["unread_count"])

		// Mark notification as read
		req = httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/notifications/%s/read", notification.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		// Verify notification is marked as read
		notifications, err = notificationService.GetUserNotifications(ctx, subscriberUUID, domain.NotificationFilter{
			UserID: subscriberUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.True(t, notifications[0].Read)
		assert.NotNil(t, notifications[0].ReadAt)

		// Test unread count after marking as read
		count, err := notificationService.GetUnreadCount(ctx, subscriberUUID)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("Private video does not create notification", func(t *testing.T) {
		// Create a private video
		privateVideo := &domain.Video{
			ID:            uuid.New().String(),
			ThumbnailID:   uuid.New().String(),
			Title:         "Private Video",
			Description:   "This is a private video",
			Privacy:       domain.PrivacyPrivate, // Private
			Status:        domain.StatusCompleted,
			UserID:        channel.ID,
			ThumbnailCID:  "QmPrivateThumbnailCID",
			FileSize:      1024 * 1024,
			ProcessedCIDs: map[string]string{},
			Tags:          []string{"private"},
			Metadata:      domain.VideoMetadata{},
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			UploadDate:    time.Now(),
		}
		err := videoRepo.Create(ctx, privateVideo)
		require.NoError(t, err)

		// Clear existing notifications
		_, err = db.Exec("DELETE FROM notifications WHERE user_id = $1", subscriber.ID)
		require.NoError(t, err)

		// Try to create notifications (should not create any)
		err = notificationService.CreateVideoNotificationForSubscribers(ctx, privateVideo, channel.Username)
		require.NoError(t, err)

		// Check that no notifications were created
		notifications, err := notificationService.GetUserNotifications(ctx, subscriberUUID, domain.NotificationFilter{
			UserID: subscriberUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 0)
	})

	t.Run("Processing video does not create notification", func(t *testing.T) {
		// Create a video still processing
		processingVideo := &domain.Video{
			ID:            uuid.New().String(),
			ThumbnailID:   uuid.New().String(),
			Title:         "Processing Video",
			Description:   "This video is still processing",
			Privacy:       domain.PrivacyPublic,
			Status:        domain.StatusProcessing, // Still processing
			UserID:        channel.ID,
			ThumbnailCID:  "QmProcessingThumbnailCID",
			FileSize:      1024 * 1024,
			ProcessedCIDs: map[string]string{},
			Tags:          []string{"processing"},
			Metadata:      domain.VideoMetadata{},
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
			UploadDate:    time.Now(),
		}
		err := videoRepo.Create(ctx, processingVideo)
		require.NoError(t, err)

		// Clear existing notifications
		_, err = db.Exec("DELETE FROM notifications WHERE user_id = $1", subscriber.ID)
		require.NoError(t, err)

		// Try to create notifications (should not create any)
		err = notificationService.CreateVideoNotificationForSubscribers(ctx, processingVideo, channel.Username)
		require.NoError(t, err)

		// Check that no notifications were created
		notifications, err := notificationService.GetUserNotifications(ctx, subscriberUUID, domain.NotificationFilter{
			UserID: subscriberUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 0)
	})
}

func TestMultipleSubscribersNotification(t *testing.T) {
	db, _, _ := setupTestNotificationEnvironment(t)

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	videoRepo := repository.NewVideoRepository(db)
	subRepo := repository.NewSubscriptionRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)

	// Initialize services
	notificationService := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)

	ctx := context.Background()

	// Create channel owner
	channel := &domain.User{
		ID:          uuid.New().String(),
		Username:    "popular_channel",
		Email:       "popular@test.com",
		DisplayName: "Popular Channel",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx" // bcrypt hash
	err := userRepo.Create(ctx, channel, passwordHash)
	require.NoError(t, err)

	// Create multiple subscribers
	subscriberIDs := []uuid.UUID{}
	for i := 0; i < 5; i++ {
		subscriber := &domain.User{
			ID:          uuid.New().String(),
			Username:    fmt.Sprintf("subscriber_%d", i),
			Email:       fmt.Sprintf("sub%d@test.com", i),
			DisplayName: fmt.Sprintf("Subscriber %d", i),
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := userRepo.Create(ctx, subscriber, passwordHash)
		require.NoError(t, err)

		subUUID, _ := uuid.Parse(subscriber.ID)
		subscriberIDs = append(subscriberIDs, subUUID)

		// Subscribe to channel
		err = subRepo.Subscribe(ctx, subscriber.ID, channel.ID)
		require.NoError(t, err)
	}

	// Upload a public video
	video := &domain.Video{
		ID:            uuid.New().String(),
		ThumbnailID:   uuid.New().String(),
		Title:         "Popular Video",
		Description:   "This video should notify all subscribers",
		Privacy:       domain.PrivacyPublic,
		Status:        domain.StatusCompleted,
		UserID:        channel.ID,
		ThumbnailCID:  "QmPopularThumbnailCID",
		FileSize:      1024 * 1024,
		ProcessedCIDs: map[string]string{},
		Tags:          []string{"popular"},
		Metadata:      domain.VideoMetadata{},
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
		UploadDate:    time.Now(),
	}
	err = videoRepo.Create(ctx, video)
	require.NoError(t, err)

	// Create notifications for all subscribers
	err = notificationService.CreateVideoNotificationForSubscribers(ctx, video, channel.Username)
	require.NoError(t, err)

	// Verify each subscriber received a notification
	for _, subscriberID := range subscriberIDs {
		notifications, err := notificationService.GetUserNotifications(ctx, subscriberID, domain.NotificationFilter{
			UserID: subscriberID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1, "Subscriber %s should have 1 notification", subscriberID)

		notification := notifications[0]
		assert.Equal(t, domain.NotificationNewVideo, notification.Type)
		assert.Equal(t, video.ID, notification.Data["video_id"])
		assert.False(t, notification.Read)
	}
}
