package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type xssCapturingRepo struct {
	mockVideoRepo
	created *domain.Video
	updated *domain.Video
}

func (r *xssCapturingRepo) Create(_ context.Context, v *domain.Video) error {
	r.created = v
	return nil
}

func (r *xssCapturingRepo) Update(_ context.Context, v *domain.Video) error {
	r.updated = v
	return nil
}

var _ usecase.VideoRepository = (*xssCapturingRepo)(nil)

func TestCreateVideoHandler_XSS_TitleStripped(t *testing.T) {
	repo := &xssCapturingRepo{}
	handler := CreateVideoHandler(repo)

	body, _ := json.Marshal(domain.VideoUploadRequest{
		Title:       "<script>alert('xss')</script>My Video",
		Description: "Normal description",
		Privacy:     domain.PrivacyPublic,
	})

	req := httptest.NewRequest(http.MethodPost, "/videos", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, repo.created)
	assert.NotContains(t, repo.created.Title, "<script>", "script tag should be stripped from title")
	assert.NotContains(t, repo.created.Title, "alert(", "JS should be stripped from title")
}

func TestCreateVideoHandler_XSS_DescriptionStripped(t *testing.T) {
	repo := &xssCapturingRepo{}
	handler := CreateVideoHandler(repo)

	body, _ := json.Marshal(domain.VideoUploadRequest{
		Title:       "My Video",
		Description: `<img src=x onerror="alert('xss')">Some description`,
		Privacy:     domain.PrivacyPublic,
	})

	req := httptest.NewRequest(http.MethodPost, "/videos", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code)
	require.NotNil(t, repo.created)
	assert.NotContains(t, repo.created.Description, "onerror=", "onerror attribute should be stripped")
}

func TestCreateVideoHandler_XSS_PureScriptTitle_Rejected(t *testing.T) {
	repo := &xssCapturingRepo{}
	handler := CreateVideoHandler(repo)

	body, _ := json.Marshal(domain.VideoUploadRequest{
		Title:       "<script>alert(1)</script>",
		Description: "Normal description",
		Privacy:     domain.PrivacyPublic,
	})

	req := httptest.NewRequest(http.MethodPost, "/videos", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-123")
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "title that sanitizes to empty should be rejected")
	assert.Nil(t, repo.created, "video should not be created")
}

func TestUpdateVideoHandler_XSS_TitleStripped(t *testing.T) {
	videoID := "550e8400-e29b-41d4-a716-446655440000"
	existing := &domain.Video{
		ID:     videoID,
		UserID: "user-123",
		Status: domain.StatusCompleted,
	}
	repo := &xssCapturingRepo{}
	repo.getByID = existing
	handler := UpdateVideoHandler(repo)

	body, _ := json.Marshal(map[string]interface{}{
		"title":       "<script>alert('xss')</script>Safe Title",
		"description": `<img src=x onerror="steal()">Normal desc`,
		"privacy":     "public",
	})

	req := httptest.NewRequest(http.MethodPut, "/videos/"+videoID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-123")
	req = req.WithContext(ctx)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, repo.updated)
	assert.NotContains(t, repo.updated.Title, "<script>", "script tag should be stripped from updated title")
	assert.NotContains(t, repo.updated.Description, "onerror=", "onerror should be stripped from updated description")
}

func TestUpdateVideoHandler_XSS_PureScriptTitle_Rejected(t *testing.T) {
	videoID := "550e8400-e29b-41d4-a716-446655440000"
	existing := &domain.Video{
		ID:     videoID,
		UserID: "user-123",
		Status: domain.StatusCompleted,
	}
	repo := &xssCapturingRepo{}
	repo.getByID = existing
	handler := UpdateVideoHandler(repo)

	body, _ := json.Marshal(map[string]interface{}{
		"title":   "<script>alert(1)</script>",
		"privacy": "public",
	})

	req := httptest.NewRequest(http.MethodPut, "/videos/"+videoID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, "user-123")
	req = req.WithContext(ctx)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code, "title that sanitizes to empty should be rejected")
	assert.Nil(t, repo.updated, "video should not be updated")
}
