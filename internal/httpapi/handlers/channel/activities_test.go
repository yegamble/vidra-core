package channel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

type mockActivityRepo struct {
	activities []domain.ChannelActivity
	total      int64
	err        error
}

func (m *mockActivityRepo) ListByChannel(_ context.Context, _ uuid.UUID, _, _ int) ([]domain.ChannelActivity, int64, error) {
	return m.activities, m.total, m.err
}

type mockChannelForActivity struct {
	channel *domain.Channel
	err     error
}

func (m *mockChannelForActivity) GetChannelByHandle(_ context.Context, _ string) (*domain.Channel, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.channel, nil
}

func TestListActivities_Success(t *testing.T) {
	channelID := uuid.New()
	repo := &mockActivityRepo{
		activities: []domain.ChannelActivity{
			{
				ID:         uuid.New(),
				ChannelID:  channelID,
				UserID:     uuid.New(),
				ActionType: domain.ActivityVideoPublish,
				TargetType: "video",
				TargetID:   "vid-1",
				CreatedAt:  time.Now(),
			},
		},
		total: 1,
	}
	chSvc := &mockChannelForActivity{channel: &domain.Channel{ID: channelID, Handle: "test-channel"}}
	h := NewActivityHandlers(repo, chSvc)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelHandle", "test-channel")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/video-channels/test-channel/activities", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
	w := httptest.NewRecorder()

	h.ListActivities(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []domain.ChannelActivity `json:"data"`
		Meta map[string]interface{}   `json:"meta"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, domain.ActivityVideoPublish, resp.Data[0].ActionType)
}

func TestListActivities_Unauthenticated(t *testing.T) {
	h := NewActivityHandlers(&mockActivityRepo{}, &mockChannelForActivity{})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelHandle", "test-channel")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/video-channels/test-channel/activities", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListActivities(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestListActivities_ChannelNotFound(t *testing.T) {
	chSvc := &mockChannelForActivity{err: domain.ErrNotFound}
	h := NewActivityHandlers(&mockActivityRepo{}, chSvc)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelHandle", "nonexistent")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/video-channels/nonexistent/activities", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
	w := httptest.NewRecorder()

	h.ListActivities(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestListActivities_EmptyResult(t *testing.T) {
	channelID := uuid.New()
	repo := &mockActivityRepo{activities: []domain.ChannelActivity{}, total: 0}
	chSvc := &mockChannelForActivity{channel: &domain.Channel{ID: channelID}}
	h := NewActivityHandlers(repo, chSvc)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("channelHandle", "empty-channel")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/video-channels/empty-channel/activities", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
	w := httptest.NewRecorder()

	h.ListActivities(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []domain.ChannelActivity `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 0)
}
