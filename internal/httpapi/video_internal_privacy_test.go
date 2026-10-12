package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/video"
)

// internalPrivacyEnv is one instance with an admin, a channel owner who is NOT
// staff, a plain signed-in member, and one published privacy=internal video
// (PeerTube's "internal": visible to any signed-in account of THIS instance,
// never to an anonymous viewer).
type internalPrivacyEnv struct {
	srv                         *Server
	adminTok, ownerTok, userTok string
	id                          string
}

func newInternalPrivacyEnv(t *testing.T) internalPrivacyEnv {
	t.Helper()
	srv, _, _, _, _ := videoServerFull(t, distributionCfg())
	// The first registered account becomes the admin.
	adminTok := registerAndToken(t, srv, `{"username":"root","email":"root@example.test","password":"supersecret"}`)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	userTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Members only","privacy":"internal"}`)
	return internalPrivacyEnv{srv: srv, adminTok: adminTok, ownerTok: ownerTok, userTok: userTok, id: id}
}

func listedVideoIDs(t *testing.T, rec *httptest.ResponseRecorder) map[string]bool {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list = %d; body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Videos []struct {
			ID string `json:"id"`
		} `json:"videos"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	ids := map[string]bool{}
	for _, v := range body.Videos {
		ids[v.ID] = true
	}
	return ids
}

func TestCreateAndUpdateAcceptInternalPrivacy(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	var v videoView
	_ = json.Unmarshal(getVideo(env.srv, env.id, env.ownerTok).Body.Bytes(), &v)
	if v.Privacy != video.PrivacyInternal {
		t.Fatalf("created privacy = %q, want internal", v.Privacy)
	}
	other := createVideo(t, env.srv, env.ownerTok, "ada", `{"title":"Later","privacy":"public"}`)
	rec := sendJSONAuth(env.srv, http.MethodPatch, "/api/v1/videos/"+other, `{"privacy":"internal"}`, env.ownerTok)
	if rec.Code != http.StatusOK {
		t.Fatalf("patch privacy=internal = %d; body=%s", rec.Code, rec.Body.String())
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	if v.Privacy != video.PrivacyInternal {
		t.Errorf("updated privacy = %q, want internal", v.Privacy)
	}
}

// The visibility matrix: every read path that serves the video or its bytes
// admits any signed-in account and refuses the anonymous viewer. The refusal is
// the deliberate 401 sign_in_required (not the 404 a private video gets) so the
// watch page can offer to sign in, exactly as password_required lets it offer
// an unlock prompt.
func TestInternalVideoVisibilityMatrix(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	paths := map[string]string{
		"detail":        "/api/v1/videos/" + env.id,
		"embed-privacy": "/api/v1/videos/" + env.id + "/embed-privacy",
		"original":      "/api/v1/videos/" + env.id + "/original",
		"downloads":     "/api/v1/videos/" + env.id + "/download",
	}
	for name, path := range paths {
		t.Run(name, func(t *testing.T) {
			rec := getWithAuth(env.srv, path, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous = %d, want 401; body=%s", rec.Code, rec.Body.String())
			}
			if b := errBodyOf(t, rec); b.Code != "sign_in_required" {
				t.Errorf("anonymous code = %q, want sign_in_required", b.Code)
			}
			for who, tok := range map[string]string{"member": env.userTok, "owner": env.ownerTok, "admin": env.adminTok} {
				if rec := getWithAuth(env.srv, path, tok); rec.Code != http.StatusOK {
					t.Errorf("%s = %d, want 200; body=%s", who, rec.Code, rec.Body.String())
				}
			}
		})
	}
}

// A <video> element cannot send an Authorization header, so a signed-in member
// reaches the bytes on the video-access cookie — and that response is
// credentialed, so it must never be stored by any cache.
func TestInternalVideoMediaOnTheCookieIsNoStore(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	for _, tc := range []struct {
		name   string
		cookie string
		want   int
	}{
		{"member cookie", env.userTok, http.StatusOK},
		{"no cookie", "", http.StatusUnauthorized},
		{"malformed cookie", "invalid", http.StatusUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+env.id+"/original", nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "vidra_video_access", Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			env.srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusOK {
				if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
					t.Errorf("Cache-Control = %q, want private, no-store (credentialed)", cc)
				}
			}
		})
	}
}

// A view from a signed-in member counts; an anonymous ping is refused.
func TestInternalVideoViewCounting(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	if rec := postView(env.srv, env.id, ""); rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound {
		t.Fatalf("anonymous view = %d, want a refusal", rec.Code)
	}
	if rec := postView(env.srv, env.id, env.userTok); rec.Code != http.StatusNoContent {
		t.Fatalf("member view = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
}

// Anything an anonymous visitor, a CMS or a crawler can read must never carry an
// internal video: the public feed, search, RSS, the sitemap, and oEmbed (which
// treats it like private — not publicly embeddable).
func TestInternalVideoNeverOnAnonymousSurfaces(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	if listedVideoIDs(t, getWithAuth(env.srv, "/api/v1/videos", ""))[env.id] {
		t.Error("anonymous feed lists the internal video")
	}
	if listedVideoIDs(t, getWithAuth(env.srv, "/api/v1/videos/search?q=Members", ""))[env.id] {
		t.Error("anonymous search lists the internal video")
	}
	for _, path := range []string{"/feeds/videos.xml", "/sitemap.xml"} {
		rec := get(t, env.srv, path)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", path, rec.Code)
		}
		if strings.Contains(rec.Body.String(), env.id) {
			t.Errorf("%s carries the internal video", path)
		}
	}
	oembed := get(t, env.srv, "/services/oembed?format=json&url="+url.QueryEscape("https://videos.example/videos/"+env.id))
	if oembed.Code != http.StatusUnauthorized {
		t.Errorf("oEmbed = %d, want 401 (not publicly embeddable)", oembed.Code)
	}
}
