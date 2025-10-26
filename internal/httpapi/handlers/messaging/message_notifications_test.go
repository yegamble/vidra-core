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

func TestMessageNotificationWorkflow(t *testing.T) {
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
	require.NoError(t, err)

	// Clean up test data
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM notifications")
		_, _ = db.Exec("DELETE FROM messages")
		_, _ = db.Exec("DELETE FROM users")
		db.Close()
	})

	// Setup router
	r := chi.NewRouter()

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	subRepo := repository.NewSubscriptionRepository(db)

	// Initialize services
	notificationService := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)

	// Setup routes
	r.Route("/api/v1/notifications", func(r chi.Router) {
		r.Use(middleware.Auth(cfg.JWTSecret))
		handlers := NewNotificationHandlers(notificationService)
		r.Get("/", handlers.GetNotifications)
		r.Get("/unread-count", handlers.GetUnreadCount)
		r.Get("/stats", handlers.GetNotificationStats)
		r.Put("/{id}/read", handlers.MarkAsRead)
		r.Delete("/{id}", handlers.DeleteNotification)
	})

	ctx := context.Background()

	t.Run("Message notification workflow", func(t *testing.T) {
		// Create test users
		sender := &domain.User{
			ID:          uuid.New().String(),
			Username:    "message_sender",
			Email:       "sender@test.com",
			DisplayName: "Message Sender",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx"
		err = userRepo.Create(ctx, sender, passwordHash)
		require.NoError(t, err)

		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "message_recipient",
			Email:       "recipient@test.com",
			DisplayName: "Message Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = userRepo.Create(ctx, recipient, passwordHash)
		require.NoError(t, err)

		// Create a message
		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    sender.ID,
			RecipientID: recipient.ID,
			Content:     "Hello! This is a test message.",
			MessageType: "text",
			IsRead:      false,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Insert message directly (simulating message creation)
		_, err = db.ExecContext(ctx, `
			INSERT INTO messages (id, sender_id, recipient_id, content, message_type, is_read, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			message.ID, message.SenderID, message.RecipientID, message.Content,
			message.MessageType, message.IsRead, message.CreatedAt, message.UpdatedAt)
		require.NoError(t, err)

		// The database trigger should have created a notification
		// Let's check recipient's notifications
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1)

		notification := notifications[0]
		assert.Equal(t, domain.NotificationNewMessage, notification.Type)
		assert.Equal(t, fmt.Sprintf("New message from %s", sender.Username), notification.Title)
		assert.Equal(t, message.Content, notification.Message)
		assert.False(t, notification.Read)

		// Verify notification data
		assert.Equal(t, message.ID, notification.Data["message_id"])
		assert.Equal(t, sender.ID, notification.Data["sender_id"])
		assert.Equal(t, sender.Username, notification.Data["sender_name"])
		assert.Equal(t, message.Content, notification.Data["message_preview"])

		// Test API endpoint to get notifications
		token := generateTestJWT(cfg.JWTSecret, recipient.ID)
		req := httptest.NewRequest("GET", "/api/v1/notifications", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var apiNotifications []domain.Notification
		err = json.Unmarshal(w.Body.Bytes(), &apiNotifications)
		require.NoError(t, err)
		assert.Len(t, apiNotifications, 1)
		assert.Equal(t, domain.NotificationNewMessage, apiNotifications[0].Type)

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

		// Test stats endpoint
		req = httptest.NewRequest("GET", "/api/v1/notifications/stats", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		var statsResponse domain.NotificationStats
		err = json.Unmarshal(w.Body.Bytes(), &statsResponse)
		require.NoError(t, err)
		assert.Equal(t, 1, statsResponse.TotalCount)
		assert.Equal(t, 1, statsResponse.UnreadCount)
		assert.Equal(t, 1, statsResponse.ByType[domain.NotificationNewMessage])

		// Mark notification as read
		req = httptest.NewRequest("PUT", fmt.Sprintf("/api/v1/notifications/%s/read", notification.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w = httptest.NewRecorder()

		r.ServeHTTP(w, req)
		assert.Equal(t, http.StatusOK, w.Code)

		// Verify notification is marked as read
		notifications, err = notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.True(t, notifications[0].Read)
		assert.NotNil(t, notifications[0].ReadAt)
	})

	t.Run("Long message preview truncation", func(t *testing.T) {
		// Create test users
		sender := &domain.User{
			ID:          uuid.New().String(),
			Username:    "long_sender",
			Email:       "long_sender@test.com",
			DisplayName: "Long Sender",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx"
		err = userRepo.Create(ctx, sender, passwordHash)
		require.NoError(t, err)

		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "long_recipient",
			Email:       "long_recipient@test.com",
			DisplayName: "Long Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = userRepo.Create(ctx, recipient, passwordHash)
		require.NoError(t, err)

		// Create a message with very long content
		longContent := "This is a very long message that should be truncated in the notification preview. " +
			"It contains more than 100 characters to test the truncation logic. " +
			"The notification should only show the first 100 characters with ellipsis."

		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    sender.ID,
			RecipientID: recipient.ID,
			Content:     longContent,
			MessageType: "text",
			IsRead:      false,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Insert message
		_, err = db.ExecContext(ctx, `
			INSERT INTO messages (id, sender_id, recipient_id, content, message_type, is_read, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			message.ID, message.SenderID, message.RecipientID, message.Content,
			message.MessageType, message.IsRead, message.CreatedAt, message.UpdatedAt)
		require.NoError(t, err)

		// Check notification
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1)

		notification := notifications[0]
		// Check that message preview is truncated
		assert.Equal(t, longContent[:97]+"...", notification.Message)
		assert.Equal(t, longContent[:97]+"...", notification.Data["message_preview"])
	})

	t.Run("System messages don't create notifications", func(t *testing.T) {
		// Create test users
		sender := &domain.User{
			ID:          uuid.New().String(),
			Username:    "system",
			Email:       "system@test.com",
			DisplayName: "System",
			Role:        domain.RoleAdmin,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx"
		err = userRepo.Create(ctx, sender, passwordHash)
		require.NoError(t, err)

		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "system_recipient",
			Email:       "sys_recipient@test.com",
			DisplayName: "System Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = userRepo.Create(ctx, recipient, passwordHash)
		require.NoError(t, err)

		// Create a system message
		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    sender.ID,
			RecipientID: recipient.ID,
			Content:     "This is a system message.",
			MessageType: "system",
			IsRead:      false,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		// Insert system message
		_, err = db.ExecContext(ctx, `
			INSERT INTO messages (id, sender_id, recipient_id, content, message_type, is_read, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
			message.ID, message.SenderID, message.RecipientID, message.Content,
			message.MessageType, message.IsRead, message.CreatedAt, message.UpdatedAt)
		require.NoError(t, err)

		// Check that no notification was created
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 0, "System messages should not create notifications")
	})

	t.Run("Multiple messages create multiple notifications", func(t *testing.T) {
		// Create test users
		sender := &domain.User{
			ID:          uuid.New().String(),
			Username:    "multi_sender",
			Email:       "multi_sender@test.com",
			DisplayName: "Multi Sender",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		passwordHash := "$2a$10$abcdefghijklmnopqrstuvwx"
		err = userRepo.Create(ctx, sender, passwordHash)
		require.NoError(t, err)

		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "multi_recipient",
			Email:       "multi_recipient@test.com",
			DisplayName: "Multi Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = userRepo.Create(ctx, recipient, passwordHash)
		require.NoError(t, err)

		// Create multiple messages
		for i := 0; i < 3; i++ {
			message := &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    sender.ID,
				RecipientID: recipient.ID,
				Content:     fmt.Sprintf("Message %d", i+1),
				MessageType: "text",
				IsRead:      false,
				CreatedAt:   time.Now().Add(time.Duration(i) * time.Second),
				UpdatedAt:   time.Now().Add(time.Duration(i) * time.Second),
			}

			_, err = db.ExecContext(ctx, `
				INSERT INTO messages (id, sender_id, recipient_id, content, message_type, is_read, created_at, updated_at)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				message.ID, message.SenderID, message.RecipientID, message.Content,
				message.MessageType, message.IsRead, message.CreatedAt, message.UpdatedAt)
			require.NoError(t, err)
		}

		// Check that 3 notifications were created
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 3, "Should have 3 notifications for 3 messages")

		// Verify all are message notifications
		for _, notif := range notifications {
			assert.Equal(t, domain.NotificationNewMessage, notif.Type)
			assert.False(t, notif.Read)
		}
	})
}

