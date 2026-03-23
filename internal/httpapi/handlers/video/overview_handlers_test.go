package video

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

type mockOverviewVideoRepo struct {
	listFn func(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error)
}

func (m *mockOverviewVideoRepo) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return m.listFn(ctx, req)
}

func TestGetOverview_Success(t *testing.T) {
	catName := "Music"
	repo := &mockOverviewVideoRepo{
		listFn: func(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
			return []*domain.Video{
				{
					ID:       "vid-1",
					Title:    "Song A",
					Tags:     []string{"rock"},
					Category: &domain.VideoCategory{Name: catName},
				},
				{
					ID:    "vid-2",
					Title: "Talk B",
					Tags:  []string{"tech"},
				},
			}, 2, nil
		},
	}
	h := NewOverviewHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overviews/videos", nil)
	w := httptest.NewRecorder()

	h.GetOverview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data overviewResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.NotEmpty(t, resp.Data.Categories)
	assert.NotEmpty(t, resp.Data.Tags)
}

func TestGetOverview_Empty(t *testing.T) {
	repo := &mockOverviewVideoRepo{
		listFn: func(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
			return []*domain.Video{}, 0, nil
		},
	}
	h := NewOverviewHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overviews/videos", nil)
	w := httptest.NewRecorder()

	h.GetOverview(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data overviewResponse `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Empty(t, resp.Data.Categories)
}

func TestGetOverview_RepoError(t *testing.T) {
	repo := &mockOverviewVideoRepo{
		listFn: func(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
			return nil, 0, errors.New("db error")
		},
	}
	h := NewOverviewHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/overviews/videos", nil)
	w := httptest.NewRecorder()

	h.GetOverview(w, req)
	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
