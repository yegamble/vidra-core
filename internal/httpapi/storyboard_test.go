package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/video"
)

// storyboarderStub satisfies video.Storyboarder with fixed bytes.
type storyboarderStub struct{}

func (storyboarderStub) Storyboard(context.Context, string, int) ([]byte, []byte, error) {
	return []byte("\xff\xd8\xff\xe0sprite"), []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nstoryboard.jpg#xywh=0,0,160,90\n"), nil
}

func TestVideoStoryboardServing(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithStoryboarder(storyboarderStub{}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	// createPublishedVideo uploads a file, which runs Process → generates the
	// storyboard via the stub.
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	// Detail reports has_storyboard.
	var detail videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &detail)
	if detail.HasStoryboard == nil || !*detail.HasStoryboard {
		t.Fatalf("detail has_storyboard = %v, want true", detail.HasStoryboard)
	}

	// The sprite sheet is served as image/jpeg.
	sb := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.jpg", "", "")
	if sb.Code != http.StatusOK {
		t.Fatalf("storyboard.jpg = %d, want 200", sb.Code)
	}
	if ct := sb.Header().Get("Content-Type"); ct != "image/jpeg" {
		t.Errorf("storyboard.jpg content-type = %q, want image/jpeg", ct)
	}

	// The VTT map is served as text/vtt.
	vtt := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.vtt", "", "")
	if vtt.Code != http.StatusOK {
		t.Fatalf("storyboard.vtt = %d, want 200", vtt.Code)
	}
	if ct := vtt.Header().Get("Content-Type"); ct != "text/vtt" {
		t.Errorf("storyboard.vtt content-type = %q, want text/vtt", ct)
	}
	if body := vtt.Body.String(); !strings.HasPrefix(body, "WEBVTT") {
		t.Errorf("storyboard.vtt body should start with WEBVTT, got %q", body)
	}
}

func TestVideoStoryboardAbsent404(t *testing.T) {
	// A video without a storyboarder wired publishes without a storyboard.
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)

	var detail videoView
	_ = json.Unmarshal(getVideo(srv, id, "").Body.Bytes(), &detail)
	if detail.HasStoryboard == nil || *detail.HasStoryboard {
		t.Errorf("detail has_storyboard = %v, want false", detail.HasStoryboard)
	}
	if sb := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.jpg", "", ""); sb.Code != http.StatusNotFound {
		t.Errorf("storyboard.jpg without one = %d, want 404", sb.Code)
	}
}

func TestVideoStoryboardPrivateVisibility(t *testing.T) {
	srv := videoServerCfg(t, testConfig(), video.WithStoryboarder(storyboarderStub{}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	// Anonymous cannot fetch a private video's storyboard.
	if sb := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.jpg", "", ""); sb.Code != http.StatusNotFound {
		t.Errorf("anon private storyboard = %d, want 404", sb.Code)
	}
	// The owner can.
	if sb := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/storyboard.jpg", "", tok); sb.Code != http.StatusOK {
		t.Errorf("owner private storyboard = %d, want 200", sb.Code)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
