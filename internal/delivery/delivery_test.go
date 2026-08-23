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

// TestPurgeIsInertWithoutACDN: with nothing configured there is provably no
// shared copy of anything, so nil is the honest answer and stays the answer.
func TestPurgeIsInertWithoutACDN(t *testing.T) {
	if err := New().Purge(context.Background(), "web-videos/x.mp4"); err != nil {
		t.Fatalf("Purge = %v, want nil", err)
	}
	if err := New(WithMirror(func(context.Context, string, string) (string, bool, error) {
		return "https://gateway.example/ipfs/bafy", true, nil
	}, nil)).Purge(context.Background(), "thumbnails/x.jpg"); err != nil {
		t.Fatalf("Purge with only a mirror = %v, want nil (an IPFS gateway is not a purgeable shared cache)", err)
	}
}

// ---------------------------------------------------------------------------
// CDN source (phase-4 delivery item 2)
// ---------------------------------------------------------------------------

const testEdgeURL = "https://cdn.example.com/streaming-playlists/x/240p/seg_00000.ts"

func edgeOK(context.Context, string) (string, bool, error) { return testEdgeURL, true, nil }

// TestCDNSourceFences walks every way a CDN source is and is not offered. Each
// row is a fence that, if it stopped holding, would put bytes somewhere they
// must not be — or would drop a source an operator paid for.
func TestCDNSourceFences(t *testing.T) {
	on := func() bool { return true }
	off := func() bool { return false }
	edgeMiss := func(context.Context, string) (string, bool, error) { return "", false, nil }
	edgeErr := func(context.Context, string) (string, bool, error) {
		return "", false, errors.New("provider unreachable")
	}
	edgeEmpty := func(context.Context, string) (string, bool, error) { return "", true, nil }

	segment := Request{
		ObjectKey:   "streaming-playlists/x/240p/seg_00000.ts",
		Class:       ClassHLSSegment,
		Eligible:    true,
		ContentType: "video/mp2t",
	}
	withReq := func(mut func(*Request)) Request {
		r := segment
		mut(&r)
		return r
	}

	cases := []struct {
		name    string
		edge    CDNLookup
		enabled func() bool
		req     Request
		want    []SourceKind
	}{
		{
			name: "configured, enabled and eligible: the edge is chosen",
			edge: edgeOK, enabled: on, req: segment,
			want: []SourceKind{SourceCDN, SourceAPIProxy},
		},
		{
			// The kill switch. Off must mean off on the very next request, with
			// no restart and no other behaviour change.
			name: "kill switch off: no edge, api-proxy serves",
			edge: edgeOK, enabled: off, req: segment,
			want: []SourceKind{SourceAPIProxy},
		},
		{
			// Eligible is the ONLY authorization input this package has. A CDN
			// can front public+published media and nothing else, ever.
			name: "not eligible: never redirected to an edge",
			edge: edgeOK, enabled: on,
			req:  withReq(func(r *Request) { r.Eligible = false }),
			want: []SourceKind{SourceAPIProxy},
		},
		{
			// A ?pt= or Authorization request is scoped to one caller's
			// authorization; an edge cache is by definition not.
			name: "credentialed request: never redirected to an edge",
			edge: edgeOK, enabled: on,
			req:  withReq(func(r *Request) { r.Credentialed = true }),
			want: []SourceKind{SourceAPIProxy},
		},
		{
			// A playlist served from the edge points players at URIs the origin
			// was going to rewrite. Correctness, not policy.
			name: "HLS playlists are never handed to an edge",
			edge: edgeOK, enabled: on,
			req: withReq(func(r *Request) {
				r.Class = ClassHLSPlaylist
				r.ObjectKey = "streaming-playlists/x/master.m3u8"
			}),
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name: "storyboard VTT is never handed to an edge",
			edge: edgeOK, enabled: on,
			req: withReq(func(r *Request) {
				r.Class = ClassStoryboardVTT
				r.ObjectKey = "storyboards/x.vtt"
			}),
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name: "no object key: nothing to address at the edge",
			edge: edgeOK, enabled: on,
			req:  withReq(func(r *Request) { r.ObjectKey = "" }),
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name: "provider says it cannot serve this key",
			edge: edgeMiss, enabled: on, req: segment,
			want: []SourceKind{SourceAPIProxy},
		},
		{
			// Fail-open: a broken provider is never a failed media request.
			name: "provider error falls open to the authoritative path",
			edge: edgeErr, enabled: on, req: segment,
			want: []SourceKind{SourceAPIProxy},
		},
		{
			// ok=true with an empty URL would put a 307 to "" on the wire.
			name: "ok with an empty URL is not a source",
			edge: edgeEmpty, enabled: on, req: segment,
			want: []SourceKind{SourceAPIProxy},
		},
		{
			name: "nil enabled means always on",
			edge: edgeOK, enabled: nil, req: segment,
			want: []SourceKind{SourceCDN, SourceAPIProxy},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := New(WithCDN(tc.edge, nil, tc.enabled)).Resolve(context.Background(), tc.req)
			if !sameKinds(kinds(got), tc.want...) {
				t.Fatalf("sources = %v, want %v", kinds(got), tc.want)
			}
			// The structural guarantee holds on every row, including the ones
			// where the edge won.
			if got[len(got)-1].Kind != SourceAPIProxy {
				t.Fatalf("last source = %q, want api-proxy", got[len(got)-1].Kind)
			}
			for _, s := range got {
				if s.Kind == SourceCDN && s.URL != testEdgeURL {
					t.Errorf("cdn source URL = %q, want %q", s.URL, testEdgeURL)
				}
			}
		})
	}
}

