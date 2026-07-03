package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/video"
)

func getDownloads(srv *Server, id, token string) *httptest.ResponseRecorder {
	return sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download", "", token)
}

func decodeDownloads(t *testing.T, rec *httptest.ResponseRecorder) videoDownloadResponse {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("download = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var body videoDownloadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// TestVideoDownloadsShape proves the entry shapes: the original carries its
// size/content type/name/probed dimensions, and a ready HLS playlist adds one
// entry per rendition (playlist URL + rung dimensions, no size).
func TestVideoDownloadsShape(t *testing.T) {
	srv, blobs, tcRepo, _ := videoServerEnv(t, testConfig(),
		video.WithProber(fakeProber{md: media.Metadata{DurationSeconds: 2, Width: 320, Height: 240}}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "tiny", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// Before transcoding: just the original, publicly listable.
	body := decodeDownloads(t, getDownloads(srv, id, ""))
	if len(body.Files) != 1 {
		t.Fatalf("files before HLS = %d, want 1; body=%+v", len(body.Files), body)
	}
	orig := body.Files[0]
	if orig.Kind != "original" || orig.URL != "/api/v1/videos/"+id+"/original" ||
		orig.ContentType != "video/mp4" || orig.SizeBytes != int64(len("tiny")) ||
		orig.OriginalName != "clip.mp4" || orig.Height != 240 || orig.Width != 320 {
		t.Errorf("original entry = %+v, want original/…/original video/mp4 4B clip.mp4 320x240", orig)
	}

	// A ready playlist adds one entry per rendition.
	seedReadyHLS(t, tcRepo, blobs, id)
	body = decodeDownloads(t, getDownloads(srv, id, ""))
	if len(body.Files) != 2 {
		t.Fatalf("files with HLS = %d, want 2; body=%+v", len(body.Files), body)
	}
	hls := body.Files[1]
	if hls.Kind != "hls" || hls.URL != "/api/v1/videos/"+id+"/hls/240p/playlist.m3u8" ||
		hls.ContentType != contentTypeM3U8 || hls.Height != 240 || hls.Width != 320 {
		t.Errorf("hls entry = %+v, want hls/240p playlist %s 320x240", hls, contentTypeM3U8)
	}
	if hls.SizeBytes != 0 {
		t.Errorf("hls size_bytes = %d, want omitted (0)", hls.SizeBytes)
	}

	// A visible video with no stored files yet returns an empty list.
	draft := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`)
	if body := decodeDownloads(t, getDownloads(srv, draft, "")); len(body.Files) != 0 {
		t.Errorf("draft files = %d, want 0", len(body.Files))
	}
}

// TestVideoDownloadsVisibility proves the guard mirrors the detail endpoint:
// private → owner only, blocked → moderators only, unknown → 404.
func TestVideoDownloadsVisibility(t *testing.T) {
	srv := videoServer(t)
	// The first registered account ("ada") becomes admin.
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	private := createPublishedVideo(t, srv, admin, "ada", `{"title":"Secret","privacy":"private"}`)
	bob := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	// Private: anon and non-owner get 404, the owner sees the original entry.
	if rec := getDownloads(srv, private, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anon private = %d, want 404", rec.Code)
	}
	if rec := getDownloads(srv, private, bob); rec.Code != http.StatusNotFound {
		t.Errorf("non-owner private = %d, want 404", rec.Code)
	}
	if body := decodeDownloads(t, getDownloads(srv, private, admin)); len(body.Files) != 1 || body.Files[0].Kind != "original" {
		t.Errorf("owner private files = %+v, want one original", body.Files)
	}

	// Blocked: hidden from regular users, still listable by moderators/admins.
	public := createPublishedVideo(t, srv, admin, "ada", `{"title":"Pub","privacy":"public"}`)
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/admin/videos/"+public+"/block", `{"reason":"tos"}`, admin); rec.Code != http.StatusNoContent {
		t.Fatalf("block = %d", rec.Code)
	}
	if rec := getDownloads(srv, public, bob); rec.Code != http.StatusNotFound {
		t.Errorf("regular user blocked = %d, want 404", rec.Code)
	}
	if body := decodeDownloads(t, getDownloads(srv, public, admin)); len(body.Files) != 1 {
		t.Errorf("admin blocked files = %d, want 1", len(body.Files))
	}

	// Unknown / malformed ids → 404.
	if rec := getDownloads(srv, uuid.New().String(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("unknown id = %d, want 404", rec.Code)
	}
	if rec := getDownloads(srv, "not-a-uuid", ""); rec.Code != http.StatusNotFound {
		t.Errorf("malformed id = %d, want 404", rec.Code)
	}
}
