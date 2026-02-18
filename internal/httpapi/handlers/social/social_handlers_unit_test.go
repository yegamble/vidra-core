//go:build !integration

package social

import (
	"bytes"
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
	"athena/internal/usecase"
	ucsocial "athena/internal/usecase/social"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockSocialRepo struct {
	upsertActorFn      func(ctx context.Context, actor *domain.ATProtoActor) error
	getActorByDIDFn    func(ctx context.Context, did string) (*domain.ATProtoActor, error)
	getActorByHandleFn func(ctx context.Context, handle string) (*domain.ATProtoActor, error)

	createFollowFn func(ctx context.Context, follow *domain.Follow) error
	revokeFollowFn func(ctx context.Context, uri string) error
	getFollowersFn func(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error)
	getFollowingFn func(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error)
	getFollowFn    func(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error)
	isFollowingFn  func(ctx context.Context, followerDID, followingDID string) (bool, error)

	createLikeFn func(ctx context.Context, like *domain.Like) error
	deleteLikeFn func(ctx context.Context, uri string) error
	getLikesFn   func(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error)
	getLikeFn    func(ctx context.Context, actorDID, subjectURI string) (*domain.Like, error)
	hasLikedFn   func(ctx context.Context, actorDID, subjectURI string) (bool, error)

	createCommentFn    func(ctx context.Context, comment *domain.SocialComment) error
	deleteCommentFn    func(ctx context.Context, uri string) error
	getCommentsFn      func(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error)
	getCommentThreadFn func(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error)

	createModerationLabelFn func(ctx context.Context, label *domain.ModerationLabel) error
	removeModerationLabelFn func(ctx context.Context, id string) error
	getModerationLabelsFn   func(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error)

	getSocialStatsFn   func(ctx context.Context, did string) (*domain.SocialStats, error)
	getBlockedLabelsFn func(ctx context.Context) ([]string, error)
}

func (m *mockSocialRepo) UpsertActor(ctx context.Context, actor *domain.ATProtoActor) error {
	if m.upsertActorFn != nil {
		return m.upsertActorFn(ctx, actor)
	}
	return nil
}

func (m *mockSocialRepo) GetActorByDID(ctx context.Context, did string) (*domain.ATProtoActor, error) {
	if m.getActorByDIDFn != nil {
		return m.getActorByDIDFn(ctx, did)
	}
	return nil, errors.New("not found")
}

func (m *mockSocialRepo) GetActorByHandle(ctx context.Context, handle string) (*domain.ATProtoActor, error) {
	if m.getActorByHandleFn != nil {
		return m.getActorByHandleFn(ctx, handle)
	}
	return nil, errors.New("not found")
}

func (m *mockSocialRepo) CreateFollow(ctx context.Context, follow *domain.Follow) error {
	if m.createFollowFn != nil {
		return m.createFollowFn(ctx, follow)
	}
	return nil
}

func (m *mockSocialRepo) RevokeFollow(ctx context.Context, uri string) error {
	if m.revokeFollowFn != nil {
		return m.revokeFollowFn(ctx, uri)
	}
	return nil
}

func (m *mockSocialRepo) GetFollowers(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	if m.getFollowersFn != nil {
		return m.getFollowersFn(ctx, did, limit, offset)
	}
	return nil, nil
}

func (m *mockSocialRepo) GetFollowing(ctx context.Context, did string, limit, offset int) ([]domain.Follow, error) {
	if m.getFollowingFn != nil {
		return m.getFollowingFn(ctx, did, limit, offset)
	}
	return nil, nil
}

func (m *mockSocialRepo) GetFollow(ctx context.Context, followerDID, followingDID string) (*domain.Follow, error) {
	if m.getFollowFn != nil {
		return m.getFollowFn(ctx, followerDID, followingDID)
	}
	return nil, nil
}

func (m *mockSocialRepo) IsFollowing(ctx context.Context, followerDID, followingDID string) (bool, error) {
	if m.isFollowingFn != nil {
		return m.isFollowingFn(ctx, followerDID, followingDID)
	}
	return false, nil
}

func (m *mockSocialRepo) CreateLike(ctx context.Context, like *domain.Like) error {
	if m.createLikeFn != nil {
		return m.createLikeFn(ctx, like)
	}
	return nil
}

func (m *mockSocialRepo) DeleteLike(ctx context.Context, uri string) error {
	if m.deleteLikeFn != nil {
		return m.deleteLikeFn(ctx, uri)
	}
	return nil
}

func (m *mockSocialRepo) GetLikes(ctx context.Context, subjectURI string, limit, offset int) ([]domain.Like, error) {
	if m.getLikesFn != nil {
		return m.getLikesFn(ctx, subjectURI, limit, offset)
	}
	return nil, nil
}

func (m *mockSocialRepo) GetLike(ctx context.Context, actorDID, subjectURI string) (*domain.Like, error) {
	if m.getLikeFn != nil {
		return m.getLikeFn(ctx, actorDID, subjectURI)
	}
	return nil, nil
}

func (m *mockSocialRepo) HasLiked(ctx context.Context, actorDID, subjectURI string) (bool, error) {
	if m.hasLikedFn != nil {
		return m.hasLikedFn(ctx, actorDID, subjectURI)
	}
	return false, nil
}

func (m *mockSocialRepo) CreateComment(ctx context.Context, comment *domain.SocialComment) error {
	if m.createCommentFn != nil {
		return m.createCommentFn(ctx, comment)
	}
	return nil
}

func (m *mockSocialRepo) DeleteComment(ctx context.Context, uri string) error {
	if m.deleteCommentFn != nil {
		return m.deleteCommentFn(ctx, uri)
	}
	return nil
}

func (m *mockSocialRepo) GetComments(ctx context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
	if m.getCommentsFn != nil {
		return m.getCommentsFn(ctx, rootURI, limit, offset)
	}
	return nil, nil
}

func (m *mockSocialRepo) GetCommentThread(ctx context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
	if m.getCommentThreadFn != nil {
		return m.getCommentThreadFn(ctx, parentURI, limit, offset)
	}
	return nil, nil
}

func (m *mockSocialRepo) CreateModerationLabel(ctx context.Context, label *domain.ModerationLabel) error {
	if m.createModerationLabelFn != nil {
		return m.createModerationLabelFn(ctx, label)
	}
	return nil
}

func (m *mockSocialRepo) RemoveModerationLabel(ctx context.Context, id string) error {
	if m.removeModerationLabelFn != nil {
		return m.removeModerationLabelFn(ctx, id)
	}
	return nil
}

func (m *mockSocialRepo) GetModerationLabels(ctx context.Context, actorDID string) ([]domain.ModerationLabel, error) {
	if m.getModerationLabelsFn != nil {
		return m.getModerationLabelsFn(ctx, actorDID)
	}
	return nil, nil
}

func (m *mockSocialRepo) GetSocialStats(ctx context.Context, did string) (*domain.SocialStats, error) {
	if m.getSocialStatsFn != nil {
		return m.getSocialStatsFn(ctx, did)
	}
	return &domain.SocialStats{}, nil
}

func (m *mockSocialRepo) GetBlockedLabels(ctx context.Context) ([]string, error) {
	if m.getBlockedLabelsFn != nil {
		return m.getBlockedLabelsFn(ctx)
	}
	return nil, nil
}

func newTestSocialHandler(repo *mockSocialRepo) *SocialHandler {
	cfg := &config.Config{}
	svc := ucsocial.NewService(cfg, repo, nil, nil)
	return NewSocialHandler((*usecase.SocialService)(svc))
}

func newTestSocialHandlerWithPDS(repo *mockSocialRepo, pdsURL string) *SocialHandler {
	cfg := &config.Config{
		ATProtoPDSURL: pdsURL,
	}
	svc := ucsocial.NewService(cfg, repo, nil, nil)
	return NewSocialHandler((*usecase.SocialService)(svc))
}

func withChiParam(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func decodeResponse(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var resp map[string]interface{}
	err := json.NewDecoder(rec.Body).Decode(&resp)
	require.NoError(t, err, "failed to decode JSON response body")
	return resp
}

func newFakePDS(actor *domain.ATProtoActor) *httptest.Server {
	mux := http.NewServeMux()

	mux.HandleFunc("/xrpc/com.atproto.identity.resolveHandle", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"did": actor.DID})
	})

	mux.HandleFunc("/xrpc/app.bsky.actor.getProfile", func(w http.ResponseWriter, r *http.Request) {
		profile := map[string]interface{}{
			"did":    actor.DID,
			"handle": actor.Handle,
		}
		if actor.DisplayName != nil {
			profile["displayName"] = *actor.DisplayName
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(profile)
	})

	mux.HandleFunc("/xrpc/com.atproto.repo.createRecord", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"uri": "at://did:plc:test/app.bsky.graph.follow/abc123",
			"cid": "bafyreiabc123",
		})
	})

	mux.HandleFunc("/xrpc/com.atproto.repo.deleteRecord", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/xrpc/app.bsky.feed.getAuthorFeed", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"feed": []interface{}{}})
	})

	return httptest.NewServer(mux)
}

