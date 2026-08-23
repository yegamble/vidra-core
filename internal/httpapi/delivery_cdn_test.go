package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/cdn"
	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/video"
)

// These tests exercise the CDN source through the REAL provider (internal/cdn)
// and the real route stack, because the thing worth proving is the seam: that
// wiring a base URL plus flipping one instance setting is the entire act of
// enabling a CDN, and that every authorization fence upstream of the resolver
// still holds when it is on.

const testCDNBase = "https://cdn.example.test/media"

// originalObjectKey is the storage key of a video's original file — the same
// literal shape the presign tests assert on, spelled once here.
func originalObjectKey(id string) string { return "web-videos/" + id + ".mp4" }

// setCDNDelivery flips the runtime toggle the way an admin would.
func setCDNDelivery(t *testing.T, srv *Server, on bool) {
	t.Helper()
	value := "false"
	if on {
		value = "true"
	}
	if err := srv.settingssvc.Apply(context.Background(), map[string]instancesettings.Update{
		instancesettings.KeyDeliveryCDNEnabled: {Value: value},
	}, uuid.Nil); err != nil {
		t.Fatalf("set delivery_cdn_enabled=%s: %v", value, err)
	}
}

// cdnServer builds the video harness with a real internal/cdn provider wired,
// exactly as cmd/api does it — two funcs across the seam, no provider type.
func cdnServer(t *testing.T, on bool, presigner storage.Presigner, presignOn bool, opts ...video.Option) (*Server, storage.Backend, *transcodeFakeRepo) {
	t.Helper()
	provider, err := cdn.New(cdn.Config{BaseURL: testCDNBase}, nil)
	if err != nil {
		t.Fatalf("cdn.New: %v", err)
	}
	httpOpts := []Option{WithDeliveryCDN(provider.EdgeURL, provider.Purge)}
	if presigner != nil {
		httpOpts = append(httpOpts, WithDeliveryPresigner(presigner))
	}
	srv, blobs, tcRepo, _, _ := videoServerFullWith(t, testConfig(), httpOpts, opts...)
	if on {
		setCDNDelivery(t, srv, true)
	}
	if presignOn {
		setPresignedDelivery(t, srv, true)
	}
	return srv, blobs, tcRepo
}

func assertCDNRedirect(t *testing.T, rec *httptest.ResponseRecorder, wantKey string) {
	t.Helper()
	if rec.Code != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want 307; body=%s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Location"), testCDNBase+"/"+wantKey; got != want {
		t.Fatalf("Location = %q, want %q", got, want)
	}
	// The header-promotion guard, asserted where an operator would see it: this
	// change makes Purge real and deliberately promotes nothing to shared
	// caching.
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheCDNRedirect {
		t.Errorf("redirect Cache-Control = %q, want %q", cc, delivery.CacheCDNRedirect)
	}
}

// TestCDNToggleOffServesBytes: with the setting at its default, a wired CDN
// changes nothing at all. Wiring is not enabling.
func TestCDNToggleOffServesBytes(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, false, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/thumbnail", "", ""), "poster-bytes")
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""), "fake-ts-bytes")
}

// TestCDNToggleOnRedirectsPublicMedia is the exit criterion in one test:
// enabling a CDN is a configuration act, not a code change.
func TestCDNToggleOnRedirectsPublicMedia(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	origKey := originalObjectKey(id)
	assertCDNRedirect(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), origKey)

	// And disabling it falls back cleanly, on the very next request.
	setCDNDelivery(t, srv, false)
	assertBytes(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), "video-bytes")
}

// TestCDNBeatsPresignWhenBothAreOn. If the edge can serve the object, the
// signed URL is strictly worse: a per-viewer bearer credential that expires,
// uncacheable at every layer, and metered against object-store egress.
func TestCDNBeatsPresignWhenBothAreOn(t *testing.T) {
	p := &deliveryFakePresigner{}
	srv, blobs, tcRepo := cdnServer(t, true, p, true)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	rec := getWith(srv, "/api/v1/videos/"+id+"/original", "", "")
	assertCDNRedirect(t, rec, originalObjectKey(id))

	// The presigner is still consulted (the resolver builds every source and
	// the caller picks the first), but nothing was served from it. What must
	// never happen is the reverse — a signed URL winning over the edge.
	if loc := rec.Header().Get("Location"); loc == testSignedURLPrefix+originalObjectKey(id) {
		t.Fatal("a presigned URL won over the CDN edge")
	}
}

