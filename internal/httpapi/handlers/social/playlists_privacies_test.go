package social

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// mockChannelResolver resolves handles for tests.
type mockChannelResolver struct {
	channels map[string]*domain.Channel
}

func (m *mockChannelResolver) GetChannelByHandle(_ context.Context, handle string) (*domain.Channel, error) {
	ch, ok := m.channels[handle]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return ch, nil
}

// mockPlaylistSvcPrivacies is a minimal PlaylistServiceInterface for privacies tests.
type mockPlaylistSvcPrivacies struct {
	PlaylistServiceInterface
	listResult *domain.PlaylistListResponse
}

func (m *mockPlaylistSvcPrivacies) ListPlaylists(_ context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error) {
	if m.listResult != nil {
		return m.listResult, nil
	}
	return &domain.PlaylistListResponse{Playlists: []*domain.Playlist{}, Total: 0}, nil
}

func TestGetPlaylistPrivacies_OK(t *testing.T) {
	svc := &mockPlaylistSvcPrivacies{}
	h := NewPlaylistHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/video-playlists/privacies", nil)
	rr := httptest.NewRecorder()
	h.GetPrivacies(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !resp.Success {
		t.Fatal("expected success=true")
	}
	if len(resp.Data) == 0 {
		t.Fatal("expected non-empty privacies map")
	}
}

func TestGetChannelPlaylists_Found(t *testing.T) {
	channelID := uuid.New()
	ownerID := uuid.New()

	resolver := &mockChannelResolver{
		channels: map[string]*domain.Channel{
			"my-channel": {ID: channelID, UserID: ownerID},
		},
	}

	playlistSvc := &mockPlaylistSvcPrivacies{
		listResult: &domain.PlaylistListResponse{
			Playlists: []*domain.Playlist{{ID: uuid.New(), Name: "Test Playlist"}},
			Total:     1,
		},
	}

	h := GetChannelPlaylistsHandler(resolver, playlistSvc)

	r := chi.NewRouter()
	r.Get("/{channelHandle}/video-playlists", h)

	req := httptest.NewRequest(http.MethodGet, "/my-channel/video-playlists", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestGetChannelPlaylists_NotFound(t *testing.T) {
	resolver := &mockChannelResolver{channels: map[string]*domain.Channel{}}
	playlistSvc := &mockPlaylistSvcPrivacies{}
	h := GetChannelPlaylistsHandler(resolver, playlistSvc)

	r := chi.NewRouter()
	r.Get("/{channelHandle}/video-playlists", h)

	req := httptest.NewRequest(http.MethodGet, "/no-such-channel/video-playlists", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}
