package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/config"
	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/video"
)

// --- harness -----------------------------------------------------------------

const testSignedURLPrefix = "https://objects.example/signed/"

// deliveryFakePresigner stands in for the S3 backend: it implements
// storage.ResponsePresigner (bare Presigner alone is deliberately not enough —
// see internal/delivery) and records what it was asked to sign, so the tests can
// assert that the redirect is header-equivalent to the byte path.
type deliveryFakePresigner struct {
	err   error
	calls []deliveryPresignCall
}

type deliveryPresignCall struct {
	key  string
	ttl  time.Duration
	resp storage.PresignResponse
}

func (p *deliveryFakePresigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	return p.PresignGetAs(ctx, key, ttl, storage.PresignResponse{})
}

func (p *deliveryFakePresigner) PresignGetAs(_ context.Context, key string, ttl time.Duration, resp storage.PresignResponse) (string, error) {
	p.calls = append(p.calls, deliveryPresignCall{key: key, ttl: ttl, resp: resp})
	if p.err != nil {
		return "", p.err
	}
	return testSignedURLPrefix + key, nil
}

// callFor returns the MOST RECENT signing request for key (routes share object
// keys — /original and /download/original sign the same object with different
// response headers, which is the point).
func (p *deliveryFakePresigner) callFor(key string) (deliveryPresignCall, bool) {
	for i := len(p.calls) - 1; i >= 0; i-- {
		if p.calls[i].key == key {
			return p.calls[i], true
		}
	}
	return deliveryPresignCall{}, false
}

func (p *deliveryFakePresigner) reset() { p.calls = nil }

// deliveryPlainPresigner can sign a bare GET but cannot pin response headers.
type deliveryPlainPresigner struct{ calls int }

func (p *deliveryPlainPresigner) PresignGet(_ context.Context, key string, _ time.Duration) (string, error) {
	p.calls++
	return testSignedURLPrefix + key, nil
}

// setPresignedDelivery flips the runtime toggle the way an admin would.
func setPresignedDelivery(t *testing.T, srv *Server, on bool) {
	t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	if err := srv.settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyDeliveryPresignEnabled: {Value: value},
	}, uuid.Nil); err != nil {
		t.Fatalf("set delivery_presign_enabled=%s: %v", value, err)
	}
}

// deliveryServer builds the full video harness with a presigner wired (or not)
// and the toggle in the requested position.
func deliveryServer(t *testing.T, presigner storage.Presigner, on bool, opts ...video.Option) (*Server, storage.Backend, *transcodeFakeRepo) {
	t.Helper()
	cfg := testConfig()
	var httpOpts []Option
	if presigner != nil {
		httpOpts = append(httpOpts, WithDeliveryPresigner(presigner))
	}
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, cfg, httpOpts, opts...)
	if on {
		setPresignedDelivery(t, srv, true)
	}
	return srv, blobs, tcRepo
}

func getWith(srv *Server, path, token, playbackToken string) *httptest.ResponseRecorder {
	if playbackToken != "" {
		sep := "?"
		if strings.ContainsRune(path, '?') {
			sep = "&"
		}
		path += sep + playbackTokenParam + "=" + playbackToken
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func assertSignedRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantKey string) {
	t.Helper()
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), testSignedURLPrefix+wantKey; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// A signed URL is a per-viewer credential: the redirect must never be
	// shared-cacheable, and must expire long before the signature does.
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CachePresignedRedirect {
		t.Errorf("redirect Cache-Control = %q, want %q", cc, delivery.CachePresignedRedirect)
	}
}

func assertBytes(t *testing.T, rec *httptest.ResponseRecorder, want string) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Location") != "" {
		t.Fatalf("bytes expected but got a redirect to %q", rec.Header().Get("Location"))
	}
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

// publishedPublicVideo creates a public+published video with an original, a
// thumbnail, and a ready HLS ladder.
func publishedPublicVideo(t *testing.T, srv *Server, blobs storage.Backend, tcRepo *transcodeFakeRepo, tok string) string {
	t.Helper()
	id := createVideo(t, srv, tok, "ada", `{"title":"Clip","privacy":"public"}`)
	if rec := uploadThumbnail(srv, id, "poster.png", "image/png", "poster-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("thumbnail upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("publish upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	seedReadyHLS(t, tcRepo, blobs, id)
	return id
}

// --- the toggle is the whole switch ------------------------------------------

// TestDeliveryToggleOffServesBytes: with the setting at its default (off), every
// media route behaves exactly as it did before the delivery wave, EVEN with a
// presigning backend wired. Nothing is signed at all.
func TestDeliveryToggleOffServesBytes(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/thumbnail", "", ""), "poster-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/download/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""), "fake-ts-bytes")
	if len(p.calls) != 0 {
		t.Fatalf("presigner called %d times with the toggle off", len(p.calls))
	}
}

