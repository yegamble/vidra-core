package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/usecase"
)

// mockAdminVideoRepo satisfies usecase.VideoRepository for admin handler tests.
type mockAdminVideoRepo struct {
	videos []*domain.Video
	err    error
}

func (m *mockAdminVideoRepo) List(_ context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	if m.err != nil {
		return nil, 0, m.err
	}
	offset := req.Offset
	limit := req.Limit
	if limit <= 0 {
		limit = 20
	}
	total := int64(len(m.videos))
	if offset >= len(m.videos) {
		return []*domain.Video{}, total, nil
	}
	end := offset + limit
	if end > len(m.videos) {
		end = len(m.videos)
	}
	return m.videos[offset:end], total, nil
}

func (m *mockAdminVideoRepo) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return m.List(ctx, req)
}

// Stub all other VideoRepository methods.
func (m *mockAdminVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return nil, domain.ErrVideoNotFound
}
func (m *mockAdminVideoRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockAdminVideoRepo) GetByUserID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockAdminVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockAdminVideoRepo) Create(_ context.Context, _ *domain.Video) error    { return m.err }
func (m *mockAdminVideoRepo) Update(_ context.Context, _ *domain.Video) error    { return m.err }
func (m *mockAdminVideoRepo) Delete(_ context.Context, _ string, _ string) error { return m.err }
func (m *mockAdminVideoRepo) UpdateProcessingInfo(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _, _ string) error {
	return nil
}
func (m *mockAdminVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _, _ string, _ map[string]string, _, _ string) error {
	return nil
}
func (m *mockAdminVideoRepo) Count(_ context.Context) (int64, error) {
	return int64(len(m.videos)), m.err
}
func (m *mockAdminVideoRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockAdminVideoRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockAdminVideoRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error { return nil }

var _ usecase.VideoRepository = (*mockAdminVideoRepo)(nil)

func newTestVideos() []*domain.Video {
	now := time.Now()
	return []*domain.Video{
		{ID: "v1", Title: "Alpha Video", UserID: "u1", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted, UploadDate: now},
		{ID: "v2", Title: "Beta Video", UserID: "u2", Privacy: domain.PrivacyPrivate, Status: domain.StatusProcessing, UploadDate: now},
		{ID: "v3", Title: "Gamma Video", UserID: "u3", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted, UploadDate: now},
	}
}

func decodeVideoAdminResponse(t *testing.T, rr *httptest.ResponseRecorder) struct {
	Data    json.RawMessage   `json:"data"`
	Error   *shared.ErrorInfo `json:"error"`
	Success bool              `json:"success"`
	Meta    *shared.Meta      `json:"meta"`
} {
	t.Helper()
	var resp struct {
		Data    json.RawMessage   `json:"data"`
		Error   *shared.ErrorInfo `json:"error"`
		Success bool              `json:"success"`
		Meta    *shared.Meta      `json:"meta"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	return resp
}

func TestAdminListVideos_Success(t *testing.T) {
	repo := &mockAdminVideoRepo{videos: newTestVideos()}
	h := NewAdminVideoHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/videos", nil)
	rr := httptest.NewRecorder()
	h.ListVideos(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	resp := decodeVideoAdminResponse(t, rr)
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	if resp.Meta == nil {
		t.Fatal("expected meta")
	}
	if resp.Meta.Total != 3 {
		t.Errorf("expected total=3, got %d", resp.Meta.Total)
	}
}

func TestAdminListVideos_Pagination(t *testing.T) {
	repo := &mockAdminVideoRepo{videos: newTestVideos()}
	h := NewAdminVideoHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/videos?limit=2&offset=0", nil)
	rr := httptest.NewRecorder()
	h.ListVideos(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeVideoAdminResponse(t, rr)
	if resp.Meta.Limit != 2 {
		t.Errorf("expected limit=2, got %d", resp.Meta.Limit)
	}
}

func TestAdminListVideos_Search(t *testing.T) {
	repo := &mockAdminVideoRepo{videos: newTestVideos()}
	h := NewAdminVideoHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/videos?search=alpha", nil)
	rr := httptest.NewRecorder()
	h.ListVideos(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestAdminListVideos_EmptyResult(t *testing.T) {
	repo := &mockAdminVideoRepo{videos: []*domain.Video{}}
	h := NewAdminVideoHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/admin/videos", nil)
	rr := httptest.NewRecorder()
	h.ListVideos(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	resp := decodeVideoAdminResponse(t, rr)
	if resp.Meta.Total != 0 {
		t.Errorf("expected total=0, got %d", resp.Meta.Total)
	}
}
