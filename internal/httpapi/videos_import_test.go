package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// loopbackAsLocalhost rewrites an httptest server's 127.0.0.1 URL to use the
// "localhost" hostname. urlsafety.ValidateURL rejects a literal loopback IP up
// front, but accepts a hostname (names are only resolved at dial time) — so this
// lets a test drive the fetch path with an injected plain client, while the
// production guarded client still refuses it (blocked at dial, post-resolution).
func loopbackAsLocalhost(u string) string {
	return strings.Replace(u, "127.0.0.1", "localhost", 1)
}

// importMediaServer is a loopback test origin serving a few fixtures: a tiny
// "video" (the harness has no prober, so any bytes publish), a non-video file,
// and an oversized body.
func importMediaServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/clip.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write([]byte("tiny-video-bytes"))
		case "/notavideo.txt":
			_, _ = w.Write([]byte("not a video"))
		case "/big.mp4":
			w.Header().Set("Content-Type", "video/mp4")
			_, _ = w.Write(make([]byte, 4096))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestImportVideoFile imports a remote file into a draft (via an injected plain
// client, since the production SSRF guard refuses the loopback test origin) and
// checks the video is stored + published, then served publicly.
func TestImportVideoFile(t *testing.T) {
	srv := videoServer(t)
	srv.importClient = &http.Client{} // reach loopback; prod uses urlsafety.NewClient
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/clip.mp4"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Video.State != "published" {
		t.Errorf("state = %q, want published", resp.Video.State)
	}
	if resp.File.SizeBytes != int64(len("tiny-video-bytes")) {
		t.Errorf("stored size = %d, want %d", resp.File.SizeBytes, len("tiny-video-bytes"))
	}
	// The imported original is now publicly reachable.
	if g := getVideo(srv, id, ""); g.Code != http.StatusOK {
		t.Errorf("public detail after import = %d, want 200", g.Code)
	}
}

// TestImportVideoRejectsNonVideoExtension: a URL without a video-container
// extension is 415 (the same allow-list as multipart upload).
func TestImportVideoRejectsNonVideoExtension(t *testing.T) {
	srv := videoServer(t)
	srv.importClient = &http.Client{}
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/notavideo.txt"}`, tok)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("import .txt = %d, want 415; body=%s", rec.Code, rec.Body.String())
	}
}

// TestImportVideoSSRFGuardBlocksLoopback: with the DEFAULT (production) client,
// the SSRF guard refuses to fetch the loopback test origin → 422. This proves the
// guard is actually wired into the handler.
func TestImportVideoSSRFGuardBlocksLoopback(t *testing.T) {
	srv := videoServer(t) // no injected client → urlsafety-guarded client
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+loopbackAsLocalhost(media.URL)+`/clip.mp4"}`, tok)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("import loopback via guarded client = %d, want 422; body=%s", rec.Code, rec.Body.String())
	}
}

// TestImportVideoAllowPrivateConfig proves the HTTP_IMPORT_ALLOW_PRIVATE_URLS
// dev/test knob flows config → urlsafety.Guard → real client: with it on, the
// same loopback origin the guard normally blocks is imported successfully — using
// the guard-built client (no injected test client) and a literal 127.0.0.1 URL
// that ValidateURL would normally reject. This is what makes the frontend
// URL-import backed e2e provable in CI.
func TestImportVideoAllowPrivateConfig(t *testing.T) {
	cfg := testConfig()
	cfg.ImportAllowPrivateURLs = true
	srv := videoServerCfg(t, cfg)
	media := importMediaServer(t)

	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)

	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"`+media.URL+`/clip.mp4"}`, tok) // literal 127.0.0.1 URL, no rewrite
	if rec.Code != http.StatusCreated {
		t.Fatalf("import with AllowPrivate = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var resp uploadVideoFileResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.Video.State != "published" {
		t.Errorf("state = %q, want published", resp.Video.State)
	}
}

func TestImportVideoValidation(t *testing.T) {
	srv := videoServer(t)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"x"}`)

	// Missing URL → 422.
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("empty url = %d, want 422", rec.Code)
	}
	// Non-http scheme → 422 (urlsafety rejects it before any fetch).
	if rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import", `{"url":"ftp://example.com/x.mp4"}`, tok); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("ftp url = %d, want 422", rec.Code)
	}
	// Anonymous → 401.
	if rec := postTo(srv, "/api/v1/videos/"+id+"/import", `{"url":"https://example.com/x.mp4"}`); rec.Code != http.StatusUnauthorized {
		t.Errorf("anon import = %d, want 401", rec.Code)
	}
}

// TestImportVideoNonOwner: a valid public URL owned by nobody-in-particular still
// 404s for a non-owner — ownership is checked before any fetch.
func TestImportVideoNonOwner(t *testing.T) {
	srv := videoServer(t)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, ownerTok, "ada", `{"title":"x"}`)

	otherTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	rec := sendJSONAuth(srv, http.MethodPost, "/api/v1/videos/"+id+"/import",
		`{"url":"https://example.com/clip.mp4"}`, otherTok)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("non-owner import = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}
