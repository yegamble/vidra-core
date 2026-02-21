package social

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/security"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
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
func (m *mockSocialRepo) GetFollow(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error) {
	args := m.Called(ctx, followerDID, followingDID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Follow), args.Error(1)
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
func (m *mockSocialRepo) GetLike(ctx context.Context, actorDID, subjectURI string) (*domain.Like, error) {
	args := m.Called(ctx, actorDID, subjectURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Like), args.Error(1)
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
func (m *mockAtprotoPublisher) AutoSyncEnabled() bool {
	return false
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

	repo.On("GetLike", mock.Anything, "did:plc:abc", "at://post/1").Return(nil, fmt.Errorf("like not found"))

	err := svc.Unlike(context.Background(), "did:plc:abc", "at://post/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not liked")
}

func TestUnfollow_NotFollowing(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "bob.bsky.social").Return(actor, nil)
	repo.On("GetFollow", mock.Anything, "did:plc:follower", "did:plc:target").Return(nil, errors.New("follow not found"))

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

// --- Helper for PDS-backed tests ---

// newTestServiceWithPDS creates a service whose ATProtoPDSURL points to an httptest server.
// The handler map keys are request paths; values are handler funcs.
func newTestServiceWithPDS(t *testing.T, handlers map[string]http.HandlerFunc) (*Service, *mockSocialRepo, *httptest.Server) {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	socialRepo := new(mockSocialRepo)
	atproto := new(mockAtprotoPublisher)
	cfg := &config.Config{
		ATProtoPDSURL:        ts.URL,
		ATProtoAppPassword:   "test-token",
		EnableATProtoLabeler: false,
	}
	svc := NewService(cfg, socialRepo, atproto, []byte("test-key-0123456789abcdef"))
	// Override client to use test server's client (trusts its TLS if any)
	svc.client = ts.Client()
	// Allow private IPs so httptest (127.0.0.1) passes SSRF checks
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	return svc, socialRepo, ts
}

// --- NewService ---

func TestNewService(t *testing.T) {
	svc, _, _ := newTestService(t)
	assert.NotNil(t, svc)
	assert.NotNil(t, svc.client)
	assert.NotNil(t, svc.urlValidator)
	assert.NotNil(t, svc.socialRepo)
}

// --- resolveActor cache miss -> HTTP ---

func TestResolveActor_CacheMiss_HTTPSuccess(t *testing.T) {
	displayName := "Alice"
	bio := "Hello world"
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "alice.test", r.URL.Query().Get("handle"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:alice"})
		},
		"/xrpc/app.bsky.actor.getProfile": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "did:plc:alice", r.URL.Query().Get("actor"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"handle":      "alice.test",
				"displayName": displayName,
				"description": bio,
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	// Cache miss
	repo.On("GetActorByHandle", mock.Anything, "alice.test").Return(nil, nil)

	actor, err := svc.ResolveActor(context.Background(), "alice.test")
	require.NoError(t, err)
	assert.Equal(t, "did:plc:alice", actor.DID)
	assert.Equal(t, "alice.test", actor.Handle)
	assert.Equal(t, &displayName, actor.DisplayName)
	assert.Equal(t, &bio, actor.Bio)
}

func TestResolveActor_CacheMiss_ResolveHandleFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "unknown.test").Return(nil, nil)

	actor, err := svc.ResolveActor(context.Background(), "unknown.test")
	assert.Error(t, err)
	assert.Nil(t, actor)
	assert.Contains(t, err.Error(), "resolve handle failed")
}

func TestResolveActor_CacheMiss_GetProfileFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{"did": "did:plc:bob"})
		},
		"/xrpc/app.bsky.actor.getProfile": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "bob.test").Return(nil, nil)

	actor, err := svc.ResolveActor(context.Background(), "bob.test")
	assert.Error(t, err)
	assert.Nil(t, actor)
	assert.Contains(t, err.Error(), "get profile failed")
}

// --- GetFollowers / GetFollowing error paths ---

