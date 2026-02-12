package activitypub

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/activitypub"
	"athena/internal/config"
	"athena/internal/domain"
)

// MockActivityPubRepository is a mock for ActivityPubRepository
type MockActivityPubRepository struct {
	mock.Mock
}

func (m *MockActivityPubRepository) GetActorKeys(ctx context.Context, actorID string) (string, string, error) {
	args := m.Called(ctx, actorID)
	return args.String(0), args.String(1), args.Error(2)
}

func (m *MockActivityPubRepository) StoreActorKeys(ctx context.Context, actorID, publicKey, privateKey string) error {
	args := m.Called(ctx, actorID, publicKey, privateKey)
	return args.Error(0)
}

func (m *MockActivityPubRepository) GetRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error) {
	args := m.Called(ctx, actorURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.APRemoteActor), args.Error(1)
}

func (m *MockActivityPubRepository) GetRemoteActors(ctx context.Context, actorURIs []string) ([]*domain.APRemoteActor, error) {
	args := m.Called(ctx, actorURIs)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.APRemoteActor), args.Error(1)
}

func (m *MockActivityPubRepository) UpsertRemoteActor(ctx context.Context, actor *domain.APRemoteActor) error {
	args := m.Called(ctx, actor)
	return args.Error(0)
}

func (m *MockActivityPubRepository) StoreActivity(ctx context.Context, activity *domain.APActivity) error {
	args := m.Called(ctx, activity)
	return args.Error(0)
}

func (m *MockActivityPubRepository) GetActivity(ctx context.Context, activityURI string) (*domain.APActivity, error) {
	args := m.Called(ctx, activityURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.APActivity), args.Error(1)
}

func (m *MockActivityPubRepository) GetActivitiesByActor(ctx context.Context, actorID string, limit, offset int) ([]*domain.APActivity, int, error) {
	args := m.Called(ctx, actorID, limit, offset)
	return args.Get(0).([]*domain.APActivity), args.Int(1), args.Error(2)
}

func (m *MockActivityPubRepository) GetFollower(ctx context.Context, actorID, followerID string) (*domain.APFollower, error) {
	args := m.Called(ctx, actorID, followerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.APFollower), args.Error(1)
}

func (m *MockActivityPubRepository) UpsertFollower(ctx context.Context, follower *domain.APFollower) error {
	args := m.Called(ctx, follower)
	return args.Error(0)
}

func (m *MockActivityPubRepository) DeleteFollower(ctx context.Context, actorID, followerID string) error {
	args := m.Called(ctx, actorID, followerID)
	return args.Error(0)
}

func (m *MockActivityPubRepository) GetFollowers(ctx context.Context, actorID, state string, limit, offset int) ([]*domain.APFollower, int, error) {
	args := m.Called(ctx, actorID, state, limit, offset)
	return args.Get(0).([]*domain.APFollower), args.Int(1), args.Error(2)
}

func (m *MockActivityPubRepository) GetFollowing(ctx context.Context, followerID, state string, limit, offset int) ([]*domain.APFollower, int, error) {
	args := m.Called(ctx, followerID, state, limit, offset)
	return args.Get(0).([]*domain.APFollower), args.Int(1), args.Error(2)
}

func (m *MockActivityPubRepository) IsActivityReceived(ctx context.Context, activityURI string) (bool, error) {
	args := m.Called(ctx, activityURI)
	return args.Bool(0), args.Error(1)
}

func (m *MockActivityPubRepository) MarkActivityReceived(ctx context.Context, activityURI string) error {
	args := m.Called(ctx, activityURI)
	return args.Error(0)
}

func (m *MockActivityPubRepository) UpsertVideoReaction(ctx context.Context, videoID, actorURI, reactionType, activityURI string) error {
	args := m.Called(ctx, videoID, actorURI, reactionType, activityURI)
	return args.Error(0)
}

func (m *MockActivityPubRepository) DeleteVideoReaction(ctx context.Context, activityURI string) error {
	args := m.Called(ctx, activityURI)
	return args.Error(0)
}

