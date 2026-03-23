package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// --- mocks ---

type mockPasswordRepo struct {
	listFn       func(ctx context.Context, videoID string) ([]domain.VideoPassword, error)
	createFn     func(ctx context.Context, videoID string, hash string) (*domain.VideoPassword, error)
	replaceAllFn func(ctx context.Context, videoID string, hashes []string) ([]domain.VideoPassword, error)
	deleteFn     func(ctx context.Context, id int64) error
}

func (m *mockPasswordRepo) ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoPassword, error) {
	return m.listFn(ctx, videoID)
}

func (m *mockPasswordRepo) Create(ctx context.Context, videoID string, hash string) (*domain.VideoPassword, error) {
	return m.createFn(ctx, videoID, hash)
}

func (m *mockPasswordRepo) ReplaceAll(ctx context.Context, videoID string, hashes []string) ([]domain.VideoPassword, error) {
	return m.replaceAllFn(ctx, videoID, hashes)
}

func (m *mockPasswordRepo) Delete(ctx context.Context, id int64) error {
	return m.deleteFn(ctx, id)
}

type mockPasswordVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockPasswordVideoRepo) GetByID(_ context.Context, id string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return &domain.Video{ID: id, UserID: "owner-1"}, nil
}

// --- tests ---

func TestListPasswords_Success(t *testing.T) {
	now := time.Now()
	pwRepo := &mockPasswordRepo{
		listFn: func(_ context.Context, _ string) ([]domain.VideoPassword, error) {
			return []domain.VideoPassword{
				{ID: 1, VideoID: "vid-1", CreatedAt: now},
				{ID: 2, VideoID: "vid-1", CreatedAt: now},
			}, nil
		},
	}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/passwords", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.ListPasswords(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp struct {
		Data []domain.VideoPassword `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Data, 2)
}

func TestListPasswords_Forbidden(t *testing.T) {
	pwRepo := &mockPasswordRepo{}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "real-owner"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/passwords", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "other-user",
	))
	w := httptest.NewRecorder()

	h.ListPasswords(w, req)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestAddPassword_Success(t *testing.T) {
	now := time.Now()
	pwRepo := &mockPasswordRepo{
		createFn: func(_ context.Context, videoID string, hash string) (*domain.VideoPassword, error) {
			return &domain.VideoPassword{ID: 1, VideoID: videoID, CreatedAt: now}, nil
		},
	}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	body := `{"password":"secret123"}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/vid-1/passwords", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.AddPassword(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestAddPassword_EmptyPassword(t *testing.T) {
	pwRepo := &mockPasswordRepo{}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	body := `{"password":""}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/vid-1/passwords", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.AddPassword(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestReplacePasswords_Success(t *testing.T) {
	now := time.Now()
	pwRepo := &mockPasswordRepo{
		replaceAllFn: func(_ context.Context, videoID string, hashes []string) ([]domain.VideoPassword, error) {
			result := make([]domain.VideoPassword, len(hashes))
			for i := range hashes {
				result[i] = domain.VideoPassword{ID: int64(i + 1), VideoID: videoID, CreatedAt: now}
			}
			return result, nil
		},
	}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	body := `{"passwords":["pw1","pw2"]}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/passwords", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.ReplacePasswords(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestDeletePassword_Success(t *testing.T) {
	pwRepo := &mockPasswordRepo{
		deleteFn: func(_ context.Context, id int64) error {
			return nil
		},
	}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	rctx.URLParams.Add("passwordId", "1")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/passwords/1", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeletePassword(w, req)
	assert.Equal(t, http.StatusNoContent, w.Code)
}

func TestDeletePassword_InvalidID(t *testing.T) {
	pwRepo := &mockPasswordRepo{}
	videoRepo := &mockPasswordVideoRepo{video: &domain.Video{ID: "vid-1", UserID: "owner-1"}}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	rctx.URLParams.Add("passwordId", "not-a-number")
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/vid-1/passwords/not-a-number", nil)
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	h.DeletePassword(w, req)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestListPasswords_Unauthenticated(t *testing.T) {
	pwRepo := &mockPasswordRepo{}
	videoRepo := &mockPasswordVideoRepo{}
	h := NewPasswordHandlers(pwRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/passwords", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	h.ListPasswords(w, req)
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}
