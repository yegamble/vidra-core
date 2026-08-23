package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/drm"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// drmTestKEK is a valid 32-byte KEK in the encoding config validates.
var drmTestKEK = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))

// drmFakeRepo is the content-key store in memory, reproducing the real query
// set's ON CONFLICT DO NOTHING insert. Using it under the REAL ClearKey provider
// rather than faking the provider is deliberate: these tests are about the
// end-to-end path (session -> license request -> key), and a fake provider would
// prove the handler talks to an interface rather than that a player can play.
type drmFakeRepo struct {
	rows map[uuid.UUID]sqlcgen.GetVideoDRMKeyRow
}

func newDRMFakeRepo() *drmFakeRepo {
	return &drmFakeRepo{rows: map[uuid.UUID]sqlcgen.GetVideoDRMKeyRow{}}
}

func (r *drmFakeRepo) GetVideoDRMKey(_ context.Context, videoID uuid.UUID) ([]sqlcgen.GetVideoDRMKeyRow, error) {
	row, ok := r.rows[videoID]
	if !ok {
		return nil, nil
	}
	return []sqlcgen.GetVideoDRMKeyRow{row}, nil
}

func (r *drmFakeRepo) InsertVideoDRMKey(_ context.Context, arg sqlcgen.InsertVideoDRMKeyParams) error {
	if _, exists := r.rows[arg.VideoID]; exists {
		return nil
	}
	r.rows[arg.VideoID] = sqlcgen.GetVideoDRMKeyRow{
		VideoID:          arg.VideoID,
		KeyID:            arg.KeyID,
		ContentKeySealed: arg.ContentKeySealed,
	}
	return nil
}

// drmEnv is a playbackSessionEnv whose server runs the real ClearKey provider.
type drmEnv struct {
	playbackSessionEnv
	provider drm.Provider
}

func newDRMEnv(t *testing.T) drmEnv {
	t.Helper()
	provider, err := drm.New(drm.Config{
		Provider: drm.ProviderClearKeyTest,
		KeyKEK:   drmTestKEK,
		Repo:     newDRMFakeRepo(),
	})
	if err != nil {
		t.Fatalf("drm.New: %v", err)
	}
	srv, blobs, tc, _, _ := videoServerFullWith(t, testConfig(), []Option{WithDRM(provider)})
	return drmEnv{
		playbackSessionEnv: playbackSessionEnv{
			srv:   srv,
			blobs: blobs,
			tc:    tc,
			owner: createChannelFor(t, srv, "ada", "ada@example.test", "ada"),
		},
		provider: provider,
	}
}

// protect runs the packaging-time half by hand — nothing in the product calls
// PrepareAsset yet — so a test can have a video that IS protected.
func (e drmEnv) protect(t *testing.T, videoID string) drm.AssetKeys {
	t.Helper()
	id, err := uuid.Parse(videoID)
	if err != nil {
		t.Fatalf("parse video id: %v", err)
	}
	keys, err := e.provider.PrepareAsset(context.Background(), id)
	if err != nil {
		t.Fatalf("PrepareAsset: %v", err)
	}
	return keys
}

// kidOf packages a video and returns the base64url-unpadded key id a CDM would
// ask for.
func kidOf(t *testing.T, env drmEnv, videoID string) string {
	t.Helper()
	keys := env.protect(t, videoID)
	return base64.RawURLEncoding.EncodeToString(keys.KeyID[:])
}

// postClearKeyLicense issues a license request, optionally carrying a bearer
// credential (an account token, or a playback token — a license request is an
// XHR, so it can set a header either way).
func postClearKeyLicense(srv *Server, videoID, bearer, body string) *httptest.ResponseRecorder {
	return doJSON(srv, http.MethodPost, "/api/v1/videos/"+videoID+"/license/clearkey", bearer, body)
}