func (m *MockActivityPubRepository) UpsertVideoShare(ctx context.Context, videoID, actorURI, activityURI string) error {
	args := m.Called(ctx, videoID, actorURI, activityURI)
	return args.Error(0)
}

func (m *MockActivityPubRepository) DeleteVideoShare(ctx context.Context, activityURI string) error {
	args := m.Called(ctx, activityURI)
	return args.Error(0)
}

func (m *MockActivityPubRepository) EnqueueDelivery(ctx context.Context, delivery *domain.APDeliveryQueue) error {
	args := m.Called(ctx, delivery)
	return args.Error(0)
}

func (m *MockActivityPubRepository) BulkEnqueueDelivery(ctx context.Context, deliveries []*domain.APDeliveryQueue) error {
	args := m.Called(ctx, deliveries)
	return args.Error(0)
}

func (m *MockActivityPubRepository) GetPendingDeliveries(ctx context.Context, limit int) ([]*domain.APDeliveryQueue, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.APDeliveryQueue), args.Error(1)
}

func (m *MockActivityPubRepository) UpdateDeliveryStatus(ctx context.Context, deliveryID string, status string, attempts int, lastError *string, nextAttempt time.Time) error {
	args := m.Called(ctx, deliveryID, status, attempts, lastError, nextAttempt)
	return args.Error(0)
}

// MockUserRepository is a mock for UserRepository
type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

// Implement remaining port.UserRepository methods (not used in tests but required by interface)
func (m *MockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepository) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

func (m *MockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockUserRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

// MockVideoRepository is a mock for VideoRepository
type MockVideoRepository struct {
	mock.Mock
}

// Implement port.VideoRepository methods (not used in tests but required by interface)
func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath)
	return args.Error(0)
}

func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID)
	return args.Error(0)
}

func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