// TestDeliveryWithoutPresignerServesBytes: the local filesystem backend cannot
// presign, so the toggle is inert rather than broken.
func TestDeliveryWithoutPresignerServesBytes(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/thumbnail", "", ""), "poster-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""), "fake-ts-bytes")
}

// TestDeliveryPlainPresignerServesBytes: a backend that can sign a bare GET but
// cannot reproduce the API proxy's response headers is never used — a redirect
// that loses Content-Type and the download filename is not the same delivery.
func TestDeliveryPlainPresignerServesBytes(t *testing.T) {
	p := &deliveryPlainPresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/download/original", "", ""), "video-bytes")
	if p.calls != 0 {
		t.Fatalf("bare presigner called %d times", p.calls)
	}
}

// --- the routes that DO redirect ---------------------------------------------

// TestDeliveryPresignsPublicMedia walks every byte route that gains presigned
// delivery, and pins the response-header equivalence for each.
func TestDeliveryPresignsPublicMedia(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	prefix := "streaming-playlists/" + id

	tests := []struct {
		name            string
		path            string
		key             string
		wantContentType string
		wantDisposition string
	}{
		{
			name: "original", path: "/api/v1/videos/" + id + "/original",
			key: "web-videos/" + id + ".mp4", wantContentType: "video/mp4",
		},
		{
			name: "thumbnail", path: "/api/v1/videos/" + id + "/thumbnail",
			key: "thumbnails/" + id + ".jpg", wantContentType: "image/png",
		},
		{
			name: "download original keeps the creator's filename",
			path: "/api/v1/videos/" + id + "/download/original",
			key:  "web-videos/" + id + ".mp4", wantContentType: "video/mp4",
			wantDisposition: `attachment; filename=clip.mp4`,
		},
		{
			name: "download audio", path: "/api/v1/videos/" + id + "/download/audio",
			key:             prefix + "/audio.m4a",
			wantContentType: "audio/mp4",
			wantDisposition: `attachment; filename=video-` + id + `-audio.m4a`,
		},
		{
			name: "canonical HLS segment", path: "/api/v1/videos/" + id + "/hls/240p/seg_00000.ts",
			key: prefix + "/240p/seg_00000.ts", wantContentType: "video/mp2t",
		},
		{
			name: "canonical HLS trick-play media", path: "/api/v1/videos/" + id + "/hls/240p/iframe.ts",
			key: prefix + "/240p/iframe.ts", wantContentType: "video/mp2t",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.reset()
			rec := getWith(srv, tt.path, "", "")
			assertSignedRedirect(t, rec, tt.key)
			call, ok := p.callFor(tt.key)
			if !ok {
				t.Fatalf("no presign call for %q; calls=%+v", tt.key, p.calls)
			}
			if call.ttl != delivery.PresignTTL {
				t.Errorf("ttl = %s, want %s", call.ttl, delivery.PresignTTL)
			}
			if call.resp.ContentType != tt.wantContentType {
				t.Errorf("signed content type = %q, want %q", call.resp.ContentType, tt.wantContentType)
			}
			if call.resp.ContentDisposition != tt.wantDisposition {
				t.Errorf("signed disposition = %q, want %q", call.resp.ContentDisposition, tt.wantDisposition)
			}
			// The bytes behind the signature must not be shared-cacheable either.
			if call.resp.CacheControl != delivery.CachePresignedRedirect {
				t.Errorf("signed cache-control = %q, want %q", call.resp.CacheControl, delivery.CachePresignedRedirect)
			}
		})
	}
}

// TestDeliveryPresignsPeerTubeHLSSegment covers the imported-ladder file shape,
// which reaches storage through the flat "peertube" compatibility route.
func TestDeliveryPresignsPeerTubeHLSSegment(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createPublishedVideo(t, srv, tok, "ada", `{"title":"Imported","privacy":"public"}`)
	seedReadyPeerTubeHLS(t, tcRepo, blobs, id)

	key := "streaming-playlists/hls/11111111-1111-1111-1111-111111111111/v1-720-fragmented.mp4"
	rec := getWith(srv, "/api/v1/videos/"+id+"/hls/peertube/v1-720-fragmented.mp4", "", "")
	assertSignedRedirect(t, rec, key)
	call, ok := p.callFor(key)
	if !ok {
		t.Fatalf("no presign call for %q; calls=%+v", key, p.calls)
	}
	if call.resp.ContentType != contentTypeMP4 {
		t.Errorf("signed content type = %q, want %q", call.resp.ContentType, contentTypeMP4)
	}
}

