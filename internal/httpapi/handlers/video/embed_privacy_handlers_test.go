package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// --- mocks ---

type mockEmbedRepo struct {
	getFn             func(ctx context.Context, videoID string) (*domain.VideoEmbedPrivacy, error)
	upsertFn          func(ctx context.Context, privacy *domain.VideoEmbedPrivacy) error
	isDomainAllowedFn func(ctx context.Context, videoID string, domainName string) (bool, error)
}

func (m *mockEmbedRepo) Get(ctx context.Context, videoID string) (*domain.VideoEmbedPrivacy, error) {
	return m.getFn(ctx, videoID)
}

func (m *mockEmbedRepo) Upsert(ctx context.Context, privacy *domain.VideoEmbedPrivacy) error {
	return m.upsertFn(ctx, privacy)
}

func (m *mockEmbedRepo) IsDomainAllowed(ctx context.Context, videoID string, domainName string) (bool, error) {
	return m.isDomainAllowedFn(ctx, videoID, domainName)
}

type mockEmbedVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockEmbedVideoRepo) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return &domain.Video{ID: id, UserID: "owner-1"}, nil
}

// --- tests ---

func TestGetEmbedPrivacy_Success(t *testing.T) {
	embedRepo := &mockEmbedRepo{
		getFn: func(_ context.Context, _ string) (*domain.VideoEmbedPrivacy, error) {
			return &domain.VideoEmbedPrivacy{
				VideoID:        "vid-1",
				Status:         domain.EmbedWhitelist,
				AllowedDomains: []string{"example.com"},
			}, nil
		},
	}
	videoRepo := &mockEmbedVideoRepo{}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/embed-privacy", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.GetEmbedPrivacy(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data domain.VideoEmbedPrivacy `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, domain.EmbedWhitelist, resp.Data.Status)
	assert.Equal(t, []string{"example.com"}, resp.Data.AllowedDomains)
}

func TestUpdateEmbedPrivacy_Success(t *testing.T) {
	var captured *domain.VideoEmbedPrivacy
	embedRepo := &mockEmbedRepo{
		upsertFn: func(_ context.Context, p *domain.VideoEmbedPrivacy) error {
			captured = p
			return nil
		},
	}
	videoRepo := &mockEmbedVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	body := `{"status":3,"allowedDomains":["example.com","test.org"]}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/embed-privacy", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.UpdateEmbedPrivacy(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, captured)
	assert.Equal(t, domain.EmbedWhitelist, captured.Status)
	assert.Equal(t, []string{"example.com", "test.org"}, captured.AllowedDomains)
}

func TestUpdateEmbedPrivacy_InvalidStatus(t *testing.T) {
	embedRepo := &mockEmbedRepo{}
	videoRepo := &mockEmbedVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	body := `{"status":99}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/embed-privacy", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.UpdateEmbedPrivacy(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateEmbedPrivacy_NotOwner(t *testing.T) {
	embedRepo := &mockEmbedRepo{}
	videoRepo := &mockEmbedVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "real-owner"}}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	body := `{"status":1}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/embed-privacy", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "other-user",
	))
	w := httptest.NewRecorder()

	h.UpdateEmbedPrivacy(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestCheckDomainAllowed_Allowed(t *testing.T) {
	embedRepo := &mockEmbedRepo{
		isDomainAllowedFn: func(_ context.Context, _ string, _ string) (bool, error) {
			return true, nil
		},
	}
	videoRepo := &mockEmbedVideoRepo{}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/embed-privacy/allowed?domain=example.com", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.CheckDomainAllowed(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data map[string]bool `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.True(t, resp.Data["allowed"])
}

func TestCheckDomainAllowed_MissingDomain(t *testing.T) {
	embedRepo := &mockEmbedRepo{}
	videoRepo := &mockEmbedVideoRepo{}
	h := NewEmbedPrivacyHandlers(embedRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/embed-privacy/allowed", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.CheckDomainAllowed(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}
