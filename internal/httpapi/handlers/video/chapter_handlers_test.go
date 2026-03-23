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

// MockChapterRepo mocks the ChapterRepository interface.
type MockChapterRepo struct {
	getByVideoIDFn func(ctx context.Context, videoID string) ([]*domain.VideoChapter, error)
	replaceAllFn   func(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error
}

func (m *MockChapterRepo) GetByVideoID(ctx context.Context, videoID string) ([]*domain.VideoChapter, error) {
	return m.getByVideoIDFn(ctx, videoID)
}

func (m *MockChapterRepo) ReplaceAll(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error {
	return m.replaceAllFn(ctx, videoID, chapters)
}

// MockVideoRepoForChapters provides a minimal video repo for ownership checks.
type MockVideoRepoForChapters struct {
	video *domain.Video
	err   error
}

func (m *MockVideoRepoForChapters) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return &domain.Video{ID: id}, nil
}

func TestGetChapters_Success(t *testing.T) {
	chapterRepo := &MockChapterRepo{
		getByVideoIDFn: func(ctx context.Context, videoID string) ([]*domain.VideoChapter, error) {
			return []*domain.VideoChapter{
				{VideoID: "vid-1", Timecode: 0, Title: "Intro", Position: 1},
				{VideoID: "vid-1", Timecode: 60, Title: "Part 1", Position: 2},
			}, nil
		},
	}
	videoRepo := &MockVideoRepoForChapters{}

	handler := NewChapterHandlers(chapterRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/vid-1/chapters", nil)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	w := httptest.NewRecorder()

	handler.GetChapters(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var resp struct {
		Data []domain.VideoChapter `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	// shared.WriteJSON wraps the slice in {"success":true,"data":[...]}
	assert.Len(t, resp.Data, 2)
	assert.Equal(t, "Intro", resp.Data[0].Title)
}

func TestPutChapters_Success(t *testing.T) {
	var captured []*domain.VideoChapter
	chapterRepo := &MockChapterRepo{
		replaceAllFn: func(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error {
			captured = chapters
			return nil
		},
	}
	videoRepo := &MockVideoRepoForChapters{
		video: &domain.Video{ID: "vid-1", UserID: "owner-1"},
	}

	handler := NewChapterHandlers(chapterRepo, videoRepo)

	body := `{"chapters":[{"timecode":0,"title":"Intro"},{"timecode":120,"title":"Main"}]}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/chapters", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	handler.PutChapters(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Len(t, captured, 2)
	assert.Equal(t, "Intro", captured[0].Title)
}

func TestPutChapters_NotOwner(t *testing.T) {
	chapterRepo := &MockChapterRepo{}
	videoRepo := &MockVideoRepoForChapters{
		video: &domain.Video{ID: "vid-1", UserID: "real-owner"},
	}

	handler := NewChapterHandlers(chapterRepo, videoRepo)

	body := `{"chapters":[]}`
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/chapters", bytes.NewBufferString(body))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "different-user",
	))
	w := httptest.NewRecorder()

	handler.PutChapters(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestPutChapters_InvalidJSON(t *testing.T) {
	chapterRepo := &MockChapterRepo{}
	// Set video with matching owner so JSON decode is reached
	videoRepo := &MockVideoRepoForChapters{
		video: &domain.Video{ID: "vid-1", UserID: "owner-1"},
	}

	handler := NewChapterHandlers(chapterRepo, videoRepo)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "vid-1")
	req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/vid-1/chapters", bytes.NewBufferString("not json"))
	req = req.WithContext(context.WithValue(
		context.WithValue(req.Context(), chi.RouteCtxKey, rctx),
		middleware.UserIDKey, "owner-1",
	))
	w := httptest.NewRecorder()

	handler.PutChapters(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
