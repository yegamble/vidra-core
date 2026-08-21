// Package delivery answers one question for the HTTP layer: for THIS request,
// where should this viewer fetch this stored object from?
//
// It is the delivery half of the storage/delivery split
// (docs/productionization/interfaces.md §4). storage.Backend says where Vidra
// can reliably RECOVER an object; this package says where a viewer should FETCH
// it — the API byte proxy, a presigned object-store URL, an IPFS gateway, or
// (later) a CDN edge. Those are deliberately not one interface: only the API
// proxy re-evaluates authorization on every byte, and pretending otherwise is
// exactly how a cache entry outlives the authorization decision that created it.
//
// Three rules here are structural rather than incidental:
//
//   - **api-proxy is always last and always present.** Every other source is an
//     optimisation that can fail, be misconfigured, or be switched off
//     mid-flight; the authoritative path must never become a dependency of the
//     optional one. This generalises httpapi's fail-open-to-authoritative IPFS
//     redirect: any provider error yields "no source", never an error response.
//   - **Eligibility is the CALLER's answer, not this package's.** Only the
//     handler knows whether the request cleared videoVisibleForMedia, the
//     password gate and the download gates. This package never re-derives
//     authorization and never presigns past a wall it cannot see: Request.
//     Eligible must mean "these bytes are servable to an anonymous public
//     visitor", and nothing else.
//   - **Purge exists from day one**, before any CDN does. A shared cache that
//     cannot be invalidated must not be allowed to hold media whose
//     authorization can change (a privacy flip, a deletion, a takedown), so the
//     hook has to be part of the interface before the first `public` cache
//     header is ever emitted for a byte payload.
package delivery

import "context"

// SourceKind names one way of delivering an object.
type SourceKind string

const (
	// SourceAPIProxy is the authoritative path: the Go API opens the object
	// from the storage backend and streams it, re-checking authorization on
	// every request. Always available, always last.
	SourceAPIProxy SourceKind = "api-proxy"
	// SourcePresigned is a time-limited, signed object-store URL the viewer
	// fetches directly. The URL is a bearer credential for that one object.
	SourcePresigned SourceKind = "presigned"
	// SourceIPFSGateway is the configured public gateway URL for an
	// already-pinned public asset (an immutable CID).
	SourceIPFSGateway SourceKind = "ipfs-gateway"
	// SourceCDN is a CDN edge URL. No provider implements it yet; the constant
	// exists so the ordering contract is written down before one does.
	SourceCDN SourceKind = "cdn"
)

// Class is the kind of media being delivered. It decides two things that must
// not be decided ad hoc per handler: the cache-header policy, and whether the
// object may be delivered by redirect at all.
type Class string

const (
	// ClassHLSPlaylist is an m3u8. NEVER redirected: the origin rewrites its
	// relative URIs (playback-token propagation and the generation version), so
	// a copy served straight from the object store points players at URIs that
	// do not resolve.
	ClassHLSPlaylist Class = "hls-playlist"
	// ClassHLSSegment is one .ts/.mp4/.m4s media object referenced by a
	// playlist. Opaque bytes, so it redirects.
	ClassHLSSegment Class = "hls-segment"
	// ClassOriginal is the stored source file served for playback.
	ClassOriginal Class = "original"
	// ClassWebM is the optional progressive VP9 alternate.
	ClassWebM Class = "webm"
	// ClassAudio is the extracted audio-only download.
	ClassAudio Class = "audio"
	// ClassDownload is an official download (attachment-dispositioned).
	ClassDownload Class = "download"
	// ClassThumbnail is a video poster image.
	ClassThumbnail Class = "thumbnail"
	// ClassStoryboard is a seek-preview sprite sheet.
	ClassStoryboard Class = "storyboard"
	// ClassStoryboardVTT is the sprite sheet's WebVTT map. NEVER redirected,
	// for the same reason as an HLS playlist: its cues address the sprite by
	// the RELATIVE name "storyboard.jpg", which only resolves while the map is
	// served from the application URL next to it.
	ClassStoryboardVTT Class = "storyboard-vtt"
	// ClassCaption is a WebVTT subtitle track.
	ClassCaption Class = "caption"
	// ClassAvatar / ClassBanner are identity images (user or channel).
	ClassAvatar Class = "avatar"
	ClassBanner Class = "banner"
	// ClassPlaylistCover is a playlist's cover image.
	ClassPlaylistCover Class = "playlist-cover"
)

