package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetSupportedQualities(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/qualities", nil)
	rr := httptest.NewRecorder()

	GetSupportedQualities(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var envelope struct {
		Data struct {
			Qualities []string `json:"qualities"`
			Default   string   `json:"default"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(envelope.Data.Qualities) == 0 {
		t.Fatalf("expected non-empty qualities list")
	}
	// Sanity checks: includes common values and default is present
	found720 := false
	found4320 := false
	for _, q := range envelope.Data.Qualities {
		if q == "720p" {
			found720 = true
		}
		if q == "4320p" {
			found4320 = true
		}
	}
	if !found720 {
		t.Fatalf("expected 720p in qualities: %+v", envelope.Data.Qualities)
	}
	if !found4320 {
		t.Fatalf("expected 4320p in qualities: %+v", envelope.Data.Qualities)
	}
	if envelope.Data.Default == "" {
		t.Fatalf("expected default quality to be set")
	}
}