// TestCDNSourceOrdering pins where the edge sits among the others. Both
// neighbours are arguments, not taste — see Resolve's doc comment — so a
// reordering has to break a test that says why.
func TestCDNSourceOrdering(t *testing.T) {
	on := func() bool { return true }
	mirror := func(context.Context, string, string) (string, bool, error) {
		return "https://gateway.example/ipfs/bafy", true, nil
	}
	presigner := &stubResponsePresigner{stubPresigner: stubPresigner{url: "https://s3.example/o"}}

	got := kinds(New(
		WithMirror(mirror, on),
		WithCDN(edgeOK, nil, on),
		WithPresign(presigner, time.Minute, on),
	).Resolve(context.Background(), Request{
		ObjectKey:   "thumbnails/x.jpg",
		Class:       ClassThumbnail,
		Eligible:    true,
		MirrorClass: "thumbnail",
		ContentType: "image/jpeg",
	}))
	// Mirror first (it shipped first and has its own master switch), edge
	// second, presign last of the optional three: a presigned URL is a
	// per-viewer bearer credential that expires, so if the edge can serve the
	// same object the signed URL is strictly the worse of the two.
	want := []SourceKind{SourceIPFSGateway, SourceCDN, SourcePresigned, SourceAPIProxy}
	if !sameKinds(got, want...) {
		t.Fatalf("sources = %v, want %v", got, want)
	}
	if presigner.call != 1 {
		t.Errorf("presigner called %d times; the resolver builds every source, the CALLER picks", presigner.call)
	}
}

// TestCDNRedirectStaysPrivate is the header-promotion guard. This change makes
// Purge real; it deliberately does NOT make anything shared-cacheable, and the
// mirror redirect must stay the only `public` value in the system.
func TestCDNRedirectStaysPrivate(t *testing.T) {
	src := New(WithCDN(edgeOK, nil, nil)).Resolve(context.Background(), Request{
		ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true,
	})
	if src[0].Kind != SourceCDN {
		t.Fatalf("first source = %q, want cdn", src[0].Kind)
	}
	if src[0].CacheControl != CacheCDNRedirect {
		t.Errorf("cdn redirect cache-control = %q, want %q", src[0].CacheControl, CacheCDNRedirect)
	}
	if !strings.HasPrefix(CacheCDNRedirect, "private") {
		t.Errorf("CacheCDNRedirect = %q; promoting a byte route to shared caching is a separate change, gated on Purge being EXERCISED", CacheCDNRedirect)
	}
	if CacheMirrorRedirect == CacheCDNRedirect {
		t.Error("the IPFS mirror redirect must remain the one public policy; the CDN redirect must not have joined it")
	}
}

