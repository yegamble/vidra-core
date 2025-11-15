package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionRepository_SubscribeToChannel(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	// Create two users
	user1ID := uuid.New()
	user2ID := uuid.New()

	// Create channels for both users
	channel1 := createTestChannel(t, channelRepo, ctx, user1ID, "channel1")
	channel2 := createTestChannel(t, channelRepo, ctx, user2ID, "channel2")

	tests := []struct {
		name         string
		subscriberID uuid.UUID
		channelID    uuid.UUID
		wantErr      bool
		errContains  string
	}{
		{
			name:         "successful subscription",
			subscriberID: user1ID,
			channelID:    channel2.ID,
			wantErr:      false,
		},
		{
			name:         "cannot subscribe to own channel",
			subscriberID: user1ID,
			channelID:    channel1.ID,
			wantErr:      true,
			errContains:  "cannot subscribe to your own channel",
		},
		{
			name:         "non-existent channel",
			subscriberID: user1ID,
			channelID:    uuid.New(),
			wantErr:      true,
		},
		{
			name:         "idempotent - subscribe twice",
			subscriberID: user1ID,
			channelID:    channel2.ID,
			wantErr:      false, // Should not error on duplicate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := subRepo.SubscribeToChannel(ctx, tt.subscriberID, tt.channelID)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
			} else {
				require.NoError(t, err)

				// Verify subscription exists
				isSubscribed, err := subRepo.IsSubscribed(ctx, tt.subscriberID, tt.channelID)
				require.NoError(t, err)
				assert.True(t, isSubscribed)
			}
		})
	}
}

func TestSubscriptionRepository_UnsubscribeFromChannel(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	user1ID := uuid.New()
	user2ID := uuid.New()

	channel := createTestChannel(t, channelRepo, ctx, user2ID, "testchannel")

	// Subscribe first
	err := subRepo.SubscribeToChannel(ctx, user1ID, channel.ID)
	require.NoError(t, err)

	tests := []struct {
		name         string
		subscriberID uuid.UUID
		channelID    uuid.UUID
		wantErr      error
	}{
		{
			name:         "successful unsubscribe",
			subscriberID: user1ID,
			channelID:    channel.ID,
			wantErr:      nil,
		},
		{
			name:         "unsubscribe when not subscribed",
			subscriberID: user1ID,
			channelID:    channel.ID,
			wantErr:      domain.ErrNotFound,
		},
		{
			name:         "unsubscribe from non-existent channel",
			subscriberID: user1ID,
			channelID:    uuid.New(),
			wantErr:      domain.ErrNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := subRepo.UnsubscribeFromChannel(ctx, tt.subscriberID, tt.channelID)
			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)

				// Verify unsubscribed
				isSubscribed, err := subRepo.IsSubscribed(ctx, tt.subscriberID, tt.channelID)
				require.NoError(t, err)
				assert.False(t, isSubscribed)
			}
		})
	}
}

func TestSubscriptionRepository_IsSubscribed(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	user1ID := uuid.New()
	user2ID := uuid.New()

	channel := createTestChannel(t, channelRepo, ctx, user2ID, "testchannel")

	// Initially not subscribed
	isSubscribed, err := subRepo.IsSubscribed(ctx, user1ID, channel.ID)
	require.NoError(t, err)
	assert.False(t, isSubscribed)

	// Subscribe
	err = subRepo.SubscribeToChannel(ctx, user1ID, channel.ID)
	require.NoError(t, err)

	// Now subscribed
	isSubscribed, err = subRepo.IsSubscribed(ctx, user1ID, channel.ID)
	require.NoError(t, err)
	assert.True(t, isSubscribed)

	// Non-existent channel
	isSubscribed, err = subRepo.IsSubscribed(ctx, user1ID, uuid.New())
	require.NoError(t, err)
	assert.False(t, isSubscribed)
}