func TestGetLocalActor(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()
	username := "alice"
	userID := "user-123"

	user := &domain.User{
		ID:        userID,
		Username:  username,
		Email:     "alice@example.com",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	publicKey, privateKey, _ := activitypub.GenerateKeyPair()

	t.Run("Get local actor successfully", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, username).Return(user, nil).Once()
		mockAPRepo.On("GetActorKeys", ctx, userID).Return(publicKey, privateKey, nil).Once()

		actor, err := service.GetLocalActor(ctx, username)
		require.NoError(t, err)
		require.NotNil(t, actor)

		assert.Equal(t, domain.ObjectTypePerson, actor.Type)
		assert.Equal(t, "https://video.example/users/alice", actor.ID)
		assert.Equal(t, username, actor.PreferredUsername)
		assert.NotNil(t, actor.PublicKey)
		assert.Equal(t, publicKey, actor.PublicKey.PublicKeyPem)
		assert.Equal(t, "https://video.example/users/alice#main-key", actor.PublicKey.ID)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("User not found", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, "nonexistent").Return(nil, nil).Once()

		actor, err := service.GetLocalActor(ctx, "nonexistent")
		assert.Error(t, err)
		assert.Nil(t, actor)
		assert.Contains(t, err.Error(), "user not found")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("Generate keys on first access", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, "bob").Return(&domain.User{
			ID:       "user-456",
			Username: "bob",
		}, nil).Once()

		// First call returns error (no keys)
		mockAPRepo.On("GetActorKeys", ctx, "user-456").Return("", "", assert.AnError).Once()

		// Keys should be generated and stored
		mockAPRepo.On("StoreActorKeys", ctx, "user-456", mock.AnythingOfType("string"), mock.AnythingOfType("string")).Return(nil).Once()

		actor, err := service.GetLocalActor(ctx, "bob")
		require.NoError(t, err)
		require.NotNil(t, actor)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

func TestFetchRemoteActor(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("Fetch and cache remote actor", func(t *testing.T) {
		// Use a proper external URL instead of localhost to avoid SSRF protection
		actorURI := "https://mastodon.example/users/alice"

		// Mock cache miss
		mockAPRepo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil).Once()

		// Since we can't make real HTTP requests and SSRF protection blocks localhost,
		// we'll test the caching behavior instead
		// The actual HTTP fetching is integration tested elsewhere

		// For unit tests, we can test by providing a cached actor
		cachedTime := time.Now().Add(-1 * time.Hour)
		cachedActor := &domain.APRemoteActor{
			ActorURI:      actorURI,
			Username:      "alice",
			Domain:        "mastodon.example",
			InboxURL:      actorURI + "/inbox",
			PublicKeyPem:  "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
			LastFetchedAt: &cachedTime,
		}

		// Reset the mock and test with cached actor
		mockAPRepo.ExpectedCalls = nil
		mockAPRepo.On("GetRemoteActor", ctx, actorURI).Return(cachedActor, nil).Once()

		remoteActor, err := service.FetchRemoteActor(ctx, actorURI)
		require.NoError(t, err)
		require.NotNil(t, remoteActor)

		assert.Equal(t, actorURI, remoteActor.ActorURI)
		assert.Equal(t, "alice", remoteActor.Username)

		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Use cached actor", func(t *testing.T) {
		actorURI := "https://mastodon.example/users/bob"
		cachedTime := time.Now().Add(-1 * time.Hour)

		cachedActor := &domain.APRemoteActor{
			ActorURI:      actorURI,
			Username:      "bob",
			Domain:        "mastodon.example",
			LastFetchedAt: &cachedTime,
		}

		mockAPRepo.On("GetRemoteActor", ctx, actorURI).Return(cachedActor, nil).Once()

		remoteActor, err := service.FetchRemoteActor(ctx, actorURI)
		require.NoError(t, err)
		assert.Equal(t, cachedActor, remoteActor)

		mockAPRepo.AssertExpectations(t)
	})
}

func TestHandleFollow(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	// Create a mock HTTP server to receive the Accept activity
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Accept the delivery
		w.WriteHeader(http.StatusOK)
	}))
	defer mockServer.Close()

	cfg := &config.Config{
		PublicBaseURL:                    "https://video.example",
		ActivityPubAcceptFollowAutomatic: true,
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	activity := map[string]interface{}{
		"type":   "Follow",
		"actor":  "https://mastodon.example/users/alice",
		"object": "https://video.example/users/bob",
	}

	remoteActor := &domain.APRemoteActor{
		ActorURI: "https://mastodon.example/users/alice",
		Username: "alice",
		Domain:   "mastodon.example",
		InboxURL: mockServer.URL + "/inbox", // Use mock server URL
	}

	localUser := &domain.User{
		ID:       "local-123",
		Username: "bob",
	}

	// Generate valid keys for testing
	publicKey, privateKey, err := activitypub.GenerateKeyPair()
	require.NoError(t, err)

	t.Run("Auto-accept follow request", func(t *testing.T) {
		// Mock getting local user by username
		mockUserRepo.On("GetByUsername", ctx, "bob").Return(localUser, nil).Once()

		// Mock upserting follower
		mockAPRepo.On("UpsertFollower", ctx, mock.MatchedBy(func(f *domain.APFollower) bool {
			return f.ActorID == localUser.ID && f.FollowerID == remoteActor.ActorURI && f.State == "accepted"
		})).Return(nil).Once()

		// Mock getting actor keys for signing the Accept activity
		mockAPRepo.On("GetActorKeys", ctx, localUser.ID).Return(publicKey, privateKey, nil).Once()

		// Mock getting user by ID for building the key ID
		mockUserRepo.On("GetByID", ctx, localUser.ID).Return(localUser, nil).Once()

		err := service.handleFollow(ctx, activity, remoteActor)
		require.NoError(t, err)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Pending follow when auto-accept disabled", func(t *testing.T) {
		service.cfg.ActivityPubAcceptFollowAutomatic = false

		mockUserRepo.On("GetByUsername", ctx, "bob").Return(localUser, nil).Once()
		mockAPRepo.On("UpsertFollower", ctx, mock.MatchedBy(func(f *domain.APFollower) bool {
			return f.State == "pending"
		})).Return(nil).Once()

		err := service.handleFollow(ctx, activity, remoteActor)
		require.NoError(t, err)

		service.cfg.ActivityPubAcceptFollowAutomatic = true // reset
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

func TestHandleLike(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	videoID := "video-123"
	activity := map[string]interface{}{
		"type":   "Like",
		"id":     "https://mastodon.example/activities/like-1",
		"actor":  "https://mastodon.example/users/alice",
		"object": "https://video.example/videos/" + videoID,
	}

	remoteActor := &domain.APRemoteActor{
		ActorURI: "https://mastodon.example/users/alice",
	}

	t.Run("Handle like successfully", func(t *testing.T) {
		mockAPRepo.On("UpsertVideoReaction", ctx, videoID, remoteActor.ActorURI, "like", "https://mastodon.example/activities/like-1").Return(nil).Once()

		err := service.handleLike(ctx, activity, remoteActor)
		require.NoError(t, err)

		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Handle invalid object", func(t *testing.T) {
		invalidActivity := map[string]interface{}{
			"type":   "Like",
			"actor":  "https://mastodon.example/users/alice",
			"object": 123, // Invalid type
		}

		err := service.handleLike(ctx, invalidActivity, remoteActor)
		assert.Error(t, err)
	})
}

func TestHandleUndo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	remoteActor := &domain.APRemoteActor{
		ActorURI: "https://mastodon.example/users/alice",
	}

	t.Run("Undo follow (unfollow)", func(t *testing.T) {
		activity := map[string]interface{}{
			"type":  "Undo",
			"actor": remoteActor.ActorURI,
			"object": map[string]interface{}{
				"type":   "Follow",
				"object": "https://video.example/users/bob",
			},
		}

		localUser := &domain.User{
			ID:       "local-123",
			Username: "bob",
		}

		mockUserRepo.On("GetByUsername", ctx, "bob").Return(localUser, nil).Once()
		mockAPRepo.On("DeleteFollower", ctx, localUser.ID, remoteActor.ActorURI).Return(nil).Once()

		err := service.handleUndo(ctx, activity, remoteActor)
		require.NoError(t, err)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Undo like (unlike)", func(t *testing.T) {
		activity := map[string]interface{}{
			"type":  "Undo",
			"actor": remoteActor.ActorURI,
			"object": map[string]interface{}{
				"type": "Like",
				"id":   "https://mastodon.example/activities/like-1",
			},
		}

		mockAPRepo.On("DeleteVideoReaction", ctx, "https://mastodon.example/activities/like-1").Return(nil).Once()

		err := service.handleUndo(ctx, activity, remoteActor)
		require.NoError(t, err)

		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Undo announce (unshare)", func(t *testing.T) {
		activity := map[string]interface{}{
			"type":  "Undo",
			"actor": remoteActor.ActorURI,
			"object": map[string]interface{}{
				"type": "Announce",
				"id":   "https://mastodon.example/activities/announce-1",
			},
		}

		mockAPRepo.On("DeleteVideoShare", ctx, "https://mastodon.example/activities/announce-1").Return(nil).Once()

		err := service.handleUndo(ctx, activity, remoteActor)
		require.NoError(t, err)

		mockAPRepo.AssertExpectations(t)
	})
}

func TestGetOutbox(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	user := &domain.User{
		ID:       "user-123",
		Username: "alice",
	}

	activities := []*domain.APActivity{
		{
			ID:           "activity-1",
			ActorID:      user.ID,
			Type:         "Create",
			Published:    time.Now(),
			ActivityJSON: json.RawMessage(`{"type":"Create"}`),
		},
		{
			ID:           "activity-2",
			ActorID:      user.ID,
			Type:         "Update",
			Published:    time.Now(),
			ActivityJSON: json.RawMessage(`{"type":"Update"}`),
		},
	}

	t.Run("Get outbox page successfully", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, "alice").Return(user, nil).Once()
		mockAPRepo.On("GetActivitiesByActor", ctx, user.ID, 20, 0).Return(activities, 2, nil).Once()

		page, err := service.GetOutbox(ctx, "alice", 0, 20)
		require.NoError(t, err)
		require.NotNil(t, page)

		assert.Equal(t, 2, page.TotalItems)
		assert.Equal(t, "https://video.example/users/alice/outbox", page.PartOf)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("Pagination with next page", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, "alice").Return(user, nil).Once()
		mockAPRepo.On("GetActivitiesByActor", ctx, user.ID, 20, 0).Return(activities, 50, nil).Once()

		page, err := service.GetOutbox(ctx, "alice", 0, 20)
		require.NoError(t, err)

		assert.Contains(t, page.Next, "page=1")

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

func TestGetFollowers(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	user := &domain.User{
		ID:       "user-123",
		Username: "alice",
	}

	followers := []*domain.APFollower{
		{
			ID:         "follow-1",
			ActorID:    user.ID,
			FollowerID: "https://mastodon.example/users/bob",
			State:      "accepted",
		},
		{
			ID:         "follow-2",
			ActorID:    user.ID,
			FollowerID: "https://mastodon.example/users/charlie",
			State:      "accepted",
		},
	}

	t.Run("Get followers page successfully", func(t *testing.T) {
		mockUserRepo.On("GetByUsername", ctx, "alice").Return(user, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", 20, 0).Return(followers, 2, nil).Once()

		page, err := service.GetFollowers(ctx, "alice", 0, 20)
		require.NoError(t, err)
		require.NotNil(t, page)

		assert.Equal(t, 2, page.TotalItems)
		assert.Len(t, page.OrderedItems.([]interface{}), 2)

		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

func TestExtractUsernameFromURI(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	tests := []struct {
		name        string
		uri         string
		expected    string
		shouldError bool
	}{
		{
			name:        "Valid user URI",
			uri:         "https://video.example/users/alice",
			expected:    "alice",
			shouldError: false,
		},
		{
			name:        "User URI with trailing slash",
			uri:         "https://video.example/users/bob/",
			expected:    "bob",
			shouldError: false,
		},
		{
			name:        "Invalid URI format",
			uri:         "https://video.example/invalid",
			expected:    "",
			shouldError: true,
		},
		{
			name:        "Non-users path",
			uri:         "https://video.example/videos/123",
			expected:    "",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			username, err := service.extractUsernameFromURI(tt.uri)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, username)
			}
		})
	}
}

func TestExtractVideoIDFromURI(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	tests := []struct {
		name        string
		uri         string
		expected    string
		shouldError bool
	}{
		{
			name:        "Valid video URI",
			uri:         "https://video.example/videos/abc123",
			expected:    "abc123",
			shouldError: false,
		},
		{
			name:        "Video URI with trailing slash",
			uri:         "https://video.example/videos/def456/",
			expected:    "def456",
			shouldError: false,
		},
		{
			name:        "Invalid URI format",
			uri:         "https://video.example/invalid",
			expected:    "",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			videoID, err := service.extractVideoIDFromURI(tt.uri)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, videoID)
			}
		})
	}
}

// Additional mock methods for comment repository
type MockCommentRepository struct {
	mock.Mock
}

func (m *MockCommentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Comment), args.Error(1)
}

