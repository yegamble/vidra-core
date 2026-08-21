package delivery

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// stubPresigner implements ONLY storage.Presigner — the "can sign a bare GET
// but cannot pin response headers" shape a future backend could have.
type stubPresigner struct {
	url  string
	err  error
	call int
}

func (p *stubPresigner) PresignGet(_ context.Context, key string, ttl time.Duration) (string, error) {
	p.call++
	if p.err != nil {
		return "", p.err
	}
	return p.url + "?key=" + key + "&ttl=" + ttl.String(), nil
}

// stubResponsePresigner implements storage.ResponsePresigner, like the S3
// backend does.
type stubResponsePresigner struct {
	stubPresigner
	got storage.PresignResponse
}

func (p *stubResponsePresigner) PresignGetAs(ctx context.Context, key string, ttl time.Duration, resp storage.PresignResponse) (string, error) {
	p.got = resp
	return p.stubPresigner.PresignGet(ctx, key, ttl)
}

func kinds(sources []Source) []SourceKind {
	out := make([]SourceKind, 0, len(sources))
	for _, s := range sources {
		out = append(out, s.Kind)
	}
	return out
}

func sameKinds(got []SourceKind, want ...SourceKind) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestResolveAlwaysEndsWithAPIProxy is the structural guarantee: whatever else
// happens, the authoritative source is present and last.
func TestResolveAlwaysEndsWithAPIProxy(t *testing.T) {
	mirror := func(context.Context, string, string) (string, bool, error) {
		return "https://gateway.example/ipfs/bafy", true, nil
	}
	on := func() bool { return true }
	cases := []struct {
		name string
		res  Resolver
		req  Request
	}{
		{"bare resolver", New(), Request{ObjectKey: "k", Class: ClassOriginal, Eligible: true}},
		{
			"every source available",
			New(WithMirror(mirror, on), WithPresign(&stubResponsePresigner{stubPresigner: stubPresigner{url: "https://s3.example/o"}}, time.Minute, on)),
			Request{ObjectKey: "k", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail", ContentType: "image/jpeg"},
		},
		{
			"presign broken",
			New(WithPresign(&stubResponsePresigner{stubPresigner: stubPresigner{err: errors.New("boom")}}, time.Minute, on)),
			Request{ObjectKey: "k", Class: ClassOriginal, Eligible: true, ContentType: "video/mp4"},
		},
		{"ineligible", New(WithMirror(mirror, on)), Request{ObjectKey: "k", Class: ClassOriginal}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.res.Resolve(context.Background(), tc.req)
			if len(got) == 0 {
				t.Fatal("Resolve returned no sources")
			}
			last := got[len(got)-1]
			if last.Kind != SourceAPIProxy {
				t.Fatalf("last source = %q, want api-proxy (kinds=%v)", last.Kind, kinds(got))
			}
			if last.URL != "" {
				t.Errorf("api-proxy source carries URL %q, want empty", last.URL)
			}
			for _, s := range got[:len(got)-1] {
				if s.URL == "" {
					t.Errorf("%s source has no URL", s.Kind)
				}
			}
		})
	}
}

