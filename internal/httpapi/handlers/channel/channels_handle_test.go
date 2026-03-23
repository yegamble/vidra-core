package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"vidra-core/internal/domain"
	ucchannel "vidra-core/internal/usecase/channel"
)

// buildHandleRouter builds a chi router mirroring the /video-channels/{channelHandle} routes
// added to routes.go for PeerTube compatibility.
func buildHandleRouter(h *ChannelHandlers) chi.Router {
	r := chi.NewRouter()
	r.Get("/{channelHandle}", h.GetChannelByHandleParam)
	r.Get("/{channelHandle}/videos", h.GetChannelVideosByHandleParam)
	return r
}

func newChannelService(channelRepo *unitChannelRepoStub) *ucchannel.Service {
	return ucchannel.NewService(channelRepo, &unitUserRepoStub{}, nil)
}

// TestGetChannelByHandleParam_Found verifies that GET /video-channels/{handle} resolves by handle.
func TestGetChannelByHandleParam_Found(t *testing.T) {
	channelRepo := &unitChannelRepoStub{}
	channelID := uuid.MustParse("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	ch := &domain.Channel{
		ID:          channelID,
		Handle:      "my-channel",
		DisplayName: "My Channel",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	channelRepo.getByHandleFn = func(_ context.Context, handle string) (*domain.Channel, error) {
		if handle == "my-channel" {
			return ch, nil
		}
		return nil, domain.ErrNotFound
	}

	svc := newChannelService(channelRepo)
	h := NewChannelHandlers(svc, nil)
	r := buildHandleRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/my-channel", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp struct {
		Success bool                   `json:"success"`
		Data    map[string]interface{} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
}

// TestGetChannelByHandleParam_NotFound verifies 404 for unknown handles.
func TestGetChannelByHandleParam_NotFound(t *testing.T) {
	channelRepo := &unitChannelRepoStub{}
	svc := newChannelService(channelRepo)
	h := NewChannelHandlers(svc, nil)
	r := buildHandleRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/no-such-channel", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rr.Code)
	}
}

// TestGetChannelVideosByHandleParam_Found verifies the videos sub-route works by handle.
func TestGetChannelVideosByHandleParam_Found(t *testing.T) {
	channelRepo := &unitChannelRepoStub{}
	channelID := uuid.MustParse("bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb")
	ch := &domain.Channel{
		ID:     channelID,
		Handle: "test-channel",
	}
	channelRepo.getByHandleFn = func(_ context.Context, handle string) (*domain.Channel, error) {
		if handle == "test-channel" {
			return ch, nil
		}
		return nil, domain.ErrNotFound
	}

	svc := newChannelService(channelRepo)
	h := NewChannelHandlers(svc, nil)
	r := buildHandleRouter(h)

	req := httptest.NewRequest(http.MethodGet, "/test-channel/videos", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
