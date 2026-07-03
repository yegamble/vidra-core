package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestVideoDetailCarriesChannelIdentity verifies the Wave A contract gap: the
// video DETAIL response includes channel_handle + channel_display_name so the
// frontend related-rail can link/label without a second request.
func TestVideoDetailCarriesChannelIdentity(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Detail","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("get video = %d; body=%s", rec.Code, rec.Body.String())
	}
	var v videoView
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if v.ChannelHandle == nil || *v.ChannelHandle != "ada" {
		t.Errorf("channel_handle = %v, want ada", v.ChannelHandle)
	}
	if v.ChannelDisplayName == nil || *v.ChannelDisplayName == "" {
		t.Errorf("channel_display_name = %v, want non-empty", v.ChannelDisplayName)
	}
}

// TestSearchVideosRejectsUnknownFilters verifies the search facet filters mirror
// the feed's validation: unknown category/language values are 422.
func TestSearchVideosRejectsUnknownFilters(t *testing.T) {
	srv := videoServer(t)
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q=x&category=bogus", "", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad category = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q=x&language=zz-nope", "", ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad language = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
	// A valid search with no filters still works.
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/search?q=nothing", "", ""); rec.Code != http.StatusOK {
		t.Fatalf("plain search = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
}