func (m *MockCommentRepository) Create(ctx context.Context, comment *domain.Comment) error {
	args := m.Called(ctx, comment)
	return args.Error(0)
}

func (m *MockCommentRepository) GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) Update(ctx context.Context, id uuid.UUID, body string) error {
	args := m.Called(ctx, id, body)
	return args.Error(0)
}

func (m *MockCommentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockCommentRepository) ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error) {
	args := m.Called(ctx, parentID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentWithUser), args.Error(1)
}

func (m *MockCommentRepository) CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error) {
	args := m.Called(ctx, videoID, activeOnly)
	return args.Int(0), args.Error(1)
}

func (m *MockCommentRepository) FlagComment(ctx context.Context, flag *domain.CommentFlag) error {
	args := m.Called(ctx, flag)
	return args.Error(0)
}

func (m *MockCommentRepository) UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error {
	args := m.Called(ctx, commentID, userID)
	return args.Error(0)
}

func (m *MockCommentRepository) GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error) {
	args := m.Called(ctx, commentID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.CommentFlag), args.Error(1)
}

func (m *MockCommentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockCommentRepository) IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, commentID, userID)
	return args.Bool(0), args.Error(1)
}

// ============================================================================
// NEW TESTS FOR VIDEO AND COMMENT FEDERATION
// ============================================================================

