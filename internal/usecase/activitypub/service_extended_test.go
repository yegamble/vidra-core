package activitypub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/security"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newTestService(apRepo *MockActivityPubRepository) *Service {
	cfg := &config.Config{
		PublicBaseURL: "https://example.com",
	}
	return NewService(apRepo, nil, nil, nil, cfg)
}

func TestFetchRemoteActor_CacheError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	repo.On("GetRemoteActor", ctx, "https://remote.example.com/users/alice").
		Return(nil, errors.New("db error"))

	_, err := svc.FetchRemoteActor(ctx, "https://remote.example.com/users/alice")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check cache")
}

func TestFetchRemoteActor_CacheHit(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	recentTime := time.Now().Add(-1 * time.Hour)
	cachedActor := &domain.APRemoteActor{
		ActorURI:      "https://remote.example.com/users/alice",
		Username:      "alice",
		Domain:        "remote.example.com",
		LastFetchedAt: &recentTime,
	}
	repo.On("GetRemoteActor", ctx, "https://remote.example.com/users/alice").
		Return(cachedActor, nil)

	result, err := svc.FetchRemoteActor(ctx, "https://remote.example.com/users/alice")
	require.NoError(t, err)
	assert.Equal(t, "alice", result.Username)
	assert.Equal(t, "remote.example.com", result.Domain)
}

func TestFetchRemoteActor_CacheExpired_FullFetch(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	actorJSON := domain.Actor{
		Type:              "Person",
		PreferredUsername: "bob",
		Name:              "Bob",
		Summary:           "Test user",
		Inbox:             "https://remote.example.com/users/bob/inbox",
		Outbox:            "https://remote.example.com/users/bob/outbox",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(actorJSON)
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/bob"

	oldTime := time.Now().Add(-48 * time.Hour)
	repo.On("GetRemoteActor", ctx, actorURI).
		Return(&domain.APRemoteActor{LastFetchedAt: &oldTime}, nil)
	repo.On("UpsertRemoteActor", ctx, mock.AnythingOfType("*domain.APRemoteActor")).Return(nil)

	result, err := svc.FetchRemoteActor(ctx, actorURI)
	require.NoError(t, err)
	assert.Equal(t, "bob", result.Username)
	assert.Contains(t, result.InboxURL, "/inbox")
	repo.AssertExpectations(t)
}

func TestFetchRemoteActor_NotCached_FullFetch(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	actorJSON := domain.Actor{
		Type:              "Person",
		PreferredUsername: "charlie",
		Name:              "Charlie",
		Inbox:             "https://remote.example.com/users/charlie/inbox",
		Outbox:            "https://remote.example.com/users/charlie/outbox",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		_ = json.NewEncoder(w).Encode(actorJSON)
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/charlie"

	repo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil)
	repo.On("UpsertRemoteActor", ctx, mock.AnythingOfType("*domain.APRemoteActor")).Return(nil)

	result, err := svc.FetchRemoteActor(ctx, actorURI)
	require.NoError(t, err)
	assert.Equal(t, "charlie", result.Username)
}

func TestFetchRemoteActor_BadStatus(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/missing"

	repo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil)

	_, err := svc.FetchRemoteActor(ctx, actorURI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status code")
}

func TestFetchRemoteActor_InvalidJSON(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/activity+json")
		fmt.Fprint(w, "not json")
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/bad"

	repo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil)

	_, err := svc.FetchRemoteActor(ctx, actorURI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to parse actor")
}

func TestFetchRemoteActor_UpsertError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	actorJSON := domain.Actor{
		Type:              "Person",
		PreferredUsername: "dave",
		Inbox:             "https://remote.example.com/users/dave/inbox",
		Outbox:            "https://remote.example.com/users/dave/outbox",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(actorJSON)
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/dave"

	repo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil)
	repo.On("UpsertRemoteActor", ctx, mock.Anything).Return(errors.New("db error"))

	_, err := svc.FetchRemoteActor(ctx, actorURI)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to cache actor")
}