func TestGetFollowers_ResolveActorError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "nobody.test").Return(nil, nil)

	result, err := svc.GetFollowers(context.Background(), "nobody.test", 50, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetFollowing_ResolveActorError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "nobody.test").Return(nil, nil)

	result, err := svc.GetFollowing(context.Background(), "nobody.test", 20, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetFollowers_RepoError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetFollowers", mock.Anything, "did:plc:abc", 50, 0).Return(nil, errors.New("db error"))

	result, err := svc.GetFollowers(context.Background(), "alice.bsky.social", 50, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetFollowing_RepoError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetFollowing", mock.Anything, "did:plc:abc", 20, 0).Return(nil, errors.New("db error"))

	result, err := svc.GetFollowing(context.Background(), "alice.bsky.social", 20, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- GetSocialStats error paths ---

func TestGetSocialStats_ResolveActorError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "nobody.test").Return(nil, nil)

	result, err := svc.GetSocialStats(context.Background(), "nobody.test")
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetSocialStats_RepoError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "alice.bsky.social").Return(actor, nil)
	repo.On("GetSocialStats", mock.Anything, "did:plc:abc").Return(nil, errors.New("db error"))

	result, err := svc.GetSocialStats(context.Background(), "alice.bsky.social")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- GetComments / GetCommentThread error paths ---

func TestGetComments_Error(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetComments", mock.Anything, "at://root/uri", 20, 0).Return(nil, errors.New("db error"))

	result, err := svc.GetComments(context.Background(), "at://root/uri", 20, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetCommentThread_Error(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetCommentThread", mock.Anything, "at://parent/uri", 50, 0).Return(nil, errors.New("db error"))

	result, err := svc.GetCommentThread(context.Background(), "at://parent/uri", 50, 0)
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- GetModerationLabels error path ---

func TestGetModerationLabels_Error(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetModerationLabels", mock.Anything, "did:plc:abc").Return(nil, errors.New("db error"))

	result, err := svc.GetModerationLabels(context.Background(), "did:plc:abc")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- Unlike GetLikes error path ---

func TestUnlike_GetLikesError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	repo.On("GetLike", mock.Anything, "did:plc:abc", "at://post/1").Return(nil, errors.New("db error"))

	err := svc.Unlike(context.Background(), "did:plc:abc", "at://post/1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

// --- Unfollow GetFollow error path ---

func TestUnfollow_GetFollowError(t *testing.T) {
	svc, repo, _ := newTestService(t)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.bsky.social"}
	repo.On("GetActorByHandle", mock.Anything, "bob.bsky.social").Return(actor, nil)
	repo.On("GetFollow", mock.Anything, "did:plc:follower", "did:plc:target").Return(nil, errors.New("db error"))

	err := svc.Unfollow(context.Background(), "did:plc:follower", "bob.bsky.social")
	assert.Error(t, err)
}

// --- Follow with HTTP (createRecord path) ---

func TestFollow_Success_WithHTTP(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, http.MethodPost, r.Method)
			assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:follower/app.bsky.graph.follow/abc123",
				"cid": "bafyreitest",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.test"}
	repo.On("GetActorByHandle", mock.Anything, "bob.test").Return(actor, nil)
	repo.On("IsFollowing", mock.Anything, "did:plc:follower", "did:plc:target").Return(false, nil)
	repo.On("CreateFollow", mock.Anything, mock.MatchedBy(func(f *domain.Follow) bool {
		return f.FollowerDID == "did:plc:follower" &&
			f.FollowingDID == "did:plc:target" &&
			f.URI == "at://did:plc:follower/app.bsky.graph.follow/abc123"
	})).Return(nil)

	err := svc.Follow(context.Background(), "did:plc:follower", "bob.test")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestFollow_CreateRecordFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.test"}
	repo.On("GetActorByHandle", mock.Anything, "bob.test").Return(actor, nil)
	repo.On("IsFollowing", mock.Anything, "did:plc:follower", "did:plc:target").Return(false, nil)

	err := svc.Follow(context.Background(), "did:plc:follower", "bob.test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create follow record")
}

// --- Like with HTTP ---

func TestLike_Success_WithHTTP(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.like/xyz",
				"cid": "bafylike",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("HasLiked", mock.Anything, "did:plc:abc", "at://post/1").Return(false, nil)
	repo.On("CreateLike", mock.Anything, mock.MatchedBy(func(l *domain.Like) bool {
		return l.ActorDID == "did:plc:abc" &&
			l.SubjectURI == "at://post/1" &&
			l.URI == "at://did:plc:abc/app.bsky.feed.like/xyz"
	})).Return(nil)

	err := svc.Like(context.Background(), "did:plc:abc", "at://post/1", "cid-subject")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestLike_VideoIDExtraction(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.like/xyz",
				"cid": "bafylike",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("HasLiked", mock.Anything, "did:plc:abc", "at://host/video/vid-42/details").Return(false, nil)
	repo.On("CreateLike", mock.Anything, mock.MatchedBy(func(l *domain.Like) bool {
		return l.VideoID != nil && *l.VideoID == "vid-42"
	})).Return(nil)

	err := svc.Like(context.Background(), "did:plc:abc", "at://host/video/vid-42/details", "cid123")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

// --- Unlike with HTTP ---

func TestUnlike_Success_WithHTTP(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("GetLike", mock.Anything, "did:plc:abc", "at://post/1").Return(&domain.Like{
		ActorDID: "did:plc:abc", URI: "at://did:plc:abc/app.bsky.feed.like/xyz",
	}, nil)
	repo.On("DeleteLike", mock.Anything, "at://did:plc:abc/app.bsky.feed.like/xyz").Return(nil)

	err := svc.Unlike(context.Background(), "did:plc:abc", "at://post/1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

// --- Unfollow with HTTP ---

func TestUnfollow_Success_WithHTTP(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.test"}
	repo.On("GetActorByHandle", mock.Anything, "bob.test").Return(actor, nil)
	repo.On("GetFollow", mock.Anything, "did:plc:follower", "did:plc:target").Return(&domain.Follow{
		FollowingDID: "did:plc:target",
		URI:          "at://did:plc:follower/app.bsky.graph.follow/abc123",
	}, nil)
	repo.On("RevokeFollow", mock.Anything, "at://did:plc:follower/app.bsky.graph.follow/abc123").Return(nil)

	err := svc.Unfollow(context.Background(), "did:plc:follower", "bob.test")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestUnfollow_DeleteRecordFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "bob.test"}
	repo.On("GetActorByHandle", mock.Anything, "bob.test").Return(actor, nil)
	repo.On("GetFollow", mock.Anything, "did:plc:follower", "did:plc:target").Return(&domain.Follow{
		FollowingDID: "did:plc:target",
		URI:          "at://did:plc:follower/app.bsky.graph.follow/abc123",
	}, nil)

	err := svc.Unfollow(context.Background(), "did:plc:follower", "bob.test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete follow record")
}

// --- Comment ---

func TestComment_Success_WithReply(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.post/comment1",
				"cid": "bafycomment",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:abc", Handle: "alice.test"}
	repo.On("GetActorByDID", mock.Anything, "did:plc:abc").Return(actor, nil)
	repo.On("CreateComment", mock.Anything, mock.MatchedBy(func(c *domain.SocialComment) bool {
		return c.Text == "Nice video!" &&
			c.ActorDID == "did:plc:abc" &&
			c.RootURI == "at://root/1" &&
			c.ActorHandle != nil && *c.ActorHandle == "alice.test"
	})).Return(nil)

	comment, err := svc.Comment(
		context.Background(),
		"did:plc:abc",
		"Nice video!",
		"at://root/1", "rootcid",
		"at://parent/1", "parentcid",
	)
	require.NoError(t, err)
	assert.Equal(t, "Nice video!", comment.Text)
	assert.Equal(t, "at://did:plc:abc/app.bsky.feed.post/comment1", comment.URI)
	repo.AssertExpectations(t)
}

func TestComment_Success_NoParentURI(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			// Verify the reply parent is set to root when parent is empty
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			record := body["record"].(map[string]interface{})
			reply := record["reply"].(map[string]interface{})
			parent := reply["parent"].(map[string]interface{})
			root := reply["root"].(map[string]interface{})
			// When parentURI is "", parent should equal root
			assert.Equal(t, root["uri"], parent["uri"])

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.post/comment2",
				"cid": "bafycomment2",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("GetActorByDID", mock.Anything, "did:plc:abc").Return(nil, nil)
	repo.On("CreateComment", mock.Anything, mock.MatchedBy(func(c *domain.SocialComment) bool {
		// ParentURI should be nil when empty string is passed
		return c.ParentURI == nil
	})).Return(nil)

	comment, err := svc.Comment(
		context.Background(),
		"did:plc:abc",
		"Top-level comment",
		"at://root/1", "rootcid",
		"", "", // No parent
	)
	require.NoError(t, err)
	assert.NotNil(t, comment)
}

func TestComment_VideoIDExtraction(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.post/c3",
				"cid": "bafyc3",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("GetActorByDID", mock.Anything, "did:plc:abc").Return(nil, nil)
	repo.On("CreateComment", mock.Anything, mock.MatchedBy(func(c *domain.SocialComment) bool {
		return c.VideoID != nil && *c.VideoID == "vid-99"
	})).Return(nil)

	_, err := svc.Comment(
		context.Background(),
		"did:plc:abc",
		"Comment on video",
		"at://host/video/vid-99/post", "rootcid",
		"", "",
	)
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestComment_CreateRecordFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("bad request"))
		},
	}

	svc, _, _ := newTestServiceWithPDS(t, handlers)

	_, err := svc.Comment(
		context.Background(),
		"did:plc:abc",
		"Test comment",
		"at://root/1", "rootcid",
		"", "",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create comment record")
}

func TestComment_DBCreateFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]string{
				"uri": "at://did:plc:abc/app.bsky.feed.post/c4",
				"cid": "bafyc4",
			})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("GetActorByDID", mock.Anything, "did:plc:abc").Return(nil, nil)
	repo.On("CreateComment", mock.Anything, mock.Anything).Return(errors.New("db error"))

	_, err := svc.Comment(
		context.Background(),
		"did:plc:abc",
		"Test comment",
		"at://root/1", "rootcid",
		"", "",
	)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

// --- DeleteComment ---

func TestDeleteComment_Success(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("DeleteComment", mock.Anything, "at://did:plc:abc/app.bsky.feed.post/c1").Return(nil)

	err := svc.DeleteComment(context.Background(), "at://did:plc:abc/app.bsky.feed.post/c1")
	assert.NoError(t, err)
	repo.AssertExpectations(t)
}

func TestDeleteComment_DeleteRecordFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server error"))
		},
	}

	svc, _, _ := newTestServiceWithPDS(t, handlers)

	err := svc.DeleteComment(context.Background(), "at://did:plc:abc/app.bsky.feed.post/c1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "delete comment record")
}

func TestDeleteComment_RepoError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	repo.On("DeleteComment", mock.Anything, "at://did:plc:abc/app.bsky.feed.post/c1").Return(errors.New("db error"))

	err := svc.DeleteComment(context.Background(), "at://did:plc:abc/app.bsky.feed.post/c1")
	assert.Error(t, err)
}

// --- IngestActorFeed ---

func TestIngestActorFeed_Success(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/app.bsky.feed.getAuthorFeed": func(w http.ResponseWriter, r *http.Request) {
			assert.Equal(t, "did:plc:alice", r.URL.Query().Get("actor"))
			w.Header().Set("Content-Type", "application/json")
			feed := map[string]interface{}{
				"feed": []interface{}{
					map[string]interface{}{
						"uri": "at://did:plc:alice/app.bsky.feed.post/p1",
						"cid": "bafyp1",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(feed)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.test"}
	repo.On("GetActorByHandle", mock.Anything, "alice.test").Return(actor, nil)
	repo.On("GetBlockedLabels", mock.Anything).Return([]string{}, nil)

	err := svc.IngestActorFeed(context.Background(), "alice.test", 10)
	assert.NoError(t, err)
}

func TestIngestActorFeed_BlockedPost(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/app.bsky.feed.getAuthorFeed": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			feed := map[string]interface{}{
				"feed": []interface{}{
					map[string]interface{}{
						"uri": "at://did:plc:alice/app.bsky.feed.post/spam1",
						"cid": "bafyspam",
						"labels": []interface{}{
							map[string]interface{}{"val": "spam"},
						},
					},
					map[string]interface{}{
						"uri": "at://did:plc:alice/app.bsky.feed.post/good1",
						"cid": "bafygood",
					},
				},
			}
			_ = json.NewEncoder(w).Encode(feed)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.test"}
	repo.On("GetActorByHandle", mock.Anything, "alice.test").Return(actor, nil)
	repo.On("GetBlockedLabels", mock.Anything).Return([]string{"spam"}, nil)

	err := svc.IngestActorFeed(context.Background(), "alice.test", 50)
	assert.NoError(t, err)
}

func TestIngestActorFeed_ResolveActorError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.identity.resolveHandle": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)
	repo.On("GetActorByHandle", mock.Anything, "unknown.test").Return(nil, nil)

	err := svc.IngestActorFeed(context.Background(), "unknown.test", 10)
	assert.Error(t, err)
}

func TestIngestActorFeed_FeedRequestFails(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/app.bsky.feed.getAuthorFeed": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.test"}
	repo.On("GetActorByHandle", mock.Anything, "alice.test").Return(actor, nil)
	repo.On("GetBlockedLabels", mock.Anything).Return([]string{}, nil)

	err := svc.IngestActorFeed(context.Background(), "alice.test", 10)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "get feed failed")
}

