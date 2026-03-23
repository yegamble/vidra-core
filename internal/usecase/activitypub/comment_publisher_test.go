package activitypub

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
)

func TestBuildNoteObject_Basic(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "This is a great video!",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Date(2025, 11, 16, 14, 30, 0, 0, time.UTC),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  "video-owner-123",
		Privacy: domain.PrivacyPublic,
	}

	t.Run("Converts basic comment fields correctly", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)
		require.NotNil(t, noteObject)

		assert.Equal(t, domain.ObjectTypeNote, noteObject.Type)
		assert.Equal(t, fmt.Sprintf("https://video.example/comments/%s", commentID.String()), noteObject.ID)
		assert.Equal(t, "This is a great video!", noteObject.Content)
		assert.NotNil(t, noteObject.Published)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Sets inReplyTo to video ActivityPub ID", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Equal(t, fmt.Sprintf("https://video.example/videos/%s", videoID.String()), noteObject.InReplyTo)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Includes attributedTo with commenter actor URI", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Equal(t, "https://video.example/users/commenter", noteObject.AttributedTo)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Returns error when user not found", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(nil, fmt.Errorf("user not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		assert.Error(t, err)
		assert.Nil(t, noteObject)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Returns error when video not found", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, fmt.Errorf("video not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		assert.Error(t, err)
		assert.Nil(t, noteObject)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})
}

func TestBuildNoteObject_NestedReplies(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	parentCommentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		ParentID:  &parentCommentID,
		Body:      "This is a reply to another comment",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Now(),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "replier",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  "video-owner-123",
		Privacy: domain.PrivacyPublic,
	}

	t.Run("Nested comment has inReplyTo pointing to parent comment", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Equal(t, fmt.Sprintf("https://video.example/comments/%s", parentCommentID.String()), noteObject.InReplyTo)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Includes tag for parent comment context", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)
		require.NotNil(t, noteObject)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})
}

func TestBuildNoteObject_Audience(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()
	videoOwnerID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "Test comment",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Now(),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	videoOwner := &domain.User{
		ID:       videoOwnerID.String(),
		Username: "videoowner",
	}

	t.Run("Public video comment has public audience", func(t *testing.T) {
		publicVideo := &domain.Video{
			ID:      videoID.String(),
			Title:   "Public Video",
			UserID:  videoOwnerID.String(),
			Privacy: domain.PrivacyPublic,
		}

		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(publicVideo, nil).Once()
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Contains(t, noteObject.To, "https://www.w3.org/ns/activitystreams#Public")

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Comment addresses video owner", func(t *testing.T) {
		publicVideo := &domain.Video{
			ID:      videoID.String(),
			Title:   "Public Video",
			UserID:  videoOwnerID.String(),
			Privacy: domain.PrivacyPublic,
		}

		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(publicVideo, nil).Once()
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Contains(t, noteObject.Cc, "https://video.example/users/videoowner")

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Unlisted video comment uses CC for public", func(t *testing.T) {
		unlistedVideo := &domain.Video{
			ID:      videoID.String(),
			Title:   "Unlisted Video",
			UserID:  videoOwnerID.String(),
			Privacy: domain.PrivacyUnlisted,
		}

		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(unlistedVideo, nil).Once()
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()

		noteObject, err := service.BuildNoteObject(ctx, comment)
		require.NoError(t, err)

		assert.Contains(t, noteObject.Cc, "https://www.w3.org/ns/activitystreams#Public")
		assert.NotContains(t, noteObject.To, "https://www.w3.org/ns/activitystreams#Public")

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})
}

func TestCreateCommentActivity(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "Test comment",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Now(),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  "video-owner-123",
		Privacy: domain.PrivacyPublic,
	}

	t.Run("Wraps Note in Create activity", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		activity, err := service.CreateCommentActivity(ctx, comment)
		require.NoError(t, err)
		require.NotNil(t, activity)

		assert.Equal(t, domain.ActivityTypeCreate, activity.Type)
		assert.Equal(t, "https://video.example/users/commenter", activity.Actor)
		assert.Equal(t, &comment.CreatedAt, activity.Published)

		note, ok := activity.Object.(*domain.NoteObject)
		require.True(t, ok)
		assert.Equal(t, domain.ObjectTypeNote, note.Type)
		assert.Equal(t, "Test comment", note.Content)
		assert.Equal(t, "https://video.example/users/commenter", note.AttributedTo)
		assert.Equal(t, fmt.Sprintf("https://video.example/videos/%s", videoID.String()), note.InReplyTo)

		assert.Equal(t, note.To, activity.To)
		assert.Equal(t, note.Cc, activity.Cc)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("Activity has unique ID", func(t *testing.T) {
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, "video-owner-123").Return(nil, fmt.Errorf("not found")).Once()

		activity, err := service.CreateCommentActivity(ctx, comment)
		require.NoError(t, err)

		assert.Contains(t, activity.ID, "https://video.example/activities/")
		assert.NotEmpty(t, activity.ID)

		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
	})
}