// TestResolveSourceOrderAndFences walks the eligibility matrix.
func TestResolveSourceOrderAndFences(t *testing.T) {
	const gatewayURL = "https://gateway.example/ipfs/bafy"
	mirrorOK := func(context.Context, string, string) (string, bool, error) { return gatewayURL, true, nil }
	mirrorMiss := func(context.Context, string, string) (string, bool, error) { return "", false, nil }
	mirrorErr := func(context.Context, string, string) (string, bool, error) {
		return "", false, errors.New("ledger unavailable")
	}
	on := func() bool { return true }
	off := func() bool { return false }

	cases := []struct {
		name    string
		mirror  MirrorLookup
		mirrorU func() bool
		presign bool
		presigU func() bool
		req     Request
		want    []SourceKind
	}{
		{
			name:   "public asset with both sources: mirror first, presign second",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "thumbnails/x.jpg", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail", ContentType: "image/jpeg"},
			want: []SourceKind{SourceIPFSGateway, SourcePresigned, SourceAPIProxy},
		},
		{
			name:   "mirror master switch off leaves presign",
			mirror: mirrorOK, mirrorU: off, presign: true, presigU: on,
			req:  Request{ObjectKey: "thumbnails/x.jpg", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail", ContentType: "image/jpeg"},
			want: []SourceKind{SourcePresigned, SourceAPIProxy},
		},
		{
			name:   "presign toggle off leaves the mirror",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: off,
			req:  Request{ObjectKey: "thumbnails/x.jpg", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail", ContentType: "image/jpeg"},
			want: []SourceKind{SourceIPFSGateway, SourceAPIProxy},
		},
		{
			name:   "not eligible: neither optional source is consulted",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, ContentType: "video/mp4"},
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name:   "credentialed request is never presigned but may still mirror",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "avatars/users/x.png", Class: ClassAvatar, Eligible: true, Credentialed: true, MirrorClass: "user_avatar", ContentType: "image/png"},
			want: []SourceKind{SourceIPFSGateway, SourceAPIProxy},
		},
		{
			name:   "no mirror class means no gateway lookup at all",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "streaming-playlists/x/240p/seg_00000.ts", Class: ClassHLSSegment, Eligible: true, ContentType: "video/mp2t"},
			want: []SourceKind{SourcePresigned, SourceAPIProxy},
		},
		{
			name:   "HLS playlists are never redirected by anything",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "streaming-playlists/x/master.m3u8", Class: ClassHLSPlaylist, Eligible: true, MirrorClass: "hls", ContentType: "application/vnd.apple.mpegurl"},
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name:   "storyboard VTT is never redirected (relative cue references)",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{ObjectKey: "storyboards/x.vtt", Class: ClassStoryboardVTT, Eligible: true, MirrorClass: "storyboard_vtt", ContentType: "text/vtt"},
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name:   "unpinned object falls through the mirror",
			mirror: mirrorMiss, mirrorU: on, presign: false,
			req:  Request{ObjectKey: "thumbnails/x.jpg", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail"},
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name:   "ledger error falls through the mirror",
			mirror: mirrorErr, mirrorU: on, presign: false,
			req:  Request{ObjectKey: "thumbnails/x.jpg", Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail"},
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name:   "no object key: nothing to deliver from anywhere",
			mirror: mirrorOK, mirrorU: on, presign: true, presigU: on,
			req:  Request{Class: ClassThumbnail, Eligible: true, MirrorClass: "thumbnail"},
			want: []SourceKind{SourceAPIProxy},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := []Option{WithMirror(tc.mirror, tc.mirrorU)}
			if tc.presign {
				opts = append(opts, WithPresign(
					&stubResponsePresigner{stubPresigner: stubPresigner{url: "https://s3.example/o"}},
					time.Minute, tc.presigU))
			}
			got := kinds(New(opts...).Resolve(context.Background(), tc.req))
			if !sameKinds(got, tc.want...) {
				t.Fatalf("sources = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPresignCarriesResponseHeaderOverrides proves the redirect is
// header-equivalent to the byte path: the content type, the attachment
// filename, and a PRIVATE cache policy all ride the signed URL.
func TestPresignCarriesResponseHeaderOverrides(t *testing.T) {
	p := &stubResponsePresigner{stubPresigner: stubPresigner{url: "https://s3.example/o"}}
	res := New(WithPresign(p, 2*time.Hour, nil))
	sources := res.Resolve(context.Background(), Request{
		ObjectKey:          "web-videos/x.mp4",
		Class:              ClassDownload,
		Eligible:           true,
		ContentType:        "video/mp4",
		ContentDisposition: `attachment; filename="My Holiday.mp4"`,
	})
	if !sameKinds(kinds(sources), SourcePresigned, SourceAPIProxy) {
		t.Fatalf("sources = %v", kinds(sources))
	}
	if p.got.ContentType != "video/mp4" {
		t.Errorf("response content type = %q, want video/mp4", p.got.ContentType)
	}
	if p.got.ContentDisposition != `attachment; filename="My Holiday.mp4"` {
		t.Errorf("response disposition = %q, want the download filename", p.got.ContentDisposition)
	}
	if p.got.CacheControl != CachePresignedRedirect {
		t.Errorf("response cache-control = %q, want %q", p.got.CacheControl, CachePresignedRedirect)
	}
	if sources[0].CacheControl != CachePresignedRedirect {
		t.Errorf("redirect cache-control = %q, want %q", sources[0].CacheControl, CachePresignedRedirect)
	}
}

// TestPresignDeclinesWithoutResponseOverrides: a backend that can sign a bare
// GET but cannot pin the response headers is never used for delivery. Every
// delivery redirect pins headers — at minimum a PRIVATE cache policy on the
// object response itself, so the signed bytes cannot be picked up out of a
// shared cache — and a URL that cannot carry them is a different resource, not
// a faster one.
func TestPresignDeclinesWithoutResponseOverrides(t *testing.T) {
	p := &stubPresigner{url: "https://s3.example/o"}
	res := New(WithPresign(p, time.Minute, nil))
	for _, req := range []Request{
		{ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true, ContentType: "video/mp4"},
		// Even with nothing else to reproduce, the cache policy alone is enough
		// to require the richer capability.
		{ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true},
	} {
		if got := kinds(res.Resolve(context.Background(), req)); !sameKinds(got, SourceAPIProxy) {
			t.Fatalf("sources = %v, want api-proxy only", got)
		}
	}
	if p.call != 0 {
		t.Errorf("bare PresignGet called %d times; a header-losing URL must never be minted", p.call)
	}
}

// TestPresignHelperUsesBareGetWhenNothingToOverride keeps the fallback path
// honest at the level it exists: asked for no overrides at all, presign uses
// the plain capability rather than requiring the richer one.
func TestPresignHelperUsesBareGetWhenNothingToOverride(t *testing.T) {
	p := &stubPresigner{url: "https://s3.example/o"}
	u, err := presign(context.Background(), p, "k", time.Minute, storage.PresignResponse{})
	if err != nil || p.call != 1 || !strings.Contains(u, "key=k") {
		t.Fatalf("presign = (%q, %v), calls=%d", u, err, p.call)
	}
}

// TestPresignErrorFallsOpen: a signing failure is never a failed media request.
func TestPresignErrorFallsOpen(t *testing.T) {
	p := &stubResponsePresigner{stubPresigner: stubPresigner{err: errors.New("credentials rejected")}}
	got := kinds(New(WithPresign(p, time.Minute, nil)).Resolve(context.Background(), Request{
		ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true, ContentType: "video/mp4",
	}))
	if !sameKinds(got, SourceAPIProxy) {
		t.Fatalf("sources = %v, want api-proxy only", got)
	}
}

// TestPresignTTLDefaultAndOverride pins the one-hour default and proves the
// redirect's own max-age stays comfortably inside it (a redirect cached past
// the signature's expiry sends the viewer to a 403).
func TestPresignTTLDefaultAndOverride(t *testing.T) {
	p := &stubResponsePresigner{stubPresigner: stubPresigner{url: "https://s3.example/o"}}
	src := New(WithPresign(p, 0, nil)).Resolve(context.Background(), Request{
		ObjectKey: "k", Class: ClassOriginal, Eligible: true,
	})
	if want := "ttl=" + PresignTTL.String(); !strings.Contains(src[0].URL, want) {
		t.Errorf("URL %q does not carry the default TTL %s", src[0].URL, PresignTTL)
	}
	if PresignTTL != time.Hour {
		t.Errorf("PresignTTL = %s, want 1h", PresignTTL)
	}
	if CachePresignedRedirect != CacheShortLived {
		t.Errorf("presigned redirect cache policy = %q; it must stay far below the %s signature TTL",
			CachePresignedRedirect, PresignTTL)
	}
}

// TestCacheControlPolicy is the cache-header policy table.
func TestCacheControlPolicy(t *testing.T) {
	cases := []struct {
		class        Class
		versioned    bool
		credentialed bool
		want         string
	}{
		{ClassHLSPlaylist, true, false, CacheVersionedImmutable},
		{ClassHLSPlaylist, false, false, CacheStableRevalidate},
		{ClassHLSSegment, true, false, CacheVersionedImmutable},
		{ClassHLSSegment, false, false, CacheStableRevalidate},
		{ClassOriginal, false, false, CacheLongLived},
		{ClassWebM, false, false, CacheLongLived},
		{ClassAudio, false, false, CacheLongLived},
		{ClassDownload, false, false, CacheLongLived},
		{ClassThumbnail, false, false, CacheShortLived},
		{ClassStoryboard, false, false, CacheShortLived},
		{ClassStoryboardVTT, false, false, CacheShortLived},
		{ClassCaption, false, false, CacheShortLived},
		{ClassAvatar, false, false, CacheShortLived},
		{ClassBanner, false, false, CacheShortLived},
		{ClassPlaylistCover, false, false, CacheShortLived},
		// A credential on the request beats every class rule, including the
		// immutable one.
		{ClassHLSSegment, true, true, CacheNoStore},
		{ClassOriginal, false, true, CacheNoStore},
		{ClassThumbnail, false, true, CacheNoStore},
		{ClassDownload, false, true, CacheNoStore},
	}
	for _, tc := range cases {
		got := CacheControl(tc.class, tc.versioned, tc.credentialed)
		if got != tc.want {
			t.Errorf("CacheControl(%s, versioned=%v, credentialed=%v) = %q, want %q",
				tc.class, tc.versioned, tc.credentialed, got, tc.want)
		}
	}
	// Nothing served as bytes is ever shared-cacheable: only the IPFS redirect,
	// which carries no credential and points at an immutable CID, is public.
	for _, class := range []Class{
		ClassHLSPlaylist, ClassHLSSegment, ClassOriginal, ClassWebM, ClassAudio, ClassDownload,
		ClassThumbnail, ClassStoryboard, ClassStoryboardVTT, ClassCaption, ClassAvatar,
		ClassBanner, ClassPlaylistCover,
	} {
		for _, versioned := range []bool{false, true} {
			if cc := CacheControl(class, versioned, false); !strings.HasPrefix(cc, "private") {
				t.Errorf("CacheControl(%s, %v, false) = %q, want a private policy", class, versioned, cc)
			}
		}
	}
}

// TestPurgeIsWiredAndInert: the hook exists on day one and does nothing yet.
func TestPurgeIsWiredAndInert(t *testing.T) {
	if err := New().Purge(context.Background(), "web-videos/x.mp4"); err != nil {
		t.Fatalf("Purge = %v, want nil", err)
	}
}