// TestCDNNeverRedirectsNonPublicMedia. Eligible is public AND published, and it
// is the resolver's only authorization input — so a private video is
// structurally origin-only no matter what is configured.
func TestCDNNeverRedirectsNonPublicMedia(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := createVideo(t, srv, tok, "ada", `{"title":"Secret","privacy":"private"}`)
	if rec := uploadVideoFile(srv, id, "clip.mp4", "video/mp4", "video-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("upload = %d; body=%s", rec.Code, rec.Body.String())
	}
	seedReadyHLS(t, tcRepo, blobs, id)

	// The owner can watch; the bytes come from the origin, never the edge.
	rec := getWith(srv, "/api/v1/videos/"+id+"/original", tok, "")
	if rec.Code == http.StatusTemporaryRedirect {
		t.Fatalf("a private video was redirected to %q", rec.Header().Get("Location"))
	}
	assertBytes(t, rec, "video-bytes")
}

// TestCDNNeverRedirectsAnHLSPlaylist. The origin rewrites the playlist's
// relative URIs; a copy served from the edge points players at URIs that do not
// resolve. A correctness constraint, not a policy one.
func TestCDNNeverRedirectsAnHLSPlaylist(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)

	for _, path := range []string{
		"/api/v1/videos/" + id + "/hls/master.m3u8",
		"/api/v1/videos/" + id + "/hls/240p/index.m3u8",
	} {
		rec := getWith(srv, path, "", "")
		if rec.Code == http.StatusTemporaryRedirect {
			t.Errorf("%s was redirected to %q", path, rec.Header().Get("Location"))
		}
	}
	// A segment, by contrast, is opaque bytes and does go to the edge.
	if rec := getWith(srv, "/api/v1/videos/"+id+"/hls/240p/seg_00000.ts", "", ""); rec.Code != http.StatusTemporaryRedirect {
		t.Errorf("HLS segment status = %d, want a 307 to the edge", rec.Code)
	}
}

// TestCDNPurgeSurfacesItsFailureAndKeepsServing. A CDN with no purge endpoint
// reports that it cannot invalidate — which is the state that must keep every
// byte route private — and delivery is completely unaffected either way.
func TestCDNPurgeSurfacesItsFailureAndKeepsServing(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	key := originalObjectKey(id)

	err := srv.deliverysvc.Purge(context.Background(), key)
	if err == nil {
		t.Fatal("Purge = nil for a CDN with no purge endpoint; that claims an invalidation that never happened")
	}
	if !errors.Is(err, cdn.ErrPurgeNotConfigured) {
		t.Errorf("Purge = %v, want cdn.ErrPurgeNotConfigured", err)
	}
	// Serving is untouched by a failed purge — they are independent paths.
	assertCDNRedirect(t, getWith(srv, "/api/v1/videos/"+id+"/original", "", ""), key)
}

// TestCDNNeverRedirectsACredentialedRequest. Any ?pt= or Authorization header
// marks a request credentialed, and a credentialed response is scoped to one
// caller's authorization — an edge cache is by definition not.
//
// This is also the constraint that bounds the playback session API (item 1):
// minting a media credential for every viewer would turn every request in the
// instance origin-only, silently, with nothing failing. The regression it
// guards against therefore lives here as well as there.
func TestCDNNeverRedirectsACredentialedRequest(t *testing.T) {
	srv, blobs, tcRepo := cdnServer(t, true, nil, false)
	tok := createChannelFor(t, srv, "ada", "ada@example.test", "ada")
	id := publishedPublicVideo(t, srv, blobs, tcRepo, tok)
	path := "/api/v1/videos/" + id + "/original"

	// The same public object, same settings: anonymous goes to the edge.
	assertCDNRedirect(t, getWith(srv, path, "", ""), originalObjectKey(id))

	// With a bearer token it does not, and the response is not stored anywhere.
	rec := getWith(srv, path, tok, "")
	if rec.Code == http.StatusTemporaryRedirect {
		t.Fatalf("a credentialed request was redirected to %q", rec.Header().Get("Location"))
	}
	assertBytes(t, rec, "video-bytes")
	if cc := rec.Header().Get("Cache-Control"); cc != delivery.CacheNoStore {
		t.Errorf("credentialed Cache-Control = %q, want %q", cc, delivery.CacheNoStore)
	}

	// And with a ?pt= playback token, which is the shape Safari native HLS uses.
	rec = getWith(srv, path, "", "some-playback-token")
	if rec.Code == http.StatusTemporaryRedirect {
		t.Fatalf("a ?pt= request was redirected to %q", rec.Header().Get("Location"))
	}
}
