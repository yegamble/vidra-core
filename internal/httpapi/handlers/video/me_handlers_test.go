package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
)

// minimalVideoRepo is a VideoRepository stub for me_handlers tests.
// Only GetByUserID is meaningful; all other methods panic.
type minimalVideoRepo struct {
	videos []*domain.Video
	total  int64
	err    error
}

func (m *minimalVideoRepo) GetByUserID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return m.videos, m.total, m.err
}

// Stub implementations (not called by GetMyVideosHandler)
func (m *minimalVideoRepo) Create(_ context.Context, _ *domain.Video) error { panic("not implemented") }
func (m *minimalVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) Update(_ context.Context, _ *domain.Video) error { panic("not implemented") }
func (m *minimalVideoRepo) Delete(_ context.Context, _, _ string) error     { panic("not implemented") }
func (m *minimalVideoRepo) List(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) Search(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) UpdateProcessingInfo(_ context.Context, _ port.VideoProcessingParams) error {
	panic("not implemented")
}
func (m *minimalVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ port.VideoProcessingWithCIDsParams) error {
	panic("not implemented")
}
func (m *minimalVideoRepo) Count(_ context.Context) (int64, error) { panic("not implemented") }
func (m *minimalVideoRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	panic("not implemented")
}
func (m *minimalVideoRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error {
	panic("not implemented")
}
func (m *minimalVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	panic("not implemented")
}

func withUserID(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), middleware.UserIDKey, id))
}

// TestGetMyVideosHandler_ReturnsUserVideos verifies GET /me/videos returns videos for auth user.
func TestGetMyVideosHandler_ReturnsUserVideos(t *testing.T) {
	repo := &minimalVideoRepo{
		videos: []*domain.Video{{ID: "vid-1", Title: "My Video"}},
		total:  1,
	}

	req := httptest.NewRequest(http.MethodGet, "/users/me/videos", nil)
	req = withUserID(req, "user-abc")
	rr := httptest.NewRecorder()

	GetMyVideosHandler(repo)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	// shared.WriteJSON wraps in {success, data, ...}
	data, ok := resp["data"]
	if !ok {
		t.Fatal("expected 'data' field in response")
	}
	d := data.(map[string]interface{})
	if d["total"].(float64) != 1 {
		t.Errorf("expected total=1, got %v", d["total"])
	}
}

// TestGetMyVideosHandler_MissingAuth verifies 401 when user is not authenticated.
func TestGetMyVideosHandler_MissingAuth(t *testing.T) {
	repo := &minimalVideoRepo{}

	req := httptest.NewRequest(http.MethodGet, "/users/me/videos", nil)
	rr := httptest.NewRecorder()

	GetMyVideosHandler(repo)(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
}

// TestGetMyCommentsHandler_ReturnsEmptyList verifies GET /me/comments returns an empty list.
func TestGetMyCommentsHandler_ReturnsEmptyList(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/users/me/comments", nil)
	req = withUserID(req, "user-abc")
	rr := httptest.NewRecorder()

	GetMyCommentsHandler()(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode: %v", err)
	}
	data := resp["data"].(map[string]interface{})
	if data["total"].(float64) != 0 {
		t.Errorf("expected total=0, got %v", data["total"])
	}
}
