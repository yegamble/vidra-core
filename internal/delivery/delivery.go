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
//   - **Purge existed from day one**, before any CDN did. A shared cache that
//     cannot be invalidated must not be allowed to hold media whose
//     authorization can change (a privacy flip, a deletion, a takedown), so the
//     hook had to be part of the interface before the first `public` cache
//     header is ever emitted for a byte payload. It now has a real
//     implementation behind it (internal/cdn); the header promotion it gates
//     is still deliberately unmade, because that gate is Purge being EXERCISED,
//     not Purge merely existing.
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
	// SourceCDN is a CDN edge URL: the operator's cache, in front of THIS API,
	// addressed by the same media route path the api-proxy serves. The provider
	// is wired as opaque funcs (CDNLookup/CDNPurge) so no vendor reaches this
	// package.
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
	// CacheCDNRedirect is the CDN-edge redirect's own cache policy, and it is
	// PRIVATE deliberately — which is NOT the obvious answer, so the reasoning
	// is written down rather than inferred.
	//
	// Unlike a presigned URL the edge URL is no credential: it is stable,
	// unsigned, and identical for every viewer, so a shared cache holding
	// "this application URL → that edge URL" would leak nothing. What stops it
	// is sequencing, not secrecy. Promoting a media response from private to
	// shared is the step that lets a cache entry outlive the authorization
	// decision that produced it, and the gate on that step has always been a
	// working Purge (risks.md §6) — real AND exercised, not merely implemented.
	// This change makes Purge real. Nothing yet calls it, so nothing here moves
	// to `public`; that is one separate, reversible change on top of a purge
	// path an operator has actually fired.
	//
	// Five revalidated minutes matches the mirror redirect's window, so an
	// operator flipping delivery_cdn_enabled off during an incident sees the
	// last viewer stop following the edge within minutes rather than an hour.
	CacheCDNRedirect = CacheShortLived
)

// The SHARED cache policies. Each is its private sibling above with `private`
// replaced by `public`, and nothing else — the windows are deliberately
// identical so "why is this cached for an hour?" keeps one answer.
//
// THEY ARE EMITTED ON EXACTLY ONE KIND OF REQUEST: one that arrived through
// the operator's own edge (Request.FromEdge — see EdgeOriginParam) for an
// object the caller has already declared servable to an anonymous public
// visitor. That is not a shortcut for "public media is public"; it is the
// whole safety argument. A shared cache entry can outlive the authorization
// decision that produced it, and the only shared cache Vidra can invalidate is
// the one it hands out edge URLs for. A viewer talking to the origin directly,
// or any intermediary between them, still gets the `private` policy, because
// nothing here could purge such a cache if the video went private a second
// later (docs/productionization/risks.md section 6).
const (
	CacheSharedVersionedImmutable = "public, max-age=31536000, immutable"
	CacheSharedStableRevalidate   = "public, max-age=0, must-revalidate"
	CacheSharedShortLived         = "public, max-age=300, must-revalidate"
	CacheSharedLongLived          = "public, max-age=3600, must-revalidate"
)

