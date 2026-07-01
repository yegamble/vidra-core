package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/video"
)

func TestVideoConfigEndpoint(t *testing.T) {
	// The route only needs the video + channel services registered; the handler
	// itself returns static data, so nil repositories are fine.
	srv := New(testConfig(), nil, nil,
		WithVideoService(video.NewService(nil, nil)),
		WithChannelService(channel.NewService(nil)),
	)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/videos/config", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var body struct {
		Categories []video.ConfigOption `json:"categories"`
		Licenses   []video.ConfigOption `json:"licenses"`
		Languages  []video.ConfigOption `json:"languages"`
		Privacies  []video.ConfigOption `json:"privacies"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Categories) == 0 || len(body.Licenses) == 0 ||
		len(body.Languages) == 0 || len(body.Privacies) == 0 {
		t.Fatalf("expected all four lists populated: %+v", body)
	}
	if body.Categories[0].ID != "1" || body.Categories[0].Label != "Music" {
		t.Errorf("first category = %+v; want {1 Music}", body.Categories[0])
	}
	if !hasID(body.Languages, "en") {
		t.Error("languages should include 'en'")
	}
	if !hasID(body.Privacies, "public") {
		t.Error("privacies should include 'public'")
	}
}

func hasID(opts []video.ConfigOption, id string) bool {
	for _, o := range opts {
		if o.ID == id {
			return true
		}
	}
	return false
}
