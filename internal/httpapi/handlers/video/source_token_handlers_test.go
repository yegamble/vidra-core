package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"vidra-core/internal/domain"
)

// mockTokenStore is an in-memory token store for testing.
type mockTokenStore struct {
	data map[string]string
}

func newMockTokenStore() *mockTokenStore {
	return &mockTokenStore{data: make(map[string]string)}
}

func (m *mockTokenStore) Set(_ context.Context, key, value string, _ time.Duration) error {
	m.data[key] = value
	return nil
}

func (m *mockTokenStore) Get(_ context.Context, key string) (string, error) {
	v, ok := m.data[key]
	if !ok {
		return "", domain.ErrNotFound
	}
	return v, nil
}

func (m *mockTokenStore) Del(_ context.Context, key string) error {
	delete(m.data, key)
	return nil
}

// mockVideoRepoSource implements only GetByID and Update for source/token tests.
type mockVideoRepoSource struct {
	videos       map[string]*domain.Video
	updateCalled bool
}

func (m *mockVideoRepoSource) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if v, ok := m.videos[id]; ok {
		return v, nil
	}
	return nil, domain.ErrVideoNotFound
}

func (m *mockVideoRepoSource) Update(_ context.Context, v *domain.Video) error {
	m.videos[v.ID] = v
	m.updateCalled = true
	return nil
}

// TestDeleteVideoSource_Owner verifies 204 when the owner deletes the source.
func TestDeleteVideoSource_Owner(t *testing.T) {
	ownerID := "user-owner"
	videoID := "vid-1"
	repo := &mockVideoRepoSource{
		videos: map[string]*domain.Video{
			videoID: {
				ID:          videoID,
				UserID:      ownerID,
				OutputPaths: map[string]string{"source": "/data/vid-1.mp4"},
			},
		},
	}

	h := DeleteVideoSourceHandler(repo)
	req := httptest.NewRequest(http.MethodDelete, "/videos/"+videoID+"/source", nil)
	req = withUserID(req, ownerID)
	req = withVideoIDParam(req, videoID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !repo.updateCalled {
		t.Fatal("expected video Update to be called")
	}
	if repo.videos[videoID].OutputPaths["source"] != "" {
		t.Errorf("expected source path cleared, got %q", repo.videos[videoID].OutputPaths["source"])
	}
}

// TestDeleteVideoSource_NotOwner verifies 403 for non-owner.
func TestDeleteVideoSource_NotOwner(t *testing.T) {
	repo := &mockVideoRepoSource{
		videos: map[string]*domain.Video{
			"vid-1": {ID: "vid-1", UserID: "owner"},
		},
	}
	h := DeleteVideoSourceHandler(repo)
	req := httptest.NewRequest(http.MethodDelete, "/videos/vid-1/source", nil)
	req = withUserID(req, "other-user")
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}

// TestDeleteVideoSource_Unauthenticated verifies 401 with no auth.
func TestDeleteVideoSource_Unauthenticated(t *testing.T) {
	repo := &mockVideoRepoSource{}
	h := DeleteVideoSourceHandler(repo)
	req := httptest.NewRequest(http.MethodDelete, "/videos/vid-1/source", nil)
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestDeleteVideoSource_NotFound verifies 404 for unknown video.
func TestDeleteVideoSource_NotFound(t *testing.T) {
	repo := &mockVideoRepoSource{videos: map[string]*domain.Video{}}
	h := DeleteVideoSourceHandler(repo)
	req := httptest.NewRequest(http.MethodDelete, "/videos/missing/source", nil)
	req = withUserID(req, "user-1")
	req = withVideoIDParam(req, "missing")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestCreateVideoToken_Owner verifies 200 with a token when owner requests.
func TestCreateVideoToken_Owner(t *testing.T) {
	ownerID := "user-owner"
	videoID := "vid-1"
	repo := &mockVideoRepoSource{
		videos: map[string]*domain.Video{
			videoID: {ID: videoID, UserID: ownerID},
		},
	}
	store := newMockTokenStore()

	h := CreateVideoTokenHandler(repo, store)
	req := httptest.NewRequest(http.MethodPost, "/videos/"+videoID+"/token", nil)
	req = withUserID(req, ownerID)
	req = withVideoIDParam(req, videoID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Data struct {
			Token     string `json:"token"`
			ExpiresIn int    `json:"expiresIn"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Token == "" {
		t.Error("expected non-empty token in response")
	}
	if resp.Data.ExpiresIn != 14400 {
		t.Errorf("expected expiresIn=14400 (4h), got %d", resp.Data.ExpiresIn)
	}
	// Token must be stored in Redis
	key := "video:access:" + resp.Data.Token
	if store.data[key] != videoID {
		t.Errorf("expected token stored in store with videoID=%s, got %q", videoID, store.data[key])
	}
}

// TestCreateVideoToken_Unauthenticated verifies 401 with no auth.
func TestCreateVideoToken_Unauthenticated(t *testing.T) {
	repo := &mockVideoRepoSource{videos: map[string]*domain.Video{}}
	store := newMockTokenStore()
	h := CreateVideoTokenHandler(repo, store)

	req := httptest.NewRequest(http.MethodPost, "/videos/vid-1/token", nil)
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestCreateVideoToken_NotOwner verifies 403 for non-owner.
func TestCreateVideoToken_NotOwner(t *testing.T) {
	repo := &mockVideoRepoSource{
		videos: map[string]*domain.Video{
			"vid-1": {ID: "vid-1", UserID: "owner"},
		},
	}
	store := newMockTokenStore()
	h := CreateVideoTokenHandler(repo, store)

	req := httptest.NewRequest(http.MethodPost, "/videos/vid-1/token", nil)
	req = withUserID(req, "other")
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rr.Code)
	}
}
