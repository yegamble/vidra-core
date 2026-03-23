package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

func TestGetVideoDescriptionHandler_Success(t *testing.T) {
	repo := &MockVideoRepoForChapters{
		video: &domain.Video{ID: "vid-1", Description: "A great video about Go programming."},
	}

	handler := GetVideoDescriptionHandler(repo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/description", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data struct {
			Description string `json:"description"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "A great video about Go programming.", resp.Data.Description)
}

func TestGetVideoDescriptionHandler_NotFound(t *testing.T) {
	repo := &MockVideoRepoForChapters{err: domain.ErrVideoNotFound}

	handler := GetVideoDescriptionHandler(repo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "missing-id")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/missing-id/description", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}
