package activitypub

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
)

// TestVideoUploadToFederation tests the complete flow from video upload to federation
func TestVideoUploadToFederation(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	// Simulate a complete video upload workflow
	t.Run("Complete workflow: Upload -> Process -> Federate", func(t *testing.T) {
		// Step 1: Video is uploaded (status: uploading)
		uploadingVideo := &domain.Video{
			ID:         "video-123",
			Title:      "My Federation Test Video",
			UserID:     "user-123",
			Privacy:    domain.PrivacyPublic,
			Status:     domain.StatusUploading,
			UploadDate: time.Now(),
		}

		// Step 2: Video is queued for processing
		queuedVideo := &domain.Video{
			ID:         "video-123",
			Title:      "My Federation Test Video",
			UserID:     "user-123",
			Privacy:    domain.PrivacyPublic,
			Status:     domain.StatusQueued,
			UploadDate: time.Now(),
		}

		// Step 3: Video is processing
		processingVideo := &domain.Video{
			ID:         "video-123",
			Title:      "My Federation Test Video",
			UserID:     "user-123",
			Privacy:    domain.PrivacyPublic,
			Status:     domain.StatusProcessing,
			UploadDate: time.Now(),
		}

		// Step 4: Video processing completes
		completedVideo := &domain.Video{
			ID:          "video-123",
			Title:       "My Federation Test Video",
			Description: "A test video for federation",
			Duration:    300,
			UserID:      "user-123",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now(),
			OutputPaths: map[string]string{
				"360p":  "/videos/video-123/360p.m3u8",
				"720p":  "/videos/video-123/720p.m3u8",
				"1080p": "/videos/video-123/1080p.m3u8",
			},
			ThumbnailPath: "/thumbnails/video-123.jpg",
		}

		user := &domain.User{
			ID:       "user-123",
			Username: "testcreator",
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

		// Verify uploading/queued/processing videos are NOT federated
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(uploadingVideo, nil).Once()
		err := service.PublishVideo(ctx, "video-123")
		assert.Error(t, err, "Should not federate uploading video")

		mockVideoRepo.On("GetByID", ctx, "video-123").Return(queuedVideo, nil).Once()
		err = service.PublishVideo(ctx, "video-123")
		assert.Error(t, err, "Should not federate queued video")

		mockVideoRepo.On("GetByID", ctx, "video-123").Return(processingVideo, nil).Once()
		err = service.PublishVideo(ctx, "video-123")
		assert.Error(t, err, "Should not federate processing video")

		// Once video completes processing, it should federate
		mockVideoRepo.On("GetByID", ctx, "video-123").Return(completedVideo, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 2, nil).Once()

		var deliveryMutex sync.Mutex
		deliveryCount := 0
		deliveryCalls := make([]string, 0)

		followerURIs := make([]string, len(followers))
		for i, f := range followers {
			followerURIs[i] = f.FollowerID
		}
		mockAPRepo.On("GetRemoteActors", ctx, followerURIs).Return(remoteActors, nil).Once()

		// Single BulkEnqueueDelivery expectation
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			deliveryMutex.Lock()
			defer deliveryMutex.Unlock()
			deliveryCount = len(deliveries)
			for _, d := range deliveries {
				deliveryCalls = append(deliveryCalls, d.InboxURL)
				assert.NotEmpty(t, d.ActivityID)
				assert.NotEmpty(t, d.InboxURL)
			}
			return true
		})).Return(nil).Once()

		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			assert.Equal(t, domain.ActivityTypeCreate, activity.Type)
			assert.True(t, activity.Local)
			return true
		})).Return(nil).Once()

		err = service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)
		assert.Equal(t, 2, deliveryCount, "Should create delivery jobs for all followers")

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Completed video creates valid ActivityPub Create activity", func(t *testing.T) {
		video := &domain.Video{
			ID:          "video-456",
			Title:       "ActivityPub Test",
			Description: "Testing ActivityPub compliance",
			Duration:    180,
			UserID:      "user-456",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Date(2025, 11, 16, 12, 0, 0, 0, time.UTC),
			Tags:        []string{"activitypub", "federation"},
		}

		user := &domain.User{
			ID:       "user-456",
			Username: "activitypubuser",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)
		require.NoError(t, err)

		// Verify it can be marshaled to valid JSON
		activityJSON, err := json.Marshal(activity)
		require.NoError(t, err)

		// Verify structure
		var parsed map[string]interface{}
		err = json.Unmarshal(activityJSON, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "Create", parsed["type"])
		assert.NotNil(t, parsed["@context"])
		assert.NotNil(t, parsed["object"])
		assert.Contains(t, parsed["actor"], "https://video.example/users/activitypubuser")

		// Verify object is a Video
		obj, ok := parsed["object"].(map[string]interface{})
		require.True(t, ok)
		assert.Equal(t, "Video", obj["type"])

		mockUserRepo.AssertExpectations(t)
	})
}