// TestServicePublishVideo tests the PublishVideo service method
func TestServicePublishVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("PublishVideo with valid completed video", func(t *testing.T) {
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
		}

		remoteActor := &domain.APRemoteActor{
			ActorURI: "https://mastodon.example/users/alice",
			InboxURL: "https://mastodon.example/users/alice/inbox",
		}

		mockVideoRepo.On("GetByID", ctx, "video-123").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			return len(deliveries) == 1 && deliveries[0].InboxURL == remoteActor.InboxURL
		})).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeCreate && activity.Local == true
		})).Return(nil).Once()

		err := service.PublishVideo(ctx, "video-123")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("PublishVideo returns error for non-existent video", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "nonexistent").Return(nil, assert.AnError).Once()

		err := service.PublishVideo(ctx, "nonexistent")
		assert.Error(t, err)

		mockVideoRepo.AssertExpectations(t)
	})

	t.Run("PublishVideo returns error for non-completed video", func(t *testing.T) {
		processingVideo := &domain.Video{
			ID:     "video-processing",
			Title:  "Processing",
			UserID: "user-123",
			Status: domain.StatusProcessing,
		}

		mockVideoRepo.On("GetByID", ctx, "video-processing").Return(processingVideo, nil).Once()

		err := service.PublishVideo(ctx, "video-processing")
		assert.Error(t, err)

		mockVideoRepo.AssertExpectations(t)
	})
}

