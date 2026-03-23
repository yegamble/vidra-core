package player

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

// ---------------------------------------------------------------------------
// Mock repository
// ---------------------------------------------------------------------------

type mockPlayerSettingsRepo struct {
	settings *domain.PlayerSettings
	err      error
}

func (m *mockPlayerSettingsRepo) GetByVideoID(_ context.Context, _ string) (*domain.PlayerSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.settings, nil
}

func (m *mockPlayerSettingsRepo) UpsertByVideoID(_ context.Context, videoID string, s *domain.PlayerSettings) (*domain.PlayerSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	s.VideoID = &videoID
	s.ID = 1
	return s, nil
}

func (m *mockPlayerSettingsRepo) GetByChannelHandle(_ context.Context, _ string) (*domain.PlayerSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.settings, nil
}

func (m *mockPlayerSettingsRepo) UpsertByChannelHandle(_ context.Context, handle string, s *domain.PlayerSettings) (*domain.PlayerSettings, error) {
	if m.err != nil {
		return nil, m.err
	}
	s.ChannelHandle = &handle
	s.ID = 1
	return s, nil
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func withPlayerChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// GetVideoSettings tests
// ---------------------------------------------------------------------------

func TestGetVideoSettings_OK(t *testing.T) {
	videoID := "vid-1"
	repo := &mockPlayerSettingsRepo{
		settings: &domain.PlayerSettings{
			ID:             1,
			VideoID:        &videoID,
			Autoplay:       true,
			DefaultQuality: "720p",
			DefaultSpeed:   1.0,
		},
	}
	h := NewSettingsHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/player-settings/videos/vid-1", nil)
	req = withPlayerChiParam(req, "videoId", "vid-1")
	rr := httptest.NewRecorder()

	h.GetVideoSettings(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data domain.PlayerSettings `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.Data.ID)
	assert.True(t, resp.Data.Autoplay)
}

func TestGetVideoSettings_NotFound(t *testing.T) {
	repo := &mockPlayerSettingsRepo{err: domain.ErrNotFound}
	h := NewSettingsHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/player-settings/videos/vid-1", nil)
	req = withPlayerChiParam(req, "videoId", "vid-1")
	rr := httptest.NewRecorder()

	h.GetVideoSettings(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

func TestGetVideoSettings_MissingParam(t *testing.T) {
	h := NewSettingsHandlers(&mockPlayerSettingsRepo{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/player-settings/videos/", nil)
	// No chi param set, URLParam returns ""
	rr := httptest.NewRecorder()

	h.GetVideoSettings(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// UpdateVideoSettings tests
// ---------------------------------------------------------------------------

func TestUpdateVideoSettings_OK(t *testing.T) {
	repo := &mockPlayerSettingsRepo{}
	h := NewSettingsHandlers(repo)

	body := `{"autoplay":false,"loop":true,"defaultQuality":"1080p","defaultSpeed":1.5}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/player-settings/videos/vid-1", strings.NewReader(body))
	req = withPlayerChiParam(req, "videoId", "vid-1")
	rr := httptest.NewRecorder()

	h.UpdateVideoSettings(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data domain.PlayerSettings `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.False(t, resp.Data.Autoplay)
	assert.True(t, resp.Data.Loop)
	assert.Equal(t, "1080p", resp.Data.DefaultQuality)
}

func TestUpdateVideoSettings_InvalidBody(t *testing.T) {
	h := NewSettingsHandlers(&mockPlayerSettingsRepo{})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/player-settings/videos/vid-1", strings.NewReader("{bad"))
	req = withPlayerChiParam(req, "videoId", "vid-1")
	rr := httptest.NewRecorder()

	h.UpdateVideoSettings(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// GetChannelSettings tests
// ---------------------------------------------------------------------------

func TestGetChannelSettings_OK(t *testing.T) {
	handle := "my-channel"
	repo := &mockPlayerSettingsRepo{
		settings: &domain.PlayerSettings{
			ID:            1,
			ChannelHandle: &handle,
			Autoplay:      false,
			Theatre:       true,
		},
	}
	h := NewSettingsHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/player-settings/video-channels/my-channel", nil)
	req = withPlayerChiParam(req, "handle", "my-channel")
	rr := httptest.NewRecorder()

	h.GetChannelSettings(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestGetChannelSettings_NotFound(t *testing.T) {
	repo := &mockPlayerSettingsRepo{err: domain.ErrNotFound}
	h := NewSettingsHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/player-settings/video-channels/unknown", nil)
	req = withPlayerChiParam(req, "handle", "unknown")
	rr := httptest.NewRecorder()

	h.GetChannelSettings(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------------------------------------------------------------------------
// UpdateChannelSettings tests
// ---------------------------------------------------------------------------

func TestUpdateChannelSettings_OK(t *testing.T) {
	repo := &mockPlayerSettingsRepo{}
	h := NewSettingsHandlers(repo)

	body := `{"autoplay":true,"theatre":true,"subtitlesEnabled":true}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/player-settings/video-channels/my-channel", strings.NewReader(body))
	req = withPlayerChiParam(req, "handle", "my-channel")
	rr := httptest.NewRecorder()

	h.UpdateChannelSettings(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data domain.PlayerSettings `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.True(t, resp.Data.Autoplay)
	assert.True(t, resp.Data.Theatre)
	assert.True(t, resp.Data.SubtitlesEnabled)
}

func TestUpdateChannelSettings_InvalidBody(t *testing.T) {
	h := NewSettingsHandlers(&mockPlayerSettingsRepo{})

	req := httptest.NewRequest(http.MethodPut, "/api/v1/player-settings/video-channels/my-channel", strings.NewReader("{bad"))
	req = withPlayerChiParam(req, "handle", "my-channel")
	rr := httptest.NewRecorder()

	h.UpdateChannelSettings(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
