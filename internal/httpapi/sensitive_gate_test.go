package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/video"
)

// sensitiveGateEnv is one instance with an admin, a channel owner, a plain
// member, and two public, published videos with every media surface seeded:
// one flagged sensitive and one not.
type sensitiveGateEnv struct {
	srv                         *Server
	adminTok, ownerTok, userTok string
	sensitive, plain            string
}

func newSensitiveGateEnv(t *testing.T) sensitiveGateEnv {
	t.Helper()
	srv, blobs, tcRepo, _, _ := videoServerFull(t, distributionCfg(),
		video.WithThumbnailer(fakeThumbnailer{jpg: []byte("\xff\xd8jpg")}),
		video.WithStoryboarder(storyboarderStub{}))
	adminTok := registerAndToken(t, srv, `{"username":"root","email":"root@example.test","password":"supersecret"}`)
	ownerTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	userTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	sensitive := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Flagged","privacy":"public","is_sensitive":true}`)
	plain := createPublishedVideo(t, srv, ownerTok, "ada", `{"title":"Plain","privacy":"public"}`)
	seedReadyHLS(t, tcRepo, blobs, sensitive)
	seedReadyHLS(t, tcRepo, blobs, plain)
	return sensitiveGateEnv{srv: srv, adminTok: adminTok, ownerTok: ownerTok, userTok: userTok, sensitive: sensitive, plain: plain}
}

// sensitiveGateRoutes is every per-video read a visitor's browser makes.
var sensitiveGateRoutes = []string{
	"", "/embed-privacy", "/original", "/thumbnail", "/captions", "/storyboard.jpg", "/storyboard.vtt",
	"/hls/master.m3u8", "/hls/240p/playlist.m3u8", "/hls/240p/seg_00000.ts", "/download", "/chapters",
}

// gateOffCacheControl is what each media route answered an ANONYMOUS caller
// before the gate existed (captured on the commit before it; no CDN wired in
// this harness). The JSON routes set no Cache-Control at all. With the gate
// off these must not move.
var gateOffCacheControl = map[string]string{
	"/original":               "private, max-age=3600, must-revalidate",
	"/thumbnail":              "private, max-age=300, must-revalidate",
	"/storyboard.jpg":         "private, max-age=300, must-revalidate",
	"/storyboard.vtt":         "private, max-age=300, must-revalidate",
	"/hls/master.m3u8":        "private, max-age=0, must-revalidate",
	"/hls/240p/playlist.m3u8": "private, max-age=0, must-revalidate",
	"/hls/240p/seg_00000.ts":  "private, max-age=0, must-revalidate",
}

type gateAnswer struct {
	code               int
	cacheControl, acao string
	body               string
}

func (env sensitiveGateEnv) answers(t *testing.T) map[string]gateAnswer {
	t.Helper()
	out := map[string]gateAnswer{}
	for _, who := range []struct{ name, tok string }{{"anon", ""}, {"member", env.userTok}} {
		for _, vid := range []struct{ name, id string }{{"sensitive", env.sensitive}, {"plain", env.plain}} {
			for _, p := range sensitiveGateRoutes {
				rec := getWithAuth(env.srv, "/api/v1/videos/"+vid.id+p, who.tok)
				out[who.name+" "+vid.name+" "+p] = gateAnswer{rec.Code, rec.Header().Get("Cache-Control"),
					rec.Header().Get("Access-Control-Allow-Origin"), rec.Body.String()}
			}
		}
	}
	return out
}

