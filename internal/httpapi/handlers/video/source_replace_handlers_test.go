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

	"athena/internal/domain"
	"athena/internal/middleware"
)

type mockSourceReplaceVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockSourceReplaceVideoRepo) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return &domain.Video{ID: id, UserID: "owner-1"}, nil
}

func TestInitiateReplace_Success(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewSourceReplaceHandlers(videoRepo)

	body := `{"filename":"new-video.mp4","fileSize":1024000}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/vid-1/source/replace-resumable", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.InitiateReplace(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data map[string]interface{} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Equal(t, "vid-1", resp.Data["videoId"])
}

func TestInitiateReplace_NotOwner(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "real-owner"}}
	h := NewSourceReplaceHandlers(videoRepo)

	body := `{"filename":"new-video.mp4","fileSize":1024000}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/vid-1/source/replace-resumable", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "other-user",
	))
	w := httptest.NewRecorder()

	h.InitiateReplace(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestInitiateReplace_MissingFilename(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewSourceReplaceHandlers(videoRepo)

	body := `{"fileSize":1024000}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/vid-1/source/replace-resumable", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.InitiateReplace(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUploadReplaceChunk_Success(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewSourceReplaceHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/source/replace-resumable", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.UploadReplaceChunk(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCancelReplace_Success(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewSourceReplaceHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/source/replace-resumable", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.CancelReplace(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestCancelReplace_Unauthenticated(t *testing.T) {
	videoRepo := &mockSourceReplaceVideoRepo{}
	h := NewSourceReplaceHandlers(videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/source/replace-resumable", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.CancelReplace(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
