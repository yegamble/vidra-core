package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
)

// TestBuildVideoObject_Basic tests converting domain.Video to ActivityPub VideoObject
func TestBuildVideoObject_Basic(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:          "video-123",
		Title:       "My Test Video",
		Description: "This is a test video description",
		Duration:    330, // 5 minutes 30 seconds
		Views:       1000,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
		UploadDate:  time.Date(2025, 11, 16, 12, 0, 0, 0, time.UTC),
		UserID:      "user-123",
		Language:    "en",
		Tags:        []string{"golang", "video", "testing"},
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Converts basic video fields correctly", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)
		require.NotNil(t, videoObject)

		assert.Equal(t, domain.ObjectTypeVideo, videoObject.Type)
		assert.Equal(t, "https://video.example/videos/video-123", videoObject.ID)
		assert.Equal(t, "My Test Video", videoObject.Name)
		assert.Equal(t, "This is a test video description", videoObject.Content)
		assert.NotNil(t, videoObject.Published)
		assert.Equal(t, video.UploadDate, *videoObject.Published)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Converts duration to ISO 8601 format", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// 330 seconds = 5 minutes 30 seconds = PT5M30S
		assert.Equal(t, "PT5M30S", videoObject.Duration)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes attributedTo with user actor URI", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.NotNil(t, videoObject.AttributedTo)
		require.Len(t, videoObject.AttributedTo, 1)
		assert.Equal(t, "https://video.example/users/testuser", videoObject.AttributedTo[0])

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes video UUID", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		assert.Equal(t, video.ID, videoObject.UUID)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Returns error when user not found", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(nil, fmt.Errorf("user not found")).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		assert.Error(t, err)
		assert.Nil(t, videoObject)
		assert.Contains(t, err.Error(), "user not found")

		mockUserRepo.AssertExpectations(t)
	})
}

// TestBuildVideoObject_URLs tests video URL generation
func TestBuildVideoObject_URLs(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:     "video-123",
		Title:  "Test Video",
		UserID: "user-123",
		OutputPaths: map[string]string{
			"360p":  "/videos/video-123/360p.m3u8",
			"720p":  "/videos/video-123/720p.m3u8",
			"1080p": "/videos/video-123/1080p.m3u8",
		},
		ThumbnailPath: "/thumbnails/video-123.jpg",
		Metadata: domain.VideoMetadata{
			Width:  1920,
			Height: 1080,
		},
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Includes HLS master playlist URL", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.NotEmpty(t, videoObject.URL)

		// Find the HLS master playlist
		var foundMasterPlaylist bool
		for _, url := range videoObject.URL {
			if url.MediaType == "application/x-mpegURL" && url.Type == "Link" {
				assert.Equal(t, "https://video.example/videos/video-123/master.m3u8", url.Href)
				foundMasterPlaylist = true
				break
			}
		}
		assert.True(t, foundMasterPlaylist, "Should include HLS master playlist")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes all quality variants", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Should have URLs for each quality variant
		qualityVariants := []string{"360p", "720p", "1080p"}
		for _, quality := range qualityVariants {
			var found bool
			for _, url := range videoObject.URL {
				if url.Href == fmt.Sprintf("https://video.example/videos/video-123/%s.m3u8", quality) {
					assert.Equal(t, "application/x-mpegURL", url.MediaType)
					found = true
					break
				}
			}
			assert.True(t, found, "Should include %s variant", quality)
		}

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes resolution metadata in URLs", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Find the 1080p variant
		for _, url := range videoObject.URL {
			if url.Height == 1080 {
				assert.Equal(t, 1920, url.Width)
				assert.Equal(t, "application/x-mpegURL", url.MediaType)
			}
		}

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes thumbnail in icon field", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.NotEmpty(t, videoObject.Icon)
		assert.Equal(t, domain.ObjectTypeImage, videoObject.Icon[0].Type)
		assert.Equal(t, "https://video.example/thumbnails/video-123.jpg", videoObject.Icon[0].URL)
		assert.Equal(t, "image/jpeg", videoObject.Icon[0].MediaType)

		mockUserRepo.AssertExpectations(t)
	})
}

