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
	"vidra-core/internal/middleware"
)

type mockFileMgmtVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockFileMgmtVideoRepo) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return &domain.Video{ID: id, UserID: "owner-1"}, nil
}

func TestGetFileMetadata_Success(t *testing.T) {
	h := NewFileManagementHandlers(&mockFileMgmtVideoRepo{})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	rctx.URLParams.Add("videoFileId", "file-42")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/metadata/file-42", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.GetFileMetadata(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "vid-1", resp.Data["videoId"])
	assert.Equal(t, "file-42", resp.Data["videoFileId"])
}

func TestGetFileMetadata_MissingVideoID(t *testing.T) {
	h := NewFileManagementHandlers(&mockFileMgmtVideoRepo{})

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("videoFileId", "file-42")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos//metadata/file-42", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.GetFileMetadata(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteAllHLS_Success(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/hls", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeleteAllHLS(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteAllHLS_NotOwner(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "real-owner"}}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/hls", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "other-user",
	))
	w := httptest.NewRecorder()

	h.DeleteAllHLS(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteHLSFile_Success(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	rctx.URLParams.Add("videoFileId", "file-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/hls/file-1", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeleteHLSFile(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteAllWebVideos_Success(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/web-videos", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeleteAllWebVideos(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteWebVideoFile_Success(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	rctx.URLParams.Add("videoFileId", "file-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/web-videos/file-1", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeleteWebVideoFile(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeleteAllHLS_Unauthenticated(t *testing.T) {
	videoRepo := &mockFileMgmtVideoRepo{}
	h := NewFileManagementHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/hls", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.DeleteAllHLS(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