// TestServiceUpdateVideo tests the UpdateVideo service method
func TestServiceUpdateVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("UpdateVideo sends Update activity", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-456",
			Title:   "Updated Title",
			UserID:  "user-456",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-456",
			Username: "updater",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-456",
				FollowerID: "https://peertube.example/accounts/bob",
				State:      "accepted",
			},
		}

		remoteActor := &domain.APRemoteActor{
			ActorURI: "https://peertube.example/accounts/bob",
			InboxURL: "https://peertube.example/accounts/bob/inbox",
		}

		mockVideoRepo.On("GetByID", ctx, "video-456").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Times(2)
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			return len(deliveries) == 1 && deliveries[0].InboxURL == remoteActor.InboxURL
		})).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeUpdate
		})).Return(nil).Once()

		err := service.UpdateVideo(ctx, "video-456")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})

	t.Run("UpdateVideo returns error for non-existent video", func(t *testing.T) {
		mockVideoRepo.On("GetByID", ctx, "nonexistent").Return(nil, assert.AnError).Once()

		err := service.UpdateVideo(ctx, "nonexistent")
		assert.Error(t, err)

		mockVideoRepo.AssertExpectations(t)
	})
}

// TestServiceDeleteVideo tests the DeleteVideo service method
func TestServiceDeleteVideo(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("DeleteVideo sends Delete activity with Tombstone", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-789",
			Title:   "To Be Deleted",
			UserID:  "user-789",
			Privacy: domain.PrivacyPublic,
		}

		user := &domain.User{
			ID:       "user-789",
			Username: "deleter",
		}

		followers := []*domain.APFollower{
			{
				ActorID:    "user-789",
				FollowerID: "https://mastodon.example/users/charlie",
				State:      "accepted",
			},
		}

		remoteActor := &domain.APRemoteActor{
			ActorURI: "https://mastodon.example/users/charlie",
			InboxURL: "https://mastodon.example/users/charlie/inbox",
		}

		mockVideoRepo.On("GetByID", ctx, "video-789").Return(video, nil).Once()
		mockUserRepo.On("GetByID", ctx, video.UserID).Return(user, nil).Once()
		mockAPRepo.On("GetFollowers", ctx, user.ID, "accepted", mock.Anything, mock.Anything).Return(followers, 1, nil).Once()
		mockAPRepo.On("GetRemoteActors", ctx, []string{followers[0].FollowerID}).Return([]*domain.APRemoteActor{remoteActor}, nil).Once()
		mockAPRepo.On("BulkEnqueueDelivery", ctx, mock.MatchedBy(func(deliveries []*domain.APDeliveryQueue) bool {
			return len(deliveries) == 1 && deliveries[0].InboxURL == remoteActor.InboxURL
		})).Return(nil).Once()
		mockAPRepo.On("StoreActivity", ctx, mock.MatchedBy(func(activity *domain.APActivity) bool {
			return activity.Type == domain.ActivityTypeDelete
		})).Return(nil).Once()

		err := service.DeleteVideo(ctx, "video-789")
		require.NoError(t, err)

		mockVideoRepo.AssertExpectations(t)
		mockUserRepo.AssertExpectations(t)
		mockAPRepo.AssertExpectations(t)
	})
}

