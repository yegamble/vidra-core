package channel

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

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// ---------------------------------------------------------------------------
// Mock repository
// ---------------------------------------------------------------------------

type mockChannelSyncRepo struct {
	sync *domain.ChannelSync
	err  error
}

func (m *mockChannelSyncRepo) CreateSync(_ context.Context, s *domain.ChannelSync) (*domain.ChannelSync, error) {
	if m.err != nil {
		return nil, m.err
	}
	s.ID = 1
	return s, nil
}

func (m *mockChannelSyncRepo) DeleteSync(_ context.Context, _ int64) error {
	return m.err
}

func (m *mockChannelSyncRepo) GetSync(_ context.Context, _ int64) (*domain.ChannelSync, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.sync, nil
}

func (m *mockChannelSyncRepo) TriggerSync(_ context.Context, _ int64) error {
	return m.err
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func withSyncUserContext(r *http.Request, userID string) *http.Request {
	ctx := context.WithValue(r.Context(), middleware.UserIDKey, userID)
	return r.WithContext(ctx)
}

func withSyncChiParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

// ---------------------------------------------------------------------------
// CreateSync tests
// ---------------------------------------------------------------------------

func TestCreateSync_OK(t *testing.T) {
	repo := &mockChannelSyncRepo{}
	h := NewSyncHandlers(repo)

	body := `{"externalChannelUrl":"https://example.com/channel/123","videoChannelId":"ch-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs", strings.NewReader(body))
	req = withSyncUserContext(req, "user-1")
	rr := httptest.NewRecorder()

	h.CreateSync(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	var resp struct {
		Data domain.ChannelSync `json:"data"`
	}
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&resp))
	assert.Equal(t, int64(1), resp.Data.ID)
	assert.Equal(t, "ch-1", resp.Data.ChannelID)
	assert.Equal(t, int(domain.ChannelSyncStateWaitingFirstRun), resp.Data.State)
}

func TestCreateSync_Unauthorized(t *testing.T) {
	h := NewSyncHandlers(&mockChannelSyncRepo{})

	body := `{"externalChannelUrl":"https://example.com/channel/123","videoChannelId":"ch-1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs", strings.NewReader(body))
	rr := httptest.NewRecorder()

	h.CreateSync(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestCreateSync_InvalidBody(t *testing.T) {
	h := NewSyncHandlers(&mockChannelSyncRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs", strings.NewReader("{bad"))
	req = withSyncUserContext(req, "user-1")
	rr := httptest.NewRecorder()

	h.CreateSync(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestCreateSync_MissingFields(t *testing.T) {
	h := NewSyncHandlers(&mockChannelSyncRepo{})

	body := `{"externalChannelUrl":"","videoChannelId":""}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs", strings.NewReader(body))
	req = withSyncUserContext(req, "user-1")
	rr := httptest.NewRecorder()

	h.CreateSync(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

// ---------------------------------------------------------------------------
// DeleteSync tests
// ---------------------------------------------------------------------------

func TestDeleteSync_OK(t *testing.T) {
	repo := &mockChannelSyncRepo{}
	h := NewSyncHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/video-channel-syncs/1", nil)
	req = withSyncUserContext(req, "user-1")
	req = withSyncChiParam(req, "id", "1")
	rr := httptest.NewRecorder()

	h.DeleteSync(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestDeleteSync_InvalidID(t *testing.T) {
	h := NewSyncHandlers(&mockChannelSyncRepo{})

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/video-channel-syncs/abc", nil)
	req = withSyncUserContext(req, "user-1")
	req = withSyncChiParam(req, "id", "abc")
	rr := httptest.NewRecorder()

	h.DeleteSync(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestDeleteSync_NotFound(t *testing.T) {
	repo := &mockChannelSyncRepo{err: domain.ErrNotFound}
	h := NewSyncHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/video-channel-syncs/999", nil)
	req = withSyncUserContext(req, "user-1")
	req = withSyncChiParam(req, "id", "999")
	rr := httptest.NewRecorder()

	h.DeleteSync(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
}

// ---------------------------------------------------------------------------
// TriggerSync tests
// ---------------------------------------------------------------------------

func TestTriggerSync_OK(t *testing.T) {
	repo := &mockChannelSyncRepo{}
	h := NewSyncHandlers(repo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs/1/trigger-now", nil)
	req = withSyncUserContext(req, "user-1")
	req = withSyncChiParam(req, "id", "1")
	rr := httptest.NewRecorder()

	h.TriggerSync(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)
}

func TestTriggerSync_Unauthorized(t *testing.T) {
	h := NewSyncHandlers(&mockChannelSyncRepo{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/video-channel-syncs/1/trigger-now", nil)
	req = withSyncChiParam(req, "id", "1")
	rr := httptest.NewRecorder()

	h.TriggerSync(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}
