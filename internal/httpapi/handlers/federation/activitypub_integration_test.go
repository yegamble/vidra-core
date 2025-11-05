package federation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/activitypub"
	"athena/internal/config"
	"athena/internal/domain"
)

// MockActivityPubService is a mock for the ActivityPub service
type MockActivityPubService struct {
	mock.Mock
}

func (m *MockActivityPubService) GetLocalActor(ctx context.Context, username string) (*domain.Actor, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Actor), args.Error(1)
}

func (m *MockActivityPubService) FetchRemoteActor(ctx context.Context, actorURI string) (*domain.APRemoteActor, error) {
	args := m.Called(ctx, actorURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.APRemoteActor), args.Error(1)
}

func (m *MockActivityPubService) HandleInboxActivity(ctx context.Context, activity map[string]interface{}, r *http.Request) error {
	args := m.Called(ctx, activity, r)
	return args.Error(0)
}

func (m *MockActivityPubService) GetOutbox(ctx context.Context, username string, page, limit int) (*domain.OrderedCollectionPage, error) {
	args := m.Called(ctx, username, page, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.OrderedCollectionPage), args.Error(1)
}

func (m *MockActivityPubService) GetFollowers(ctx context.Context, username string, page, limit int) (*domain.OrderedCollectionPage, error) {
	args := m.Called(ctx, username, page, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.OrderedCollectionPage), args.Error(1)
}

func (m *MockActivityPubService) GetFollowing(ctx context.Context, username string, page, limit int) (*domain.OrderedCollectionPage, error) {
	args := m.Called(ctx, username, page, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.OrderedCollectionPage), args.Error(1)
}

func (m *MockActivityPubService) DeliverActivity(ctx context.Context, actorID, inboxURL string, activity interface{}) error {
	args := m.Called(ctx, actorID, inboxURL, activity)
	return args.Error(0)
}

func TestGetActorIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	publicKey, _, _ := activitypub.GenerateKeyPair()

	actor := &domain.Actor{
		Context:           []interface{}{domain.ActivityStreamsContext, domain.SecurityContext},
		Type:              domain.ObjectTypePerson,
		ID:                "https://video.example/users/alice",
		PreferredUsername: "alice",
		Name:              "Alice",
		Inbox:             "https://video.example/users/alice/inbox",
		Outbox:            "https://video.example/users/alice/outbox",
		Followers:         "https://video.example/users/alice/followers",
		Following:         "https://video.example/users/alice/following",
		PublicKey: &domain.PublicKey{
			ID:           "https://video.example/users/alice#main-key",
			Owner:        "https://video.example/users/alice",
			PublicKeyPem: publicKey,
		},
	}

	t.Run("Get actor with ActivityPub content type", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice", nil)
		req.Header.Set("Accept", "application/activity+json")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		mockService.On("GetLocalActor", req.Context(), "alice").Return(actor, nil).Once()

		handlers.GetActor(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/activity+json")

		var response domain.Actor
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, actor.ID, response.ID)
		assert.Equal(t, actor.PreferredUsername, response.PreferredUsername)
		assert.NotNil(t, response.PublicKey)

		mockService.AssertExpectations(t)
	})

	t.Run("Get actor not found", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/nonexistent", nil)
		req.Header.Set("Accept", "application/activity+json")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "nonexistent")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		mockService.On("GetLocalActor", req.Context(), "nonexistent").Return(nil, assert.AnError).Once()

		handlers.GetActor(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)

		mockService.AssertExpectations(t)
	})
}

func TestPostInboxIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Post valid follow activity to inbox", func(t *testing.T) {
		activity := map[string]interface{}{
			"@context": domain.ActivityStreamsContext,
			"type":     "Follow",
			"id":       "https://mastodon.example/activities/follow-1",
			"actor":    "https://mastodon.example/users/alice",
			"object":   "https://video.example/users/bob",
		}

		body, _ := json.Marshal(activity)
		req := httptest.NewRequest("POST", "/users/bob/inbox", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/activity+json")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "bob")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		mockService.On("HandleInboxActivity", req.Context(), mock.MatchedBy(func(act map[string]interface{}) bool {
			return act["type"] == "Follow"
		}), req).Return(nil).Once()

		handlers.PostInbox(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		mockService.AssertExpectations(t)
	})

	t.Run("Post activity with invalid JSON", func(t *testing.T) {
		req := httptest.NewRequest("POST", "/users/bob/inbox", bytes.NewReader([]byte("invalid json")))
		req.Header.Set("Content-Type", "application/activity+json")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "bob")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		handlers.PostInbox(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Post activity processing error", func(t *testing.T) {
		activity := map[string]interface{}{
			"type": "Follow",
		}

		body, _ := json.Marshal(activity)
		req := httptest.NewRequest("POST", "/users/bob/inbox", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/activity+json")

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "bob")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		mockService.On("HandleInboxActivity", req.Context(), mock.Anything, req).Return(assert.AnError).Once()

		handlers.PostInbox(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)

		mockService.AssertExpectations(t)
	})
}

func TestPostSharedInboxIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Post to shared inbox", func(t *testing.T) {
		activity := map[string]interface{}{
			"@context": domain.ActivityStreamsContext,
			"type":     "Create",
			"id":       "https://mastodon.example/activities/create-1",
			"actor":    "https://mastodon.example/users/alice",
			"object": map[string]interface{}{
				"type":    "Note",
				"content": "Hello world",
			},
		}

		body, _ := json.Marshal(activity)
		req := httptest.NewRequest("POST", "/inbox", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/activity+json")

		w := httptest.NewRecorder()

		mockService.On("HandleInboxActivity", req.Context(), mock.MatchedBy(func(act map[string]interface{}) bool {
			return act["type"] == "Create"
		}), req).Return(nil).Once()

		handlers.PostSharedInbox(w, req)

		assert.Equal(t, http.StatusAccepted, w.Code)

		mockService.AssertExpectations(t)
	})
}

func TestGetOutboxIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Get outbox collection (non-paginated)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice/outbox", nil)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		handlers.GetOutbox(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/activity+json")

		var collection domain.OrderedCollection
		err := json.Unmarshal(w.Body.Bytes(), &collection)
		require.NoError(t, err)

		assert.Equal(t, domain.ObjectTypeOrderedCollection, collection.Type)
		assert.Equal(t, "https://video.example/users/alice/outbox", collection.ID)
	})

	t.Run("Get outbox page (paginated)", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice/outbox?page=0", nil)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		collectionPage := &domain.OrderedCollectionPage{
			Context:      domain.ActivityStreamsContext,
			Type:         domain.ObjectTypeOrderedCollectionPage,
			ID:           "https://video.example/users/alice/outbox?page=0",
			TotalItems:   5,
			PartOf:       "https://video.example/users/alice/outbox",
			OrderedItems: []interface{}{},
		}

		mockService.On("GetOutbox", req.Context(), "alice", 0, 20).Return(collectionPage, nil).Once()

		handlers.GetOutbox(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response domain.OrderedCollectionPage
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, domain.ObjectTypeOrderedCollectionPage, response.Type)
		assert.Equal(t, 5, response.TotalItems)

		mockService.AssertExpectations(t)
	})
}

func TestGetFollowersIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 20,
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Get followers collection page", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice/followers?page=0", nil)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		followers := []interface{}{
			"https://mastodon.example/users/bob",
			"https://mastodon.example/users/charlie",
		}

		collectionPage := &domain.OrderedCollectionPage{
			Context:      domain.ActivityStreamsContext,
			Type:         domain.ObjectTypeOrderedCollectionPage,
			ID:           "https://video.example/users/alice/followers?page=0",
			TotalItems:   2,
			PartOf:       "https://video.example/users/alice/followers",
			OrderedItems: followers,
		}

		mockService.On("GetFollowers", req.Context(), "alice", 0, 20).Return(collectionPage, nil).Once()

		handlers.GetFollowers(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response domain.OrderedCollectionPage
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, 2, response.TotalItems)
		assert.Len(t, response.OrderedItems.([]interface{}), 2)

		mockService.AssertExpectations(t)
	})
}