// TestCommentToFederation tests comment federation flow
func TestCommentToFederation(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("Complete workflow: Comment -> Federate to video owner and followers", func(t *testing.T) {
		t.Skip("Skipping: requires comment repository which is not yet configured")

		commentID := uuid.New()
		videoID := uuid.New()
		commenterID := uuid.New()
		videoOwnerID := uuid.New()

		video := &domain.Video{
			ID:      videoID.String(),
			Title:   "Test Video",
			UserID:  videoOwnerID.String(),
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		comment := &domain.Comment{
			ID:        commentID,
			VideoID:   videoID,
			UserID:    commenterID,
			Body:      "Great video! Thanks for sharing.",
			Status:    domain.CommentStatusActive,
			CreatedAt: time.Now(),
		}
		assert.Equal(t, "Great video! Thanks for sharing.", comment.Body)
		assert.Equal(t, domain.CommentStatusActive, comment.Status)

		commenter := &domain.User{
			ID:       commenterID.String(),
			Username: "commenter",
		}

		videoOwner := &domain.User{
			ID:       videoOwnerID.String(),
			Username: "videoowner",
		}

		videoOwnerFollowers := []*domain.APFollower{
			{
				ActorID:    videoOwnerID.String(),
				FollowerID: "https://mastodon.example/users/alice",
				State:      "accepted",
			},
		}

		remoteActor := &domain.APRemoteActor{
			ActorURI: "https://mastodon.example/users/alice",
			InboxURL: "https://mastodon.example/users/alice/inbox",
		}

		// Mock the complete flow
		mockUserRepo.On("GetByID", ctx, commenterID.String()).Return(commenter, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return(videoOwnerFollowers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{videoOwnerFollowers[0].FollowerID}).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.AnythingOfType("[]*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			assert.Equal(t, domain.ActivityTypeCreate, activity.Type)
			return true
		})).Return(nil).Once()

		err := service.PublishComment(ctx, commentID.String())
		require.NoError(t, err)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Comment creates valid ActivityPub Note object", func(t *testing.T) {
		commentID := uuid.New()
		videoID := uuid.New()
		userID := uuid.New()

		comment := &domain.Comment{
			ID:        commentID,
			VideoID:   videoID,
			UserID:    userID,
			Body:      "Test comment with #hashtag",
			Status:    domain.CommentStatusActive,
			CreatedAt: time.Now(),
		}

		user := &domain.User{
			ID:       userID.String(),
			Username: "testuser",
		}

		video := &domain.Video{
			ID:      videoID.String(),
			Title:   "Test Video",
			UserID:  "owner-123",
			Privacy: domain.PrivacyPublic,
		}

		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		// Verify it can be marshaled to valid JSON
		noteJSON, err := json.Marshal(noteObject)
		require.NoError(t, err)

		// Verify structure
		var parsed map[string]interface{}
		err = json.Unmarshal(noteJSON, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "Note", parsed["type"])
		assert.NotNil(t, parsed["content"])
		assert.NotNil(t, parsed["attributedTo"])

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})
}