func TestMessageNotificationService(t *testing.T) {
	// Unit tests for the notification service message methods

	// Skip in short mode
	if testing.Short() {
		t.Skip("Skipping unit test in short mode")
	}

	// Setup test database
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://test_user:test_password@localhost:5433/athena_test?sslmode=disable"
	}

	db, err := sqlx.Connect("postgres", dbURL)
	require.NoError(t, err)
	defer db.Close()

	// Clean up test data
	t.Cleanup(func() {
		_, _ = db.Exec("DELETE FROM notifications")
		_, _ = db.Exec("DELETE FROM users")
	})

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)
	notificationRepo := repository.NewNotificationRepository(db)
	subRepo := repository.NewSubscriptionRepository(db)

	// Initialize service
	notificationService := usecase.NewNotificationService(notificationRepo, subRepo, userRepo)

	ctx := context.Background()

	t.Run("CreateMessageNotification", func(t *testing.T) {
		// Create test users
		sender := &domain.User{
			ID:          uuid.New().String(),
			Username:    "test_sender",
			Email:       "test_sender@test.com",
			DisplayName: "Test Sender",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := userRepo.Create(ctx, sender, "password_hash")
		require.NoError(t, err)

		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "test_recipient",
			Email:       "test_recipient@test.com",
			DisplayName: "Test Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = userRepo.Create(ctx, recipient, "password_hash")
		require.NoError(t, err)

		// Create a message
		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    sender.ID,
			RecipientID: recipient.ID,
			Content:     "Test message content",
			MessageType: "text",
		}

		// Call the service method directly
		err = notificationService.CreateMessageNotification(ctx, message, sender.Username)
		require.NoError(t, err)

		// Verify notification was created
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1)
		assert.Equal(t, domain.NotificationNewMessage, notifications[0].Type)
	})

	t.Run("CreateMessageNotification with unknown sender", func(t *testing.T) {
		// Create only recipient
		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "test_recipient2",
			Email:       "test_recipient2@test.com",
			DisplayName: "Test Recipient 2",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := userRepo.Create(ctx, recipient, "password_hash")
		require.NoError(t, err)

		// Create a message from non-existent sender
		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    uuid.New().String(), // Non-existent sender
			RecipientID: recipient.ID,
			Content:     "Message from unknown",
			MessageType: "text",
		}

		// Call the service method without sender name (should fetch and fail gracefully)
		err = notificationService.CreateMessageNotification(ctx, message, "")
		require.NoError(t, err)

		// Verify notification was created with "Unknown" sender
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 1)
		assert.Equal(t, "New message from Unknown", notifications[0].Title)
	})

	t.Run("System messages don't create notifications", func(t *testing.T) {
		// Create test user
		recipient := &domain.User{
			ID:          uuid.New().String(),
			Username:    "sys_test_recipient",
			Email:       "sys_test_recipient@test.com",
			DisplayName: "Sys Test Recipient",
			Role:        domain.RoleUser,
			IsActive:    true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := userRepo.Create(ctx, recipient, "password_hash")
		require.NoError(t, err)

		// Create a system message
		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    uuid.New().String(),
			RecipientID: recipient.ID,
			Content:     "System message",
			MessageType: "system",
		}

		// Call the service method
		err = notificationService.CreateMessageNotification(ctx, message, "System")
		require.NoError(t, err)

		// Verify no notification was created
		recipientUUID, _ := uuid.Parse(recipient.ID)
		notifications, err := notificationService.GetUserNotifications(ctx, recipientUUID, domain.NotificationFilter{
			UserID: recipientUUID,
			Limit:  10,
		})
		require.NoError(t, err)
		assert.Len(t, notifications, 0)
	})
}
