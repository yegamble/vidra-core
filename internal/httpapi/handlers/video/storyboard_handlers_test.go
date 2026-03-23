package video

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

type mockStoryboardRepo struct {
	listFn func(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error)
}

func (m *mockStoryboardRepo) ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoStoryboard, error) {
	return m.listFn(ctx, videoID)
}

func TestListStoryboards_Success(t *testing.T) {
	repo := &mockStoryboardRepo{
		listFn: func(_ context.Context, _ string) ([]domain.VideoStoryboard, error) {
			return []domain.VideoStoryboard{
				{ID: 1, VideoID: "vid-1", Filename: "storyboard.jpg", TotalWidth: 1920, TotalHeight: 1080, SpriteWidth: 160, SpriteHeight: 90, SpriteDuration: 2.0},
			}, nil
		},
	}
	h := NewStoryboardHandlers(repo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/storyboards", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListStoryboards(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []domain.VideoStoryboard `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "storyboard.jpg", resp.Data[0].Filename)
}

func TestListStoryboards_Empty(t *testing.T) {
	repo := &mockStoryboardRepo{
		listFn: func(_ context.Context, _ string) ([]domain.VideoStoryboard, error) {
			return nil, nil
		},
	}
	h := NewStoryboardHandlers(repo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/storyboards", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListStoryboards(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []domain.VideoStoryboard `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 0)
}

func TestListStoryboards_MissingVideoID(t *testing.T) {
	repo := &mockStoryboardRepo{}
	h := NewStoryboardHandlers(repo)

	rctx := chi.NewRouteContext()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos//storyboards", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListStoryboards(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListStoryboards_RepoError(t *testing.T) {
	repo := &mockStoryboardRepo{
		listFn: func(_ context.Context, _ string) ([]domain.VideoStoryboard, error) {
			return nil, errors.New("database error")
		},
	}
	h := NewStoryboardHandlers(repo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/storyboards", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListStoryboards(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