// EdgeOriginParam is the query parameter the API mints into every CDN edge URL
// it hands a viewer, and reads back to recognise the edge's own origin fetch.
//
// WHY A MINTED PARAMETER IS THE MECHANISM. With the api as the CDN's origin,
// the edge fetches the SAME route a viewer does, so the api must be able to
// tell the two apart or it answers the edge's origin fetch with a redirect
// back to the edge — a loop, not a cache. Three candidates were considered and
// this is the smallest that is honest:
//
//   - A TRUSTED HEADER the operator configures at the CDN. It works, but the
//     failure mode of forgetting it IS the redirect loop, and it makes the
//     feature unusable on any edge that cannot add an origin header.
//   - THE Host HEADER, matched against a configured edge hostname. Some CDNs
//     preserve the edge Host to the origin and some send the origin's own, so
//     it is a signal that silently is not there on half the providers.
//   - THIS: the api puts the marker in the URL it mints, so the edge carries
//     it to the origin as part of the request it was asked for. It needs no
//     operator configuration, it cannot be forgotten, and it cannot loop —
//     every URL the api hands the edge already identifies itself.
//
// IT IS NOT A CREDENTIAL AND MUST NEVER BECOME ONE. Anyone may send it. All it
// can do is decline the redirect and serve the authoritative bytes, through
// exactly the same authorization every media route already runs — which is
// what an unauthenticated caller can already force today by attaching any
// Authorization header at all. It buys the sender nothing and it is checked
// for nothing.
//
// It also carries a second property worth naming: because the marker is part
// of the URL, the edge's origin request and the viewer's request are DIFFERENT
// URLs. The shared cache policy above is therefore attached to a URL only the
// edge ever asks for, with no Vary and no chance of a shared entry being
// served to a request that should have had a private one.
const (
	EdgeOriginParam = "__vidra_edge"
	EdgeOriginValue = "1"
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
	// ObjectKey is the storage key, relative and opaque (migration 0008). It is
	// what the API PROXY opens and what a presigned URL signs. It is no longer
	// what a CDN edge is addressed by — see Path.
	ObjectKey string
	// Path is this request's own media route path with its query, exactly as
	// the client sent it ("/api/v1/videos/<id>/hls/cmaf/chunk-0-00001.m4s?v=…").
	//
	// It is the CDN's unit of addressing, because the CDN's origin is THIS API.
	// The edge URL is the operator's base plus this path, so the edge fetches
	// the same route the api-proxy serves and every answer it caches was
	// produced by the api: authorization, privacy, Cache-Control, Content-Type
	// and Content-Disposition are decided in one place, and the object store
	// stays private. It is also the unit of PURGE, for the same reason — an
	// invalidation has to name the URL the edge actually holds.
	Path string
	// FromEdge reports that this request IS the edge's origin fetch, recognised
	// by EdgeOriginParam. Such a request is never answered with a redirect of
	// any kind (the edge is the cache; sending it onward is a loop at worst and
	// a defeated cache at best) and is the only request that may be answered
	// with a shared cache policy.
	FromEdge bool
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
	// Purge invalidates every shared-cache copy of one media URL — mediaPath
	// is a media route path with its query, the same shape Request.Path
	// carries, because that is what the edge holds an entry under now that the
	// CDN's origin is this API. Callers name the URL, not the object behind it:
	// one object is reachable at more than one route (an original is both
	// /original and /download/original) and each is a separate cache entry.
	//
	// It is the precondition for promoting a byte response from private to
	// shared caching, which CacheControl now does for edge origin fetches.
	//
	// It is deliberately NOT gated on the CDN kill switch. Turning delivery off
	// stops handing viewers edge URLs; it does not evict what the edge already
	// holds, and an incident in which an operator has just switched the CDN off
	// is exactly when a purge has to still work.
	//
	// With no CDN configured this returns nil, because there genuinely is no
	// shared copy to invalidate. With a CDN configured but no purge endpoint it
	// returns an error, because there is one and it cannot be reached — a state
	// that must be loud rather than silently indistinguishable from success.
	Purge(ctx context.Context, mediaPath string) error
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
// shared promotes the answer from `private` to `public` at the same window, and
// it is true for EXACTLY ONE caller: an origin fetch from the operator's own
// edge (Request.FromEdge) for an object the route has already declared publicly
// servable. Every other request — a viewer talking to the origin directly
// included — still gets `private`. Media routes are authorization gates and a
// shared cache entry can outlive the decision that produced it, so the only
// shared cache allowed to hold one is the cache Vidra can invalidate
// (docs/productionization/risks.md §6, whose gate was a Purge that is real AND
// exercised: internal/httpapi/media_purge.go wires it, and the A32/A33
// acceptance run fired it against a real caching edge).
func CacheControl(class Class, versioned, credentialed, shared bool) string {
	if credentialed {
		// A credential outranks everything, including shared: a response scoped
		// to one caller's authorization must not be stored anywhere at all.
		return CacheNoStore
	}
	switch class {
	case ClassHLSPlaylist, ClassHLSSegment:
		if versioned {
			if shared {
				return CacheSharedVersionedImmutable
			}
			return CacheVersionedImmutable
		}
		if shared {
			return CacheSharedStableRevalidate
		}
		return CacheStableRevalidate
	case ClassOriginal, ClassWebM, ClassAudio, ClassDownload:
		if shared {
			return CacheSharedLongLived
		}
		return CacheLongLived
	default:
		if shared {
			return CacheSharedShortLived
		}
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
//
// AN API-ORIGINED EDGE DOES NOT DISSOLVE EITHER OF THEM, which is worth saying
// because it looks as though it should: the edge caches the api's own rewritten
// bytes at the api's own path, so the relative references would in fact
// resolve. The playlist stays here anyway, for a reason the object-store origin
// never had — the master and variant playlists are the GENERATION SWITCH. They
// are the one URL whose bytes must change the instant a new transcode
// generation is promoted, and an edge entry for them is the one entry that
// would keep players walking into the previous generation. Serving them from
// the origin is what makes the switch atomic.
func Redirectable(class Class) bool {
	switch class {
	case ClassHLSPlaylist, ClassStoryboardVTT:
		return false
	default:
		return true
	}
}
