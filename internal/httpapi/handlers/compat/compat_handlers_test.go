package compat

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

// --- Minimal mock satisfying port.VideoRepository ---

type mockVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	if m.err != nil {
		return nil, m.err
	}
	if m.video != nil {
		return m.video, nil
	}
	return nil, domain.NewDomainError("NOT_FOUND", "not found")
}

func (m *mockVideoRepo) List(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	if m.video != nil {
		return []*domain.Video{m.video}, 1, nil
	}
	return []*domain.Video{}, 0, nil
}

// Unused interface methods — satisfy port.VideoRepository
func (m *mockVideoRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByUserID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Update(_ context.Context, _ *domain.Video) error   { return nil }
func (m *mockVideoRepo) Delete(_ context.Context, _, _ string) error       { return nil }
func (m *mockVideoRepo) Search(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) UpdateProcessingInfo(_ context.Context, _ port.VideoProcessingParams) error {
	return nil
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ port.VideoProcessingWithCIDsParams) error {
	return nil
}
func (m *mockVideoRepo) Count(_ context.Context) (int64, error) { return 0, nil }
func (m *mockVideoRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}
func (m *mockVideoRepo) AppendOutputPath(_ context.Context, _, _, _ string) error { return nil }

// Compile-time check
var _ port.VideoRepository = (*mockVideoRepo)(nil)

// --- Tests ---

func TestGetVideoDescription(t *testing.T) {
	videoID := uuid.New().String()
	repo := &mockVideoRepo{
		video: &domain.Video{
			ID:          videoID,
			Title:       "Test Video",
			Description: "This is a test video description",
		},
	}

	handler := NewVideoCompatHandlers(repo)

	t.Run("returns description for existing video", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/description", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoDescription(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		data, ok := response["data"].(map[string]interface{})
		if ok {
			assert.Equal(t, "This is a test video description", data["description"])
		}
	})

	t.Run("returns 400 for missing ID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos//description", nil)
		rctx := chi.NewRouteContext()
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.GetVideoDescription(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})

	t.Run("returns 404 for nonexistent video", func(t *testing.T) {
		notFoundRepo := &mockVideoRepo{err: domain.NewDomainError("NOT_FOUND", "not found")}
		h := NewVideoCompatHandlers(notFoundRepo)

		fakeID := uuid.New()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+fakeID.String()+"/description", nil)
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", fakeID.String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		h.GetVideoDescription(w, req)

		assert.Equal(t, http.StatusNotFound, w.Code)
	})
}

func TestTrackWatching(t *testing.T) {
	handler := NewVideoCompatHandlers(&mockVideoRepo{})

	t.Run("accepts watching request and returns 204", func(t *testing.T) {
		body, _ := json.Marshal(VideoWatchingRequest{CurrentTime: 42.5})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/123/watching", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("id", uuid.New().String())
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.TrackWatching(w, req)

		assert.Equal(t, http.StatusNoContent, w.Code)
	})

	t.Run("returns 400 for missing ID", func(t *testing.T) {
		body, _ := json.Marshal(VideoWatchingRequest{CurrentTime: 10})
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos//watching", bytes.NewReader(body))
		rctx := chi.NewRouteContext()
		req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler.TrackWatching(w, req)

		assert.Equal(t, http.StatusBadRequest, w.Code)
	})
}

func TestGetVideoOverview(t *testing.T) {
	video := &domain.Video{
		ID:          uuid.New().String(),
		Title:       "Trending Video",
		Description: "A trending video",
	}
	repo := &mockVideoRepo{video: video}
	handler := NewOverviewHandlers(repo)

	t.Run("returns overview with categories", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/overviews/videos", nil)
		w := httptest.NewRecorder()
		handler.GetVideoOverview(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		data, ok := response["data"].(map[string]interface{})
		if !ok {
			data = response
		}

		assert.Contains(t, data, "categories")
		assert.Contains(t, data, "channels")
		assert.Contains(t, data, "tags")
	})

	t.Run("returns empty overview on repo error", func(t *testing.T) {
		errRepo := &mockVideoRepo{err: domain.NewDomainError("DB_ERROR", "database error")}
		h := NewOverviewHandlers(errRepo)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/overviews/videos", nil)
		w := httptest.NewRecorder()
		h.GetVideoOverview(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
	})
}

func TestPeerTubeNotImplemented(t *testing.T) {
	handler := PeerTubeNotImplemented("Test Feature")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/test", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotImplemented, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	errObj, ok := response["error"].(map[string]interface{})
	if ok {
		assert.Contains(t, errObj["message"], "Test Feature")
	}
}