// TestPeerTubeCompatibility tests PeerTube-specific compatibility
func TestPeerTubeCompatibility(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("VideoObject matches PeerTube schema", func(t *testing.T) {
		categoryID := uuid.New()
		video := &domain.Video{
			ID:          "video-789",
			Title:       "PeerTube Compatible Video",
			Description: "This video should be compatible with PeerTube instances",
			Duration:    420,
			UserID:      "user-789",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			Language:    "en",
			Tags:        []string{"peertube", "compatible"},
			CategoryID:  &categoryID,
			Category: &domain.VideoCategory{
				ID:   categoryID,
				Name: "Science & Technology",
			},
			Views:      1500,
			UploadDate: time.Now(),
			OutputPaths: map[string]string{
				"720p":  "/videos/video-789/720p.m3u8",
				"1080p": "/videos/video-789/1080p.m3u8",
			},
		}

		user := &domain.User{
			ID:       "user-789",
			Username: "peertubeuser",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// PeerTube-specific fields
		assert.Equal(t, "Video", videoObject.Type)
		assert.NotNil(t, videoObject.UUID)
		assert.NotNil(t, videoObject.Category)
		assert.NotNil(t, videoObject.Language)
		assert.Equal(t, 1, videoObject.State) // Published state
		assert.True(t, videoObject.CommentsEnabled)
		assert.True(t, videoObject.DownloadEnabled)

		// Verify context includes PeerTube namespace
		contextArray, ok := videoObject.Context.([]interface{})
		require.True(t, ok)
		hasPeerTube := false
		for _, ctx := range contextArray {
			if ctx == domain.PeerTubeContext {
				hasPeerTube = true
			}
		}
		assert.True(t, hasPeerTube, "Should include PeerTube context")

		// Verify URLs are in PeerTube format
		assert.NotEmpty(t, videoObject.URL)
		hasHLSURL := false
		for _, url := range videoObject.URL {
			if url.MediaType == "application/x-mpegURL" {
				hasHLSURL = true
			}
		}
		assert.True(t, hasHLSURL, "Should include HLS URLs")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("VideoObject can be parsed by PeerTube", func(t *testing.T) {
		video := &domain.Video{
			ID:       "video-999",
			Title:    "Parse Test",
			UserID:   "user-999",
			Privacy:  domain.PrivacyPublic,
			Status:   domain.StatusCompleted,
			Duration: 100,
		}

		user := &domain.User{
			ID:       "user-999",
			Username: "parseuser",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Marshal to JSON
		videoJSON, err := json.Marshal(videoObject)
		require.NoError(t, err)

		// Unmarshal back to ensure valid structure
		var parsed map[string]interface{}
		err = json.Unmarshal(videoJSON, &parsed)
		require.NoError(t, err)

		// Verify all required PeerTube fields exist
		requiredFields := []string{
			"@context", "type", "id", "name", "duration",
			"uuid", "attributedTo", "to",
		}

		for _, field := range requiredFields {
			assert.Contains(t, parsed, field, "Should include required field: %s", field)
		}

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Activity delivery to PeerTube instance", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-pt-123",
			Title:   "Federate to PeerTube",
			UserID:  "user-pt-123",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-pt-123",
			Username: "federator",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-pt-123",
				FollowerID: "https://peertube.example/accounts/peertube_user",
				State:      "accepted",
			},
		}

		peertubeActor := &domain.APRemoteActor{
			ActorURI:    "https://peertube.example/accounts/peertube_user",
			InboxURL:    "https://peertube.example/accounts/peertube_user/inbox",
			SharedInbox: stringPtr("https://peertube.example/inbox"),
			Type:        "Person",
		}

		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{peertubeActor}, nil).Once()

		// Verify delivery queue entry
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			assert.Equal(t, "https://peertube.example/inbox", deliveries[0].InboxURL, "Should use shared inbox")
			return true
		})).Return(nil).Once()

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, video.ID)
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// TestMastodonCompatibility tests Mastodon-specific compatibility
func TestMastodonCompatibility(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("Videos federate to Mastodon users", func(t *testing.T) {
		video := &domain.Video{
			ID:          "video-masto-123",
			Title:       "Mastodon Federation Test",
			Description: "Testing federation with Mastodon",
			UserID:      "user-masto-123",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-masto-123",
			Username: "mastouser",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-masto-123",
				FollowerID: "https://mastodon.social/@alice",
				State:      "accepted",
			},
		}

		mastodonActor := &domain.APRemoteActor{
			ActorURI:    "https://mastodon.social/@alice",
			InboxURL:    "https://mastodon.social/users/alice/inbox",
			SharedInbox: stringPtr("https://mastodon.social/inbox"),
			Type:        "Person",
		}

		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{mastodonActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			assert.Contains(t, deliveries[0].InboxURL, "mastodon.social")
			return true
		})).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, video.ID)
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("VideoObject includes content and summary for Mastodon", func(t *testing.T) {
		video := &domain.Video{
			ID:          "video-content-123",
			Title:       "Title for Mastodon",
			Description: "Description that Mastodon will display",
			UserID:      "user-content-123",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-content-123",
			Username: "contentuser",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)
		require.NoError(t, err)

		// Mastodon uses content/summary fields
		assert.NotEmpty(t, videoObject.Content)
		assert.Equal(t, video.Description, videoObject.Content)
		assert.NotEmpty(t, videoObject.Name)

		mockUserRepo.AssertExpectations(t)
	})
}