func TestIngestActorFeed_DefaultLimit(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/app.bsky.feed.getAuthorFeed": func(w http.ResponseWriter, r *http.Request) {
			// Verify limit is clamped to 50 when 0 is passed
			assert.Equal(t, "50", r.URL.Query().Get("limit"))
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"feed": []interface{}{}})
		},
	}

	svc, repo, _ := newTestServiceWithPDS(t, handlers)

	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.test"}
	repo.On("GetActorByHandle", mock.Anything, "alice.test").Return(actor, nil)
	repo.On("GetBlockedLabels", mock.Anything).Return([]string{}, nil)

	err := svc.IngestActorFeed(context.Background(), "alice.test", 0)
	assert.NoError(t, err)
}

// --- shouldBlock additional edge cases ---

func TestShouldBlock_MultipleLabels_OneMatches(t *testing.T) {
	svc, _, _ := newTestService(t)

	post := map[string]interface{}{
		"labels": []interface{}{
			map[string]interface{}{"val": "safe"},
			map[string]interface{}{"val": "educational"},
			map[string]interface{}{"val": "nsfw"},
		},
	}
	assert.True(t, svc.shouldBlock(post, []string{"nsfw"}))
}

func TestShouldBlock_LabelsWrongType(t *testing.T) {
	svc, _, _ := newTestService(t)

	// labels is a string instead of []interface{} -- should not block
	post := map[string]interface{}{
		"labels": "not-an-array",
	}
	assert.False(t, svc.shouldBlock(post, []string{"spam"}))
}

