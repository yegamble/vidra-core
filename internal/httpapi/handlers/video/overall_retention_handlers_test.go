package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/domain"
)

type mockVideoRepoForStats struct {
	video *domain.Video
	err   error
}

func (m *mockVideoRepoForStats) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.video, m.err
}

// TestGetVideoStatsOverall_OK verifies 200 with stats shape.
func TestGetVideoStatsOverall_OK(t *testing.T) {
	repo := &mockVideoRepoForStats{
		video: &domain.Video{
			Views: 42,
		},
	}
	h := GetVideoStatsOverallHandler(repo)
	req := httptest.NewRequest(http.MethodGet, "/videos/vid-1/stats/overall", nil)
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data struct {
			Views         int64 `json:"views"`
			Likes         int64 `json:"likes"`
			Dislikes      int64 `json:"dislikes"`
			UniqueViewers int64 `json:"uniqueViewers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Views != 42 {
		t.Errorf("expected views=42, got %d", resp.Data.Views)
	}
	// Likes/dislikes come from the rating subsystem, not Video model
	if resp.Data.Likes != 0 {
		t.Errorf("expected likes=0 (from rating subsystem), got %d", resp.Data.Likes)
	}
	if resp.Data.Dislikes != 0 {
		t.Errorf("expected dislikes=0 (from rating subsystem), got %d", resp.Data.Dislikes)
	}
}

// TestGetVideoStatsOverall_NilRepo verifies handler works with nil repo.
func TestGetVideoStatsOverall_NilRepo(t *testing.T) {
	h := GetVideoStatsOverallHandler(nil)
	req := httptest.NewRequest(http.MethodGet, "/videos/vid-1/stats/overall", nil)
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data struct {
			Views int64 `json:"views"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Views != 0 {
		t.Errorf("expected views=0 with nil repo, got %d", resp.Data.Views)
	}
}

// TestGetVideoStatsRetention_OK verifies 200 with retention array shape.
func TestGetVideoStatsRetention_OK(t *testing.T) {
	h := GetVideoStatsRetentionHandler()
	req := httptest.NewRequest(http.MethodGet, "/videos/vid-1/stats/retention", nil)
	req = withVideoIDParam(req, "vid-1")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		Data struct {
			Data []float64 `json:"data"`
		} `json:"data"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Data.Data == nil {
		t.Error("expected retention data array in response")
	}
}