// TestDeliveryPresignsIdentityAndPlaylistImages covers the routes with no video
// row behind them.
func TestDeliveryPresignsIdentityAndPlaylistImages(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, _, _ := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "cover-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("cover upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertSignedRedirect(t, getWith(srv, "/api/v1/playlists/"+pl.ID+"/thumbnail", "", ""),
		"playlist-thumbnails/"+pl.ID+".jpg")

	// A PRIVATE playlist's cover is owner-only and never leaves the origin.
	priv := createPlaylist(t, srv, tok, `{"title":"Secret","visibility":"private"}`)
	if rec := uploadPlaylistThumbnail(srv, priv.ID, "cover.jpg", "secret-cover", tok); rec.Code != http.StatusCreated {
		t.Fatalf("private cover upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertBytes(t, getWith(srv, "/api/v1/playlists/"+priv.ID+"/thumbnail", tok, ""), "secret-cover")
}

func TestDeliveryPresignsProfileImages(t *testing.T) {
	p := &deliveryFakePresigner{}
	cfg := testConfig()
	srv := profileImageServerWith(t, cfg, WithDeliveryPresigner(p),
		WithSettingsService(newPresignEnabledSettings(t, cfg)))
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := uploadImage(srv, "/api/v1/me/avatar", "me.png", "user-avatar", tok); rec.Code != http.StatusCreated {
		t.Fatalf("avatar upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	assertSignedRedirect(t, getPath(srv, "/api/v1/users/"+userID+"/avatar"), "avatars/users/"+userID+".png")
	call, _ := p.callFor("avatars/users/" + userID + ".png")
	if call.resp.ContentType != "image/png" {
		t.Errorf("signed content type = %q, want image/png", call.resp.ContentType)
	}
}

// newPresignEnabledSettings builds a settings overlay with the delivery toggle
// already on (the profile-image harness has no admin route to PATCH).
func newPresignEnabledSettings(t *testing.T, cfg *config.Config) *instancesettings.Service {
	t.Helper()
	svc := instancesettings.NewService(newInstanceSettingsFakeRepo(), settingsDefaultsFromConfig(cfg))
	if err := svc.Load(context.Background()); err != nil {
		t.Fatalf("settings load: %v", err)
	}
	if err := svc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyDeliveryPresignEnabled: {Value: "true"},
	}, uuid.Nil); err != nil {
		t.Fatalf("enable delivery_presign_enabled: %v", err)
	}
	return svc
}

// --- the walls presigning must never cross -----------------------------------

// TestDeliveryNeverPresignsGatedMedia is the privacy fence. Every case here is
// a request the API is willing to answer with bytes, and none of them may be
// answered with a transferable signed URL.
func TestDeliveryNeverPresignsGatedMedia(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")

	// A password-protected video, played with a valid unlock token.
	pwID := createPasswordVideo(t, srv, tok, "ada", "Locked", "hunter2secret")
	seedReadyHLS(t, tcRepo, blobs, pwID)
	pt := unlockToken(t, srv, pwID, "hunter2secret")

	// A private video, fetched by its owner.
	privID := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, privID, "clip.mp4", "video/mp4", "private-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("private upload = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A public but UNPUBLISHED (draft) video, fetched by its owner: the row is
	// public, the bytes are not yet.
	draftID := createVideo(t, srv, tok, "ada", `{"title":"Draft","privacy":"public"}`)
	if rec := uploadThumbnail(srv, draftID, "poster.png", "image/png", "draft-poster", tok); rec.Code != http.StatusCreated {
		t.Fatalf("draft thumbnail = %d; body=%s", rec.Code, rec.Body.String())
	}

	// A fully public video, for the Authorization-header and HLS-playlist cases.
	pubID := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(pubID)])

	tests := []struct {
		name          string
		path          string
		token         string
		playbackToken string
		wantBody      string
		wantCache     string
	}{
		{
			name: "password video segment with a valid playback token",
			path: "/api/v1/videos/" + pwID + "/hls/240p/seg_00000.ts", playbackToken: pt,
			wantBody: "fake-ts-bytes", wantCache: delivery.CacheNoStore,
		},
		{
			name: "password video original with a valid playback token",
			path: "/api/v1/videos/" + pwID + "/original", playbackToken: pt,
			wantBody: "tiny", wantCache: delivery.CacheNoStore,
		},
		{
			name: "private video original on the owner path",
			path: "/api/v1/videos/" + privID + "/original", token: tok,
			wantBody: "private-bytes", wantCache: delivery.CacheNoStore,
		},
		{
			name: "unpublished public video thumbnail on the owner path",
			path: "/api/v1/videos/" + draftID + "/thumbnail", token: tok,
			wantBody: "draft-poster", wantCache: delivery.CacheNoStore,
		},
		{
			name: "public media requested with an Authorization header",
			path: "/api/v1/videos/" + pubID + "/original", token: tok,
			wantBody: "video-bytes", wantCache: delivery.CacheNoStore,
		},
		{
			name:     "HLS master playlist is never redirected",
			path:     "/api/v1/videos/" + pubID + "/hls/master.m3u8?v=" + version,
			wantBody: "", wantCache: delivery.CacheVersionedImmutable,
		},
		{
			name:     "HLS variant playlist is never redirected",
			path:     "/api/v1/videos/" + pubID + "/hls/240p/playlist.m3u8?v=" + version,
			wantBody: "", wantCache: delivery.CacheVersionedImmutable,
		},
		{
			name:     "storyboard VTT is never redirected",
			path:     "/api/v1/videos/" + pubID + "/storyboard.vtt",
			wantBody: "", wantCache: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(p.calls)
			rec := getWith(srv, tt.path, tt.token, tt.playbackToken)
			if rec.Code == http.StatusTemporaryRedirect {
				t.Fatalf("redirected to %q; this request must stay on the API byte path", rec.Header().Get("Location"))
			}
			if len(p.calls) != before {
				t.Fatalf("presigner called for %q: %+v", tt.path, p.calls[before:])
			}
			if tt.wantBody != "" {
				assertBytes(t, rec, tt.wantBody)
			}
			if tt.wantCache != "" {
				if cc := rec.Header().Get("Cache-Control"); cc != tt.wantCache {
					t.Errorf("Cache-Control = %q, want %q", cc, tt.wantCache)
				}
			}
		})
	}
}

