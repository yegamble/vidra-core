package social

import (
	"context"
	"errors"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type mockSocialRepo struct{ mock.Mock }

func (m *mockSocialRepo) UpsertActor(ctx context.Context, actor *domain.ATProtoActor) error {
	return m.Called(ctx, actor).Error(0)
}
func (m *mockSocialRepo) GetActorByDID(ctx context.Context, did string) (*domain.ATProtoActor, error) {
	args := m.Called(ctx, did)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ATProtoActor), args.Error(1)
}
func (m *mockSocialRepo) GetActorByHandle(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ATProtoActor), args.Error(1)
}
func (m *mockSocialRepo) CreateFollow(ctx context.Context, follow *domain.Follow) error {
	return m.Called(ctx, follow).Error(0)
}
func (m *mockSocialRepo) RevokeFollow(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *mockSocialRepo) GetFollowers(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	args := m.Called(ctx, did, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Follow), args.Error(1)
}
func (m *mockSocialRepo) GetFollowing(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	args := m.Called(ctx, did, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Follow), args.Error(1)
}
func (m *mockSocialRepo) IsFollowing(ctx context.Context, followerDID, followingDID string) (bool, error) {
	args := m.Called(ctx, followerDID, followingDID)
	return args.Bool(0), args.Error(1)
}
func (m *mockSocialRepo) CreateLike(ctx context.Context, like *domain.Like) error {
	return m.Called(ctx, like).Error(0)
}
func (m *mockSocialRepo) DeleteLike(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *mockSocialRepo) GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error) {
	args := m.Called(ctx, subjectURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Like), args.Error(1)
}
func (m *mockSocialRepo) HasLiked(ctx context.Context, actorDID, subjectURI string) (bool, error) {
	args := m.Called(ctx, actorDID, subjectURI)
	return args.Bool(0), args.Error(1)
}
func (m *mockSocialRepo) CreateComment(ctx context.Context, comment *domain.SocialComment) error {
	return m.Called(ctx, comment).Error(0)
}
func (m *mockSocialRepo) DeleteComment(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *mockSocialRepo) GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
	args := m.Called(ctx, rootURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.SocialComment), args.Error(1)
}
func (m *mockSocialRepo) GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
	args := m.Called(ctx, parentURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.SocialComment), args.Error(1)
}
func (m *mockSocialRepo) CreateModerationLabel(ctx context.Context, label *domain.ModerationLabel) error {
	return m.Called(ctx, label).Error(0)
}
func (m *mockSocialRepo) RemoveModerationLabel(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockSocialRepo) GetModerationLabels(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error) {
	args := m.Called(ctx, actorDID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.ModerationLabel), args.Error(1)
}
func (m *mockSocialRepo) GetSocialStats(ctx context.Context, did string) (*domain.SocialStats, error) {
	args := m.Called(ctx, did)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SocialStats), args.Error(1)
}
func (m *mockSocialRepo) GetBlockedLabels(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

type mockAtprotoPublisher struct{ mock.Mock }

func (m *mockAtprotoPublisher) PublishVideo(ctx context.Context, v *domain.Video) error {
	return m.Called(ctx, v).Error(0)
}
func (m *mockAtprotoPublisher) StartBackgroundRefresh(ctx context.Context, interval time.Duration) {
	m.Called(ctx, interval)
}

// --- Helper ---

func newTestService(t *testing.T) (*Service, *mockSocialRepo, *mockAtprotoPublisher) {
	t.Helper()
	socialRepo := new(mockSocialRepo)
	atproto := new(mockAtprotoPublisher)
	cfg := &config.Config{
		ATProtoPDSURL:        "https://bsky.social",
		EnableATProtoLabeler: false,
	}
	svc := NewService(cfg, socialRepo, atproto, []byte("test-key-0123456789abcdef"))
	return svc, socialRepo, atproto
}

// --- Tests for pure delegation methods ---

func TestGetLikes_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	likes := []domain.Like{
		{ActorDID: "did:plc:abc", SubjectURI: "at://did:plc:xyz/app.bsky.feed.post/123"},
		{ActorDID: "did:plc:def", SubjectURI: "at://did:plc:xyz/app.bsky.feed.post/123"},
	}
	repo.On("GetLikes", mock.Anything, "at://test/post/1", 50, 0).Return(likes, nil)

	result, err := svc.GetLikes(context.Background(), "at://test/post/1", 50, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetLikes_Error(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetLikes", mock.Anything, "at://test", 10, 0).Return(nil, errors.New("db error"))

	result, err := svc.GetLikes(context.Background(), "at://test", 10, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetComments_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	comments := []domain.SocialComment{
		{ActorDID: "did:plc:abc", Text: "Great video!"},
	}
	repo.On("GetComments", mock.Anything, "at://root/uri", 20, 0).Return(comments, nil)

	result, err := svc.GetComments(context.Background(), "at://root/uri", 20, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "Great video!", result[0].Text)
}

func TestGetCommentThread_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	comments := []domain.SocialComment{
		{ActorDID: "did:plc:abc", Text: "Reply 1"},
		{ActorDID: "did:plc:def", Text: "Reply 2"},
	}
	repo.On("GetCommentThread", mock.Anything, "at://parent/uri", 50, 0).Return(comments, nil)

	result, err := svc.GetCommentThread(context.Background(), "at://parent/uri", 50, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetModerationLabels_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	labels := []domain.ModerationLabel{
		{ActorDID: "did:plc:abc", LabelType: "spam"},
	}
	repo.On("GetModerationLabels", mock.Anything, "did:plc:abc").Return(labels, nil)

	result, err := svc.GetModerationLabels(context.Background(), "did:plc:abc")
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, "spam", result[0].LabelType)
}

func TestRemoveModerationLabel_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("RemoveModerationLabel", mock.Anything, "label-1").Return(nil)

	err := svc.RemoveModerationLabel(context.Background(), "label-1")
	assert.NoError(t, err)
}

func TestRemoveModerationLabel_Error(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("RemoveModerationLabel", mock.Anything, "nonexistent").Return(errors.New("not found"))

	err := svc.RemoveModerationLabel(context.Background(), "nonexistent")
	assert.Error(t, err)
}

// --- Tests for ApplyModerationLabel ---

func TestApplyModerationLabel_Success(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("CreateModerationLabel", mock.Anything, mock.AnythingOfType("*domain.ModerationLabel")).Return(nil)

	err := svc.ApplyModerationLabel(
		context.Background(),
		"did:plc:abc",
		"spam",
		"Spamming comments",
		"did:plc:admin",
		"at://did:plc:abc/app.bsky.feed.post/123",
		24*time.Hour,
	)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestApplyModerationLabel_NoReason(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("CreateModerationLabel", mock.Anything, mock.MatchedBy(func(label *domain.ModerationLabel) bool {
		return label.Reason == nil && label.ActorDID == "did:plc:abc"
	})).Return(nil)

	err := svc.ApplyModerationLabel(
		context.Background(),
		"did:plc:abc",
		"spam",
		"", // No reason
		"did:plc:admin",
		"",
		0,
	)
	assert.NoError(t, err)
}

func TestApplyModerationLabel_WithExpiry(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("CreateModerationLabel", mock.Anything, mock.MatchedBy(func(label *domain.ModerationLabel) bool {
		return label.ExpiresAt != nil
	})).Return(nil)

	err := svc.ApplyModerationLabel(
		context.Background(),
		"did:plc:abc",
		"temp-ban",
		"Temporary",
		"did:plc:admin",
		"",
		7*24*time.Hour,
	)
	assert.NoError(t, err)
}

func TestApplyModerationLabel_DBError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("CreateModerationLabel", mock.Anything, mock.Anything).Return(errors.New("db error"))

	err := svc.ApplyModerationLabel(
		context.Background(),
		"did:plc:abc",
		"spam",
		"test",
		"did:plc:admin",
		"",
		0,
	)
	assert.Error(t, err)
}

// --- Tests for shouldBlock ---

func TestShouldBlock_NoBlockedLabels(t *testing.T) {
	svc, _, _ := newTestService(t)

	post := map[string]interface{}{
		"labels": []interface{}{
			map[string]interface{}{"val": "spam"},
		},
	}
	assert.False(t, svc.shouldBlock(post, nil))
	assert.False(t, svc.shouldBlock(post, []string{}))
}

func TestShouldBlock_MatchingLabel(t *testing.T) {
	svc, _, _ := newTestService(t)

	post := map[string]interface{}{
		"labels": []interface{}{
			map[string]interface{}{"val": "spam"},
		},
	}
	assert.True(t, svc.shouldBlock(post, []string{"spam", "nsfw"}))
}

func TestShouldBlock_NoMatchingLabel(t *testing.T) {
	svc, _, _ := newTestService(t)

	post := map[string]interface{}{
		"labels": []interface{}{
			map[string]interface{}{"val": "safe"},
		},
	}
	assert.False(t, svc.shouldBlock(post, []string{"spam", "nsfw"}))
}

func TestShouldBlock_NoLabelsOnPost(t *testing.T) {
	svc, _, _ := newTestService(t)

	post := map[string]interface{}{"text": "Hello world"}
	assert.False(t, svc.shouldBlock(post, []string{"spam"}))
}

// --- Tests for GetFollowers/GetFollowing with cached actor ---

func TestGetFollowers_CachedActor(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	follows := []domain.Follow{
		{FollowerDID: "did:plc:def", FollowingDID: "did:plc:abc"},
	}

	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetFollowers", mock.Anything, "did:plc:abc", 50, 0).Return(follows, nil)

	result, err := svc.GetFollowers(context.Background(), "alice.bsky.social", 50, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestGetFollowing_CachedActor(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	follows := []domain.Follow{
		{FollowerDID: "did:plc:abc", FollowingDID: "did:plc:def"},
	}

	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetFollowing", mock.Anything, "did:plc:abc", 20, 0).Return(follows, nil)

	result, err := svc.GetFollowing(context.Background(), "alice.bsky.social", 20, 0)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
}

// --- Tests for GetSocialStats with cached actor ---

func TestGetSocialStats_CachedActor(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	stats := &domain.SocialStats{Follows: 100, Followers: 50, Likes: 200}

	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetSocialStats", mock.Anything, "did:plc:abc").Return(stats, nil)

	result, err := svc.GetSocialStats(context.Background(), "alice.bsky.social")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, int64(100), result.Follows)
	assert.Equal(t, int64(50), result.Followers)
}

// --- Tests for ResolveActor with cached actor ---

func TestResolveActor_CachedActor(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)

	result, err := svc.ResolveActor(context.Background(), "alice.bsky.social")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "did:plc:abc", result.DID)
}