func TestFetchRemoteActor_WithPublicKeyAndEndpoints(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	actorJSON := domain.Actor{
		Type:              "Person",
		PreferredUsername: "eve",
		Inbox:             "https://remote.example.com/users/eve/inbox",
		Outbox:            "https://remote.example.com/users/eve/outbox",
		PublicKey: &domain.PublicKey{
			ID:           "https://remote.example.com/users/eve#main-key",
			Owner:        "https://remote.example.com/users/eve",
			PublicKeyPem: "-----BEGIN PUBLIC KEY-----\ntest\n-----END PUBLIC KEY-----",
		},
		Endpoints: &domain.Endpoints{
			SharedInbox: "https://remote.example.com/inbox",
		},
		Icon:  &domain.Image{URL: "https://remote.example.com/avatar.png"},
		Image: &domain.Image{URL: "https://remote.example.com/banner.png"},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(actorJSON)
	}))
	defer server.Close()

	svc.httpClient = server.Client()
	svc.urlValidator = security.NewURLValidatorAllowPrivate()
	actorURI := server.URL + "/users/eve"

	repo.On("GetRemoteActor", ctx, actorURI).Return(nil, nil)
	repo.On("UpsertRemoteActor", ctx, mock.AnythingOfType("*domain.APRemoteActor")).Return(nil)

	result, err := svc.FetchRemoteActor(ctx, actorURI)
	require.NoError(t, err)
	assert.Equal(t, "eve", result.Username)
	assert.NotEmpty(t, result.PublicKeyPem)
	require.NotNil(t, result.SharedInbox)
	assert.Equal(t, "https://remote.example.com/inbox", *result.SharedInbox)
	require.NotNil(t, result.IconURL)
	require.NotNil(t, result.ImageURL)
}

func TestHandleInboxActivity_MissingActor(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)

	activity := map[string]interface{}{
		"type": "Follow",
	}

	err := svc.HandleInboxActivity(context.Background(), activity, httptest.NewRequest("POST", "/inbox", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing or invalid actor")
}

func TestHandleInboxActivity_FetchRemoteActorError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	repo.On("GetRemoteActor", ctx, "https://remote.example.com/users/alice").
		Return(nil, errors.New("cache error"))

	activity := map[string]interface{}{
		"actor": "https://remote.example.com/users/alice",
		"type":  "Follow",
	}

	err := svc.HandleInboxActivity(ctx, activity, httptest.NewRequest("POST", "/inbox", nil))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to fetch remote actor")
}

func TestUpdateComment_NilCommentRepo(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	svc.commentRepo = nil

	err := svc.UpdateComment(context.Background(), uuid.New().String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comment repository not configured")
}

func TestUpdateComment_InvalidUUID(t *testing.T) {
	repo := new(MockActivityPubRepository)
	commentRepo := new(MockCommentRepository)
	svc := newTestService(repo)
	svc.commentRepo = commentRepo

	err := svc.UpdateComment(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid comment ID")
}

func TestUpdateComment_GetByIDError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	commentRepo := new(MockCommentRepository)
	svc := newTestService(repo)
	svc.commentRepo = commentRepo
	ctx := context.Background()

	commentID := uuid.New()
	commentRepo.On("GetByID", ctx, commentID).Return(nil, errors.New("not found"))

	err := svc.UpdateComment(ctx, commentID.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get comment")
}

func TestDeleteComment_NilCommentRepo(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	svc.commentRepo = nil

	err := svc.DeleteComment(context.Background(), uuid.New().String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "comment repository not configured")
}

func TestDeleteComment_InvalidUUID(t *testing.T) {
	repo := new(MockActivityPubRepository)
	commentRepo := new(MockCommentRepository)
	svc := newTestService(repo)
	svc.commentRepo = commentRepo

	err := svc.DeleteComment(context.Background(), "not-a-uuid")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid comment ID")
}

func TestDeleteComment_GetByIDError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	commentRepo := new(MockCommentRepository)
	svc := newTestService(repo)
	svc.commentRepo = commentRepo
	ctx := context.Background()

	commentID := uuid.New()
	commentRepo.On("GetByID", ctx, commentID).Return(nil, errors.New("not found"))

	err := svc.DeleteComment(ctx, commentID.String())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get comment")
}

func TestDeliverActivity_GetKeysError(t *testing.T) {
	repo := new(MockActivityPubRepository)
	svc := newTestService(repo)
	ctx := context.Background()

	repo.On("GetActorKeys", ctx, "user-1").Return("", "", errors.New("no keys"))

	err := svc.DeliverActivity(ctx, "user-1", "https://remote.example.com/inbox", map[string]string{"type": "Follow"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get actor keys")
}