func TestSubscriptionRepository_ListUserSubscriptions(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)

	ctx := context.Background()

	userID := uuid.New()
	user2ID := uuid.New()
	user3ID := uuid.New()
	user4ID := uuid.New()

	// Create multiple channels
	channel1 := createTestChannel(t, channelRepo, ctx, user2ID, "channel1")
	channel2 := createTestChannel(t, channelRepo, ctx, user3ID, "channel2")
	channel3 := createTestChannel(t, channelRepo, ctx, user4ID, "channel3")

	// Subscribe to all channels
	err := subRepo.SubscribeToChannel(ctx, userID, channel1.ID)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	err = subRepo.SubscribeToChannel(ctx, userID, channel2.ID)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	err = subRepo.SubscribeToChannel(ctx, userID, channel3.ID)
	require.NoError(t, err)

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int
		wantTotal int
	}{
		{
			name:      "get all subscriptions",
			limit:     10,
			offset:    0,
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name:      "paginate - first page",
			limit:     2,
			offset:    0,
			wantCount: 2,
			wantTotal: 3,
		},
		{
			name:      "paginate - second page",
			limit:     2,
			offset:    2,
			wantCount: 1,
			wantTotal: 3,
		},
		{
			name:      "offset beyond results",
			limit:     10,
			offset:    10,
			wantCount: 0,
			wantTotal: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := subRepo.ListUserSubscriptions(ctx, userID, tt.limit, tt.offset)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, response.Total)
			assert.Len(t, response.Data, tt.wantCount)

			// Verify subscriptions have channel details
			for _, sub := range response.Data {
				assert.NotNil(t, sub.Channel)
				assert.NotEqual(t, uuid.Nil, sub.Channel.ID)
				assert.NotEmpty(t, sub.Channel.Handle)
			}

			// Verify ordering (newest first)
			if len(response.Data) > 1 {
				for i := 1; i < len(response.Data); i++ {
					assert.True(t, response.Data[i-1].CreatedAt.After(response.Data[i].CreatedAt) ||
						response.Data[i-1].CreatedAt.Equal(response.Data[i].CreatedAt))
				}
			}
		})
	}
}

func TestSubscriptionRepository_ListUserSubscriptions_EmptyResult(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)

	ctx := context.Background()
	userID := uuid.New()

	response, err := subRepo.ListUserSubscriptions(ctx, userID, 10, 0)
	require.NoError(t, err)
	assert.Equal(t, 0, response.Total)
	assert.Empty(t, response.Data)
}

func TestSubscriptionRepository_ListChannelSubscribers(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	channelOwnerID := uuid.New()
	channel := createTestChannel(t, channelRepo, ctx, channelOwnerID, "popular_channel")

	// Create multiple users and subscribe them
	user1 := createTestUserWithID(t, userRepo, ctx, uuid.New(), "user1", "user1@example.com")
	user2 := createTestUserWithID(t, userRepo, ctx, uuid.New(), "user2", "user2@example.com")
	user3 := createTestUserWithID(t, userRepo, ctx, uuid.New(), "user3", "user3@example.com")

	user1UUID, _ := uuid.Parse(user1.ID)
	user2UUID, _ := uuid.Parse(user2.ID)
	user3UUID, _ := uuid.Parse(user3.ID)

	err := subRepo.SubscribeToChannel(ctx, user1UUID, channel.ID)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	err = subRepo.SubscribeToChannel(ctx, user2UUID, channel.ID)
	require.NoError(t, err)
	time.Sleep(10 * time.Millisecond)

	err = subRepo.SubscribeToChannel(ctx, user3UUID, channel.ID)
	require.NoError(t, err)

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int
		wantTotal int
	}{
		{
			name:      "get all subscribers",
			limit:     10,
			offset:    0,
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name:      "paginate subscribers",
			limit:     2,
			offset:    0,
			wantCount: 2,
			wantTotal: 3,
		},
		{
			name:      "second page",
			limit:     2,
			offset:    2,
			wantCount: 1,
			wantTotal: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := subRepo.ListChannelSubscribers(ctx, channel.ID, tt.limit, tt.offset)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, response.Total)
			assert.Len(t, response.Data, tt.wantCount)

			// Verify subscribers have user details
			for _, sub := range response.Data {
				assert.NotNil(t, sub.Subscriber)
				assert.NotEmpty(t, sub.Subscriber.ID)
				assert.NotEmpty(t, sub.Subscriber.Username)
			}

			// Verify ordering (newest first)
			if len(response.Data) > 1 {
				for i := 1; i < len(response.Data); i++ {
					assert.True(t, response.Data[i-1].CreatedAt.After(response.Data[i].CreatedAt) ||
						response.Data[i-1].CreatedAt.Equal(response.Data[i].CreatedAt))
				}
			}
		})
	}
}