// --- deleteRecord with valid URI via HTTP ---

func TestDeleteRecord_Success(t *testing.T) {
	var receivedBody map[string]interface{}
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.deleteRecord": func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			w.WriteHeader(http.StatusOK)
		},
	}

	svc, _, _ := newTestServiceWithPDS(t, handlers)

	// URI format: at://repo/collection/rkey
	err := svc.deleteRecord(context.Background(), "at://did:plc:abc/app.bsky.feed.post/rkey123")
	assert.NoError(t, err)
	assert.Equal(t, "did:plc:abc", receivedBody["repo"])
	assert.Equal(t, "app.bsky.feed.post", receivedBody["collection"])
	assert.Equal(t, "rkey123", receivedBody["rkey"])
}

// --- createRecord error paths ---

func TestCreateRecord_HTTPError(t *testing.T) {
	handlers := map[string]http.HandlerFunc{
		"/xrpc/com.atproto.repo.createRecord": func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, "forbidden")
		},
	}

	svc, _, _ := newTestServiceWithPDS(t, handlers)

	_, _, err := svc.createRecord(context.Background(), "did:plc:abc", "app.bsky.feed.like", map[string]string{"test": "data"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "create record failed")
	assert.Contains(t, err.Error(), "403")
}

// --- ApplyModerationLabel with labeler enabled ---

func TestApplyModerationLabel_LabelerEnabled(t *testing.T) {
	socialRepo := new(mockSocialRepo)
	atproto := new(mockAtprotoPublisher)
	cfg := &config.Config{
		ATProtoPDSURL:        "https://bsky.social",
		EnableATProtoLabeler: true,
	}
	svc := NewService(cfg, socialRepo, atproto, []byte("test-key-0123456789abcdef"))

	// createLabel is a no-op in current implementation, so this should succeed
	socialRepo.On("CreateModerationLabel", mock.Anything, mock.AnythingOfType("*domain.ModerationLabel")).Return(nil)

	err := svc.ApplyModerationLabel(
		context.Background(),
		"did:plc:abc",
		"spam",
		"reason",
		"did:plc:admin",
		"at://uri",
		time.Hour,
	)
	assert.NoError(t, err)
	socialRepo.AssertExpectations(t)
}
