package channel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestChannelNotifications_Integration tests that notifications properly use channel_id
func TestChannelNotifications_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	// Setup repositories and services
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	subRepo := repository.NewSubscriptionRepository(td.DB)
	notifRepo := repository.NewNotificationRepository(td.DB)
	notifService := usecase.NewNotificationService(notifRepo, subRepo, userRepo)

	ctx := context.Background()

	// Helper to create user with channel
	createUserWithChannel := func(t *testing.T, username, email string) (*domain.User, *domain.Channel) {
		user := &domain.User{
			ID:        uuid.NewString(),
			Username:  username,
			Email:     email,
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		err := userRepo.Create(ctx, user, string(hash))
		require.NoError(t, err)

		channel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(user.ID),
			Handle:      username + "-channel",
			DisplayName: username + "'s Channel",
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = channelRepo.Create(ctx, channel)
		require.NoError(t, err)

		return user, channel
	}

	t.Run("New Video Notification Includes Channel ID", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "videos", "subscriptions", "notifications")

		// Create content creator with channel
		creator, channel := createUserWithChannel(t, "creator", "creator@test.com")

		// Create subscribers
		var subscribers []*domain.User
		for i := 0; i < 3; i++ {
			sub, _ := createUserWithChannel(t, fmt.Sprintf("subscriber%d", i), fmt.Sprintf("sub%d@test.com", i))
			subscribers = append(subscribers, sub)

			// Subscribe to the channel
			err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(sub.ID), channel.ID)
			require.NoError(t, err)
		}

		// Create a new public video
		video := &domain.Video{
			ID:          uuid.NewString(),
			Title:       "New Video",
			Description: "Test video description",
			UserID:      creator.ID,
			ChannelID:   channel.ID,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now(),
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := videoRepo.Create(ctx, video)
		require.NoError(t, err)

		// Trigger notifications (this would normally be done by a database trigger)
		// For testing, we'll create notifications manually as the trigger would
		for _, subscriber := range subscribers {
			notif := &domain.Notification{
				ID:        uuid.New(),
				UserID:    uuid.MustParse(subscriber.ID),
				Type:      domain.NotificationNewVideo,
				Read:      false,
				CreatedAt: time.Now(),
				Data: map[string]interface{}{
					"video_id":     video.ID,
					"video_title":  video.Title,
					"channel_id":   channel.ID.String(),
					"channel_name": channel.DisplayName,
					"creator_id":   creator.ID,
					"creator_name": creator.Username,
				},
			}
			err := notifRepo.Create(ctx, notif)
			require.NoError(t, err)
		}

		// Verify notifications were created with channel_id
		for _, subscriber := range subscribers {
			filter := domain.NotificationFilter{
				UserID: uuid.MustParse(subscriber.ID),
				Limit:  10,
				Offset: 0,
			}
			notifs, err := notifRepo.ListByUser(ctx, filter)
			require.NoError(t, err)
			assert.Len(t, notifs, 1)

			notif := notifs[0]
			assert.Equal(t, domain.NotificationNewVideo, notif.Type)
			assert.False(t, notif.Read)

			// Check data contains channel information
			data := notif.Data
			assert.Equal(t, channel.ID.String(), data["channel_id"])
			assert.Equal(t, channel.DisplayName, data["channel_name"])
			assert.Equal(t, video.ID, data["video_id"])
			assert.Equal(t, video.Title, data["video_title"])
		}
	})

	t.Run("Channel Subscription Notification", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "notifications")

		// Create channel owner and subscriber
		owner, channel := createUserWithChannel(t, "channel_owner", "owner@test.com")
		subscriber, _ := createUserWithChannel(t, "new_subscriber", "subscriber@test.com")

		// Subscribe to channel
		err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
		require.NoError(t, err)

		// Create notification for channel owner about new subscriber
		notif := &domain.Notification{
			ID:        uuid.New(),
			UserID:    uuid.MustParse(owner.ID),
			Type:      domain.NotificationNewSubscriber,
			Read:      false,
			CreatedAt: time.Now(),
			Data: map[string]interface{}{
				"subscriber_id":   subscriber.ID,
				"subscriber_name": subscriber.Username,
				"channel_id":      channel.ID.String(),
				"channel_name":    channel.DisplayName,
			},
		}
		err = notifRepo.Create(ctx, notif)
		require.NoError(t, err)

		// Verify notification
		filter := domain.NotificationFilter{
			UserID: uuid.MustParse(owner.ID),
			Limit:  10,
			Offset: 0,
		}
		notifs, err := notifRepo.ListByUser(ctx, filter)
		require.NoError(t, err)
		require.NotEmpty(t, notifs)

		var matched map[string]interface{}
		for i := range notifs {
			data := notifs[i].Data
			if data["channel_id"] == channel.ID.String() && data["subscriber_name"] == subscriber.Username {
				matched = data
				break
			}
		}
		require.NotNil(t, matched)
		assert.Equal(t, channel.ID.String(), matched["channel_id"])
		assert.Equal(t, subscriber.Username, matched["subscriber_name"])
	})

	t.Run("Multiple Channel Notifications", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "videos", "subscriptions", "notifications")

		subscriber, _ := createUserWithChannel(t, "multi_subscriber", "multi@test.com")

		// Create multiple creators with channels
		channels := make([]*domain.Channel, 0)
		for i := 0; i < 3; i++ {
			creator, channel := createUserWithChannel(t, fmt.Sprintf("creator%d", i), fmt.Sprintf("creator%d@test.com", i))
			channels = append(channels, channel)

			// Subscribe to each channel
			err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
			require.NoError(t, err)

			// Each channel publishes a video
			video := &domain.Video{
				ID:         uuid.NewString(),
				Title:      fmt.Sprintf("Video from Channel %d", i),
				UserID:     creator.ID,
				ChannelID:  channel.ID,
				Privacy:    domain.PrivacyPublic,
				Status:     domain.StatusCompleted,
				UploadDate: time.Now().Add(-time.Duration(i) * time.Minute),
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}
			err = videoRepo.Create(ctx, video)
			require.NoError(t, err)

			// Create notification
			notif := &domain.Notification{
				ID:        uuid.New(),
				UserID:    uuid.MustParse(subscriber.ID),
				Type:      domain.NotificationNewVideo,
				Read:      false,
				CreatedAt: time.Now().Add(-time.Duration(i) * time.Minute),
				Data: map[string]interface{}{
					"video_id":     video.ID,
					"video_title":  video.Title,
					"channel_id":   channel.ID.String(),
					"channel_name": channel.DisplayName,
				},
			}
			err = notifRepo.Create(ctx, notif)
			require.NoError(t, err)
		}

		// Get all notifications
		filter := domain.NotificationFilter{
			UserID: uuid.MustParse(subscriber.ID),
			Limit:  100,
			Offset: 0,
		}
		notifs, err := notifRepo.ListByUser(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, notifs, 3)

		// Verify each notification has correct channel_id
		channelIDs := make(map[string]bool)
		for _, notif := range notifs {
			data := notif.Data
			channelID := data["channel_id"].(string)
			channelIDs[channelID] = true

			// Verify channel_id is valid
			found := false
			for _, ch := range channels {
				if ch.ID.String() == channelID {
					found = true
					break
				}
			}
			assert.True(t, found, "Notification has invalid channel_id: %s", channelID)
		}

		// Should have 3 unique channel IDs
		assert.Len(t, channelIDs, 3)
	})

	t.Run("Notification Handler Returns Channel Info", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "notifications")

		user, channel := createUserWithChannel(t, "test_user", "test@test.com")

		// Create a notification with channel info
		notif := &domain.Notification{
			ID:        uuid.New(),
			UserID:    uuid.MustParse(user.ID),
			Type:      domain.NotificationNewVideo,
			Read:      false,
			CreatedAt: time.Now(),
			Data: map[string]interface{}{
				"video_id":     uuid.NewString(),
				"video_title":  "Test Video",
				"channel_id":   channel.ID.String(),
				"channel_name": channel.DisplayName,
			},
		}
		err := notifRepo.Create(ctx, notif)
		require.NoError(t, err)

		// Create handler
		handler := NewNotificationHandlers(notifService)

		// Get notifications
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user.ID))
		rr := httptest.NewRecorder()

		handler.GetNotifications(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err = json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		data := response["data"].([]interface{})
		assert.Len(t, data, 1)

		notifData := data[0].(map[string]interface{})
		notifContent := notifData["data"].(map[string]interface{})
		assert.Equal(t, channel.ID.String(), notifContent["channel_id"])
		assert.Equal(t, channel.DisplayName, notifContent["channel_name"])
	})

	t.Run("Private Video No Notifications", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "videos", "subscriptions", "notifications")

		creator, channel := createUserWithChannel(t, "creator", "creator@test.com")
		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Subscribe to channel
		err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
		require.NoError(t, err)

		// Create a private video
		video := &domain.Video{
			ID:         uuid.NewString(),
			Title:      "Private Video",
			UserID:     creator.ID,
			ChannelID:  channel.ID,
			Privacy:    domain.PrivacyPrivate, // Private video
			Status:     domain.StatusCompleted,
			UploadDate: time.Now(),
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		err = videoRepo.Create(ctx, video)
		require.NoError(t, err)

		// No notification should be created for private videos
		// (the trigger should handle this, but for testing we verify no notification exists)
		filter := domain.NotificationFilter{
			UserID: uuid.MustParse(subscriber.ID),
			Limit:  10,
			Offset: 0,
		}
		notifs, err := notifRepo.ListByUser(ctx, filter)
		require.NoError(t, err)
		assert.Len(t, notifs, 0)
	})

	t.Run("Notification Statistics Include Channel Info", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "notifications")

		user, _ := createUserWithChannel(t, "stats_user", "stats@test.com")

		// Create notifications from different channels
		for i := 0; i < 5; i++ {
			channelID := uuid.New()
			notif := &domain.Notification{
				ID:        uuid.New(),
				UserID:    uuid.MustParse(user.ID),
				Type:      domain.NotificationNewVideo,
				Read:      i < 2, // First 2 are read
				CreatedAt: time.Now().Add(-time.Duration(i) * time.Hour),
				Data: map[string]interface{}{
					"channel_id":   channelID.String(),
					"channel_name": fmt.Sprintf("Channel %d", i),
					"video_title":  fmt.Sprintf("Video %d", i),
				},
			}
			err := notifRepo.Create(ctx, notif)
			require.NoError(t, err)
		}

		// Get statistics
		handler := NewNotificationHandlers(notifService)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications/stats", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user.ID))
		rr := httptest.NewRecorder()

		handler.GetNotificationStats(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var resp struct {
			Data struct {
				TotalCount  int            `json:"total_count"`
				UnreadCount int            `json:"unread_count"`
				ByType      map[string]int `json:"by_type"`
			} `json:"data"`
		}
		err := json.NewDecoder(rr.Body).Decode(&resp)
		require.NoError(t, err)

		// Verify statistics
		assert.Equal(t, 5, resp.Data.TotalCount)
		assert.Equal(t, 3, resp.Data.UnreadCount)
		assert.Equal(t, 2, resp.Data.TotalCount-resp.Data.UnreadCount)

		// Type breakdown should show new_video notifications
		assert.Equal(t, 5, resp.Data.ByType["new_video"])
	})
}