func TestSubscriptionRepository_GetSubscriptionVideos(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	subRepo := NewSubscriptionRepository(testDB.DB)
	channelRepo := NewChannelRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)

	ctx := context.Background()

	subscriberID := uuid.New()
	channelOwner1 := uuid.New()
	channelOwner2 := uuid.New()

	// Create channels
	channel1 := createTestChannel(t, channelRepo, ctx, channelOwner1, "channel1")
	channel2 := createTestChannel(t, channelRepo, ctx, channelOwner2, "channel2")

	// Subscribe to both channels
	err := subRepo.SubscribeToChannel(ctx, subscriberID, channel1.ID)
	require.NoError(t, err)
	err = subRepo.SubscribeToChannel(ctx, subscriberID, channel2.ID)
	require.NoError(t, err)

	// Create videos in subscribed channels
	video1 := createTestVideoInChannel(t, videoRepo, ctx, channelOwner1, channel1.ID, domain.PrivacyPublic, domain.StatusCompleted)
	_ = createTestVideoInChannel(t, videoRepo, ctx, channelOwner1, channel1.ID, domain.PrivacyPublic, domain.StatusCompleted)
	_ = createTestVideoInChannel(t, videoRepo, ctx, channelOwner2, channel2.ID, domain.PrivacyPublic, domain.StatusCompleted)

	// Create private video (should not appear)
	_ = createTestVideoInChannel(t, videoRepo, ctx, channelOwner1, channel1.ID, domain.PrivacyPrivate, domain.StatusCompleted)

	// Create processing video (should not appear)
	_ = createTestVideoInChannel(t, videoRepo, ctx, channelOwner1, channel1.ID, domain.PrivacyPublic, domain.StatusProcessing)

	tests := []struct {
		name      string
		limit     int
		offset    int
		wantCount int
		wantTotal int
	}{
		{
			name:      "get all subscription videos",
			limit:     10,
			offset:    0,
			wantCount: 3,
			wantTotal: 3,
		},
		{
			name:      "paginate videos",
			limit:     2,
			offset:    0,
			wantCount: 2,
			wantTotal: 3,
		},
		{
			name:      "second page",
			limit:     2,
			offset:    2,
			wantCount: 1,
			wantTotal: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videos, total, err := subRepo.GetSubscriptionVideos(ctx, subscriberID, tt.limit, tt.offset)
			require.NoError(t, err)
			assert.Equal(t, tt.wantTotal, total)
			assert.Len(t, videos, tt.wantCount)

			// Verify all videos are public and completed
			for _, video := range videos {
				assert.Equal(t, domain.PrivacyPublic, video.Privacy)
				assert.Equal(t, domain.StatusCompleted, video.Status)
			}

			// Verify ordering (newest first)
			if len(videos) > 1 {
				for i := 1; i < len(videos); i++ {
					assert.True(t, videos[i-1].UploadDate.After(videos[i].UploadDate) ||
						videos[i-1].UploadDate.Equal(videos[i].UploadDate))
				}
			}
		})
	}

	// Test with user who has no subscriptions
	t.Run("no subscriptions", func(t *testing.T) {
		otherUserID := uuid.New()
		videos, total, err := subRepo.GetSubscriptionVideos(ctx, otherUserID, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		assert.Empty(t, videos)
	})

	// Ensure only subscribed channel videos appear
	t.Run("only subscribed channels", func(t *testing.T) {
		videos, _, err := subRepo.GetSubscriptionVideos(ctx, subscriberID, 10, 0)
		require.NoError(t, err)

		// Check that video1 is in the results
		found := false
		for _, v := range videos {
			if v.ID == video1.ID {
				found = true
				break
			}
		}
		assert.True(t, found, "Expected to find video from subscribed channel")
	})
}

// Helper functions

func createTestChannel(t *testing.T, repo *ChannelRepository, ctx context.Context, accountID uuid.UUID, handle string) *domain.Channel {
	t.Helper()

	channel := &domain.Channel{
		AccountID:   accountID,
		Handle:      handle,
		DisplayName: "Test Channel " + handle,
		Description: "Test description",
		IsLocal:     true,
	}

	err := repo.Create(ctx, channel)
	require.NoError(t, err)

	return channel
}

func createTestUserWithID(t *testing.T, repo *UserRepository, ctx context.Context, id uuid.UUID, username, email string) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:        id.String(),
		Username:  username,
		Email:     email,
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := repo.Create(ctx, user, "hashed_password")
	require.NoError(t, err)

	return user
}

func createTestVideoInChannel(t *testing.T, repo *VideoRepository, ctx context.Context, userID uuid.UUID, channelID uuid.UUID, privacy domain.Privacy, status domain.ProcessingStatus) *domain.Video {
	t.Helper()

	video := &domain.Video{
		ID:          uuid.New(),
		UserID:      userID.String(),
		ChannelID:   &channelID,
		Title:       "Test Video " + uuid.New().String(),
		Description: "Test description",
		Privacy:     privacy,
		Status:      status,
		UploadDate:  time.Now(),
		Duration:    120,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	err := repo.Create(ctx, video)
	require.NoError(t, err)

	return video
}