// TestSensitiveGateOffChangesNothing: shipped off. With the setting untouched,
// and with it set to "open" explicitly, every route answers every caller what
// it answered before the gate existed — status, cache policy, CORS and body.
func TestSensitiveGateOffChangesNothing(t *testing.T) {
	env := newSensitiveGateEnv(t)
	if got := publicInstance(t, env.srv).SensitiveAnonymousAccess; got != "open" {
		t.Fatalf("sensitive_anonymous_access default = %q, want open", got)
	}
	untouched := env.answers(t)
	for key, a := range untouched {
		who, route := strings.Fields(key)[0], strings.TrimPrefix(key[strings.LastIndex(key, " "):], " ")
		if a.code != http.StatusOK {
			t.Errorf("%s: status %d, want 200 as before the gate", key, a.code)
		}
		want, wantACAO := gateOffCacheControl[route], ""
		if _, media := gateOffCacheControl[route]; media {
			wantACAO = "*"
			if who == "member" { // a credentialed media read was already no-store
				want, wantACAO = "private, no-store", ""
			}
		}
		if a.cacheControl != want || a.acao != wantACAO {
			t.Errorf("%s: Cache-Control %q / ACAO %q, want %q / %q as before the gate", key, a.cacheControl, a.acao, want, wantACAO)
		}
	}
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"open"}`)
	for key, a := range env.answers(t) {
		if a != untouched[key] {
			t.Errorf("%s: setting the gate to open changed the answer\n was %+v\n now %+v", key, untouched[key], a)
		}
	}
}

// TestSensitiveGateRouteTable: with sensitive_anonymous_access=sign_in, a caller
// without an account gets 401 sign_in_required on EVERY read of a sensitive
// video — metadata and bytes alike — and nothing else changes: a signed-in
// caller reads it as before, and a video that is not sensitive is untouched for
// everyone, byte for byte.
func TestSensitiveGateRouteTable(t *testing.T) {
	env := newSensitiveGateEnv(t)
	before := env.answers(t)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	if got := publicInstance(t, env.srv).SensitiveAnonymousAccess; got != "sign_in" {
		t.Fatalf("sensitive_anonymous_access = %q after the switch", got)
	}
	after := env.answers(t)
	for key, a := range after {
		if strings.HasPrefix(key, "anon sensitive ") {
			if a.code != http.StatusUnauthorized || !strings.Contains(a.body, `"sign_in_required"`) {
				t.Errorf("%s: %d %s, want 401 sign_in_required", key, a.code, a.body)
			}
			if a.acao != "" || strings.Contains(a.cacheControl, "max-age") {
				t.Errorf("%s: the refusal is cacheable or cross-origin readable: Cache-Control %q, ACAO %q", key, a.cacheControl, a.acao)
			}
			continue
		}
		if a != before[key] {
			t.Errorf("%s: the gate changed an answer it should not touch\n was %+v\n now %+v", key, before[key], a)
		}
	}
}

// A <video> or <img> cannot send an Authorization header: a signed-in viewer
// reaches a gated video's bytes on the video-access cookie, as for an internal
// video, and that credentialed answer is never stored.
func TestSensitiveGateAdmitsTheVideoAccessCookie(t *testing.T) {
	env := newSensitiveGateEnv(t)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	for _, p := range []string{"/original", "/thumbnail", "/hls/master.m3u8", "/hls/240p/seg_00000.ts"} {
		for _, tc := range []struct {
			name, cookie string
			want         int
		}{{"member cookie", env.userTok, http.StatusOK}, {"no cookie", "", http.StatusUnauthorized}, {"malformed cookie", "invalid", http.StatusUnauthorized}} {
			req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+env.sensitive+p, nil)
			if tc.cookie != "" {
				req.AddCookie(&http.Cookie{Name: "vidra_video_access", Value: tc.cookie})
			}
			rec := httptest.NewRecorder()
			env.srv.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s with %s = %d, want %d; body=%s", p, tc.name, rec.Code, tc.want, rec.Body.String())
			}
			if tc.want == http.StatusOK && rec.Header().Get("Cache-Control") != "private, no-store" {
				t.Errorf("%s with %s: Cache-Control %q, want private, no-store", p, tc.name, rec.Header().Get("Cache-Control"))
			}
		}
	}
}

// The sign-in 401 admits the video exists, so — like the internal-privacy one —
// it is answered only after the moderation gates: a blocked sensitive video is
// the same 404 to an anonymous caller as any other hidden one.
func TestSensitiveGateDoesNotRevealABlockedVideo(t *testing.T) {
	env := newSensitiveGateEnv(t)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	if rec := postJSONAuth(env.srv, "/api/v1/admin/videos/"+env.sensitive+"/block", `{"reason":"tos"}`, env.adminTok); rec.Code/100 != 2 {
		t.Fatalf("block = %d; body=%s", rec.Code, rec.Body.String())
	}
	for _, p := range []string{"", "/original", "/embed-privacy", "/hls/master.m3u8"} {
		if rec := getWithAuth(env.srv, "/api/v1/videos/"+env.sensitive+p, ""); rec.Code != http.StatusNotFound {
			t.Errorf("blocked + gated %q = %d, want 404", p, rec.Code)
		}
	}
}

// With a CDN in front, the wall has to hold at the edge too: a gated video's
// bytes are no longer redirected to it, the edge's own anonymous origin fetch
// is refused (so it cannot refill), a signed-in viewer is served by the API
// with a no-store answer, and a video that is not sensitive keeps its redirect.
func TestSensitiveGateKeepsGatedBytesOffTheCDN(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	adminTok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	memberTok := registerAndToken(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)
	gated := publishedPublicVideo(t, srv, blobs, tcRepo, adminTok)
	plain := publishedPublicVideo(t, srv, blobs, tcRepo, adminTok)
	if rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/videos/"+gated, `{"is_sensitive":true}`, adminTok); rec.Code != http.StatusOK {
		t.Fatalf("flag sensitive = %d; body=%s", rec.Code, rec.Body.String())
	}
	original := func(id string) string { return "/api/v1/videos/" + id + "/original" }

	// Gate off: a sensitive public video is ordinary public media, as before.
	assertCDNRedirect(t, getWith(srv, original(gated), "", ""), original(gated))

	patchSettings(t, srv, adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	if rec := getWith(srv, original(gated), "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous = %d, want 401; body=%s", rec.Code, rec.Body.String())
	}
	edgeFetch := original(gated) + "?" + delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue
	if rec := getWith(srv, edgeFetch, "", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the edge's origin fetch = %d, want 401 (it must not be able to refill); body=%s", rec.Code, rec.Body.String())
	}
	rec := getWithAuth(srv, original(gated), memberTok)
	assertBytes(t, rec, "video-bytes")
	if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
		t.Errorf("signed-in read of a gated video: Cache-Control %q, want private, no-store", cc)
	}
	assertCDNRedirect(t, getWith(srv, original(plain), "", ""), original(plain))
}