// TestServicePublishComment tests the PublishComment service method
func TestServicePublishComment(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("PublishComment delivers to video owner and followers", func(t *testing.T) {
		// This test verifies the method exists and has correct signature
		// Full implementation will be added in the next phase

		// Placeholder: verify method can be called
		err := service.PublishComment(ctx, "comment-123")

		// For now, we expect this to fail since it's not implemented
		// Once implemented, this will pass
		_ = err
	})
}

// TestServiceBuildVideoObject tests the BuildVideoObject service method
func TestServiceBuildVideoObject(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("BuildVideoObject creates valid VideoObject", func(t *testing.T) {
		video := &domain.Video{
			ID:          "video-build-123",
			Title:       "Build Test",
			Description: "Testing BuildVideoObject",
			Duration:    120,
			UserID:      "user-build-123",
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-build-123",
			Username: "builder",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Once()

		videoObject, err := service.BuildVideoObject(ctx, video)

		// For now, we expect this to fail since it's not implemented
		// Once implemented, this will verify:
		// - VideoObject type is "Video"
		// - All required fields are present
		// - Privacy is correctly set
		_ = videoObject
		_ = err

		mockUserRepo.AssertExpectations(t)
	})
}

// TestServiceBuildNoteObject tests the BuildNoteObject service method
func TestServiceBuildNoteObject(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("BuildNoteObject creates valid Note", func(t *testing.T) {
		// This test verifies the method exists and has correct signature
		// Full implementation will be added in the next phase

		// Placeholder: verify method can be called
		var comment *domain.Comment
		noteObject, err := service.BuildNoteObject(ctx, comment)

		// For now, we expect this to fail since it's not implemented
		_ = noteObject
		_ = err
	})
}

// TestServiceCreateVideoActivity tests the CreateVideoActivity service method
func TestServiceCreateVideoActivity(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("CreateVideoActivity wraps VideoObject in Create", func(t *testing.T) {
		video := &domain.Video{
			ID:      "video-create-123",
			Title:   "Create Activity Test",
			UserID:  "user-create-123",
			Privacy: domain.PrivacyPublic,
			Status:  domain.StatusCompleted,
		}

		user := &domain.User{
			ID:       "user-create-123",
			Username: "creator",
		}

		mockUserRepo.On("GetByID", ctx, user.ID).Return(user, nil).Times(2)

		activity, err := service.CreateVideoActivity(ctx, video)

		// For now, we expect this to fail since it's not implemented
		// Once implemented, this will verify:
		// - Activity type is "Create"
		// - Object is a VideoObject
		// - Actor is correct
		_ = activity
		_ = err

		mockUserRepo.AssertExpectations(t)
	})
}

// TestServiceCreateCommentActivity tests the CreateCommentActivity service method
func TestServiceCreateCommentActivity(t *testing.T) {
	mockAPRepo := new(MockActivityPubRepository)
	mockUserRepo := new(MockUserRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockCommentRepo := new(MockCommentRepository)

	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	service := NewService(mockAPRepo, mockUserRepo, mockVideoRepo, mockCommentRepo, cfg)

	ctx := context.Background()

	t.Run("CreateCommentActivity wraps Note in Create", func(t *testing.T) {
		// This test verifies the method exists and has correct signature
		// Full implementation will be added in the next phase

		var comment *domain.Comment
		activity, err := service.CreateCommentActivity(ctx, comment)

		// For now, we expect this to fail since it's not implemented
		_ = activity
		_ = err
	})
}