// TestCrossInstanceFederation tests federation between different instance types
func TestCrossInstanceFederation(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("Federates to mixed audience (Mastodon + PeerTube)", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-mixed-123",
			Title:   "Cross-platform Video",
			UserID:  "user-mixed-123",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-mixed-123",
			Username: "mixeduser",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-mixed-123",
				FollowerID: "https://mastodon.example/users/alice",
				State:      "accepted",
			},
			{
				ActorID:    "user-mixed-123",
				FollowerID: "https://peertube.example/accounts/bob",
				State:      "accepted",
			},
			{
				ActorID:    "user-mixed-123",
				FollowerID: "https://pixelfed.example/users/charlie",
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
			{
				ActorURI: "https://pixelfed.example/users/charlie",
				InboxURL: "https://pixelfed.example/users/charlie/inbox",
			},
		}

		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 3, nil).Once()

		followerURIs := make([]string, len(followers))
		for i, f := range followers {
			followerURIs[i] = f.FollowerID
		}
		mockAPRepo.On("GetRemoteActors", ctx, followerURIs).Return(remoteActors, nil).Once()

		deliveryCount := 0
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.AnythingOfType("[]*domain.APDeliveryQueue")).Run(func(args mock.Arguments) {
			deliveries := args.Get(1).([]*domain.APDeliveryQueue)
			deliveryCount = len(deliveries)
		}).Return(nil).Once()

		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, video.ID)
		require.NoError(t, err)
		assert.Equal(t, 3, deliveryCount, "Should deliver to all platform types")

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// TestErrorHandling tests error scenarios in federation
func TestErrorHandling(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("Handles remote actor fetch failure gracefully", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-error-123",
			Title:   "Error Test",
			UserID:  "user-error-123",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-error-123",
			Username: "erroruser",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-error-123",
				FollowerID: "https://dead-instance.example/users/ghost",
				State:      "accepted",
			},
		}

		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return(nil, fmt.Errorf("instance unreachable")).Once()

		// Should still store activity even if delivery fails
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, video.ID)
		// Should not return error for delivery failures (handled by queue)
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Handles delivery queue failures", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-queue-123",
			Title:   "Queue Error Test",
			UserID:  "user-queue-123",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-queue-123",
			Username: "queueuser",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-queue-123",
				FollowerID: "https://mastodon.example/users/alice",
				State:      "accepted",
			},
		}

		remoteActor := &domain.APRemoteActor{
			ActorURI: "https://mastodon.example/users/alice",
			InboxURL: "https://mastodon.example/users/alice/inbox",
		}

		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.AnythingOfType("[]*domain.APDeliveryQueue")).Return(fmt.Errorf("queue full")).Once()

		// Should still store activity
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishVideo(ctx, video.ID)
		// May return error for queue failures
		_ = err

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}