// TestDeliveryNeverPresignsWhenDownloadsAreOff: a moderator may still download
// while the instance gate is shut, but that authorization is theirs alone — a
// signed URL is transferable, so the gate closes the redirect too.
func TestDeliveryNeverPresignsWhenDownloadsAreOff(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	// Baseline: downloads on, anonymous download redirects.
	assertSignedRedirect(t, getWith(srv, "/api/v1/videos/"+id+"/download/original", "", ""), "web-videos/"+id+".mp4")

	if err := srv.settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyDownloadsEnabled: {Value: "false"},
	}, uuid.Nil); err != nil {
		t.Fatalf("disable downloads: %v", err)
	}
	// The anonymous download is now refused outright, and nothing was signed.
	p.reset()
	if rec := getWith(srv, "/api/v1/videos/"+id+"/download/original", "", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("anonymous download with downloads off = %d, want 403", rec.Code)
	}
	if len(p.calls) != 0 {
		t.Fatalf("presigner called for a refused download: %+v", p.calls)
	}

	// A privileged caller (the harness's first account is the instance owner)
	// still gets the BYTES — their gate bypass is intact — but never a
	// transferable signed URL. Two independent fences agree here: the request
	// carries an Authorization header, and publicDownload sees the instance gate
	// shut. The second is the one that matters, because it is what keeps a
	// cookie-authenticated moderator from being handed a shareable URL either.
	p.reset()
	rec := getWith(srv, "/api/v1/videos/"+id+"/download/original", tok, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("privileged download with downloads off = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if len(p.calls) != 0 {
		t.Fatalf("presigner called for a gate-bypassing download: %+v", p.calls)
	}
}

// TestDeliveryPresignErrorFallsBackToBytes: a signing failure is a delivery
// optimisation that did not happen, never a failed media request.
func TestDeliveryPresignErrorFallsBackToBytes(t *testing.T) {
	p := &deliveryFakePresigner{err: errors.New("credentials rejected")}
	srv, blobs, tcRepo := deliveryServer(t, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/thumbnail", "", ""), "poster-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""), "fake-ts-bytes")
	if len(p.calls) == 0 {
		t.Fatal("the presigner was never tried")
	}
	// The failing URL must never appear anywhere in the response.
	if rec := getWith(srv, "/api/v1/videos/"+id+"/original", "", ""); strings.Contains(rec.Body.String(), testSignedURLPrefix) {
		t.Error("response body leaks a signed URL")
	}
}