// Cache-control policies. They are constants rather than per-handler literals
// because "why is this response private?" must have exactly one answer per
// class, and because the HLS routes and the new delivery seam have to agree
// byte-for-byte (httpapi/hls.go aliases the two HLS values).
const (
	// CacheNoStore is what any request carrying a credential gets — a playback
	// token in ?pt= or an Authorization header. Such a response is scoped to one
	// caller's authorization and must not survive it anywhere.
	CacheNoStore = "private, no-store"
	// CacheVersionedImmutable is for a generation-versioned URL, which by
	// construction never changes meaning.
	CacheVersionedImmutable = "private, max-age=31536000, immutable"
	// CacheStableRevalidate is for a stable (unversioned) URL whose bytes may be
	// replaced under it.
	CacheStableRevalidate = "private, max-age=0, must-revalidate"
	// CacheShortLived is the page-fan-out image window: replaceable bytes on a
	// stable URL, reused briefly by the viewer's own browser.
	CacheShortLived = "private, max-age=300, must-revalidate"
	// CacheLongLived is for whole-file media (originals, downloads, the audio
	// and webm alternates). Replacing one of these mints a transcode/replace
	// cycle rather than a silent in-place swap, but the URL is still stable, so
	// this stays a revalidated hour rather than an immutable year.
	CacheLongLived = "private, max-age=3600, must-revalidate"

	// CacheMirrorRedirect is the IPFS-gateway redirect's own cache policy. It is
	// the ONE public value in the system: the redirect carries no credential and
	// its target is immutable by CID, while the stable application URL it is
	// answering may point at replacement bytes later — hence the short window.
	CacheMirrorRedirect = "public, max-age=300, must-revalidate"
	// CachePresignedRedirect is the presigned redirect's own cache policy. It is
	// PRIVATE even though the object behind it is public, because the redirect
	// body is a signed URL — a bearer credential for that object, which must not
	// be handed to the next viewer out of a shared cache. Its max-age must also
	// stay far below PresignTTL: a redirect cached past the signature's
	// expiry sends the viewer to a 403 the API can no longer rescue.
	CachePresignedRedirect = CacheShortLived
)

// Source is one place a viewer may fetch the object from. URL is empty for
// SourceAPIProxy (the caller is already serving that route). CacheControl is
// the header the response carrying this source must set.
type Source struct {
	Kind         SourceKind
	URL          string
	CacheControl string
}

// Request describes one media byte-serving request.
type Request struct {
	// ObjectKey is the storage key, relative and opaque (migration 0008).
	ObjectKey string
	// Class decides cache policy and redirect eligibility.
	Class Class
	// Eligible is the caller's assertion that these exact bytes are servable to
	// an anonymous public visitor: the video is public AND published (or the
	// asset is public by nature, like an identity image), it is not
	// password-protected, and every feature gate the route enforces has passed.
	// It is the ONLY authorization input this package has, and the reason a
	// private, unpublished or password-gated object can never be redirected.
	Eligible bool
	// Versioned reports that the request URL carries a generation version, so
	// the response can be cached as immutable.
	Versioned bool
	// Credentialed reports that the request carried a playback token or an
	// Authorization header. Such a request is never redirected to a signed URL
	// (the redirect would put a credential in front of a credential) and its
	// response is never stored.
	Credentialed bool
	// MirrorClass is the peer-mirror media class as an opaque token (the
	// httpapi adapter passes ipfsmirror.MediaClass through it, so this package
	// keeps no dependency on the mirror). Empty means "this route has no mirror
	// concept" and suppresses the gateway source entirely.
	MirrorClass string
	// ContentType and ContentDisposition are the response headers the API proxy
	// sets from the DATABASE. A redirect target must reproduce them or the
	// delivery is not equivalent: Vidra stores objects with no content type at
	// all, and download filenames come from the video row, not the key.
	ContentType        string
	ContentDisposition string
}

// Resolver returns the ordered delivery sources for a request, best first.
type Resolver interface {
	// Resolve returns at least one source; the last is always SourceAPIProxy.
	// It never returns an error: an unreachable or misconfigured optional
	// source degrades to the authoritative one.
	Resolve(ctx context.Context, req Request) []Source
	// Purge invalidates every shared-cache copy of an object. It is the
	// precondition for ever promoting a byte response from private to shared
	// caching, and is wired (as a no-op) before any such promotion exists.
	Purge(ctx context.Context, objectKey string) error
}

// CacheControl is the single cache-header policy for stored media. Every media
// route's header comes from here so the rules live in one place:
//
//	credentialed request   → no-store, whatever the class
//	versioned HLS URL      → immutable for a year
//	stable HLS URL         → revalidate every time
//	whole-file media       → an hour, revalidated
//	everything else        → five minutes, revalidated
//
// Nothing here is ever `public`. Media routes are authorization gates, and a
// shared cache entry can outlive the decision that produced it; promoting any
// of these requires the purge hook above to have a real implementation behind
// it (docs/productionization/risks.md §6).
func CacheControl(class Class, versioned, credentialed bool) string {
	if credentialed {
		return CacheNoStore
	}
	switch class {
	case ClassHLSPlaylist, ClassHLSSegment:
		if versioned {
			return CacheVersionedImmutable
		}
		return CacheStableRevalidate
	case ClassOriginal, ClassWebM, ClassAudio, ClassDownload:
		return CacheLongLived
	default:
		return CacheShortLived
	}
}

// Redirectable reports whether a class may be delivered by redirect at all.
//
// The two exclusions are not policy choices that could be flipped by
// configuration — they are correctness constraints. An HLS playlist and a
// storyboard VTT both carry RELATIVE references that the origin rewrites or
// that resolve against the application URL; served from anywhere else, those
// references point at nothing.
func Redirectable(class Class) bool {
	switch class {
	case ClassHLSPlaylist, ClassStoryboardVTT:
		return false
	default:
		return true
	}
}