// TestPlaybackSessionJSONUnchangedByDefault IS THE REGRESSION TEST FOR THIS
// WHOLE SLICE.
//
// The DRM seam adds a field to a response every player already consumes. If a
// `drm` key appeared on the default configuration — which is every install —
// that would be a silent contract change: clients would start seeing a
// protection block for media that is not protected, and the ones that act on it
// would try to negotiate a CDM for clear bytes. So this asserts on the RAW BODY
// that the key set is exactly what it was before internal/drm existed, rather
// than on a decoded struct (which cannot tell an absent field from a zero one).
func TestPlaybackSessionJSONUnchangedByDefault(t *testing.T) {
	cases := []struct {
		name     string
		setup    func(t *testing.T, env playbackSessionEnv) (id, bearer, pt string)
		wantKeys []string
	}{
		{
			name: "public video with a ready tree",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantKeys: []string{"hls_url", "packaging_format", "renditions", "session_id", "video_id"},
		},
		{
			name: "cmaf video advertises dash and nothing else",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"CMAF","privacy":"public"}`)
				seedReadyCMAF(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantKeys: []string{"dash_url", "hls_url", "packaging_format", "renditions", "session_id", "video_id"},
		},
		{
			name: "no ready tree yet",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				return createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Fresh","privacy":"public"}`), "", ""
			},
			wantKeys: []string{"session_id", "video_id"},
		},
		{
			// The one tier that carries a credential. Its key set must not gain
			// a drm block either.
			name: "password video with a playback token",
			setup: func(t *testing.T, env playbackSessionEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", unlockToken(t, env.srv, id, "hunter2secret")
			},
			wantKeys: []string{"expires_in", "hls_url", "packaging_format", "playback_token", "renditions", "session_id", "video_id"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// The DEFAULT server: no WithDRM, exactly as an install with no
			// DRM_PROVIDER runs.
			env := newPlaybackSessionEnv(t)
			id, bearer, pt := tc.setup(t, env)
			rec := postPlaybackSession(env.srv, id, bearer, pt)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if strings.Contains(body, `"drm"`) {
				t.Fatalf("the default session response carries a drm key: %s", body)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal([]byte(body), &raw); err != nil {
				t.Fatalf("decode session: %v; body=%s", err, body)
			}
			got := make([]string, 0, len(raw))
			for k := range raw {
				got = append(got, k)
			}
			sort.Strings(got)
			if strings.Join(got, ",") != strings.Join(tc.wantKeys, ",") {
				t.Fatalf("session keys = %v, want %v — the default session response must be byte-identical to the one that shipped before the DRM seam existed", got, tc.wantKeys)
			}
		})
	}
}

// TestPlaybackSessionDRMBlockAppearsOnlyForProtectedMedia. Wiring a provider is
// not the same as protecting anything: only a video that has been through
// PrepareAsset carries a drm block, which is what keeps turning a provider on
// from changing the session of every video already published.
func TestPlaybackSessionDRMBlockAppearsOnlyForProtectedMedia(t *testing.T) {
	env := newDRMEnv(t)

	clear := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clear","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, clear)
	rec := postPlaybackSession(env.srv, clear, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), `"drm"`) {
		t.Fatalf("an unpackaged video reports protection with a provider merely wired: %s", rec.Body.String())
	}

	protected := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Locked","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, protected)
	keys := env.protect(t, protected)

	rec = postPlaybackSession(env.srv, protected, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got playbackSessionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if got.DRM == nil {
		t.Fatalf("a protected video's session carries no drm block: %s", rec.Body.String())
	}
	if got.DRM.Scheme != drm.SchemeCENC {
		t.Errorf("scheme = %q, want %q", got.DRM.Scheme, drm.SchemeCENC)
	}
	if got.DRM.KeyID != keys.KeyID.String() {
		t.Errorf("key_id = %q, want %q", got.DRM.KeyID, keys.KeyID)
	}
	if len(got.DRM.KeySystems) != 1 {
		t.Fatalf("key_systems = %+v, want exactly one", got.DRM.KeySystems)
	}
	if got.DRM.KeySystems[0].KeySystem != drm.KeySystemClearKey {
		t.Errorf("key_system = %q, want %q", got.DRM.KeySystems[0].KeySystem, drm.KeySystemClearKey)
	}
	// The advertised URL must be the route that is actually registered — a
	// license URL pointing at a 404 is worse than none.
	wantURL := "/api/v1/videos/" + protected + "/license/clearkey"
	if got.DRM.KeySystems[0].LicenseURL != wantURL {
		t.Errorf("license_url = %q, want %q", got.DRM.KeySystems[0].LicenseURL, wantURL)
	}
	// A session's drm block never carries key material.
	if strings.Contains(rec.Body.String(), base64.RawURLEncoding.EncodeToString(keys.Key)) {
		t.Error("the content key appears in the playback session response")
	}
}

