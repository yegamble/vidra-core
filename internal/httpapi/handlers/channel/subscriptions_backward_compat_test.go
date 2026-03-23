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

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

// TestSubscriptionsBackwardCompatibility tests that user-based subscription endpoints
// still work correctly by using the default channel under the hood
func TestSubscriptionsBackwardCompatibility_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	// Setup repositories and services
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	subRepo := repository.NewSubscriptionRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)

	ctx := context.Background()

	// Helper to create user with default channel.
	// userRepo.Create already creates a default channel (handle = username),
	// so we retrieve it instead of creating a duplicate.
	createUserWithChannel := func(t *testing.T, username, email string) (*domain.User, *domain.Channel) {
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
		err = userRepo.Create(ctx, user, string(hash))
		require.NoError(t, err)

		// Retrieve the default channel auto-created by userRepo.Create
		channel, err := channelRepo.GetByHandle(ctx, username)
		require.NoError(t, err)

		return user, channel
	}

	t.Run("Subscribe Using User ID (Deprecated)", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		// Create two users with default channels
		user1, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")
		user2, channel2 := createUserWithChannel(t, "creator", "creator@test.com")

		// Create handler for deprecated user subscription
		handler := SubscribeToUserHandler(subRepo, userRepo)

		// Subscribe using deprecated user endpoint
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+user2.ID+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		req = withChannelParam(req, "id", user2.ID)
		rr := httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusNoContent, rr.Code)

		// Verify subscription exists on the channel
		response, err := subRepo.ListUserSubscriptions(ctx, uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, response.Total)
		assert.Len(t, response.Data, 1)
		assert.Equal(t, channel2.ID, response.Data[0].ChannelID)
	})

	t.Run("Unsubscribe Using User ID (Deprecated)", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		user1, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")
		user2, channel2 := createUserWithChannel(t, "creator", "creator@test.com")

		// First subscribe to the channel
		err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(user1.ID), channel2.ID)
		require.NoError(t, err)

		// Unsubscribe using deprecated user endpoint
		handler := UnsubscribeFromUserHandler(subRepo)
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/users/"+user2.ID+"/unsubscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		req = withChannelParam(req, "id", user2.ID)
		rr := httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusNoContent, rr.Code)

		// Verify no subscriptions exist
		response, err := subRepo.ListUserSubscriptions(ctx, uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, response.Total)
		assert.Len(t, response.Data, 0)
	})

	t.Run("List My Subscriptions Returns Channels", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Create multiple creators with channels
		for i := 0; i < 3; i++ {
			_, channel := createUserWithChannel(t, fmt.Sprintf("creator%d", i), fmt.Sprintf("creator%d@test.com", i))

			// Subscribe to each channel
			err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
			require.NoError(t, err)
		}

		// Use deprecated ListMySubscriptions endpoint
		handler := ListMySubscriptionsHandler(subRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/subscriptions", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		rr := httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response Response
		err := json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		// Should return channel information (backward compatible)
		assert.NotNil(t, response.Data)
		assert.NotNil(t, response.Meta)
		assert.Equal(t, int64(3), response.Meta.Total)
	})

	t.Run("Subscription Videos Feed Works", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Create creators with videos
		for i := 0; i < 2; i++ {
			creator, channel := createUserWithChannel(t, fmt.Sprintf("creator%d", i), fmt.Sprintf("creator%d@test.com", i))

			// Subscribe to channel
			err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
			require.NoError(t, err)

			// Create videos
			for j := 0; j < 2; j++ {
				video := &domain.Video{
					ID:         uuid.NewString(),
					Title:      fmt.Sprintf("Video %d-%d", i, j),
					UserID:     creator.ID,
					ChannelID:  channel.ID,
					Privacy:    domain.PrivacyPublic,
					Status:     domain.StatusCompleted,
					UploadDate: time.Now().Add(-time.Duration(j) * time.Hour),
					CreatedAt:  time.Now(),
					UpdatedAt:  time.Now(),
				}
				err := videoRepo.Create(ctx, video)
				require.NoError(t, err)
			}
		}

		// Get subscription videos feed
		handler := ListSubscriptionVideosHandler(subRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/subscriptions", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		rr := httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response Response
		err := json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		assert.NotNil(t, response.Meta)
		assert.Equal(t, int64(4), response.Meta.Total) // 2 creators * 2 videos each

		// Parse videos from response
		var videos []*domain.Video
		videosBytes, err := json.Marshal(response.Data)
		require.NoError(t, err)
		err = json.Unmarshal(videosBytes, &videos)
		require.NoError(t, err)

		assert.Len(t, videos, 4)
		// All videos should have channel IDs
		for _, v := range videos {
			assert.NotEqual(t, uuid.Nil, v.ChannelID)
		}
	})

	t.Run("Mixed Subscriptions Work", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Create user with default channel
		user1, defaultChannel := createUserWithChannel(t, "user1", "user1@test.com")

		// Create user with additional non-default channel
		user2, _ := createUserWithChannel(t, "user2", "user2@test.com")
		additionalChannel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(user2.ID),
			Handle:      "user2-extra",
			DisplayName: "User2 Extra Channel",
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := channelRepo.Create(ctx, additionalChannel)
		require.NoError(t, err)

		// Subscribe to user1 via deprecated endpoint (should subscribe to default channel)
		handler := SubscribeToUserHandler(subRepo, userRepo)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+user1.ID+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		req = withChannelParam(req, "id", user1.ID)
		rr := httptest.NewRecorder()
		handler(rr, req)
		assert.Equal(t, http.StatusNoContent, rr.Code)

		// Subscribe to user2's additional channel directly
		err = subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), additionalChannel.ID)
		require.NoError(t, err)

		// List all subscriptions
		response, err := subRepo.ListUserSubscriptions(ctx, uuid.MustParse(subscriber.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 2, response.Total)
		assert.Len(t, response.Data, 2)

		// Verify we have both channels
		channelIDs := map[uuid.UUID]bool{}
		for _, sub := range response.Data {
			channelIDs[sub.ChannelID] = true
		}
		assert.True(t, channelIDs[defaultChannel.ID])
		assert.True(t, channelIDs[additionalChannel.ID])
	})

	t.Run("User Without Default Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		// Create subscriber with default channel
		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Create user, then delete its auto-created default channel to simulate edge case
		userWithoutChannel := &domain.User{
			ID:        uuid.NewString(),
			Username:  "no-channel",
			Email:     "no-channel@test.com",
			Role:      domain.RoleUser,
			IsActive:  true,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}
		hash, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.DefaultCost)
		err := userRepo.Create(ctx, userWithoutChannel, string(hash))
		require.NoError(t, err)

		// Remove the auto-created channel so the user has no default channel
		autoChannel, chErr := channelRepo.GetByHandle(ctx, "no-channel")
		if chErr == nil {
			_ = channelRepo.Delete(ctx, autoChannel.ID)
		}

		// Try to subscribe to user without default channel
		handler := SubscribeToUserHandler(subRepo, userRepo)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/users/"+userWithoutChannel.ID+"/subscribe", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		req = withChannelParam(req, "id", userWithoutChannel.ID)
		rr := httptest.NewRecorder()

		handler(rr, req)

		// Should fail gracefully (user exists but no default channel)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("Pagination With Backward Compatible Endpoints", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "videos")

		subscriber, _ := createUserWithChannel(t, "subscriber", "subscriber@test.com")

		// Create many creators and subscribe
		for i := 0; i < 10; i++ {
			_, channel := createUserWithChannel(t, fmt.Sprintf("creator%d", i), fmt.Sprintf("creator%d@test.com", i))
			err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channel.ID)
			require.NoError(t, err)
		}

		// Test page/pageSize parameters
		handler := ListMySubscriptionsHandler(subRepo)
		req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/subscriptions?page=2&pageSize=3", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		rr := httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var response Response
		err := json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		assert.NotNil(t, response.Meta)
		assert.Equal(t, int64(10), response.Meta.Total)
		assert.Equal(t, 2, response.Meta.Page)
		assert.Equal(t, 3, response.Meta.PageSize)

		// Test limit/offset fallback
		req = httptest.NewRequest(http.MethodGet, "/api/v1/users/me/subscriptions?limit=5&offset=5", nil)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, subscriber.ID))
		rr = httptest.NewRecorder()

		handler(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		err = json.NewDecoder(rr.Body).Decode(&response)
		require.NoError(t, err)

		assert.NotNil(t, response.Meta)
		assert.Equal(t, int64(10), response.Meta.Total)
		assert.Equal(t, 5, response.Meta.Limit)
		assert.Equal(t, 5, response.Meta.Offset)
	})
}
