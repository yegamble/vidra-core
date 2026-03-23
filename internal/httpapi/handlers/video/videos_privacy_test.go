package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"
)

type mockVideoRepoPrivacy struct {
	v     *domain.Video
	list  []*domain.Video
	total int64
}

func (m *mockVideoRepoPrivacy) Create(context.Context, *domain.Video) error { return nil }
func (m *mockVideoRepoPrivacy) GetByID(context.Context, string) (*domain.Video, error) {
	return m.v, nil
}
func (m *mockVideoRepoPrivacy) GetByIDs(context.Context, []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepoPrivacy) GetByUserID(context.Context, string, int, int) ([]*domain.Video, int64, error) {
	return m.list, m.total, nil
}
func (m *mockVideoRepoPrivacy) Update(context.Context, *domain.Video) error  { return nil }
func (m *mockVideoRepoPrivacy) Delete(context.Context, string, string) error { return nil }
func (m *mockVideoRepoPrivacy) List(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepoPrivacy) Search(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepoPrivacy) UpdateProcessingInfo(context.Context, string, domain.ProcessingStatus, map[string]string, string, string) error {
	return nil
}
func (m *mockVideoRepoPrivacy) UpdateProcessingInfoWithCIDs(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _ string, _ string, _ map[string]string, _ string, _ string) error {
	return nil
}
func (m *mockVideoRepoPrivacy) Count(_ context.Context) (int64, error) {
	return 0, nil
}
func (m *mockVideoRepoPrivacy) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepoPrivacy) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepoPrivacy) CreateRemoteVideo(_ context.Context, _ *domain.Video) error {
	return nil
}
func (m *mockVideoRepoPrivacy) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepoPrivacy) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func TestGetVideo_PrivacyGate(t *testing.T) {
	ownerID := "owner-1"
	mv := &mockVideoRepoPrivacy{v: &domain.Video{ID: "v1", UserID: ownerID, Privacy: domain.PrivacyPrivate}}
	h := GetVideoHandler(mv, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1", nil)
	rr := httptest.NewRecorder()
	req = withChiURLParam(req, "id", "123e4567-e89b-12d3-a456-426614174000")
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-owner private video, got %d", rr.Code)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1", nil)
	req2 = withChiURLParam(req2, "id", "123e4567-e89b-12d3-a456-426614174000")
	req2 = req2.WithContext(context.WithValue(req2.Context(), middleware.UserIDKey, ownerID))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", rr2.Code)
	}
}

func TestGetUserVideos_PrivacyFilterForNonOwner(t *testing.T) {
	ownerID := "123e4567-e89b-12d3-a456-426614174001"
	vids := []*domain.Video{
		{ID: "pub", UserID: ownerID, Privacy: domain.PrivacyPublic, Title: "public"},
		{ID: "prv", UserID: ownerID, Privacy: domain.PrivacyPrivate, Title: "private"},
	}
	mv := &mockVideoRepoPrivacy{list: vids, total: int64(len(vids))}
	h := GetUserVideosHandler(mv)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+ownerID+"/videos?limit=10", nil)
	req = withChiURLParam(req, "id", ownerID)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var env Response
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var got []*domain.Video
	b, _ := json.Marshal(env.Data)
	_ = json.Unmarshal(b, &got)
	if len(got) != 1 || got[0].ID != "pub" {
		t.Fatalf("expected only public video, got %+v", got)
	}

	req2 := httptest.NewRequest(http.MethodGet, "/api/v1/users/"+ownerID+"/videos?limit=10", nil)
	req2 = withChiURLParam(req2, "id", ownerID)
	req2 = req2.WithContext(context.WithValue(req2.Context(), middleware.UserIDKey, ownerID))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr2.Code)
	}
	var env2 Response
	_ = json.Unmarshal(rr2.Body.Bytes(), &env2)
	var got2 []*domain.Video
	b2, _ := json.Marshal(env2.Data)
	_ = json.Unmarshal(b2, &got2)
	if len(got2) != 2 {
		t.Fatalf("expected 2 videos for owner, got %d", len(got2))
	}
}
