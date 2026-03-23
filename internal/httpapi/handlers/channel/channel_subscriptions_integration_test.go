package channel

import (
	"bytes"
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

func ptr(s string) *string {
	return &s
}

func TestChannelSubscriptions_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	channelService := usecase.NewChannelService(channelRepo, userRepo, nil)
	subRepo := repository.NewSubscriptionRepository(td.DB)
	authRepo := repository.NewAuthRepository(td.DB)

	channelHandlers := NewChannelHandlers(channelService, subRepo)

	s := NewServer(userRepo, authRepo, "test-secret", nil, 0, "", "", 0, nil)

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
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens", "notifications")

		_, err := td.DB.Exec(`ALTER TABLE notifications ALTER COLUMN title SET NOT NULL`)
		require.NoError(t, err)
		t.Cleanup(func() {
			_, _ = td.DB.Exec(`ALTER TABLE notifications ALTER COLUMN title DROP NOT NULL`)
		})

		user1, _ := createUser(t, "subscriber", "subscriber@test.com")
		user2, _ := createUser(t, "creator", "creator@test.com")

		channel := createChannel(t, user2.ID, "creator-channel", "Creator's Channel")

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = withChannelParam(req, "id", channel.ID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		rr := httptest.NewRecorder()

		channelHandlers.SubscribeToChannel(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		response, err := subRepo.ListUserSubscriptions(context.Background(), uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, response.Total)
		assert.Len(t, response.Data, 1)
		assert.Equal(t, channel.ID, response.Data[0].ChannelID)

		var notif struct {
			Title     string `db:"title"`
			Type      string `db:"type"`
			ChannelID string `db:"channel_id"`
		}
		err = td.DB.Get(&notif, `
			SELECT title, type, data->>'channel_id' AS channel_id
			FROM notifications
			WHERE user_id = $1
			ORDER BY created_at DESC
			LIMIT 1
		`, uuid.MustParse(user2.ID))
		require.NoError(t, err)
		assert.Equal(t, "new_subscriber", notif.Type)
		assert.NotEmpty(t, notif.Title)
		assert.Equal(t, channel.ID.String(), notif.ChannelID)
	})

	t.Run("Cannot Subscribe to Own Channel", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		user, _ := createUser(t, "owner", "owner@test.com")
		channel := createChannel(t, user.ID, "owner-channel", "Owner's Channel")

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = withChannelParam(req, "id", channel.ID.String())
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

		err := subRepo.SubscribeToChannel(context.Background(), uuid.MustParse(user1.ID), channel.ID)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodDelete, "/api/v1/channels/"+channel.ID.String()+"/subscribe", nil)
		req = withChannelParam(req, "id", channel.ID.String())
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, user1.ID))
		rr := httptest.NewRecorder()

		channelHandlers.UnsubscribeFromChannel(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		response, err := subRepo.ListUserSubscriptions(context.Background(), uuid.MustParse(user1.ID), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, response.Total)
		assert.Len(t, response.Data, 0)
	})

	t.Run("List Channel Subscribers", func(t *testing.T) {
		td.TruncateTables(t, "users", "channels", "subscriptions", "refresh_tokens")

		creator, _ := createUser(t, "creator", "creator@test.com")
		channel := createChannel(t, creator.ID, "popular-channel", "Popular Channel")

		for i := 0; i < 5; i++ {
			user, _ := createUser(t, fmt.Sprintf("subscriber%d", i), fmt.Sprintf("sub%d@test.com", i))
			err := subRepo.SubscribeToChannel(context.Background(), uuid.MustParse(user.ID), channel.ID)
			require.NoError(t, err)
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/"+channel.ID.String()+"/subscribers?page=1&pageSize=3", nil)
		req = withChannelParam(req, "id", channel.ID.String())
		rr := httptest.NewRecorder()

		channelHandlers.GetChannelSubscribers(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var env integResp
		err := json.NewDecoder(rr.Body).Decode(&env)
		require.NoError(t, err)
		require.True(t, env.Success)

		var response map[string]interface{}
		err = json.Unmarshal(env.Data, &response)
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
		req = withChannelParam(req, "id", fakeChannelID)
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

	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	subRepo := repository.NewSubscriptionRepository(td.DB)

	ctx := context.Background()

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
		err := subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[0].ID)
		require.NoError(t, err)
		err = subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[1].ID)
		require.NoError(t, err)

		videos, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)

		assert.Equal(t, 6, total)
		assert.Len(t, videos, 6)

		for _, video := range videos {
			assert.NotEqual(t, uuid.Nil, video.ChannelID)
			assert.True(t, video.ChannelID == channels[0].ID || video.ChannelID == channels[1].ID)
		}

		for i := 1; i < len(videos); i++ {
			assert.True(t, videos[i-1].UploadDate.After(videos[i].UploadDate) ||
				videos[i-1].UploadDate.Equal(videos[i].UploadDate))
		}
	})

	t.Run("Feed Updates When Subscriptions Change", func(t *testing.T) {
		_, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total)

		err = subRepo.SubscribeToChannel(ctx, uuid.MustParse(subscriber.ID), channels[2].ID)
		require.NoError(t, err)

		_, total, err = subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 9, total)

		err = subRepo.UnsubscribeFromChannel(ctx, uuid.MustParse(subscriber.ID), channels[0].ID)
		require.NoError(t, err)

		videos, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total)

		for _, video := range videos {
			assert.NotEqual(t, uuid.Nil, video.ChannelID)
			assert.True(t, video.ChannelID == channels[1].ID || video.ChannelID == channels[2].ID)
		}
	})

	t.Run("Feed Respects Video Privacy", func(t *testing.T) {
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

		videos, _, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 100, 0)
		require.NoError(t, err)

		for _, video := range videos {
			assert.NotEqual(t, privateVideo.ID, video.ID)
			assert.Equal(t, domain.PrivacyPublic, video.Privacy)
		}
	})

	t.Run("Feed Pagination", func(t *testing.T) {
		page1, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 3, 0)
		require.NoError(t, err)
		assert.Equal(t, 6, total)
		assert.Len(t, page1, 3)

		page2, total, err := subRepo.GetSubscriptionVideos(ctx, uuid.MustParse(subscriber.ID), 3, 3)
		require.NoError(t, err)
		assert.Equal(t, 6, total)
		assert.Len(t, page2, 3)

		page1IDs := make(map[string]bool)
		for _, v := range page1 {
			page1IDs[v.ID] = true
		}

		for _, v := range page2 {
			assert.False(t, page1IDs[v.ID], "Video %s appears in both pages", v.ID)
		}
	})
}
