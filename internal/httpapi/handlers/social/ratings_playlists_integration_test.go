package social

import (
	"context"
	"fmt"
	"testing"
	"time"
	"vidra-core/internal/domain"
	"vidra-core/internal/repository"
	"vidra-core/internal/testutil"
	"vidra-core/internal/usecase"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/bcrypt"
)

func TestRatingsPlaylists_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	td := testutil.SetupTestDB(t)

	// Setup repositories and services
	userRepo := repository.NewUserRepository(td.DB)
	channelRepo := repository.NewChannelRepository(td.DB)
	videoRepo := repository.NewVideoRepository(td.DB)
	ratingRepo := repository.NewRatingRepository(td.DB)
	playlistRepo := repository.NewPlaylistRepository(td.DB)

	ratingService := usecase.NewRatingService(ratingRepo, videoRepo)
	playlistService := usecase.NewPlaylistService(playlistRepo, videoRepo)

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

		now := time.Now()
		claims := jwt.MapClaims{
			"sub": user.ID,
			"iat": now.Unix(),
			"exp": now.Add(time.Hour).Unix(),
		}
		tokenObj := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		token, err := tokenObj.SignedString([]byte("test-secret"))
		require.NoError(t, err)

		return user, token
	}

	// Helper to create channel
	createChannel := func(t *testing.T, userID string) *domain.Channel {
		channel := &domain.Channel{
			ID:          uuid.New(),
			AccountID:   uuid.MustParse(userID),
			Handle:      fmt.Sprintf("channel_%s", uuid.New()),
			DisplayName: "Test Channel",
			IsLocal:     true,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}
		err := channelRepo.Create(context.Background(), channel)
		require.NoError(t, err)
		return channel
	}

	// Helper to create video
	createVideo := func(t *testing.T, userID string, channelID uuid.UUID) *domain.Video {
		video := &domain.Video{
			ID:            uuid.NewString(),
			ThumbnailID:   uuid.NewString(),
			ChannelID:     channelID,
			UserID:        userID,
			Title:         "Test Video",
			Description:   "Test Description",
			Duration:      120,
			Privacy:       domain.PrivacyPublic,
			Status:        domain.StatusCompleted,
			Tags:          []string{},
			FileSize:      1024,
			Metadata:      domain.VideoMetadata{},
			ProcessedCIDs: map[string]string{},
			OutputPaths:   map[string]string{},
			CreatedAt:     time.Now(),
			UpdatedAt:     time.Now(),
		}
		err := videoRepo.Create(context.Background(), video)
		require.NoError(t, err)
		return video
	}

	// Create test users and content
	user1, _ := createUser(t, "user1", "user1@example.com")
	user2, _ := createUser(t, "user2", "user2@example.com")

	channel1 := createChannel(t, user1.ID)
	video1 := createVideo(t, user1.ID, channel1.ID)
	video2 := createVideo(t, user1.ID, channel1.ID)

	t.Run("Ratings", func(t *testing.T) {
		t.Run("SetRating_Like", func(t *testing.T) {
			err := ratingService.SetRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video1.ID), domain.RatingLike)
			require.NoError(t, err)

			// Verify rating was set
			rating, err := ratingService.GetRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video1.ID))
			require.NoError(t, err)
			assert.Equal(t, domain.RatingLike, rating)

			// Verify stats
			userID := uuid.MustParse(user1.ID)
			stats, err := ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video1.ID), &userID)
			require.NoError(t, err)
			assert.Equal(t, 1, stats.LikesCount)
			assert.Equal(t, 0, stats.DislikesCount)
			assert.Equal(t, domain.RatingLike, stats.UserRating)
		})

		t.Run("SetRating_Dislike", func(t *testing.T) {
			err := ratingService.SetRating(context.Background(), uuid.MustParse(user2.ID), uuid.MustParse(video1.ID), domain.RatingDislike)
			require.NoError(t, err)

			// Verify stats updated
			stats, err := ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video1.ID), nil)
			require.NoError(t, err)
			assert.Equal(t, 1, stats.LikesCount)
			assert.Equal(t, 1, stats.DislikesCount)
		})

		t.Run("ChangeRating", func(t *testing.T) {
			// Change from dislike to like
			err := ratingService.SetRating(context.Background(), uuid.MustParse(user2.ID), uuid.MustParse(video1.ID), domain.RatingLike)
			require.NoError(t, err)

			// Verify stats updated
			stats, err := ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video1.ID), nil)
			require.NoError(t, err)
			assert.Equal(t, 2, stats.LikesCount)
			assert.Equal(t, 0, stats.DislikesCount)
		})

		t.Run("RemoveRating", func(t *testing.T) {
			err := ratingService.RemoveRating(context.Background(), uuid.MustParse(user2.ID), uuid.MustParse(video1.ID))
			require.NoError(t, err)

			// Verify stats updated
			stats, err := ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video1.ID), nil)
			require.NoError(t, err)
			assert.Equal(t, 1, stats.LikesCount)
			assert.Equal(t, 0, stats.DislikesCount)

			// Verify rating is none
			rating, err := ratingService.GetRating(context.Background(), uuid.MustParse(user2.ID), uuid.MustParse(video1.ID))
			require.NoError(t, err)
			assert.Equal(t, domain.RatingNone, rating)
		})

		t.Run("Idempotent", func(t *testing.T) {
			// Set same rating multiple times
			for i := 0; i < 3; i++ {
				err := ratingService.SetRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video2.ID), domain.RatingLike)
				require.NoError(t, err)
			}

			// Verify count is still 1
			stats, err := ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video2.ID), nil)
			require.NoError(t, err)
			assert.Equal(t, 1, stats.LikesCount)

			// Remove rating multiple times
			for i := 0; i < 3; i++ {
				err := ratingService.RemoveRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video2.ID))
				require.NoError(t, err)
			}

			// Verify count is 0
			stats, err = ratingService.GetVideoRatingStats(context.Background(), uuid.MustParse(video2.ID), nil)
			require.NoError(t, err)
			assert.Equal(t, 0, stats.LikesCount)
		})

		t.Run("GetUserRatings", func(t *testing.T) {
			// Set ratings on multiple videos
			_ = ratingService.SetRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video1.ID), domain.RatingLike)
			_ = ratingService.SetRating(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video2.ID), domain.RatingDislike)

			ratings, err := ratingService.GetUserRatings(context.Background(), uuid.MustParse(user1.ID), 10, 0)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(ratings), 2)
		})
	})

	t.Run("Playlists", func(t *testing.T) {
		var playlist1 *domain.Playlist

		t.Run("CreatePlaylist", func(t *testing.T) {
			req := &domain.CreatePlaylistRequest{
				Name:        "My Test Playlist",
				Description: strPtr("A test playlist"),
				Privacy:     domain.PrivacyPublic,
			}

			playlist, err := playlistService.CreatePlaylist(context.Background(), uuid.MustParse(user1.ID), req)
			require.NoError(t, err)
			assert.Equal(t, "My Test Playlist", playlist.Name)
			assert.Equal(t, domain.PrivacyPublic, playlist.Privacy)
			assert.False(t, playlist.IsWatchLater)

			playlist1 = playlist
		})

		t.Run("GetPlaylist_Public", func(t *testing.T) {
			// User2 can access public playlist
			user2ID := uuid.MustParse(user2.ID)
			playlist, err := playlistService.GetPlaylist(context.Background(), playlist1.ID, &user2ID)
			require.NoError(t, err)
			assert.Equal(t, playlist1.ID, playlist.ID)
		})

		t.Run("CreatePrivatePlaylist", func(t *testing.T) {
			req := &domain.CreatePlaylistRequest{
				Name:    "Private Playlist",
				Privacy: domain.PrivacyPrivate,
			}

			playlist, err := playlistService.CreatePlaylist(context.Background(), uuid.MustParse(user1.ID), req)
			require.NoError(t, err)

			// User2 cannot access private playlist
			user2ID := uuid.MustParse(user2.ID)
			_, err = playlistService.GetPlaylist(context.Background(), playlist.ID, &user2ID)
			assert.Equal(t, domain.ErrUnauthorized, err)

			// Owner can access
			user1ID := uuid.MustParse(user1.ID)
			_, err = playlistService.GetPlaylist(context.Background(), playlist.ID, &user1ID)
			assert.NoError(t, err)
		})

		t.Run("UpdatePlaylist", func(t *testing.T) {
			newName := "Updated Playlist Name"
			req := domain.UpdatePlaylistRequest{
				Name: &newName,
			}

			err := playlistService.UpdatePlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, req)
			require.NoError(t, err)

			// Verify update
			user1ID := uuid.MustParse(user1.ID)
			playlist, err := playlistService.GetPlaylist(context.Background(), playlist1.ID, &user1ID)
			require.NoError(t, err)
			assert.Equal(t, newName, playlist.Name)

			// Non-owner cannot update
			err = playlistService.UpdatePlaylist(context.Background(), uuid.MustParse(user2.ID), playlist1.ID, req)
			assert.Equal(t, domain.ErrUnauthorized, err)
		})

		t.Run("AddVideoToPlaylist", func(t *testing.T) {
			err := playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, uuid.MustParse(video1.ID), nil)
			require.NoError(t, err)

			// Add second video
			err = playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, uuid.MustParse(video2.ID), nil)
			require.NoError(t, err)

			// Get playlist items
			user1ID := uuid.MustParse(user1.ID)
			items, err := playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 2, len(items))

			// Non-owner cannot add videos
			err = playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user2.ID), playlist1.ID, uuid.MustParse(video1.ID), nil)
			assert.Equal(t, domain.ErrUnauthorized, err)
		})

		t.Run("AddVideoToPlaylist_Idempotent", func(t *testing.T) {
			// Adding same video multiple times should not error
			for i := 0; i < 3; i++ {
				err := playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, uuid.MustParse(video1.ID), nil)
				require.NoError(t, err)
			}

			// Should still have 2 items
			user1ID := uuid.MustParse(user1.ID)
			items, err := playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 2, len(items))
		})

		t.Run("RemoveVideoFromPlaylist", func(t *testing.T) {
			// Get items first to get item ID
			user1ID := uuid.MustParse(user1.ID)
			items, err := playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			require.Greater(t, len(items), 0)

			itemID := items[0].ID

			// Remove item
			err = playlistService.RemoveVideoFromPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, itemID)
			require.NoError(t, err)

			// Verify removal
			items, err = playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, 1, len(items))
		})

		t.Run("ReorderPlaylistItem", func(t *testing.T) {
			// Add videos back with specific positions
			pos0 := 0
			pos1 := 1
			err := playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, uuid.MustParse(video1.ID), &pos0)
			require.NoError(t, err)
			err = playlistService.AddVideoToPlaylist(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, uuid.MustParse(video2.ID), &pos1)
			require.NoError(t, err)

			user1ID := uuid.MustParse(user1.ID)
			items, err := playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)

			// Find the item to reorder
			var itemToMove *domain.PlaylistItem
			for _, item := range items {
				if item.VideoID == uuid.MustParse(video2.ID) {
					itemToMove = item
					break
				}
			}
			require.NotNil(t, itemToMove)

			// Move video2 to position 0
			err = playlistService.ReorderPlaylistItem(context.Background(), uuid.MustParse(user1.ID), playlist1.ID, itemToMove.ID, 0)
			require.NoError(t, err)

			// Verify new order
			items, err = playlistService.GetPlaylistItems(context.Background(), playlist1.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			assert.Equal(t, uuid.MustParse(video2.ID), items[0].VideoID)
		})

		t.Run("ListPlaylists", func(t *testing.T) {
			user1ID := uuid.MustParse(user1.ID)
			opts := domain.PlaylistListOptions{
				UserID: &user1ID,
				Limit:  10,
				Offset: 0,
			}

			resp, err := playlistService.ListPlaylists(context.Background(), opts)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, resp.Total, 2) // At least 2 playlists created
		})

		t.Run("WatchLater", func(t *testing.T) {
			// Get or create watch later playlist
			watchLater, err := playlistService.GetOrCreateWatchLater(context.Background(), uuid.MustParse(user1.ID))
			require.NoError(t, err)
			assert.True(t, watchLater.IsWatchLater)
			assert.Equal(t, "Watch Later", watchLater.Name)
			assert.Equal(t, domain.PrivacyPrivate, watchLater.Privacy)

			// Add video to watch later
			err = playlistService.AddToWatchLater(context.Background(), uuid.MustParse(user1.ID), uuid.MustParse(video1.ID))
			require.NoError(t, err)

			// Verify video was added
			user1ID := uuid.MustParse(user1.ID)
			items, err := playlistService.GetPlaylistItems(context.Background(), watchLater.ID, &user1ID, 10, 0)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(items), 1)

			// Cannot delete system playlist
			err = playlistService.DeletePlaylist(context.Background(), uuid.MustParse(user1.ID), watchLater.ID)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "cannot delete system playlist")

			// Cannot rename system playlist
			newName := "Renamed"
			err = playlistService.UpdatePlaylist(context.Background(), uuid.MustParse(user1.ID), watchLater.ID, domain.UpdatePlaylistRequest{Name: &newName})
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "cannot rename system playlist")
		})

		t.Run("DeletePlaylist", func(t *testing.T) {
			// Create a playlist to delete
			req := &domain.CreatePlaylistRequest{
				Name:    "To Delete",
				Privacy: domain.PrivacyPrivate,
			}
			playlist, err := playlistService.CreatePlaylist(context.Background(), uuid.MustParse(user1.ID), req)
			require.NoError(t, err)

			// Delete it
			err = playlistService.DeletePlaylist(context.Background(), uuid.MustParse(user1.ID), playlist.ID)
			require.NoError(t, err)

			// Verify deletion
			user1ID := uuid.MustParse(user1.ID)
			_, err = playlistService.GetPlaylist(context.Background(), playlist.ID, &user1ID)
			assert.Equal(t, domain.ErrNotFound, err)

			// Non-owner cannot delete
			err = playlistService.DeletePlaylist(context.Background(), uuid.MustParse(user2.ID), playlist1.ID)
			assert.Equal(t, domain.ErrUnauthorized, err)
		})
	})
}

func strPtr(s string) *string {
	return &s
}