// TestBuildVideoObject_Metadata tests video metadata handling
func TestBuildVideoObject_Metadata(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	categoryID := uuid.New()
	video := &domain.Video{
		ID:         "video-123",
		Title:      "Test Video",
		UserID:     "user-123",
		Tags:       []string{"golang", "activitypub", "federation"},
		Language:   "en",
		CategoryID: &categoryID,
		Category: &domain.VideoCategory{
			ID:   categoryID,
			Name: "Technology",
		},
		Views: 5000,
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Converts tags to hashtags", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.Len(t, videoObject.Tag, 3)

		expectedTags := map[string]bool{
			"#golang":      false,
			"#activitypub": false,
			"#federation":  false,
		}

		for _, tag := range videoObject.Tag {
			assert.Equal(t, "Hashtag", tag.Type)
			expectedTags[tag.Name] = true
		}

		for tagName, found := range expectedTags {
			assert.True(t, found, "Should include tag %s", tagName)
		}

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes category information", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.NotNil(t, videoObject.Category)
		assert.Equal(t, categoryID.String(), videoObject.Category.Identifier)
		assert.Equal(t, "Technology", videoObject.Category.Name)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes language information", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.NotNil(t, videoObject.Language)
		assert.Equal(t, "en", videoObject.Language.Identifier)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes view count", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		assert.Equal(t, 5000, videoObject.Views)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes collection URLs for likes, dislikes, shares, comments", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		baseURL := "https://video.example/videos/video-123"
		assert.Equal(t, baseURL+"/likes", videoObject.Likes)
		assert.Equal(t, baseURL+"/dislikes", videoObject.Dislikes)
		assert.Equal(t, baseURL+"/shares", videoObject.Shares)
		assert.Equal(t, baseURL+"/comments", videoObject.Comments)

		mockUserRepo.AssertExpectations(t)
	})
}

// TestBuildVideoObject_Privacy tests privacy handling
func TestBuildVideoObject_Privacy(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Public videos have public audience", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-123",
			Title:   "Public Video",
			UserID:  "user-123",
			Privacy: domain.PrivacyPublic,
		}

		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		require.Contains(t, videoObject.To, "https://www.w3.org/ns/activitystreams#Public")
		assert.NotContains(t, videoObject.Cc, "https://www.w3.org/ns/activitystreams#Public")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Unlisted videos have public in CC", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-123",
			Title:   "Unlisted Video",
			UserID:  "user-123",
			Privacy: domain.PrivacyUnlisted,
		}

		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		assert.NotContains(t, videoObject.To, "https://www.w3.org/ns/activitystreams#Public")
		require.Contains(t, videoObject.Cc, "https://www.w3.org/ns/activitystreams#Public")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Private videos only to followers", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-123",
			Title:   "Private Video",
			UserID:  "user-123",
			Privacy: domain.PrivacyPrivate,
		}

		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Private videos should only be sent to followers
		assert.NotContains(t, videoObject.To, "https://www.w3.org/ns/activitystreams#Public")
		assert.NotContains(t, videoObject.Cc, "https://www.w3.org/ns/activitystreams#Public")

		// Should include followers collection
		require.Contains(t, videoObject.To, "https://video.example/users/testuser/followers")

		mockUserRepo.AssertExpectations(t)
	})
}