func TestWebFingerIntegration(t *testing.T) {
	cfg := &config.Config{
		PublicBaseURL:     "https://video.example",
		ActivityPubDomain: "video.example",
	}

	handlers := &ActivityPubHandlers{
		service: nil, // Not needed for WebFinger
		cfg:     cfg,
	}

	t.Run("WebFinger with acct resource", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=acct:alice@video.example", nil)
		w := httptest.NewRecorder()

		handlers.WebFinger(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "application/jrd+json")

		var response domain.WebFingerResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "acct:alice@video.example", response.Subject)

		// Check for self link
		var foundSelfLink bool
		for _, link := range response.Links {
			if link.Rel == "self" && link.Type == "application/activity+json" {
				foundSelfLink = true
				assert.Equal(t, "https://video.example/users/alice", link.Href)
			}
		}
		assert.True(t, foundSelfLink, "Expected to find self link")
	})

	t.Run("WebFinger with https resource", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=https://video.example/users/bob", nil)
		w := httptest.NewRecorder()

		handlers.WebFinger(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response domain.WebFingerResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "acct:bob@video.example", response.Subject)
	})

	t.Run("WebFinger with malformed resource", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/.well-known/webfinger?resource=invalid:format", nil)
		w := httptest.NewRecorder()

		handlers.WebFinger(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestActivityTypesIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	activityTypes := []struct {
		name     string
		activity map[string]interface{}
	}{
		{
			name: "Follow Activity",
			activity: map[string]interface{}{
				"type":   "Follow",
				"id":     "https://mastodon.example/activities/1",
				"actor":  "https://mastodon.example/users/alice",
				"object": "https://video.example/users/bob",
			},
		},
		{
			name: "Like Activity",
			activity: map[string]interface{}{
				"type":   "Like",
				"id":     "https://mastodon.example/activities/2",
				"actor":  "https://mastodon.example/users/alice",
				"object": "https://video.example/videos/123",
			},
		},
		{
			name: "Announce Activity",
			activity: map[string]interface{}{
				"type":   "Announce",
				"id":     "https://mastodon.example/activities/3",
				"actor":  "https://mastodon.example/users/alice",
				"object": "https://video.example/videos/123",
			},
		},
		{
			name: "Create Activity",
			activity: map[string]interface{}{
				"type":  "Create",
				"id":    "https://mastodon.example/activities/4",
				"actor": "https://mastodon.example/users/alice",
				"object": map[string]interface{}{
					"type":    "Note",
					"content": "Test comment",
				},
			},
		},
		{
			name: "Update Activity",
			activity: map[string]interface{}{
				"type":  "Update",
				"id":    "https://mastodon.example/activities/5",
				"actor": "https://mastodon.example/users/alice",
				"object": map[string]interface{}{
					"type": "Person",
					"name": "Alice Updated",
				},
			},
		},
		{
			name: "Delete Activity",
			activity: map[string]interface{}{
				"type":   "Delete",
				"id":     "https://mastodon.example/activities/6",
				"actor":  "https://mastodon.example/users/alice",
				"object": "https://mastodon.example/statuses/123",
			},
		},
	}

	for _, tt := range activityTypes {
		t.Run(tt.name, func(t *testing.T) {
			body, _ := json.Marshal(tt.activity)
			req := httptest.NewRequest("POST", "/users/bob/inbox", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/activity+json")

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("username", "bob")
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()

			mockService.On("HandleInboxActivity", req.Context(), mock.Anything, req).Return(nil).Once()

			handlers.PostInbox(w, req)

			assert.Equal(t, http.StatusAccepted, w.Code)

			mockService.AssertExpectations(t)
		})
	}
}

func TestPaginationIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL:                   "https://video.example",
		ActivityPubMaxActivitiesPerPage: 10,
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Outbox pagination with next and prev", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice/outbox?page=1", nil)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		collectionPage := &domain.OrderedCollectionPage{
			Context:      domain.ActivityStreamsContext,
			Type:         domain.ObjectTypeOrderedCollectionPage,
			ID:           "https://video.example/users/alice/outbox?page=1",
			TotalItems:   25,
			PartOf:       "https://video.example/users/alice/outbox",
			Prev:         "https://video.example/users/alice/outbox?page=0",
			Next:         "https://video.example/users/alice/outbox?page=2",
			OrderedItems: []interface{}{},
		}

		mockService.On("GetOutbox", req.Context(), "alice", 1, 10).Return(collectionPage, nil).Once()

		handlers.GetOutbox(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response domain.OrderedCollectionPage
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Contains(t, response.Prev, "page=0")
		assert.Contains(t, response.Next, "page=2")

		mockService.AssertExpectations(t)
	})
}

func TestErrorHandlingIntegration(t *testing.T) {
	mockService := new(MockActivityPubService)
	cfg := &config.Config{
		PublicBaseURL: "https://video.example",
	}

	handlers := NewActivityPubHandlers(mockService, cfg, nil, nil)

	t.Run("Missing username parameter", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users//outbox", nil)
		w := httptest.NewRecorder()

		handlers.GetActor(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("Service returns error", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/users/alice/outbox?page=0", nil)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("username", "alice")
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()

		mockService.On("GetOutbox", req.Context(), "alice", 0, cfg.ActivityPubMaxActivitiesPerPage).
			Return(nil, assert.AnError).Once()

		handlers.GetOutbox(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)

		mockService.AssertExpectations(t)
	})
}
