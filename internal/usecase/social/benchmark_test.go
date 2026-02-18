package social

import (
	"context"
	"net/http"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/security"

	"github.com/stretchr/testify/mock"
)

// BenchmarkUnfollow_Old measures the performance of fetching 1000 follows and filtering in memory.
// This simulates the logic previously present in Service.Unfollow before optimization.
func BenchmarkUnfollow_Old(b *testing.B) {
	svc, repo, _ := newBenchmarkService(b)

	// Setup context
	ctx := context.Background()
	followerDID := "did:plc:follower"
	targetDID := "did:plc:target"
	targetHandle := "target.bsky.social"

	// Create a list of 1000 follows, where the target is at the end (worst case)
	follows := make([]domain.Follow, 1000)
	for i := 0; i < 999; i++ {
		follows[i] = domain.Follow{
			FollowingDID: "did:plc:other",
			URI:          "at://other",
		}
	}
	follows[999] = domain.Follow{
		FollowingDID: targetDID,
		URI:          "at://target/uri",
	}

	// Prepare mocks for the benchmark loop
	actor := &domain.ATProtoActor{DID: targetDID, Handle: targetHandle}
	repo.On("GetActorByHandle", mock.Anything, targetHandle).Return(actor, nil)
	repo.On("GetFollowing", mock.Anything, followerDID, 1000, 0).Return(follows, nil)
	repo.On("RevokeFollow", mock.Anything, "at://target/uri").Return(nil)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Manual implementation of old logic
		targetActor, _ := svc.ResolveActor(ctx, targetHandle)
		foundFollows, _ := repo.GetFollowing(ctx, followerDID, 1000, 0)

		var followURI string
		for _, f := range foundFollows {
			if f.FollowingDID == targetActor.DID {
				followURI = f.URI
				break
			}
		}

		if followURI != "" {
			_ = repo.RevokeFollow(ctx, followURI)
		}
		// Skipping deleteRecord HTTP call here as we want to measure logic overhead
	}
}

// BenchmarkUnfollow_New measures the performance of the optimized Unfollow method.
func BenchmarkUnfollow_New(b *testing.B) {
	svc, repo, _ := newBenchmarkService(b)

	// Setup context
	ctx := context.Background()
	followerDID := "did:plc:follower"
	targetDID := "did:plc:target"
	targetHandle := "target.bsky.social"

	// Prepare mocks
	actor := &domain.ATProtoActor{DID: targetDID, Handle: targetHandle}
	repo.On("GetActorByHandle", mock.Anything, targetHandle).Return(actor, nil)

	// Mock GetFollow (Optimized)
	follow := &domain.Follow{
		FollowingDID: targetDID,
		URI:          "at://target/uri",
	}
	repo.On("GetFollow", mock.Anything, followerDID, targetDID).Return(follow, nil)

	repo.On("RevokeFollow", mock.Anything, "at://target/uri").Return(nil)

	// Mock HTTP transport for deleteRecord call inside Unfollow
	svc.client.Transport = &mockTransport{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.Unfollow(ctx, followerDID, targetHandle)
	}
}

// BenchmarkUnlike_Old measures the performance of fetching 1000 likes and filtering in memory.
// This simulates the logic previously present in Service.Unlike before optimization.
func BenchmarkUnlike_Old(b *testing.B) {
	svc, repo, _ := newBenchmarkService(b)

	// Setup context
	ctx := context.Background()
	actorDID := "did:plc:actor"
	subjectURI := "at://subject/uri"

	// Create a list of 1000 likes, where the actor's like is at the end (worst case)
	likes := make([]domain.Like, 1000)
	for i := 0; i < 999; i++ {
		likes[i] = domain.Like{
			ActorDID: "did:plc:other",
			URI:      "at://other/like",
		}
	}
	likes[999] = domain.Like{
		ActorDID: actorDID,
		URI:      "at://actor/like",
	}

	// Prepare mocks for the benchmark loop
	repo.On("GetLikes", mock.Anything, subjectURI, 1000, 0).Return(likes, nil)
	repo.On("DeleteLike", mock.Anything, "at://actor/like").Return(nil)

	// Mock HTTP transport for deleteRecord call inside Unlike
	svc.client.Transport = &mockTransport{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Manual implementation of old logic
		foundLikes, _ := repo.GetLikes(ctx, subjectURI, 1000, 0)

		var likeURI string
		for _, l := range foundLikes {
			if l.ActorDID == actorDID {
				likeURI = l.URI
				break
			}
		}

		if likeURI != "" {
			// Simulate deleteRecord call
			_ = svc.deleteRecord(ctx, likeURI)
			_ = repo.DeleteLike(ctx, likeURI)
		}
	}
}

type benchMockAtprotoPublisher struct{ mock.Mock }

func (m *benchMockAtprotoPublisher) PublishVideo(ctx context.Context, v *domain.Video) error {
	return m.Called(ctx, v).Error(0)
}
func (m *benchMockAtprotoPublisher) StartBackgroundRefresh(ctx context.Context, interval time.Duration) {
	m.Called(ctx, interval)
}