// TestBuildVideoObject_PeerTubeCompatibility tests PeerTube-specific fields
func TestBuildVideoObject_PeerTubeCompatibility(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:     "video-123",
		Title:  "PeerTube Compat Test",
		UserID: "user-123",
		Status: domain.StatusCompleted,
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Includes PeerTube context", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Context should be an array including PeerTube namespace
		contextArray, ok := videoObject.Context.([]interface{})
		require.True(t, ok, "Context should be an array")

		hasActivityStreams := false
		hasPeerTube := false

		for _, ctx := range contextArray {
			if ctx == domain.ActivityStreamsContext {
				hasActivityStreams = true
			}
			if ctx == domain.PeerTubeContext {
				hasPeerTube = true
			}
		}

		assert.True(t, hasActivityStreams, "Should include ActivityStreams context")
		assert.True(t, hasPeerTube, "Should include PeerTube context")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Includes PeerTube state field", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// State 1 = published in PeerTube
		assert.Equal(t, 1, videoObject.State)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Sets commentsEnabled and downloadEnabled", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		assert.True(t, videoObject.CommentsEnabled)
		assert.True(t, videoObject.DownloadEnabled)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Sets waitTranscoding based on processing status", func(t *testing.T) {
		processingVideo := &domain.Video{
			ID:     "video-456",
			Title:  "Processing Video",
			UserID: "user-123",
			Status: domain.StatusProcessing,
		}

		mockUserRepo.On("GetByID", ctx, processingVideo.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, processingVideo)
		require.NoError(t, err)

		assert.True(t, videoObject.WaitTranscoding)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Can be marshaled to valid JSON-LD", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Marshal to JSON
		jsonBytes, err := json.Marshal(videoObject)
		require.NoError(t, err)

		// Unmarshal back to verify structure
		var result map[string]interface{}
		err = json.Unmarshal(jsonBytes, &result)
		require.NoError(t, err)

		assert.Equal(t, "Video", result["type"])
		assert.NotNil(t, result["@context"])
		assert.NotNil(t, result["id"])

		mockUserRepo.AssertExpectations(t)
	})
}

// TestCreateVideoActivity tests Create activity generation
func TestCreateVideoActivity(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:         "video-123",
		Title:      "Test Video",
		UserID:     "user-123",
		Privacy:    domain.PrivacyPublic,
		UploadDate: time.Date(2025, 11, 16, 12, 0, 0, 0, time.UTC),
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	t.Run("Wraps VideoObject in Create activity", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)
		require.NoError(t, err)
		require.NotNil(t, activity)

		assert.Equal(t, domain.ActivityTypeCreate, activity.Type)
		assert.Equal(t, "https://video.example/users/testuser", activity.Actor)
		assert.NotNil(t, activity.Object)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Sets correct activity ID", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)
		require.NoError(t, err)

		// Activity ID should be unique
		assert.Contains(t, activity.ID, "https://video.example/activities/")
		assert.NotEmpty(t, activity.ID)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Sets published timestamp", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)
		require.NoError(t, err)

		require.NotNil(t, activity.Published)
		// Should be recent
		assert.WithinDuration(t, time.Now(), *activity.Published, 5*time.Second)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Inherits audience from VideoObject", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)
		require.NoError(t, err)

		// Public video should have public audience
		require.Contains(t, activity.To, "https://www.w3.org/ns/activitystreams#Public")

		mockUserRepo.AssertExpectations(t)
	})
}