func TestPublishComment(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()
	videoOwnerID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "Test comment",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Now(),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	videoOwner := &domain.User{
		ID:       videoOwnerID.String(),
		Username: "videoowner",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  videoOwnerID.String(),
		Privacy: domain.PrivacyPublic,
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

	t.Run("Delivers comment to video owner", func(t *testing.T) {
		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Times(2)
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()

		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{}, 0, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, mock.Anything).Return([]*domain.APRemoteActor{}, nil).Maybe()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Delivers comment to video followers", func(t *testing.T) {
		mockAPRepo := new(MockActivityPubRepository)
		mockUserRepo := new(MockUserRepository)
		mockVideoRepo := new(MockVideoRepository)
		mockCommentRepo := new(MockCommentRepository)

		service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Times(2)
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return(videoOwnerFollowers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, mock.Anything).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.AnythingOfType("[]*domain.APDeliveryQueue")).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.PublishComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Does not publish deleted comments", func(t *testing.T) {
		deletedComment := &domain.Comment{
			ID:        commentID,
			VideoID:   videoID,
			UserID:    userID,
			Body:      "Deleted comment",
			Status:    domain.CommentStatusDeleted,
			CreatedAt: time.Now(),
		}

		mockCommentRepo.On("GetByID", ctx, commentID).Return(deletedComment, nil).Once()

		err := service.PublishComment(ctx, commentID.String())

		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot publish deleted comment")

		mockCommentRepo.AssertExpectations(t)
	})
}

func TestUpdateComment(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()
	videoOwnerID := uuid.New()
	editedTime := time.Now()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "Edited comment text",
		Status:    domain.CommentStatusActive,
		CreatedAt: time.Now().Add(-1 * time.Hour),
		EditedAt:  &editedTime,
	}
	assert.Equal(t, "Edited comment text", comment.Body)
	assert.NotNil(t, comment.EditedAt)

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	videoOwner := &domain.User{
		ID:       videoOwnerID.String(),
		Username: "videoowner",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  videoOwnerID.String(),
		Privacy: domain.PrivacyPublic,
	}

	t.Run("Sends Update activity when comment is edited", func(t *testing.T) {
		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Times(2)
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{}, 0, nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeUpdate
		})).Return(nil).Once()

		err := service.UpdateComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Update activity includes updated timestamp", func(t *testing.T) {
		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Times(2)
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Times(2)
		mockUserRepo.On("GetByID", ctx, videoOwnerID.String()).Return(videoOwner, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{}, 0, nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.UpdateComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

func TestDeleteComment(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	ctx := context.Background()

	commentID := uuid.New()
	videoID := uuid.New()
	userID := uuid.New()
	videoOwnerID := uuid.New()

	comment := &domain.Comment{
		ID:        commentID,
		VideoID:   videoID,
		UserID:    userID,
		Body:      "Deleted comment",
		Status:    domain.CommentStatusDeleted,
		CreatedAt: time.Now(),
	}

	user := &domain.User{
		ID:       userID.String(),
		Username: "commenter",
	}

	video := &domain.Video{
		ID:      videoID.String(),
		Title:   "Test Video",
		UserID:  videoOwnerID.String(),
		Privacy: domain.PrivacyPublic,
	}

	t.Run("Sends Delete activity when comment is deleted", func(t *testing.T) {
		mockAPRepo := new(MockActivityPubRepository)
		mockUserRepo := new(MockUserRepository)
		mockVideoRepo := new(MockVideoRepository)
		mockCommentRepo := new(MockCommentRepository)

		service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{}, 0, nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeDelete
		})).Return(nil).Once()

		err := service.DeleteComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Delete activity object is comment URI", func(t *testing.T) {
		mockAPRepo := new(MockActivityPubRepository)
		mockUserRepo := new(MockUserRepository)
		mockVideoRepo := new(MockVideoRepository)
		mockCommentRepo := new(MockCommentRepository)

		service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

		mockCommentRepo.On("GetByID", ctx, commentID).Return(comment, nil).Once()
		mockUserRepo.On("GetByID", ctx, userID.String()).Return(user, nil).Once()
		mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(video, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, videoOwnerID.String(), "accepted", mock.Anything, mock.Anything).Return([]*domain.APFollower{}, 0, nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.AnythingOfType("*domain.APActivity")).Return(nil).Once()

		err := service.DeleteComment(ctx, commentID.String())
		require.NoError(t, err)

		mockCommentRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockVideoRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}
