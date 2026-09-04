package httpapi

import (
	"context"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/delivery"
	"github.com/vidra/vidra-core/internal/instancesettings"
	"github.com/vidra/vidra-core/internal/ipfsmirror"
)

// This file is the ONE seam between the media routes and internal/delivery
// (docs/productionization/interfaces.md §4). Every route that streams a stored
// object goes through serveMediaAsset, which asks the resolver where this
// viewer should fetch the object from and then either redirects or streams the
// authoritative bytes.
//
// What the handlers keep, and must keep, is authorization. The resolver is
// handed one boolean — Eligible — meaning "this request has already cleared
// every gate, and these exact bytes are servable to an anonymous public
// visitor". Nothing downstream re-derives it, so a route that computes it
// wrongly is the only way a private object can be redirected. Each call site
// therefore computes it from the row it already fetched for the auth check
// (videoVisibleForMedia / videoForDownload / the playlist visibility check),
// never from a second lookup.

// mediaAsset describes one stored object a media route is about to serve.
type mediaAsset struct {
	// key is the storage key; contentType the media type the API proxy sets.
	key         string
	contentType string
	class       delivery.Class
	// filename, when set, makes this an attachment download with that name.
	filename string
	// mirrorClass is the IPFS pin-ledger class for this asset, or "" for routes
	// with no mirror concept (HLS segments, originals, downloads). Empty
	// suppresses the gateway source entirely — it is not a lookup that returns
	// nothing, it is a lookup that never happens.
	mirrorClass ipfsmirror.MediaClass
	// eligible is the caller's public-and-published assertion. See above.
	eligible bool
	// versioned reports a generation-versioned request URL (HLS only today).
	versioned bool
	// notFound is the 404 message for this route ("video not found",
	// "playlist not found", "avatar not found", …).
	notFound string
}

// newDeliveryResolver builds the resolver from the seams the options already
// wired. It is constructed once per server, after options are applied, so the
// mirror closure and the presigner reflect the final wiring — and both switches
// it consults (the IPFS master switch, the presign admin toggle) are read per
// request, never baked in here.
func (s *Server) newDeliveryResolver() delivery.Resolver {
	opts := []delivery.Option{
		delivery.WithLogger(s.logger),
		delivery.WithMirror(func(ctx context.Context, objectKey, class string) (string, bool, error) {
			if s.ipfsmirrorsvc == nil {
				return "", false, nil
			}
			return s.ipfsmirrorsvc.PublicAssetURL(ctx, objectKey, ipfsmirror.MediaClass(class))
		}, s.ipfsMirrorEnabled),
	}
	// No CDN configured (no DELIVERY_CDN_BASE_URL — the default) means the CDN
	// source simply does not exist. The purge hook rides along with the edge
	// lookup: they are the same provider, and a resolver that could redirect to
	// an edge it cannot invalidate is precisely the pairing this seam exists to
	// make impossible to assemble by accident.
	if s.mediaCDNEdge != nil {
		opts = append(opts, delivery.WithCDN(s.mediaCDNEdge, s.mediaCDNPurge, s.cdnDeliveryEnabled))
	}
	// No presigner wired (local storage, or a migration fallback is active —
	// see cmd/api) means the presigned source simply does not exist.
	if s.mediaPresigner != nil {
		opts = append(opts, delivery.WithPresign(s.mediaPresigner, delivery.PresignTTL, s.presignedDeliveryEnabled))
	}
	return delivery.New(opts...)
}

// presignedDeliveryEnabled reports the runtime direct-delivery toggle
// (delivery_presign_enabled). Default OFF, and off for every unit-test server
// (which wires no settings service) — so adding the resolver changed nothing
// about how bytes are served until an operator says so.
func (s *Server) presignedDeliveryEnabled() bool {
	return s.settingBool(instancesettings.KeyDeliveryPresignEnabled, false)
}

// cdnDeliveryEnabled reports the runtime CDN toggle (delivery_cdn_enabled).
// Default OFF, off for every unit-test server, and read PER REQUEST — flipping
// it stops the next viewer being sent to the edge without a restart, which is
// the only incident response worth having for a delivery path a third party
// operates.
func (s *Server) cdnDeliveryEnabled() bool {
	return s.settingBool(instancesettings.KeyDeliveryCDNEnabled, false)
}

// credentialedMediaRequest reports whether the request carried a credential: a
// playback token in ?pt= (password-protected media, CORE-17) or an
// Authorization header. Such a response is never stored anywhere and is never
// answered with a signed URL — a redirect would hand out a second, longer-lived
// credential in exchange for the first, and ?pt= tokens must not reach an
// intermediary's access log (risks.md §6).
func credentialedMediaRequest(c echo.Context) bool {
	return c.QueryParam(playbackTokenParam) != "" || c.Request().Header.Get("Authorization") != ""
}

// serveMediaAsset serves one stored object through the delivery resolver.
//
// The loop is the fail-open contract in code: optional sources are tried in
// order and the api-proxy source — always present, always last — ends it by
// streaming the authoritative bytes. A source the resolver could not build
// simply is not in the list, so there is no error path here that could turn an
// optional delivery hop into a failed media request.
func (s *Server) serveMediaAsset(c echo.Context, a mediaAsset) error {
	req := delivery.Request{
		ObjectKey:    a.key,
		Class:        a.class,
		Eligible:     a.eligible,
		Versioned:    a.versioned,
		Credentialed: credentialedMediaRequest(c),
		MirrorClass:  string(a.mirrorClass),
		ContentType:  a.contentType,
	}
	if a.filename != "" {
		req.ContentDisposition = attachmentHeader(a.filename)
	}
	header := c.Response().Header()
	for _, src := range s.deliverysvc.Resolve(c.Request().Context(), req) {
		if src.Kind != delivery.SourceAPIProxy {
			header.Set("Cache-Control", src.CacheControl)
			return c.Redirect(http.StatusTemporaryRedirect, src.URL)
		}
		if src.CacheControl != "" {
			header.Set("Cache-Control", src.CacheControl)
		}
		if req.ContentDisposition != "" {
			header.Set(echo.HeaderContentDisposition, req.ContentDisposition)
		}
		return s.serveStoredObjectNamed(c, a.key, a.contentType, a.notFound)
	}
	// Unreachable: Resolve always terminates with the api-proxy source. Serving
	// the bytes is the only safe interpretation if that contract were ever
	// broken, so say so rather than 500.
	return s.serveStoredObjectNamed(c, a.key, a.contentType, a.notFound)
}

// mediaObjectNotFound is the 404 for a media route whose object is absent from
// the store.
//
// It RESETS the cache policy the route already stamped on the response. Media
// routes set Cache-Control before they open the object, and returning an error
// does not undo a header that is already set — so a versioned HLS URL, whose
// policy is "max-age=31536000, immutable", used to hand the client a 404 under a
// one-year immutable directive. Repairing the missing object then changed
// nothing for any browser that had already cached the failure, which is how a
// transient gap becomes a permanently unplayable video. The BYTES behind a
// versioned URL are immutable; their ABSENCE is not, and must never be cached.
func mediaObjectNotFound(c echo.Context, msg string) error {
	c.Response().Header().Set("Cache-Control", delivery.CacheNoStore)
	return echo.NewHTTPError(http.StatusNotFound, msg)
}

// setMediaCacheControl applies the class cache policy to a route that streams
// its bytes without going through the resolver (the caption routes, which read
// through video.Service and never see a storage key).
func setMediaCacheControl(c echo.Context, class delivery.Class) {
	c.Response().Header().Set("Cache-Control",
		delivery.CacheControl(class, false, credentialedMediaRequest(c)))
}
