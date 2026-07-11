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

// TestVideoDownloadsShape proves that metadata points only at downloadable
// single files: original, muxed/video-only MP4 at each HLS rung, audio-only,
// and WebVTT subtitles.
func TestVideoDownloadsShape(t *testing.T) {
	srv, blobs, tcRepo, _, repo := videoServerFull(t, testConfig(),
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
	if orig.Kind != "original" || orig.URL != "/api/v1/videos/"+id+"/download/original" ||
		orig.ContentType != "video/mp4" || orig.SizeBytes != int64(len("tiny")) ||
		orig.OriginalName != "clip.mp4" || orig.Filename != "clip.mp4" || orig.Height != 240 || orig.Width != 320 {
		t.Errorf("original entry = %+v, want original download URL/video/mp4/4B/clip.mp4/320x240", orig)
	}

	// A ready playlist adds one progressive MP4 entry per rendition plus audio.
	seedReadyHLS(t, tcRepo, blobs, id)
	if rec := uploadCaption(srv, id, "en", "English", sampleVTT, tok, true); rec.Code != http.StatusCreated {
		t.Fatalf("caption upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	body = decodeDownloads(t, getDownloads(srv, id, ""))
	if len(body.Files) != 4 {
		t.Fatalf("files with HLS/audio/subtitle = %d, want 4; body=%+v", len(body.Files), body)
	}
	hls := body.Files[1]
	if hls.Kind != "hls" || hls.URL != "/api/v1/videos/"+id+"/download/hls/240" ||
		hls.VideoOnlyURL != "/api/v1/videos/"+id+"/download/hls/240?audio=false" ||
		hls.ContentType != media.HLSMP4ContentType || hls.Height != 240 || hls.Width != 320 {
		t.Errorf("hls entry = %+v, want muxed + video-only progressive MP4 320x240", hls)
	}
	if hls.SizeBytes != int64(len("fake-muxed-mp4")) {
		t.Errorf("hls size_bytes = %d, want %d", hls.SizeBytes, len("fake-muxed-mp4"))
	}
	if audio := body.Files[2]; audio.Kind != "audio" || audio.URL != "/api/v1/videos/"+id+"/download/audio" || audio.ContentType != media.HLSM4AContentType {
		t.Errorf("audio entry = %+v, want audio-only M4A", audio)
	}
	if subtitle := body.Files[3]; subtitle.Kind != "subtitle" || subtitle.Language != "en" || subtitle.Label != "English" || subtitle.URL != "/api/v1/videos/"+id+"/download/subtitles/en" {
		t.Errorf("subtitle entry = %+v, want English WebVTT", subtitle)
	}

	// Each advertised URL serves attachment bytes; HLS video-only is selected by
	// query without changing the quality.
	for _, tc := range []struct {
		path, body string
	}{
		{"/api/v1/videos/" + id + "/download/hls/240", "fake-muxed-mp4"},
		{"/api/v1/videos/" + id + "/download/hls/240?audio=false", "fake-video-only-mp4"},
		{"/api/v1/videos/" + id + "/download/audio", "fake-audio-m4a"},
		{"/api/v1/videos/" + id + "/download/subtitles/en", sampleVTT},
	} {
		rec := sendJSONAuth(srv, http.MethodGet, tc.path, "", "")
		if rec.Code != http.StatusOK || rec.Body.String() != tc.body {
			t.Errorf("GET %s = %d %q, want 200 %q", tc.path, rec.Code, rec.Body.String(), tc.body)
		}
		if got := rec.Header().Get("Content-Disposition"); got == "" {
			t.Errorf("GET %s omitted Content-Disposition", tc.path)
		}
	}

	// Direct rendition URLs reject stale rows from a failed playlist and prove
	// object existence before adding attachment headers.
	videoID := uuid.MustParse(id)
	playlist := tcRepo.playlists[videoID]
	playlist.State = "failed"
	tcRepo.playlists[videoID] = playlist
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/240", "", ""); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("failed-playlist direct HLS = %d disposition=%q, want 404 without attachment header", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	playlist.State = "ready"
	tcRepo.playlists[videoID] = playlist
	if err := blobs.Delete(t.Context(), media.HLSDownloadKey("streaming-playlists/"+id+"/240p", true)); err != nil {
		t.Fatalf("delete muxed fixture: %v", err)
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+id+"/download/hls/240", "", ""); rec.Code != http.StatusNotFound || rec.Header().Get("Content-Disposition") != "" {
		t.Errorf("missing-object direct HLS = %d disposition=%q, want 404 without attachment header", rec.Code, rec.Header().Get("Content-Disposition"))
	}

	// Non-published UUIDs never become public by-id surfaces. The owner can still
	// inspect a draft, which truthfully has no downloadable file yet.
	draft := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`)
	if rec := getDownloads(srv, draft, ""); rec.Code != http.StatusNotFound {
		t.Errorf("anonymous draft downloads = %d, want 404", rec.Code)
	}
	if body := decodeDownloads(t, getDownloads(srv, draft, tok)); len(body.Files) != 0 {
		t.Errorf("draft files = %d, want 0", len(body.Files))
	}

	// A failed public upload retains its original for owner/admin recovery, but
	// the rejected bytes must not be reachable anonymously through any media or
	// official-download route.
	failed := createVideo(t, srv, tok, "ada", `{"title":"Rejected","privacy":"public"}`)
	if rec := uploadVideoFile(srv, failed, "rejected.mp4", "video/mp4", "rejected-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload failed-state fixture = %d; body=%s", rec.Code, rec.Body.String())
	}
	failedID := uuid.MustParse(failed)
	row := repo.videos[failedID]
	row.State = "failed"
	repo.videos[failedID] = row
	for _, path := range []string{
		"/api/v1/videos/" + failed + "/original",
		"/api/v1/videos/" + failed + "/download",
		"/api/v1/videos/" + failed + "/download/original",
	} {
		if rec := sendJSONAuth(srv, http.MethodGet, path, "", ""); rec.Code != http.StatusNotFound {
			t.Errorf("anonymous failed media GET %s = %d, want 404", path, rec.Code)
		}
	}
	if rec := sendJSONAuth(srv, http.MethodGet, "/api/v1/videos/"+failed+"/download/original", "", tok); rec.Code != http.StatusOK || rec.Body.String() != "rejected-bytes" {
		t.Errorf("owner failed original download = %d %q, want 200 rejected-bytes", rec.Code, rec.Body.String())
	}
}

// TestVideoDownloadsToggleAndPrivilegedBypass proves the operator toggle gates
// anonymous/regular access while moderator/admin tooling stays available.
func TestVideoDownloadsToggleAndPrivilegedBypass(t *testing.T) {
	srv := videoServer(t)
	admin := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	owner := createChannelFor(t, srv, "bob", "bob@example.test", "bob")
	moderatorBefore, moderatorID := registerAndUser(t, srv, `{"username":"mia","email":"mia@example.test","password":"supersecret"}`)
	_ = moderatorBefore
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/users/"+moderatorID, `{"role":"moderator"}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("promote moderator = %d; body=%s", rec.Code, rec.Body.String())
	}
	login := postTo(srv, "/api/v1/auth/login", `{"email":"mia@example.test","password":"supersecret"}`)
	var auth authResponse
	_ = json.Unmarshal(login.Body.Bytes(), &auth)
	private := createPublishedVideo(t, srv, owner, "bob", `{"title":"Private","privacy":"private"}`)
	public := createPublishedVideo(t, srv, owner, "bob", `{"title":"Public","privacy":"public"}`)

	setToggle(t, srv, admin, "downloads_enabled", false)
	for name, token := range map[string]string{"anonymous": "", "regular owner": owner} {
		if rec := getDownloads(srv, public, token); rec.Code != http.StatusForbidden || errorCode(t, rec) != "feature_disabled" {
			t.Errorf("%s disabled download = %d/%s, want 403/feature_disabled", name, rec.Code, errorCode(t, rec))
		}
	}
	for name, token := range map[string]string{"admin": admin, "moderator": auth.Token} {
		if rec := getDownloads(srv, private, token); rec.Code != http.StatusOK {
			t.Errorf("%s private download with toggle off = %d; body=%s", name, rec.Code, rec.Body.String())
		}
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
