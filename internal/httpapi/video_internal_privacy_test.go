package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
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
	blobs                       storage.Backend
	tcRepo                      *transcodeFakeRepo
	repo                        *videoFakeRepo
}

func newInternalPrivacyEnv(t *testing.T, opts ...video.Option) internalPrivacyEnv {
	t.Helper()
	srv, blobs, tcRepo, _, repo := videoServerFull(t, distributionCfg(), opts...)
	// The first registered account becomes the admin.
	adminTok := registerAndToken(t, srv, `{"username":"root","email":"root@example.test","password":"supersecret"}`)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	userTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	id := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Members only","privacy":"internal"}`)
	return internalPrivacyEnv{
		srv: srv, adminTok: adminTok, ownerTok: ownerTok, userTok: userTok, id: id,
		blobs: blobs, tcRepo: tcRepo, repo: repo,
	}
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

// A signed-in member finds an internal video where they browse — the main feed
// in every sort, the channel page, and (following the channel) the
// subscriptions feed — with totals that count it. An anonymous visitor finds
// it on none of them. Search stays public-only for everyone.
func TestInternalVideoListedToSignedInMembers(t *testing.T) {
	env := newInternalPrivacyEnv(t)
	if rec := postJSONAuth(env.srv, "/api/v1/channels/ada/follow", `{}`, env.userTok); rec.Code/100 != 2 {
		t.Fatalf("follow = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/v1/videos?sort=recent", "/api/v1/videos?sort=trending", "/api/v1/channels/ada/videos"} {
		if listedVideoIDs(t, getWithAuth(env.srv, path, ""))[env.id] {
			t.Errorf("anonymous %s lists the internal video", path)
		}
		if !listedVideoIDs(t, getWithAuth(env.srv, path, env.userTok))[env.id] {
			t.Errorf("member %s does not list the internal video", path)
		}
	}
	if !listedVideoIDs(t, getWithAuth(env.srv, "/api/v1/me/subscriptions/videos", env.userTok))[env.id] {
		t.Error("subscriptions feed does not list the followed channel's internal video")
	}
	if listedVideoIDs(t, getWithAuth(env.srv, "/api/v1/videos/search?q=Members", env.userTok))[env.id] {
		t.Error("member search lists the internal video; search stays public-only")
	}
}

// F4 (T18 security review): the rest of the media surface — HLS master, variant
// playlist and segment, the poster, the caption list and the storyboard — sits
// behind the same gate. Every one of them is seeded so a member gets a real 200,
// which proves the refusal an anonymous caller gets is the privacy gate and not
// a missing object.
func TestInternalVideoMediaSurfacesMatrix(t *testing.T) {
	env := newInternalPrivacyEnv(t,
		video.WithThumbnailer(fakeThumbnailer{jpg: []byte("\xff\xd8jpg")}),
		video.WithStoryboarder(storyboarderStub{}))
	seedReadyHLS(t, env.tcRepo, env.blobs, env.id)
	for _, p := range []string{
		"/hls/master.m3u8", "/hls/240p/playlist.m3u8", "/hls/240p/seg_00000.ts",
		"/thumbnail", "/captions", "/storyboard.jpg",
	} {
		path := "/api/v1/videos/" + env.id + p
		t.Run(p, func(t *testing.T) {
			rec := getWithAuth(env.srv, path, "")
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("anonymous = %d, want 401; body=%s", rec.Code, rec.Body.String())
			}
			if b := errBodyOf(t, rec); b.Code != "sign_in_required" {
				t.Errorf("anonymous code = %q, want sign_in_required", b.Code)
			}
			if rec := getWithAuth(env.srv, path, env.userTok); rec.Code != http.StatusOK {
				t.Errorf("member = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

// F3 (T18 security review): the sign-in 401 admits that a video exists, so it is
// answered only AFTER the moderation and lifecycle gates. A blocked, quarantined,
// scheduled, transcoding-held or rejected internal video is the same
// indistinguishable 404 to an anonymous caller as any other hidden video — on the
// detail route and on media.
func TestHiddenInternalVideoIs404NotSignInToAnonymous(t *testing.T) {
	for _, tc := range []struct {
		name string
		hide func(t *testing.T, env internalPrivacyEnv)
	}{
		{"blocked", func(t *testing.T, env internalPrivacyEnv) {
			rec := postJSONAuth(env.srv, "/api/v1/admin/videos/"+env.id+"/block", `{"reason":"tos"}`, env.adminTok)
			if rec.Code/100 != 2 {
				t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
			}
		}},
		{"quarantined", setFakeVideoState("quarantined")},
		{"scheduled", setFakeVideoState("scheduled")},
		{"transcoding", setFakeVideoState("transcoding")},
		{"rejected", setFakeVideoState("failed")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := newInternalPrivacyEnv(t)
			tc.hide(t, env)
			for _, p := range []string{"", "/original", "/embed-privacy"} {
				rec := getWithAuth(env.srv, "/api/v1/videos/"+env.id+p, "")
				if rec.Code != http.StatusNotFound {
					t.Errorf("anonymous GET %q = %d, want 404 (a hidden video's existence is not admitted); body=%s",
						p, rec.Code, rec.Body.String())
				}
			}
		})
	}
}

// setFakeVideoState moves the env's video to a lifecycle state directly in the
// video fake, the way the transitions that own those states would leave it.
func setFakeVideoState(state string) func(t *testing.T, env internalPrivacyEnv) {
	return func(t *testing.T, env internalPrivacyEnv) {
		t.Helper()
		id := uuid.MustParse(env.id)
		r, ok := env.repo.videos[id]
		if !ok {
			t.Fatalf("video %s not in the fake", env.id)
		}
		r.State = state
		env.repo.videos[id] = r
	}
}
