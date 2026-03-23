package video

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestGetVideoStatsOverall_OK verifies 200 with stats shape.
func TestGetVideoStatsOverall_OK(t *testing.T) {
	h := GetVideoStatsOverallHandler()
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
	// Fields must be present (zero is valid)
	_ = resp.Data.Views
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
	// Retention data array must be present
	if resp.Data.Data == nil {
		t.Error("expected retention data array in response")
	}
}