// --- Tests for Follow/Like with already-existing state ---

func TestFollow_AlreadyFollowing(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "bob.bsky.social").Return(actor, nil)
	repo.On("IsFollowing", mock.Anything, "did:plc:follower", "did:plc:target").Return(true, nil)

	err := svc.Follow(context.Background(), "did:plc:follower", "bob.bsky.social")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already following")
}

func TestLike_AlreadyLiked(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("HasLiked", mock.Anything, "did:plc:abc", "at://post/1").Return(true, nil)

	err := svc.Like(context.Background(), "did:plc:abc", "at://post/1", "cid123")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already liked")
}

// --- Tests for Unlike/Unfollow not found ---

func TestUnlike_NotLiked(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetLikes", mock.Anything, "at://post/1", 1000, 0).Return([]domain.Like{
		{ActorDID: "did:plc:other", URI: "at://like/1"},
	}, nil)

	err := svc.Unlike(context.Background(), "did:plc:abc", "at://post/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not liked")
}

func TestUnfollow_NotFollowing(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "bob.bsky.social").Return(actor, nil)
	repo.On("GetFollowing", mock.Anything, "did:plc:follower", 1000, 0).Return([]domain.Follow{
		{FollowingDID: "did:plc:other", URI: "at://follow/1"},
	}, nil)

	err := svc.Unfollow(context.Background(), "did:plc:follower", "bob.bsky.social")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not following")
}

// --- Tests for deleteRecord URI parsing ---

func TestDeleteRecord_InvalidURI(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.deleteRecord(context.Background(), "invalid")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid URI format")
}