// TestPurgeReachesTheProvider: the hook is no longer inert. The key arrives
// verbatim, and both outcomes propagate — a purge that quietly swallowed a
// failure would report "nothing stale survives" when something does.
func TestPurgeReachesTheProvider(t *testing.T) {
	var got []string
	purge := func(_ context.Context, key string) error {
		got = append(got, key)
		return nil
	}
	res := New(WithCDN(edgeOK, purge, func() bool { return true }))
	if err := res.Purge(context.Background(), "web-videos/x.mp4"); err != nil {
		t.Fatalf("Purge = %v, want nil", err)
	}
	if len(got) != 1 || got[0] != "web-videos/x.mp4" {
		t.Fatalf("provider saw %v, want one call with the object key", got)
	}

	boom := errors.New("edge rejected the purge")
	failing := New(WithCDN(edgeOK, func(context.Context, string) error { return boom }, nil))
	if err := failing.Purge(context.Background(), "web-videos/x.mp4"); !errors.Is(err, boom) {
		t.Fatalf("Purge = %v, want the provider error", err)
	}
}

// TestPurgeIgnoresTheKillSwitch: turning delivery off stops handing viewers
// edge URLs. It evicts nothing — and an incident in which an operator has just
// switched the CDN off is exactly when a purge still has to work.
func TestPurgeIgnoresTheKillSwitch(t *testing.T) {
	called := 0
	res := New(WithCDN(edgeOK, func(context.Context, string) error { called++; return nil },
		func() bool { return false }))
	if got := kinds(res.Resolve(context.Background(), Request{
		ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true,
	})); !sameKinds(got, SourceAPIProxy) {
		t.Fatalf("with the switch off sources = %v, want api-proxy only", got)
	}
	if err := res.Purge(context.Background(), "web-videos/x.mp4"); err != nil || called != 1 {
		t.Fatalf("Purge = %v after %d provider calls; want nil after 1", err, called)
	}
}

// TestPurgeWithoutAnInvalidationPath is the honesty case. A configured CDN with
// no purge endpoint has a DIFFERENT postcondition from no CDN at all: with no
// CDN there is provably no shared copy, and here there may well be one. Nil
// would tell the eventual header-promotion caller that it is safe to go shared.
func TestPurgeWithoutAnInvalidationPath(t *testing.T) {
	res := New(WithCDN(edgeOK, nil, nil))
	if err := res.Purge(context.Background(), "web-videos/x.mp4"); err == nil {
		t.Fatal("Purge = nil for a CDN with no purge hook; that claims an invalidation that never happened")
	}
	// And an empty key must never be rendered into a purge template: at the
	// root of most templates it addresses the whole zone.
	if err := res.Purge(context.Background(), ""); err == nil {
		t.Fatal("Purge(\"\") = nil; an empty key is not a purge of nothing, it is a purge of everything")
	}
}

// TestPurgeFailureDoesNotBreakServing: purge and Resolve are independent paths.
// A CDN that rejects every invalidation still delivers bytes, and a purge that
// panicked or errored must not leave the resolver unable to answer — serving is
// the job that must never fail.
func TestPurgeFailureDoesNotBreakServing(t *testing.T) {
	res := New(WithCDN(edgeOK, func(context.Context, string) error {
		return errors.New("purge API returned 500")
	}, func() bool { return true }))
	req := Request{ObjectKey: "web-videos/x.mp4", Class: ClassOriginal, Eligible: true}

	for i := range 3 {
		if err := res.Purge(context.Background(), req.ObjectKey); err == nil {
			t.Fatalf("purge %d: want an error", i)
		}
		got := res.Resolve(context.Background(), req)
		if !sameKinds(kinds(got), SourceCDN, SourceAPIProxy) {
			t.Fatalf("after a failed purge sources = %v, want cdn then api-proxy", kinds(got))
		}
		if got[len(got)-1].Kind != SourceAPIProxy {
			t.Fatalf("after a failed purge the authoritative source is gone: %v", kinds(got))
		}
	}
}
