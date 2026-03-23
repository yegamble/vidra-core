package video

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetVideoLicences(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/licences", nil)
	rr := httptest.NewRecorder()

	GetVideoLicences(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var envelope struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(envelope.Data) == 0 {
		t.Fatal("expected non-empty licences map")
	}
	// PeerTube licence IDs are integer strings: "1" = CC BY, "7" = Public Domain
	if _, ok := envelope.Data["1"]; !ok {
		t.Fatalf("expected licence id '1' (CC BY), got: %v", envelope.Data)
	}
	if _, ok := envelope.Data["7"]; !ok {
		t.Fatalf("expected licence id '7' (Public Domain), got: %v", envelope.Data)
	}
}

func TestGetVideoLanguages(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/languages", nil)
	rr := httptest.NewRecorder()

	GetVideoLanguages(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var envelope struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(envelope.Data) == 0 {
		t.Fatal("expected non-empty languages map")
	}
	// PeerTube includes ISO 639-1 codes as keys
	if _, ok := envelope.Data["en"]; !ok {
		t.Fatalf("expected language 'en' (English), got keys: %v", envelope.Data)
	}
	if _, ok := envelope.Data["fr"]; !ok {
		t.Fatalf("expected language 'fr' (French), got keys: %v", envelope.Data)
	}
}

func TestGetVideoPrivacies(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/privacies", nil)
	rr := httptest.NewRecorder()

	GetVideoPrivacies(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var envelope struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("invalid json: %v", err)
	}
	if len(envelope.Data) == 0 {
		t.Fatal("expected non-empty privacies map")
	}
	// PeerTube privacy IDs: "1"=Public, "2"=Unlisted, "3"=Private, "4"=Internal
	if _, ok := envelope.Data["1"]; !ok {
		t.Fatalf("expected privacy id '1' (Public), got: %v", envelope.Data)
	}
	if _, ok := envelope.Data["3"]; !ok {
		t.Fatalf("expected privacy id '3' (Private), got: %v", envelope.Data)
	}
}
