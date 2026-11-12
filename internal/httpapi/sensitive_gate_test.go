package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/cdn"
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
	// A CDN that can purge: raising the wall in front of one that cannot is
	// refused (TestSensitiveGateSwitchOnWithAWarmCDN).
	provider, err := cdn.New(cdn.Config{BaseURL: testCDNBase}, nil)
	if err != nil {
		t.Fatalf("cdn.New: %v", err)
	}
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), []Option{
		WithDeliveryCDN(provider.EdgeURL, provider.Purge), WithCDNPurgeAvailable(func() bool { return true }),
	})
	setCDNDelivery(t, srv, true)
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

// A sensitive video that the policy still LISTS is marked for a caller without
// an account while the wall is up — sign_in_required, and no poster, because
// the poster route answers 401 — and is unmarked for a signed-in caller and
// whenever the wall is down.
func TestSensitiveGateMarksListedCardsForAnonymousCallers(t *testing.T) {
	env := newSensitiveGateEnv(t)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_content_policy":"blur"}`)
	type card struct {
		ID             string `json:"id"`
		SignInRequired bool   `json:"sign_in_required"`
		HasThumbnail   *bool  `json:"has_thumbnail"`
	}
	cards := func(path, tok string) map[string]card {
		t.Helper()
		rec := getWithAuth(env.srv, path, tok)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		var body struct {
			Videos []card `json:"videos"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		out := map[string]card{}
		for _, c := range body.Videos {
			out[c.ID] = c
		}
		return out
	}
	check := func(step, tok string, wantMarked bool) {
		t.Helper()
		for _, path := range []string{"/api/v1/videos?limit=50", "/api/v1/videos/search?q=Flagged&limit=50", "/api/v1/channels/ada/videos?limit=50"} {
			got := cards(path, tok)
			s, ok := got[env.sensitive]
			if !ok {
				t.Fatalf("%s %s: the sensitive video is not listed under the blur policy", step, path)
			}
			if s.SignInRequired != wantMarked || s.HasThumbnail == nil || *s.HasThumbnail == wantMarked {
				t.Errorf("%s %s: sensitive card sign_in_required=%v has_thumbnail=%v, want %v / %v", step, path, s.SignInRequired, s.HasThumbnail, wantMarked, !wantMarked)
			}
			if p, ok := got[env.plain]; ok && (p.SignInRequired || p.HasThumbnail == nil || !*p.HasThumbnail) {
				t.Errorf("%s %s: the plain card was touched: %+v", step, path, p)
			}
		}
	}
	check("gate off, anonymous", "", false)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	check("gate on, anonymous", "", true)
	check("gate on, signed in", env.userTok, false)
}

// A feed reader, a crawler and a third-party page can never sign in, so the
// RSS feed, the sitemap and oEmbed leave a gated video out rather than hand out
// a link that answers 401.
func TestSensitiveGateLeavesGatedVideosOutOfFeedsSitemapAndOEmbed(t *testing.T) {
	env := newSensitiveGateEnv(t)
	patchSettings(t, env.srv, env.adminTok, `{"sensitive_content_policy":"blur"}`)
	// The feed names a video by id, the sitemap by its short code.
	shortCode := func(id string) string {
		t.Helper()
		var v struct {
			ShortCode string `json:"short_code"`
		}
		rec := getWithAuth(env.srv, "/api/v1/videos/"+id, env.adminTok)
		if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil || v.ShortCode == "" {
			t.Fatalf("short code of %s: %v; body=%s", id, err, rec.Body.String())
		}
		return v.ShortCode
	}
	codes := map[string]string{env.sensitive: shortCode(env.sensitive), env.plain: shortCode(env.plain)}
	mentions := func(path, id string) bool {
		t.Helper()
		rec := getWithAuth(env.srv, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d; body=%s", path, rec.Code, rec.Body.String())
		}
		return strings.Contains(rec.Body.String(), id) || strings.Contains(rec.Body.String(), codes[id])
	}
	oembed := func(id string) int {
		return getWithAuth(env.srv, "/services/oembed?url="+url.QueryEscape("https://videos.example/videos/"+id), "").Code
	}
	for _, path := range []string{"/feeds/videos.xml", "/sitemap.xml"} {
		if !mentions(path, env.sensitive) || !mentions(path, env.plain) {
			t.Fatalf("gate off: %s should carry both videos", path)
		}
	}
	if oembed(env.sensitive) != http.StatusOK {
		t.Fatalf("gate off: oEmbed of the sensitive video = %d, want 200", oembed(env.sensitive))
	}

	patchSettings(t, env.srv, env.adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
	for _, path := range []string{"/feeds/videos.xml", "/sitemap.xml"} {
		if mentions(path, env.sensitive) {
			t.Errorf("gate on: %s still advertises the gated video", path)
		}
		if !mentions(path, env.plain) {
			t.Errorf("gate on: %s lost the plain video", path)
		}
	}
	if got := oembed(env.sensitive); got != http.StatusUnauthorized {
		t.Errorf("gate on: oEmbed of the gated video = %d, want 401", got)
	}
	if got := oembed(env.plain); got != http.StatusOK {
		t.Errorf("gate on: oEmbed of the plain video = %d, want 200", got)
	}
}

// Raising the wall on an instance whose CDN is already holding sensitive
// videos: refused outright when nothing can recall those copies, and allowed
// with an instruction to purge when something can.
func TestSensitiveGateSwitchOnWithAWarmCDN(t *testing.T) {
	provider, err := cdn.New(cdn.Config{BaseURL: testCDNBase}, nil)
	if err != nil {
		t.Fatalf("cdn.New: %v", err)
	}
	patch := func(srv *Server, tok, body string) (int, []string, string) {
		rec := sendJSONAuth(srv, http.MethodPatch, "/api/v1/admin/instance-settings", body, tok)
		var out struct {
			Notices []string `json:"notices"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out.Notices, rec.Body.String()
	}
	for _, tc := range []struct {
		name            string
		cdnWired, cdnOn bool
		canPurge        bool
		wantCode        int
		wantNotice      bool
	}{
		{"no CDN", false, false, false, http.StatusOK, false},
		{"CDN delivering, no purge endpoint", true, true, false, http.StatusUnprocessableEntity, false},
		{"CDN delivering, purge endpoint", true, true, true, http.StatusOK, true},
		{"CDN wired but delivery off", true, false, false, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var httpOpts []Option
			if tc.cdnWired {
				httpOpts = append(httpOpts, WithDeliveryCDN(provider.EdgeURL, provider.Purge),
					WithCDNPurgeAvailable(func() bool { return tc.canPurge }))
			}
			srv, _, _, _, _ := videoServerFullWith(t, testConfig(), httpOpts)
			adminTok := registerAndToken(t, srv, `{"username":"root","email":"root@example.test","password":"supersecret"}`)
			if tc.cdnOn {
				setCDNDelivery(t, srv, true)
			}
			code, notices, body := patch(srv, adminTok, `{"sensitive_anonymous_access":"sign_in"}`)
			if code != tc.wantCode {
				t.Fatalf("PATCH = %d, want %d; body=%s", code, tc.wantCode, body)
			}
			wantValue := "sign_in"
			if tc.wantCode != http.StatusOK {
				wantValue = "open"
				if !strings.Contains(body, `"sensitive_anonymous_access"`) || !strings.Contains(body, "purge") {
					t.Errorf("the refusal does not name the setting and the reason: %s", body)
				}
			}
			if got := publicInstance(t, srv).SensitiveAnonymousAccess; got != wantValue {
				t.Errorf("sensitive_anonymous_access = %q after the PATCH, want %q", got, wantValue)
			}
			if has := len(notices) == 1 && strings.Contains(notices[0], "Purge the CDN zone"); has != tc.wantNotice {
				t.Errorf("notices = %v, want a purge notice: %v", notices, tc.wantNotice)
			}
			// Restating the value is not raising the wall again: no second notice.
			if tc.wantCode == http.StatusOK {
				if _, again, _ := patch(srv, adminTok, `{"sensitive_anonymous_access":"sign_in"}`); len(again) != 0 {
					t.Errorf("a restated value produced notices: %v", again)
				}
			}
		})
	}
}