// TestPublishVideo tests video publishing to followers
func TestPublishVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:      "video-123",
		Title:   "Test Video",
		UserID:  "user-123",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	followers := []*domain.APFollower{
		{
			ActorID:    "user-123",
			FollowerID: "https://mastodon.example/users/alice",
			State:      "accepted",
		},
		{
			ActorID:    "user-123",
			FollowerID: "https://peertube.example/accounts/bob",
			State:      "accepted",
		},
	}

	remoteActors := []*domain.APRemoteActor{
		{
			ActorURI:    "https://mastodon.example/users/alice",
			InboxURL:    "https://mastodon.example/users/alice/inbox",
			SharedInbox: stringPtr("https://mastodon.example/inbox"),
		},
		{
			ActorURI: "https://peertube.example/accounts/bob",
			InboxURL: "https://peertube.example/accounts/bob/inbox",
		},
	}

	t.Run("Fetches video from repository", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 2, nil).Once()

		for i, follower := range followers {
			mockAPRepo.On("GetRemoteActor", ctx, follower.FollowerID).Return(remoteActors[i], nil).Once()
			mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		}

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Returns error if video not found", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "nonexistent").Return(nil, fmt.Errorf("video not found")).Once()

		err := service.PublishVideo(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "video not found")

		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Only publishes completed videos", func(t *testing.T) {
		processingVideo := &domain.Video{
			ID:     "video-456",
			Title:  "Processing Video",
			UserID: "user-123",
			Status: domain.StatusProcessing,
		}

		mockVideoRepo.On("GetByID", ctx, "video-456").Return(processingVideo, nil).Once()

		err := service.PublishVideo(ctx, "video-456")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not completed")

		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Does not publish private videos", func(t *testing.T) {
		privateVideo := &domain.Video{
			ID:      "video-789",
			Title:   "Private Video",
			UserID:  "user-123",
			Privacy: domain.PrivacyPrivate,
			Status:  domain.StatusCompleted,
		}

		mockVideoRepo.On("GetByID", ctx, "video-789").Return(privateVideo, nil).Once()

		// Private videos can still be published to followers
		mockUserRepo.On("GetByID", ctx, privateVideo.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 2, nil).Once()

		for i, follower := range followers {
			mockAPRepo.On("GetRemoteActor", ctx, follower.FollowerID).Return(remoteActors[i], nil).Once()
			mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		}

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-789")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Creates delivery jobs for all followers", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 2, nil).Once()

		deliveryCount := 0
		for i, follower := range followers {
			mockAPRepo.On("GetRemoteActor", ctx, follower.FollowerID).Return(remoteActors[i], nil).Once()
			mockAPRepo.On("EnqueueDelivery", ctx, mock.MatchedBy(func(delivery *domain.APDeliveryQueue) bool {
				deliveryCount++
				return delivery.ActorID == user.ID
			})).Return(nil).Once()
		}

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)
		assert.Equal(t, 2, deliveryCount)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Uses shared inbox when available", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{followers[0]}, 1, nil).Once()

		mockAPRepo.On("GetRemoteActor", ctx, followers[0].FollowerID).Return(remoteActors[0], nil).Once()
		mockAPRepo.On("EnqueueDelivery", ctx, mock.MatchedBy(func(delivery *domain.APDeliveryQueue) bool {
			// Should use shared inbox
			return delivery.InboxURL == "https://mastodon.example/inbox"
		})).Return(nil).Once()

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Stores activity locally", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 2, nil).Once()

		for i, follower := range followers {
			mockAPRepo.On("GetRemoteActor", ctx, follower.FollowerID).Return(remoteActors[i], nil).Once()
			mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		}

		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeCreate && activity.Local == true
		})).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// TestUpdateVideo tests Update activity generation
func TestUpdateVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:      "video-123",
		Title:   "Updated Video Title",
		UserID:  "user-123",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusCompleted,
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	followers := []*domain.APFollower{
		{
			ActorID:    "user-123",
			FollowerID: "https://mastodon.example/users/alice",
			State:      "accepted",
		},
	}

	remoteActor := &domain.APRemoteActor{
		ActorURI: "https://mastodon.example/users/alice",
		InboxURL: "https://mastodon.example/users/alice/inbox",
	}

	t.Run("Sends Update activity when video metadata changes", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActor", ctx, followers[0].FollowerID).Return(remoteActor, nil).Once()
		mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeUpdate
		})).Return(nil).Once()

		err := service.UpdateVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Update activity contains updated VideoObject", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActor", ctx, followers[0].FollowerID).Return(remoteActor, nil).Once()
		mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.UpdateVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// TestDeleteVideo tests Delete activity generation
func TestDeleteVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, cfg)

	ctx := context.Background()

	video := &domain.Video{
		ID:      "video-123",
		Title:   "Deleted Video",
		UserID:  "user-123",
		Privacy: domain.PrivacyPublic,
	}

	user := &domain.User{
		ID:       "user-123",
		Username: "testuser",
	}

	followers := []*domain.APFollower{
		{
			ActorID:    "user-123",
			FollowerID: "https://mastodon.example/users/alice",
			State:      "accepted",
		},
	}

	remoteActor := &domain.APRemoteActor{
		ActorURI: "https://mastodon.example/users/alice",
		InboxURL: "https://mastodon.example/users/alice/inbox",
	}

	t.Run("Sends Delete activity when video is deleted", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActor", ctx, followers[0].FollowerID).Return(remoteActor, nil).Once()
		mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeDelete
		})).Return(nil).Once()

		err := service.DeleteVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Delete activity object is video URI (Tombstone)", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActor", ctx, followers[0].FollowerID).Return(remoteActor, nil).Once()
		mockAPRepo.On("EnqueueDelivery", ctx, mock.AnythingOfType("*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.DeleteVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// Helper function
func stringPtr(s string) *string {
	return &s
}