// --- cache-header policy ------------------------------------------------------

// TestMediaCacheHeaderPolicy pins the header every media route emits on the
// byte path. Before this wave, /original, /webm, /download/*, /captions/{lang},
// /storyboard.*, avatars, banners and playlist covers set NO Cache-Control at
// all — a stored media response with no directives is at the mercy of whatever
// heuristic the next cache applies.
func TestMediaCacheHeaderPolicy(t *testing.T) {
	srv, blobs, tcRepo := deliveryServer(t, nil, false, video.WithStoryboarder(storyboarderStub{}))
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	if rec := uploadCaption(srv, id, "en", "English", sampleVTT, tok, true); rec.Code != http.StatusCreated {
		t.Fatalf("caption upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	pl := createPlaylist(t, srv, tok, `{"title":"Faves","visibility":"public"}`)
	if rec := uploadPlaylistThumbnail(srv, pl.ID, "cover.jpg", "cover-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("cover upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	version := hlsCacheVersion(tcRepo.playlists[uuid.MustParse(id)])

	tests := []struct {
		name string
		path string
		want string
	}{
		{"original", "/api/v1/videos/" + id + "/original", delivery.CacheLongLived},
		{"download original", "/api/v1/videos/" + id + "/download/original", delivery.CacheLongLived},
		{"download hls rendition", "/api/v1/videos/" + id + "/download/hls/240", delivery.CacheLongLived},
		{"download audio", "/api/v1/videos/" + id + "/download/audio", delivery.CacheLongLived},
		{"thumbnail", "/api/v1/videos/" + id + "/thumbnail", delivery.CacheShortLived},
		{"storyboard vtt", "/api/v1/videos/" + id + "/storyboard.vtt", delivery.CacheShortLived},
		{"caption", "/api/v1/videos/" + id + "/captions/en", delivery.CacheShortLived},
		{"download subtitle", "/api/v1/videos/" + id + "/download/subtitles/en", delivery.CacheShortLived},
		{"playlist cover", "/api/v1/playlists/" + pl.ID + "/thumbnail", delivery.CacheShortLived},
		{"hls segment (versioned)", "/api/v1/videos/" + id + "/hls/240p/seg_00000.ts?v=" + version, delivery.CacheVersionedImmutable},
		{"hls segment (stable)", "/api/v1/videos/" + id + "/hls/240p/seg_00000.ts", delivery.CacheStableRevalidate},
		{"hls playlist (versioned)", "/api/v1/videos/" + id + "/hls/master.m3u8?v=" + version, delivery.CacheVersionedImmutable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := getWith(srv, tt.path, "", "")
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
			}
			if cc := rec.Header().Get("Cache-Control"); cc != tt.want {
				t.Errorf("Cache-Control = %q, want %q", cc, tt.want)
			}
			if strings.HasPrefix(rec.Header().Get("Cache-Control"), "public") {
				t.Errorf("Cache-Control = %q; media byte responses are never shared-cacheable",
					rec.Header().Get("Cache-Control"))
			}
		})
	}

	// An authenticated request for the same objects is never retained.
	for _, path := range []string{
		"/api/v1/videos/" + id + "/original",
		"/api/v1/videos/" + id + "/thumbnail",
		"/api/v1/videos/" + id + "/download/original",
		"/api/v1/videos/" + id + "/captions/en",
	} {
		rec := getWith(srv, path, tok, "")
		if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheNoStore {
			t.Errorf("%s authenticated Cache-Control = %q, want %q", path, cc, delivery.CacheNoStore)
		}
	}
}

// TestIdentityImageCacheHeaders covers the avatar/banner routes, which live on a
// harness of their own.
func TestIdentityImageCacheHeaders(t *testing.T) {
	srv := profileImageServerWith(t, testConfig())
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	for _, kind := range []string{"avatar", "banner"} {
		if rec := uploadImage(srv, "/api/v1/me/"+kind, "me.png", kind+"-bytes", tok); rec.Code != http.StatusCreated {
			t.Fatalf("%s upload = %d; body=%s", kind, rec.Code, rec.Body.String())
		}
		rec := getPath(srv, "/api/v1/users/"+userID+"/"+kind)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s = %d", kind, rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheShortLived {
			t.Errorf("%s Cache-Control = %q, want %q", kind, cc, delivery.CacheShortLived)
		}
	}
}