// TestClearKeyLicenseAuthorizationMatchesMedia. A license is authority over the
// KEY to a video's bytes, so it must not be obtainable by anyone who could not
// obtain the bytes. This is the same table shape as the playback session's
// authorization test, and for the same reason: the endpoint calls
// videoVisibleForMedia rather than restating it, and this is what proves it.
func TestClearKeyLicenseAuthorizationMatchesMedia(t *testing.T) {
	cases := []struct {
		name string
		// setup returns the video id, the bearer credential, and the
		// base64url-unpadded key id the request should ask for ("" when the
		// video was never packaged, so the case exercises an unprotected one).
		setup      func(t *testing.T, env drmEnv) (id, bearer, kid string)
		wantStatus int
		wantCode   string
	}{
		{
			// The password tier reaching the endpoint the way a real player
			// does: the session's playback token as an Authorization header.
			// A license request is an XHR, so it can set one.
			name: "password video with the session playback token",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, unlockToken(t, env.srv, id, "hunter2secret"), kidOf(t, env, id)
			},
			wantStatus: http.StatusOK,
		},
		{
			// The unlock prompt's trigger, byte-identical to the media routes'.
			name: "password video with no credential",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createPasswordVideo(t, env.srv, env.owner, "ada", "Locked", "hunter2secret")
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", kidOf(t, env, id)
			},
			wantStatus: http.StatusUnauthorized,
			wantCode:   "password_required",
		},
		{
			// Existence is not leaked, protected or not.
			name: "private video, anonymous",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Secret","privacy":"private"}`)
				return id, "", kidOf(t, env, id)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unpublished video, anonymous",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createVideo(t, env.srv, env.owner, "ada", `{"title":"Draft","privacy":"public"}`)
				return id, "", kidOf(t, env, id)
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unpublished video, owner",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createVideo(t, env.srv, env.owner, "ada", `{"title":"Draft","privacy":"public"}`)
				return id, env.owner, kidOf(t, env, id)
			},
			wantStatus: http.StatusOK,
		},
		{
			// Authorised, and simply not protected — nothing calls PrepareAsset
			// for it. Same 404 as "no provider configured": telling these apart
			// would report whether a video is DRM-protected to a caller with no
			// business knowing.
			name: "public video that was never packaged with keys",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clear","privacy":"public"}`)
				seedReadyHLS(t, env.tc, env.blobs, id)
				return id, "", ""
			},
			wantStatus: http.StatusNotFound,
		},
		{
			name: "unknown video id",
			setup: func(t *testing.T, env drmEnv) (string, string, string) {
				return uuid.NewString(), "", ""
			},
			wantStatus: http.StatusNotFound,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := newDRMEnv(t)
			id, bearer, kid := tc.setup(t, env)
			if kid == "" {
				// A syntactically valid key id no video has, so the ONLY thing
				// that can make this request fail is the gate under test.
				any := uuid.New()
				kid = base64.RawURLEncoding.EncodeToString(any[:])
			}
			rec := postClearKeyLicense(env.srv, id, bearer, `{"kids":["`+kid+`"],"type":"temporary"}`)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantCode != "" && !strings.Contains(rec.Body.String(), `"code":"`+tc.wantCode+`"`) {
				t.Fatalf("body = %s, want error code %q", rec.Body.String(), tc.wantCode)
			}
		})
	}
}

