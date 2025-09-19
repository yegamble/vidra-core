package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// ptr is a helper function to get a pointer to a string
func ptr(s string) *string {
	return &s
}

func TestChannelSubscriptions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	// Setup repositories and services
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	channelService := usecase.NewChannelService(channelRepo, userRepo)
	subRepo := repository.NewSubscriptionRepository(td.DB)
	authRepo := repository.NewAuthRepository(td.DB)

	// Create handlers
	channelHandlers := NewChannelHandlers(channelService, subRepo)

	// Create test server for auth
	s := NewServer(userRepo, authRepo, "test-secret", nil, 0, "", "", 0, nil)

	// Helper function to create authenticated user
	createUser := func(t *testing.T, username, email string) (*domain.User, string) {
		pw := "password123"
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		require.NoError(t, err)

		user := &domain.User{
			ID:        uuid.NewString(),
			Username:  username,
			Email:     email,
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err = userRepo.Create(context.Background(), user, string(hash))
		require.NoError(t, err)

		// Login to get token
		body := map[string]any{"email": email, "password": pw}
		b, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/auth/login", bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		s.Login(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)

		var env integResp
		err = json.NewDecoder(rr.Body).Decode(&env)
		require.NoError(t, err)

		var payload authResp
		err = json.Unmarshal(env.Data, &payload)
		require.NoError(t, err)

		return user, payload.AccessToken
	}

	// Helper to create channel
	createChannel := func(t *testing.T, userID string, handle, name string) *domain.Channel {
		channel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(userID),
			Handle:      handle,
			DisplayName: name,
			Description: ptr("Test channel description"),
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := channelRepo.Create(context.Background(), channel)
		require.NoError(t, err)
		return channel
	}

	t.Run("Subscribe to Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		// Create two users
		user1, _ := createUser(t, "subscriber", "subscriber@test.com")
		user2, _ := createUser(t, "creator", "creator@test.com")

		// Create channel for user2
		channel := createChannel(t, user2.ID, "creator-channel", "Creator's Channel")

		// User1 subscribes to channel
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		rr := httptest.NewRecorder()

		channelHandlers.SubscribeToChannel(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify subscription exists
		response, err := subRepo.ListUserSubscriptions(context.Background(), uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, response.Total)
		assert.Len(t, response.Data, 1)
		assert.Equal(t, channel.ID, response.Data[0].ChannelID)
	})

	t.Run("Cannot Subscribe to Own Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		user, _ := createUser(t, "owner", "owner@test.com")
		channel := createChannel(t, user.ID, "owner-channel", "Owner's Channel")

		// Try to subscribe to own channel
		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user.ID))
		rr := httptest.NewRecorder()

		channelHandlers.SubscribeToChannel(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Unsubscribe from Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		user1, _ := createUser(t, "subscriber", "subscriber@test.com")
		user2, _ := createUser(t, "creator", "creator@test.com")
		channel := createChannel(t, user2.ID, "creator-channel", "Creator's Channel")

		// Subscribe first
		err := subRepo.SubscribeToChannel(context.Background(), uuid.MustParse(user1.ID), channel.ID)
		require.NoError(t, err)

		// Unsubscribe
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		rr := httptest.NewRecorder()

		channelHandlers.UnsubscribeFromChannel(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		// Verify no subscriptions
		response, err := subRepo.ListUserSubscriptions(context.Background(), uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, response.Total)
		assert.Len(t, response.Data, 0)
	})

	t.Run("List Channel Subscribers", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		creator, _ := createUser(t, "creator", "creator@test.com")
		channel := createChannel(t, creator.ID, "popular-channel", "Popular Channel")

		// Create multiple subscribers
		for i := 0; i < 5; i++ {
			user, _ := createUser(t, fmt.Sprintf("subscriber%d", i), fmt.Sprintf("sub%d@test.com", i))
			err := subRepo.SubscribeToChannel(context.Background(), uuid.MustParse(user.ID), channel.ID)
			require.NoError(t, err)
		}

		// Get subscribers
		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channel.ID.String()+"/subscribers?page=1&pageSize=3", nil)
		rr := httptest.NewRecorder()

		channelHandlers.GetChannelSubscribers(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		assert.Equal(t, float64(5), response["total"])
		assert.Equal(t, float64(1), response["page"])
		assert.Equal(t, float64(3), response["pageSize"])

		data, ok := response["data"].([]interface{})
		assert.True(t, ok)
		assert.Len(t, data, 3)
	})

	t.Run("Subscribe to Non-Existent Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		user, _ := createUser(t, "subscriber", "subscriber@test.com")
		fakeChannelID := uuid.NewString()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+fakeChannelID+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user.ID))
		rr := httptest.NewRecorder()

		channelHandlers.SubscribeToChannel(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestChannelSubscriptionFeed_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)
	td.TruncateTables(t, "users", "channels", "subscriptions", "videos", "refresh_tokens")

	// Setup repositories
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	subRepo := repository.NewSubscriptionRepository(td.DB)

	ctx := context.Background()

	// Create users
	subscriber := &domain.User{
		ID:        uuid.NewString(),
		Username:  "subscriber",
		Email:     "subscriber@test.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
	err := userRepo.Create(ctx, subscriber, string(hash))
	require.NoError(t, err)

	// Create multiple content creators with channels
	channels := []*domain.Channel{}
	for i := 0; i < 3; i++ {
		creator := &domain.User{
			ID:        uuid.NewString(),
			Username:  fmt.Sprintf("creator%d", i),
			Email:     fmt.Sprintf("creator%d@test.com", i),
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		err := userRepo.Create(ctx, creator, string(hash))
		require.NoError(t, err)

		channel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(creator.ID),
			Handle:      fmt.Sprintf("channel%d", i),
			DisplayName: fmt.Sprintf("Channel %d", i),
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err = channelRepo.Create(ctx, channel)
		require.NoError(t, err)
		channels = append(channels, channel)

		// Create videos for each channel
		for j := 0; j < 3; j++ {
			video := &domain.Video{
				ID:         uuid.NewString(),
				Title:      fmt.Sprintf("Video %d-%d", i, j),
				UserID:     creator.ID,
				ChannelID:  channel.ID,
				Privacy:    domain.PrivacyPublic,
				Status:     domain.StatusCompleted,
				UploadDate: time.Now().Add(-time.Duration(i*3+j) * time.Hour),
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			}
			err = videoRepo.Create(ctx, video)
			require.NoError(t, err)
		}
	}

	t.Run("Feed Shows Only Subscribed Channel Videos", func(t *testing.T) {
		// Subscribe to first two channels only
		err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[0].ID)
		require.NoError(t, err)
		err = subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[1].ID)
		require.NoError(t, err)

		// Get subscription feed
		videos, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)

		// Should have 6 videos (3 from each of 2 subscribed channels)
		assert.Equal(t, 6, total)
		assert.Len(t, videos, 6)

		// Verify all videos are from subscribed channels
		for _, video := range videos {
			assert.NotEqual(t, uuid.Nil, video.ChannelID)
			assert.True(t, video.ChannelID == channels[0].ID || video.ChannelID == channels[1].ID)
		}

		// Videos should be ordered by published date (newest first)
		for i := 1; i < len(videos); i++ {
			assert.True(t, videos[i-1].UploadDate.After(videos[i].UploadDate) ||
				videos[i-1].UploadDate.Equal(videos[i].UploadDate))
		}
	})

	t.Run("Feed Updates When Subscriptions Change", func(t *testing.T) {
		// Initially subscribed to channels 0 and 1
		videos, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total)

		// Subscribe to channel 2
		err = subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[2].ID)
		require.NoError(t, err)

		// Feed should now have 9 videos
		videos, total, err = subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 9, total)

		// Unsubscribe from channel 0
		err = subRepo.UnsubscribeFromChannel(ctx, uuid.MustParse(subscriber.ID), channels[0].ID)
		require.NoError(t, err)

		// Feed should now have 6 videos (from channels 1 and 2)
		videos, total, err = subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total)

		// Verify videos are from correct channels
		for _, video := range videos {
			assert.NotEqual(t, uuid.Nil, video.ChannelID)
			assert.True(t, video.ChannelID == channels[1].ID || video.ChannelID == channels[2].ID)
		}
	})

	t.Run("Feed Respects Video Privacy", func(t *testing.T) {
		// Create a private video in subscribed channel
		privateVideo := &domain.Video{
			ID:         uuid.NewString(),
			Title:      "Private Video",
			UserID:     channels[1].AccountID.String(),
			ChannelID:  channels[1].ID,
			Privacy:    domain.PrivacyPrivate,
			Status:     domain.StatusCompleted,
			UploadDate: time.Now(),
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		err := videoRepo.Create(ctx, privateVideo)
		require.NoError(t, err)

		// Feed should not include private video
		videos, _, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)

		for _, video := range videos {
			assert.NotEqual(t, privateVideo.ID, video.ID)
			assert.Equal(t, domain.PrivacyPublic, video.Privacy)
		}
	})

	t.Run("Feed Pagination", func(t *testing.T) {
		// Test pagination
		page1, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 3, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total) // Total should remain same
		assert.Len(t, page1, 3)

		page2, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 3, 3)
		require.NoError(t, err)
		assert.Equal(t, 6, total)
		assert.Len(t, page2, 3)

		// Pages should have different videos
		page1IDs := make(map[string]bool)
		for _, v := range page1 {
			page1IDs[v.ID] = true
		}

		for _, v := range page2 {
			assert.False(t, page1IDs[v.ID], "Video %s appears in both pages", v.ID)
		}
	})
}