func TestUnit_GetActor_CachedActor(t *testing.T) {
	actor := &domain.ATProtoActor{
		DID:       "did:plc:test123",
		Handle:    "alice.bsky.social",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IndexedAt: time.Now(),
	}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, handle string) (*domain.ATProtoActor, error) {
			if handle == "alice.bsky.social" {
				return actor, nil
			}
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/alice.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "alice.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	assert.True(t, resp["success"].(bool))
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "did:plc:test123", data["did"])
	assert.Equal(t, "alice.bsky.social", data["handle"])
}

func TestUnit_GetActor_NotFound(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/nonexistent.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "nonexistent.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetActor(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeResponse(t, rec)
	assert.False(t, resp["success"].(bool))
}

func TestUnit_GetActorStats_Success(t *testing.T) {
	actor := &domain.ATProtoActor{
		DID:       "did:plc:statsuser",
		Handle:    "bob.bsky.social",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		IndexedAt: time.Now(),
	}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, handle string) (*domain.ATProtoActor, error) {
			if handle == "bob.bsky.social" {
				return actor, nil
			}
			return nil, errors.New("not found")
		},
		getSocialStatsFn: func(_ context.Context, did string) (*domain.SocialStats, error) {
			if did == "did:plc:statsuser" {
				return &domain.SocialStats{
					Follows:   10,
					Followers: 25,
					Likes:     100,
					Comments:  50,
				}, nil
			}
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/bob.bsky.social/stats", nil)
	req = withChiParam(req, map[string]string{"handle": "bob.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetActorStats(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	assert.True(t, resp["success"].(bool))
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, float64(25), data["followers"])
	assert.Equal(t, float64(100), data["likes"])
}

func TestUnit_GetActorStats_ResolveError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/unknown/stats", nil)
	req = withChiParam(req, map[string]string{"handle": "unknown"})
	rec := httptest.NewRecorder()

	h.GetActorStats(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetActorStats_RepoError(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:err", Handle: "err.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getSocialStatsFn: func(_ context.Context, _ string) (*domain.SocialStats, error) {
			return nil, errors.New("db connection lost")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/err.bsky.social/stats", nil)
	req = withChiParam(req, map[string]string{"handle": "err.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetActorStats(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_Follow_InvalidJSON(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/social/follow", bytes.NewBufferString(`{invalid`))
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Follow(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_Follow_ServiceError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"target":"someone.bsky.social"}`
	req := httptest.NewRequest(http.MethodPost, "/social/follow", bytes.NewBufferString(body))
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Follow(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_Follow_AlreadyFollowing(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "target.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		isFollowingFn: func(_ context.Context, _, _ string) (bool, error) {
			return true, nil
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"target":"target.bsky.social"}`
	req := httptest.NewRequest(http.MethodPost, "/social/follow", bytes.NewBufferString(body))
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Follow(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeResponse(t, rec)
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "failed to follow")
}

func TestUnit_Follow_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:target", Handle: "target.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var followCreated bool
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		isFollowingFn: func(_ context.Context, _, _ string) (bool, error) {
			return false, nil
		},
		createFollowFn: func(_ context.Context, f *domain.Follow) error {
			followCreated = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	body := `{"target":"target.bsky.social"}`
	req := httptest.NewRequest(http.MethodPost, "/social/follow", bytes.NewBufferString(body))
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Follow(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, followCreated, "follow should have been persisted")
	resp := decodeResponse(t, rec)
	assert.Equal(t, "followed", resp["data"].(map[string]interface{})["status"])
}

func TestUnit_Unfollow_MissingAuth(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodDelete, "/social/follow/alice", nil)
	req = withChiParam(req, map[string]string{"handle": "alice"})
	rec := httptest.NewRecorder()

	h.Unfollow(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_Unfollow_ResolveError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/social/follow/alice", nil)
	req = withChiParam(req, map[string]string{"handle": "alice"})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Unfollow(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_Unfollow_NotFollowing(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowFn: func(_ context.Context, _, _ string) (*domain.Follow, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/social/follow/alice.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "alice.bsky.social"})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Unfollow(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeResponse(t, rec)
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "failed to unfollow")
}

func TestUnit_Unfollow_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:alice", Handle: "alice.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var revoked bool
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowFn: func(_ context.Context, _, _ string) (*domain.Follow, error) {
			return &domain.Follow{FollowingDID: "did:plc:alice", URI: "at://did:plc:me/app.bsky.graph.follow/abc123"}, nil
		},
		revokeFollowFn: func(_ context.Context, _ string) error {
			revoked = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	req := httptest.NewRequest(http.MethodDelete, "/social/follow/alice.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "alice.bsky.social"})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()

	h.Unfollow(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, revoked, "follow should have been revoked")
}

func TestUnit_GetFollowers_Success(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:popular", Handle: "popular.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	cid := "bafyabc"
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowersFn: func(_ context.Context, did string, limit, offset int) ([]domain.Follow, error) {
			if did == "did:plc:popular" {
				return []domain.Follow{
					{FollowerDID: "did:plc:fan1", FollowingDID: did, URI: "at://did:plc:fan1/app.bsky.graph.follow/1", CID: &cid, CreatedAt: time.Now()},
					{FollowerDID: "did:plc:fan2", FollowingDID: did, URI: "at://did:plc:fan2/app.bsky.graph.follow/2", CID: &cid, CreatedAt: time.Now()},
				}, nil
			}
			return nil, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/followers/popular.bsky.social?limit=10&offset=0", nil)
	req = withChiParam(req, map[string]string{"handle": "popular.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetFollowers(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	assert.True(t, resp["success"].(bool))
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
}

func TestUnit_GetFollowers_ResolveError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/followers/nobody", nil)
	req = withChiParam(req, map[string]string{"handle": "nobody"})
	rec := httptest.NewRecorder()

	h.GetFollowers(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetFollowers_RepoError(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:x", Handle: "x.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowersFn: func(_ context.Context, _ string, _, _ int) ([]domain.Follow, error) {
			return nil, errors.New("database timeout")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/followers/x.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "x.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetFollowers(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetFollowing_Success(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:me", Handle: "me.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	cid := "bafyabc"
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowingFn: func(_ context.Context, did string, limit, offset int) ([]domain.Follow, error) {
			return []domain.Follow{
				{FollowerDID: did, FollowingDID: "did:plc:friend", URI: "at://did:plc:me/app.bsky.graph.follow/1", CID: &cid, CreatedAt: time.Now()},
			}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/following/me.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "me.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetFollowing(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 1)
}

func TestUnit_GetFollowing_ResolveError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/following/nobody", nil)
	req = withChiParam(req, map[string]string{"handle": "nobody"})
	rec := httptest.NewRecorder()

	h.GetFollowing(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_Like_InvalidJSON(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/social/like", bytes.NewBufferString(`not json`))
	rec := httptest.NewRecorder()

	h.Like(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_Like_AlreadyLiked(t *testing.T) {
	repo := &mockSocialRepo{
		hasLikedFn: func(_ context.Context, _, _ string) (bool, error) {
			return true, nil
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:me","subject_uri":"at://did:plc:other/app.bsky.feed.post/123"}`
	req := httptest.NewRequest(http.MethodPost, "/social/like", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Like(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeResponse(t, rec)
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "failed to like")
}

func TestUnit_Like_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:me", Handle: "me.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var likeCreated bool
	repo := &mockSocialRepo{
		hasLikedFn: func(_ context.Context, _, _ string) (bool, error) {
			return false, nil
		},
		createLikeFn: func(_ context.Context, _ *domain.Like) error {
			likeCreated = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	body := `{"actor_did":"did:plc:me","subject_uri":"at://did:plc:other/app.bsky.feed.post/123","subject_cid":"bafycid"}`
	req := httptest.NewRequest(http.MethodPost, "/social/like", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Like(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, likeCreated)
	resp := decodeResponse(t, rec)
	assert.Equal(t, "liked", resp["data"].(map[string]interface{})["status"])
}

func TestUnit_Unlike_InvalidJSON(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodDelete, "/social/like", bytes.NewBufferString(`{bad`))
	rec := httptest.NewRecorder()

	h.Unlike(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_Unlike_NotLiked(t *testing.T) {
	repo := &mockSocialRepo{
		getLikeFn: func(_ context.Context, _, _ string) (*domain.Like, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:me","subject_uri":"at://did:plc:other/app.bsky.feed.post/123"}`
	req := httptest.NewRequest(http.MethodDelete, "/social/like", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Unlike(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	resp := decodeResponse(t, rec)
	assert.Contains(t, resp["error"].(map[string]interface{})["message"], "failed to unlike")
}

func TestUnit_Unlike_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:me", Handle: "me.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var likeDeleted bool
	repo := &mockSocialRepo{
		getLikeFn: func(_ context.Context, _, _ string) (*domain.Like, error) {
			return &domain.Like{ActorDID: "did:plc:me", URI: "at://did:plc:me/app.bsky.feed.like/xyz"}, nil
		},
		deleteLikeFn: func(_ context.Context, _ string) error {
			likeDeleted = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	body := `{"actor_did":"did:plc:me","subject_uri":"at://did:plc:other/app.bsky.feed.post/123"}`
	req := httptest.NewRequest(http.MethodDelete, "/social/like", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.Unlike(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, likeDeleted)
}

func TestUnit_GetLikes_Success(t *testing.T) {
	repo := &mockSocialRepo{
		getLikesFn: func(_ context.Context, uri string, limit, offset int) ([]domain.Like, error) {
			return []domain.Like{
				{ActorDID: "did:plc:fan1", SubjectURI: uri, URI: "at://did:plc:fan1/app.bsky.feed.like/1", CreatedAt: time.Now()},
			}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/likes/at%3A%2F%2Fdid%3Aplc%3Aother%2Fpost%2F1", nil)
	req = withChiParam(req, map[string]string{"uri": "at://did:plc:other/post/1"})
	rec := httptest.NewRecorder()

	h.GetLikes(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 1)
}

func TestUnit_GetLikes_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		getLikesFn: func(_ context.Context, _ string, _, _ int) ([]domain.Like, error) {
			return nil, errors.New("db error")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/likes/someuri", nil)
	req = withChiParam(req, map[string]string{"uri": "someuri"})
	rec := httptest.NewRecorder()

	h.GetLikes(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetLikes_Empty(t *testing.T) {
	repo := &mockSocialRepo{
		getLikesFn: func(_ context.Context, _ string, _, _ int) ([]domain.Like, error) {
			return []domain.Like{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/likes/someuri", nil)
	req = withChiParam(req, map[string]string{"uri": "someuri"})
	rec := httptest.NewRecorder()

	h.GetLikes(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUnit_CreateComment_InvalidJSON(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/social/comment", bytes.NewBufferString(`not-json`))
	rec := httptest.NewRecorder()

	h.CreateComment(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CreateComment_ServiceError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByDIDFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not found")
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:me","text":"hello","root_uri":"at://did:plc:other/post/1"}`
	req := httptest.NewRequest(http.MethodPost, "/social/comment", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.CreateComment(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_CreateComment_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:me", Handle: "me.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var commentCreated bool
	repo := &mockSocialRepo{
		getActorByDIDFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		createCommentFn: func(_ context.Context, _ *domain.SocialComment) error {
			commentCreated = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	body := `{"actor_did":"did:plc:me","text":"Great video!","root_uri":"at://did:plc:other/post/1","root_cid":"bafyroot"}`
	req := httptest.NewRequest(http.MethodPost, "/social/comment", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.CreateComment(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.True(t, commentCreated)
	resp := decodeResponse(t, rec)
	assert.True(t, resp["success"].(bool))
}

func TestUnit_DeleteComment_ServiceError(t *testing.T) {
	repo := &mockSocialRepo{}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/social/comment/at%3A%2F%2Fdid%3Aplc%3Ame%2Fapp.bsky.feed.post%2Fabc", nil)
	req = withChiParam(req, map[string]string{"uri": "at://did:plc:me/app.bsky.feed.post/abc"})
	rec := httptest.NewRecorder()

	h.DeleteComment(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_DeleteComment_SuccessWithPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:me", Handle: "me.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	var commentDeleted bool
	repo := &mockSocialRepo{
		deleteCommentFn: func(_ context.Context, _ string) error {
			commentDeleted = true
			return nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	req := httptest.NewRequest(http.MethodDelete, "/social/comment/uri", nil)
	req = withChiParam(req, map[string]string{"uri": "at://did:plc:me/app.bsky.feed.post/abc"})
	rec := httptest.NewRecorder()

	h.DeleteComment(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, commentDeleted)
}

func TestUnit_GetComments_Success(t *testing.T) {
	repo := &mockSocialRepo{
		getCommentsFn: func(_ context.Context, rootURI string, limit, offset int) ([]domain.SocialComment, error) {
			return []domain.SocialComment{
				{ActorDID: "did:plc:commenter", URI: "at://did:plc:commenter/post/1", Text: "nice!", RootURI: rootURI, CreatedAt: time.Now(), IndexedAt: time.Now()},
			}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/at%3A%2F%2Fdid%3Aplc%3Aother%2Fpost%2F1", nil)
	req = withChiParam(req, map[string]string{"uri": "at://did:plc:other/post/1"})
	rec := httptest.NewRecorder()

	h.GetComments(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 1)
}

func TestUnit_GetComments_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		getCommentsFn: func(_ context.Context, _ string, _, _ int) ([]domain.SocialComment, error) {
			return nil, errors.New("db error")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/someuri", nil)
	req = withChiParam(req, map[string]string{"uri": "someuri"})
	rec := httptest.NewRecorder()

	h.GetComments(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetComments_PaginationDefaults(t *testing.T) {
	var capturedLimit, capturedOffset int
	repo := &mockSocialRepo{
		getCommentsFn: func(_ context.Context, _ string, limit, offset int) ([]domain.SocialComment, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []domain.SocialComment{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/someuri", nil)
	req = withChiParam(req, map[string]string{"uri": "someuri"})
	rec := httptest.NewRecorder()

	h.GetComments(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 50, capturedLimit, "default limit should be 50")
	assert.Equal(t, 0, capturedOffset, "default offset should be 0")
}

func TestUnit_GetCommentThread_Success(t *testing.T) {
	repo := &mockSocialRepo{
		getCommentThreadFn: func(_ context.Context, parentURI string, limit, offset int) ([]domain.SocialComment, error) {
			return []domain.SocialComment{
				{ActorDID: "did:plc:r1", URI: "at://r1/post/1", Text: "reply1", RootURI: parentURI, CreatedAt: time.Now(), IndexedAt: time.Now()},
				{ActorDID: "did:plc:r2", URI: "at://r2/post/2", Text: "reply2", RootURI: parentURI, CreatedAt: time.Now(), IndexedAt: time.Now()},
			}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/at%3A%2F%2Fparent/thread", nil)
	req = withChiParam(req, map[string]string{"uri": "at://parent"})
	rec := httptest.NewRecorder()

	h.GetCommentThread(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 2)
}

func TestUnit_GetCommentThread_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		getCommentThreadFn: func(_ context.Context, _ string, _, _ int) ([]domain.SocialComment, error) {
			return nil, errors.New("thread not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/uri/thread", nil)
	req = withChiParam(req, map[string]string{"uri": "uri"})
	rec := httptest.NewRecorder()

	h.GetCommentThread(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_ApplyLabel_InvalidJSON(t *testing.T) {
	h := NewSocialHandler(nil)

	req := httptest.NewRequest(http.MethodPost, "/social/moderation/label", bytes.NewBufferString(`{bad`))
	rec := httptest.NewRecorder()

	h.ApplyLabel(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_ApplyLabel_Success(t *testing.T) {
	var labelCreated bool
	var capturedLabel *domain.ModerationLabel
	repo := &mockSocialRepo{
		createModerationLabelFn: func(_ context.Context, label *domain.ModerationLabel) error {
			labelCreated = true
			capturedLabel = label
			return nil
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:spammer","label_type":"spam","reason":"test spam","applied_by":"did:plc:admin"}`
	req := httptest.NewRequest(http.MethodPost, "/social/moderation/label", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.ApplyLabel(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, labelCreated)
	assert.Equal(t, "did:plc:spammer", capturedLabel.ActorDID)
	assert.Equal(t, "spam", capturedLabel.LabelType)
	assert.Equal(t, "did:plc:admin", capturedLabel.AppliedBy)
	assert.NotNil(t, capturedLabel.Reason)
	assert.Equal(t, "test spam", *capturedLabel.Reason)

	resp := decodeResponse(t, rec)
	assert.Equal(t, "label_applied", resp["data"].(map[string]interface{})["status"])
}

func TestUnit_ApplyLabel_WithExpiration(t *testing.T) {
	var capturedLabel *domain.ModerationLabel
	repo := &mockSocialRepo{
		createModerationLabelFn: func(_ context.Context, label *domain.ModerationLabel) error {
			capturedLabel = label
			return nil
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:user","label_type":"nudity","applied_by":"did:plc:mod","expires_in":60}`
	req := httptest.NewRequest(http.MethodPost, "/social/moderation/label", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.ApplyLabel(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedLabel.ExpiresAt, "ExpiresAt should be set when expires_in is provided")
	assert.True(t, capturedLabel.ExpiresAt.After(time.Now()), "ExpiresAt should be in the future")
}

func TestUnit_ApplyLabel_WithURI(t *testing.T) {
	var capturedLabel *domain.ModerationLabel
	repo := &mockSocialRepo{
		createModerationLabelFn: func(_ context.Context, label *domain.ModerationLabel) error {
			capturedLabel = label
			return nil
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:user","label_type":"nsfw","applied_by":"did:plc:mod","uri":"at://did:plc:user/app.bsky.feed.post/123"}`
	req := httptest.NewRequest(http.MethodPost, "/social/moderation/label", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.ApplyLabel(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	require.NotNil(t, capturedLabel.URI)
	assert.Equal(t, "at://did:plc:user/app.bsky.feed.post/123", *capturedLabel.URI)
}

func TestUnit_ApplyLabel_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		createModerationLabelFn: func(_ context.Context, _ *domain.ModerationLabel) error {
			return errors.New("duplicate label")
		},
	}
	h := newTestSocialHandler(repo)

	body := `{"actor_did":"did:plc:user","label_type":"spam","applied_by":"did:plc:mod"}`
	req := httptest.NewRequest(http.MethodPost, "/social/moderation/label", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	h.ApplyLabel(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_RemoveLabel_Success(t *testing.T) {
	var removedID string
	repo := &mockSocialRepo{
		removeModerationLabelFn: func(_ context.Context, id string) error {
			removedID = id
			return nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/social/moderation/label/label-123", nil)
	req = withChiParam(req, map[string]string{"id": "label-123"})
	rec := httptest.NewRecorder()

	h.RemoveLabel(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "label-123", removedID)
	resp := decodeResponse(t, rec)
	assert.Equal(t, "label_removed", resp["data"].(map[string]interface{})["status"])
}

func TestUnit_RemoveLabel_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		removeModerationLabelFn: func(_ context.Context, _ string) error {
			return errors.New("label not found")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodDelete, "/social/moderation/label/nonexistent", nil)
	req = withChiParam(req, map[string]string{"id": "nonexistent"})
	rec := httptest.NewRecorder()

	h.RemoveLabel(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_GetLabels_Success(t *testing.T) {
	reason := "repeated spam"
	repo := &mockSocialRepo{
		getModerationLabelsFn: func(_ context.Context, did string) ([]domain.ModerationLabel, error) {
			return []domain.ModerationLabel{
				{ID: "lbl-1", ActorDID: did, LabelType: "spam", Reason: &reason, AppliedBy: "did:plc:mod", CreatedAt: time.Now()},
			}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/moderation/labels/did:plc:user", nil)
	req = withChiParam(req, map[string]string{"did": "did:plc:user"})
	rec := httptest.NewRecorder()

	h.GetLabels(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].([]interface{})
	assert.Len(t, data, 1)
	label := data[0].(map[string]interface{})
	assert.Equal(t, "spam", label["label_type"])
}

func TestUnit_GetLabels_Empty(t *testing.T) {
	repo := &mockSocialRepo{
		getModerationLabelsFn: func(_ context.Context, _ string) ([]domain.ModerationLabel, error) {
			return []domain.ModerationLabel{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/moderation/labels/did:plc:clean", nil)
	req = withChiParam(req, map[string]string{"did": "did:plc:clean"})
	rec := httptest.NewRecorder()

	h.GetLabels(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUnit_GetLabels_RepoError(t *testing.T) {
	repo := &mockSocialRepo{
		getModerationLabelsFn: func(_ context.Context, _ string) ([]domain.ModerationLabel, error) {
			return nil, errors.New("connection refused")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/moderation/labels/did:plc:user", nil)
	req = withChiParam(req, map[string]string{"did": "did:plc:user"})
	rec := httptest.NewRecorder()

	h.GetLabels(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_IngestFeed_ResolveError(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("unknown actor")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodPost, "/social/ingest/unknown.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "unknown.bsky.social"})
	rec := httptest.NewRecorder()

	h.IngestFeed(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_IngestFeed_SSRFBlocksLocalPDS(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:creator", Handle: "creator.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	pds := newFakePDS(actor)
	defer pds.Close()

	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getBlockedLabelsFn: func(_ context.Context) ([]string, error) {
			return []string{}, nil
		},
	}
	h := newTestSocialHandlerWithPDS(repo, pds.URL)

	req := httptest.NewRequest(http.MethodPost, "/social/ingest/creator.bsky.social?limit=10", nil)
	req = withChiParam(req, map[string]string{"handle": "creator.bsky.social"})
	rec := httptest.NewRecorder()

	h.IngestFeed(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_IngestFeed_NoPDSConfigured(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:creator", Handle: "creator.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getBlockedLabelsFn: func(_ context.Context) ([]string, error) {
			return []string{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodPost, "/social/ingest/creator.bsky.social", nil)
	req = withChiParam(req, map[string]string{"handle": "creator.bsky.social"})
	rec := httptest.NewRecorder()

	h.IngestFeed(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestUnit_RegisterRoutes(t *testing.T) {
	h := NewSocialHandler(nil)
	r := chi.NewRouter()
	h.RegisterRoutes(r, "test-secret")

	var routes []string
	_ = chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		routes = append(routes, fmt.Sprintf("%s %s", method, route))
		return nil
	})

	expectedRoutes := []string{
		"GET /social/actors/{handle}",
		"GET /social/actors/{handle}/stats",
		"POST /social/follow",
		"DELETE /social/follow/{handle}",
		"GET /social/followers/{handle}",
		"GET /social/following/{handle}",
		"POST /social/like",
		"DELETE /social/like",
		"GET /social/likes/{uri}",
		"POST /social/comment",
		"DELETE /social/comment/{uri}",
		"GET /social/comments/{uri}",
		"GET /social/comments/{uri}/thread",
		"POST /social/moderation/label",
		"DELETE /social/moderation/label/{id}",
		"GET /social/moderation/labels/{did}",
		"POST /social/ingest/{handle}",
	}

	for _, expected := range expectedRoutes {
		assert.Contains(t, routes, expected, "expected route %q not found", expected)
	}
}

func TestUnit_NewSocialHandler(t *testing.T) {
	cfg := &config.Config{}
	svc := ucsocial.NewService(cfg, &mockSocialRepo{}, nil, nil)
	handler := NewSocialHandler((*usecase.SocialService)(svc))

	require.NotNil(t, handler)
	require.NotNil(t, handler.socialService)
}

func TestUnit_NewSocialHandler_Nil(t *testing.T) {
	handler := NewSocialHandler(nil)
	require.NotNil(t, handler)
}

func TestUnit_SocialHandler_StatusCodes(t *testing.T) {
	h := NewSocialHandler(nil)

	cases := []struct {
		name   string
		method string
		url    string
		body   string
		params map[string]string
		auth   bool
		call   func(http.ResponseWriter, *http.Request)
		status int
	}{
		{
			name:   "Follow empty body",
			method: http.MethodPost,
			url:    "/social/follow",
			body:   "",
			auth:   true,
			call:   h.Follow,
			status: http.StatusBadRequest,
		},
		{
			name:   "Like empty body",
			method: http.MethodPost,
			url:    "/social/like",
			body:   "",
			call:   h.Like,
			status: http.StatusBadRequest,
		},
		{
			name:   "Unlike empty body",
			method: http.MethodDelete,
			url:    "/social/like",
			body:   "",
			call:   h.Unlike,
			status: http.StatusBadRequest,
		},
		{
			name:   "CreateComment empty body",
			method: http.MethodPost,
			url:    "/social/comment",
			body:   "",
			call:   h.CreateComment,
			status: http.StatusBadRequest,
		},
		{
			name:   "ApplyLabel empty body",
			method: http.MethodPost,
			url:    "/social/moderation/label",
			body:   "",
			call:   h.ApplyLabel,
			status: http.StatusBadRequest,
		},
		{
			name:   "Unfollow no auth",
			method: http.MethodDelete,
			url:    "/social/follow/handle",
			params: map[string]string{"handle": "handle"},
			call:   h.Unfollow,
			status: http.StatusUnauthorized,
		},
		{
			name:   "Follow malformed JSON",
			method: http.MethodPost,
			url:    "/social/follow",
			body:   `{"target":123}`,
			auth:   true,
			call:   h.Follow,
			status: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if tc.body != "" {
				req = httptest.NewRequest(tc.method, tc.url, bytes.NewBufferString(tc.body))
			} else {
				req = httptest.NewRequest(tc.method, tc.url, nil)
			}
			if tc.params != nil {
				req = withChiParam(req, tc.params)
			}
			if tc.auth {
				req = withSocialAuthUser(req)
			}
			rec := httptest.NewRecorder()
			tc.call(rec, req)
			assert.Equal(t, tc.status, rec.Code, "unexpected status for %s", tc.name)
		})
	}
}

func TestUnit_GetActor_NotCachedNoPDS(t *testing.T) {
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return nil, errors.New("not cached")
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/alice.test", nil)
	req = withChiParam(req, map[string]string{"handle": "alice.test"})
	rec := httptest.NewRecorder()

	h.GetActor(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	resp := decodeResponse(t, rec)
	assert.False(t, resp["success"].(bool))
}

func TestUnit_GetActor_CachedWithDisplayName(t *testing.T) {
	displayName := "Alice"
	actor := &domain.ATProtoActor{
		DID:         "did:plc:alice",
		Handle:      "alice.test",
		DisplayName: &displayName,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
		IndexedAt:   time.Now(),
	}
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/actors/alice.test", nil)
	req = withChiParam(req, map[string]string{"handle": "alice.test"})
	rec := httptest.NewRecorder()

	h.GetActor(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	resp := decodeResponse(t, rec)
	data := resp["data"].(map[string]interface{})
	assert.Equal(t, "did:plc:alice", data["did"])
	assert.Equal(t, "Alice", data["display_name"])
}

func TestUnit_GetFollowers_CustomPagination(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:paged", Handle: "paged.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	var capturedLimit, capturedOffset int
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowersFn: func(_ context.Context, _ string, limit, offset int) ([]domain.Follow, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []domain.Follow{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/followers/paged.bsky.social?limit=5&offset=10", nil)
	req = withChiParam(req, map[string]string{"handle": "paged.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetFollowers(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 5, capturedLimit)
	assert.Equal(t, 10, capturedOffset)
}

func TestUnit_GetFollowing_CustomPagination(t *testing.T) {
	actor := &domain.ATProtoActor{DID: "did:plc:paged2", Handle: "paged2.bsky.social", CreatedAt: time.Now(), UpdatedAt: time.Now(), IndexedAt: time.Now()}
	var capturedLimit, capturedOffset int
	repo := &mockSocialRepo{
		getActorByHandleFn: func(_ context.Context, _ string) (*domain.ATProtoActor, error) {
			return actor, nil
		},
		getFollowingFn: func(_ context.Context, _ string, limit, offset int) ([]domain.Follow, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []domain.Follow{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/following/paged2.bsky.social?limit=25&offset=50", nil)
	req = withChiParam(req, map[string]string{"handle": "paged2.bsky.social"})
	rec := httptest.NewRecorder()

	h.GetFollowing(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 25, capturedLimit)
	assert.Equal(t, 50, capturedOffset)
}

func TestUnit_GetLikes_CustomPagination(t *testing.T) {
	var capturedLimit, capturedOffset int
	repo := &mockSocialRepo{
		getLikesFn: func(_ context.Context, _ string, limit, offset int) ([]domain.Like, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []domain.Like{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/likes/uri?limit=3&offset=6", nil)
	req = withChiParam(req, map[string]string{"uri": "uri"})
	rec := httptest.NewRecorder()

	h.GetLikes(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 3, capturedLimit)
	assert.Equal(t, 6, capturedOffset)
}

func TestUnit_GetCommentThread_CustomPagination(t *testing.T) {
	var capturedLimit, capturedOffset int
	repo := &mockSocialRepo{
		getCommentThreadFn: func(_ context.Context, _ string, limit, offset int) ([]domain.SocialComment, error) {
			capturedLimit = limit
			capturedOffset = offset
			return []domain.SocialComment{}, nil
		},
	}
	h := newTestSocialHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/social/comments/uri/thread?limit=20&offset=40", nil)
	req = withChiParam(req, map[string]string{"uri": "uri"})
	rec := httptest.NewRecorder()

	h.GetCommentThread(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, 20, capturedLimit)
	assert.Equal(t, 40, capturedOffset)
}

func TestUnit_CommentHandlers_UpdateComment_InvalidCommentID(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/comments/bad", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.UpdateComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_DeleteComment_InvalidCommentID(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/bad", nil)
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.DeleteComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_DeleteComment_Unauthorized(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/id", nil)
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.DeleteComment(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CommentHandlers_FlagComment_InvalidCommentID(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/bad/flag", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.FlagComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_FlagComment_Unauthorized(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/flag", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.FlagComment(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CommentHandlers_FlagComment_InvalidBody(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/flag", bytes.NewBufferString(`{`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.FlagComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_UnflagComment_InvalidCommentID(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/comments/bad/flag", nil)
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.UnflagComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_ModerateComment_InvalidCommentID(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/bad/moderate", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.ModerateComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CommentHandlers_ModerateComment_Unauthorized(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/moderate", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.ModerateComment(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CommentHandlers_ModerateComment_InvalidBody(t *testing.T) {
	h := NewCommentHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/comments/id/moderate", bytes.NewBufferString(`{`))
	req = withSocialRouteParams(req, map[string]string{"commentId": socialUnitItemID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.ModerateComment(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_DeletePlaylist_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/id", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.DeletePlaylist(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_PlaylistHandlers_DeletePlaylist_InvalidID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/bad", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.DeletePlaylist(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_UpdatePlaylist_InvalidID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/bad", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.UpdatePlaylist(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_AddVideoToPlaylist_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/id/items", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.AddVideoToPlaylist(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_PlaylistHandlers_RemoveVideoFromPlaylist_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/id/items/item", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID, "itemId": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.RemoveVideoFromPlaylist(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_PlaylistHandlers_RemoveVideoFromPlaylist_InvalidPlaylistID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/bad/items/item", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "itemId": socialUnitVideoID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.RemoveVideoFromPlaylist(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_GetPlaylistItems_InvalidID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/bad/items", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.GetPlaylistItems(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_ReorderPlaylistItem_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/id/items/item/reorder", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID, "itemId": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.ReorderPlaylistItem(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_PlaylistHandlers_ReorderPlaylistItem_InvalidPlaylistID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/bad/items/item/reorder", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "itemId": socialUnitVideoID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.ReorderPlaylistItem(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_ReorderPlaylistItem_InvalidItemID(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/id/items/bad/reorder", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitItemID, "itemId": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.ReorderPlaylistItem(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_PlaylistHandlers_GetWatchLater_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/watch-later", nil)
	rec := httptest.NewRecorder()
	h.GetWatchLater(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_PlaylistHandlers_AddToWatchLater_Unauthorized(t *testing.T) {
	h := NewPlaylistHandlers(nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/watch-later", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.AddToWatchLater(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_RatingHandlers_SetRating_InvalidVideoID(t *testing.T) {
	h := NewRatingHandlers(nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/bad/rating", bytes.NewBufferString(`{"rating":1}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.SetRating(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_RatingHandlers_GetRating_ValidUUID(t *testing.T) {
	h := NewRatingHandlers(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/rating", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.GetRating(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_RatingHandlers_RemoveRating_InvalidVideoID(t *testing.T) {
	h := NewRatingHandlers(nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/bad/rating", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.RemoveRating(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_CreateCaption_Unauthorized(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.CreateCaption(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionHandlers_GetCaptions_InvalidVideoID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/captions", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.GetCaptions(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_GetCaptionContent_InvalidVideoID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/captions/id/content", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "captionId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.GetCaptionContent(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_GetCaptionContent_InvalidCaptionID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/bad/content", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitBadUUID})
	rec := httptest.NewRecorder()
	h.GetCaptionContent(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_UpdateCaption_Unauthorized(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/captions/caption", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.UpdateCaption(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionHandlers_UpdateCaption_InvalidVideoID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/bad/captions/caption", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "captionId": socialUnitItemID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.UpdateCaption(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_UpdateCaption_InvalidCaptionID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/id/captions/bad", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.UpdateCaption(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_DeleteCaption_Unauthorized(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/id/captions/caption", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitItemID})
	rec := httptest.NewRecorder()
	h.DeleteCaption(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionHandlers_DeleteCaption_InvalidVideoID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/bad/captions/caption", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "captionId": socialUnitItemID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.DeleteCaption(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionHandlers_DeleteCaption_InvalidCaptionID(t *testing.T) {
	h := NewCaptionHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/id/captions/bad", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "captionId": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.DeleteCaption(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_GenerateCaptions_Unauthorized(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/id/captions/generate", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.GenerateCaptions(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_GenerateCaptions_InvalidVideoID(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/bad/captions/generate", bytes.NewBufferString(`{}`))
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.GenerateCaptions(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_GetJob_Unauthorized(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs/job", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "jobId": socialUnitJobID})
	rec := httptest.NewRecorder()
	h.GetCaptionGenerationJob(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_GetJob_InvalidVideoID(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/captions/jobs/job", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID, "jobId": socialUnitJobID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.GetCaptionGenerationJob(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_GetJob_InvalidJobID(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs/bad", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID, "jobId": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.GetCaptionGenerationJob(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_ListJobs_Unauthorized(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/captions/jobs", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitVideoID})
	rec := httptest.NewRecorder()
	h.ListCaptionGenerationJobs(rec, req)
	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestUnit_CaptionGenerationHandlers_ListJobs_InvalidVideoID(t *testing.T) {
	h := NewCaptionGenerationHandlers(nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/bad/captions/jobs", nil)
	req = withSocialRouteParams(req, map[string]string{"id": socialUnitBadUUID})
	req = withSocialAuthUser(req)
	rec := httptest.NewRecorder()
	h.ListCaptionGenerationJobs(rec, req)
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func buildTestSocialRouter(t *testing.T) http.Handler {
	t.Helper()
	h := newTestSocialHandler(&mockSocialRepo{})
	r := chi.NewRouter()
	h.RegisterRoutes(r, "test-jwt-secret")
	return r
}

func TestSocialRoutes_MutatingEndpoints_RequireAuth(t *testing.T) {
	router := buildTestSocialRouter(t)

	mutating := []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/social/follow"},
		{http.MethodDelete, "/social/follow/alice.bsky.social"},
		{http.MethodPost, "/social/like"},
		{http.MethodDelete, "/social/like"},
		{http.MethodPost, "/social/comment"},
		{http.MethodDelete, "/social/comment/some-comment-uri"},
		{http.MethodPost, "/social/moderation/label"},
		{http.MethodDelete, "/social/moderation/label/1"},
		{http.MethodPost, "/social/ingest/alice.bsky.social"},
	}

	for _, tc := range mutating {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusUnauthorized, rec.Code,
				"expected 401 without auth on %s %s", tc.method, tc.path)
		})
	}
}

func TestSocialRoutes_ReadOnlyEndpoints_PublicAccess(t *testing.T) {
	router := buildTestSocialRouter(t)

	readonly := []struct {
		path string
	}{
		{"/social/actors/alice.bsky.social"},
		{"/social/actors/alice.bsky.social/stats"},
		{"/social/followers/alice.bsky.social"},
		{"/social/following/alice.bsky.social"},
	}

	for _, tc := range readonly {
		t.Run(tc.path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			assert.NotEqual(t, http.StatusUnauthorized, rec.Code,
				"expected public access (not 401) on GET %s", tc.path)
		})
	}
}