// TestClearKeyLicenseWireResponse checks what a CDM actually receives, and that
// the key material is never cacheable. A shared cache holding a license would
// hand the content key to viewers who never cleared the gate.
func TestClearKeyLicenseWireResponse(t *testing.T) {
	env := newDRMEnv(t)
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Locked","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, id)
	keys := env.protect(t, id)
	kid := base64.RawURLEncoding.EncodeToString(keys.KeyID[:])

	rec := postClearKeyLicense(env.srv, id, "", `{"kids":["`+kid+`"],"type":"temporary"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if cc := rec.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("Cache-Control = %q, want no-store — the body is key material", cc)
	}
	var got drm.ClearKeyLicense
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode license: %v; body=%s", err, rec.Body.String())
	}
	if got.Type != drm.LicenseTypeTemporary || len(got.Keys) != 1 {
		t.Fatalf("license = %+v, want one temporary key", got)
	}
	if got.Keys[0].KID != kid || got.Keys[0].KTY != "oct" {
		t.Errorf("jwk = %+v, want kid %q and kty oct", got.Keys[0], kid)
	}
	gotKey, err := base64.RawURLEncoding.DecodeString(got.Keys[0].K)
	if err != nil {
		t.Fatalf("k is not base64url-unpadded: %v", err)
	}
	if string(gotKey) != string(keys.Key) {
		t.Error("k is not this video's content key")
	}
}

// TestClearKeyLicenseRejectsMalformedRequests. The body is caller-supplied and
// the cap bounds per-request work; neither message echoes a key id back.
func TestClearKeyLicenseRejectsMalformedRequests(t *testing.T) {
	env := newDRMEnv(t)
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Locked","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, id)
	env.protect(t, id)

	many := make([]string, drm.MaxLicenseKeyIDs+1)
	for i := range many {
		many[i] = `"AAAAAAAAAAAAAAAAAAAAAA"`
	}
	cases := map[string]string{
		"no body at all":  `{}`,
		"empty kids":      `{"kids":[]}`,
		"kids is not set": `{"type":"temporary"}`,
		"too many kids":   `{"kids":[` + strings.Join(many, ",") + `]}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			rec := postClearKeyLicense(env.srv, id, "", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422; body=%s", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "AAAAAAAAAAAAAAAAAAAAAA") {
				t.Errorf("a validation message echoed a caller-supplied key id: %s", rec.Body.String())
			}
		})
	}
	// A well-formed request for someone else's key id is 404, not a validation
	// error: it is a valid request that this video cannot answer.
	foreign := uuid.New()
	rec := postClearKeyLicense(env.srv, id, "", `{"kids":["`+base64.RawURLEncoding.EncodeToString(foreign[:])+`"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("foreign key id: status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
}

// TestClearKeyLicense404sWithNoProviderConfigured pins the shipped
// configuration: the route exists (so the documented contract does not move with
// DRM_PROVIDER) and answers 404 for every video, indistinguishably from a
// provider that has protected nothing.
func TestClearKeyLicense404sWithNoProviderConfigured(t *testing.T) {
	env := newPlaybackSessionEnv(t)
	id := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Clip","privacy":"public"}`)
	seedReadyHLS(t, env.tc, env.blobs, id)

	rec := postClearKeyLicense(env.srv, id, "", `{"kids":["AAAAAAAAAAAAAAAAAAAAAA"]}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
	}
	// The invisible-video answer must be the same one, so the endpoint reveals
	// nothing about whether DRM is configured at all.
	private := createPublishedVideo(t, env.srv, env.owner, "ada", `{"title":"Secret","privacy":"private"}`)
	if got := postClearKeyLicense(env.srv, private, "", `{"kids":["AAAAAAAAAAAAAAAAAAAAAA"]}`); got.Code != http.StatusNotFound {
		t.Fatalf("private video: status = %d, want 404", got.Code)
	}
}

// TestClearKeyLicenseRouteMatchesProviderPath. The provider decides the license
// URL it advertises (drm.ClearKeyLicensePath) and this package decides the route
// it registers. They are two spellings of one thing, and a drift between them
// would advertise a URL that 404s — visible only to a player mid-playback.
func TestClearKeyLicenseRouteMatchesProviderPath(t *testing.T) {
	id := uuid.New()
	want := strings.Replace(drm.ClearKeyLicensePath(id), id.String(), ":id", 1)

	srv := New(testConfig(), nil, nil, fullRouteOptions()...)
	for _, r := range srv.Handler().Routes() {
		if r.Method == http.MethodPost && r.Path == want {
			return
		}
	}
	t.Fatalf("no POST route registered at %q, which is the license URL the provider advertises", want)
}