type mockTransport struct{}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: 200,
		Body:       http.NoBody,
	}, nil
}

// Redefine for benchmark usage
func newBenchmarkService(t testing.TB) (*Service, *benchMockSocialRepo, *benchMockAtprotoPublisher) {
	t.Helper()
	socialRepo := new(benchMockSocialRepo)
	atproto := new(benchMockAtprotoPublisher)
	cfg := &config.Config{
		ATProtoPDSURL:        "https://bsky.social",
		EnableATProtoLabeler: false,
	}
	svc := NewService(cfg, socialRepo, atproto, []byte("test-key-0123456789abcdef"))
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	return svc, socialRepo, atproto
}

// To avoid the `testing.T` vs `testing.B` issue with `newTestService` in `service_test.go`,
// let's just create the mocks locally in this file for the benchmark, or use a slightly different setup.

type benchMockSocialRepo struct{ mock.Mock }

func (m *benchMockSocialRepo) UpsertActor(ctx context.Context, actor *domain.ATProtoActor) error {
	return m.Called(ctx, actor).Error(0)
}
func (m *benchMockSocialRepo) GetActorByDID(ctx context.Context, did string) (*domain.ATProtoActor, error) {
	args := m.Called(ctx, did)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ATProtoActor), args.Error(1)
}
func (m *benchMockSocialRepo) GetActorByHandle(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ATProtoActor), args.Error(1)
}
func (m *benchMockSocialRepo) CreateFollow(ctx context.Context, follow *domain.Follow) error {
	return m.Called(ctx, follow).Error(0)
}
func (m *benchMockSocialRepo) RevokeFollow(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *benchMockSocialRepo) GetFollowers(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	args := m.Called(ctx, did, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Follow), args.Error(1)
}
func (m *benchMockSocialRepo) GetFollowing(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	args := m.Called(ctx, did, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Follow), args.Error(1)
}
func (m *benchMockSocialRepo) GetFollow(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error) {
	args := m.Called(ctx, followerDID, followingDID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Follow), args.Error(1)
}
func (m *benchMockSocialRepo) IsFollowing(ctx context.Context, followerDID, followingDID string) (bool, error) {
	args := m.Called(ctx, followerDID, followingDID)
	return args.Bool(0), args.Error(1)
}
func (m *benchMockSocialRepo) CreateLike(ctx context.Context, like *domain.Like) error {
	return m.Called(ctx, like).Error(0)
}
func (m *benchMockSocialRepo) DeleteLike(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *benchMockSocialRepo) GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error) {
	args := m.Called(ctx, subjectURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Like), args.Error(1)
}
func (m *benchMockSocialRepo) GetLike(ctx context.Context, actorDID, subjectURI string) (*domain.Like, error) {
	args := m.Called(ctx, actorDID, subjectURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Like), args.Error(1)
}
func (m *benchMockSocialRepo) HasLiked(ctx context.Context, actorDID, subjectURI string) (bool, error) {
	args := m.Called(ctx, actorDID, subjectURI)
	return args.Bool(0), args.Error(1)
}
func (m *benchMockSocialRepo) CreateComment(ctx context.Context, comment *domain.SocialComment) error {
	return m.Called(ctx, comment).Error(0)
}
func (m *benchMockSocialRepo) DeleteComment(ctx context.Context, uri string) error {
	return m.Called(ctx, uri).Error(0)
}
func (m *benchMockSocialRepo) GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
	args := m.Called(ctx, rootURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.SocialComment), args.Error(1)
}
func (m *benchMockSocialRepo) GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
	args := m.Called(ctx, parentURI, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.SocialComment), args.Error(1)
}
func (m *benchMockSocialRepo) CreateModerationLabel(ctx context.Context, label *domain.ModerationLabel) error {
	return m.Called(ctx, label).Error(0)
}
func (m *benchMockSocialRepo) RemoveModerationLabel(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *benchMockSocialRepo) GetModerationLabels(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error) {
	args := m.Called(ctx, actorDID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.ModerationLabel), args.Error(1)
}
func (m *benchMockSocialRepo) GetSocialStats(ctx context.Context, did string) (*domain.SocialStats, error) {
	args := m.Called(ctx, did)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SocialStats), args.Error(1)
}
func (m *benchMockSocialRepo) GetBlockedLabels(ctx context.Context) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// BenchmarkUnlike_New measures the performance of the optimized Unlike method.
func BenchmarkUnlike_New(b *testing.B) {
	svc, repo, _ := newBenchmarkService(b)

	// Setup context
	ctx := context.Background()
	actorDID := "did:plc:actor"
	subjectURI := "at://subject/uri"

	// Prepare mocks
	like := &domain.Like{
		ActorDID: actorDID,
		URI:      "at://actor/like",
	}
	repo.On("GetLike", mock.Anything, actorDID, subjectURI).Return(like, nil)
	repo.On("DeleteLike", mock.Anything, "at://actor/like").Return(nil)

	// Mock HTTP transport for deleteRecord call inside Unlike
	svc.client.Transport = &mockTransport{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = svc.Unlike(ctx, actorDID, subjectURI)
	}
}
