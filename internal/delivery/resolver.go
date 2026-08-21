package delivery

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

// PresignTTL is how long a delivery redirect's signed URL stays valid.
//
// One hour is chosen against two opposing failure modes. Too short and a long
// download or a paused video dies mid-transfer with a 403 the API cannot
// rescue, because the viewer is no longer talking to the API. Too long and the
// URL — which is a bearer credential for that object, guessable-key doctrine or
// not — outlives the authorization that minted it by more than the window in
// which an operator could plausibly react to a privacy flip. An hour also
// bounds the damage of the header this cannot control: a signed URL that lands
// in a viewer's browser history or an intermediary's access log.
const PresignTTL = time.Hour

// MirrorLookup resolves an object key to a public peer-mirror (IPFS gateway)
// URL. ok=false means "not mirrored"; an error means the ledger could not
// answer. Both degrade to the authoritative source.
//
// It is a function rather than an interface so this package keeps no dependency
// on internal/ipfsmirror: the caller passes the mirror's media class through
// Request.MirrorClass as an opaque token and converts it back here.
type MirrorLookup func(ctx context.Context, objectKey, mirrorClass string) (string, bool, error)

// errNoResponseOverrides is the internal "this backend can presign but cannot
// reproduce the response headers the API proxy sets" signal. It is a
// fall-through, not a failure.
var errNoResponseOverrides = errors.New("delivery: backend cannot override presigned response headers")

// chain is the composed resolver: an ordered set of optional source providers
// terminated by the always-present api-proxy source.
type chain struct {
	mirror        MirrorLookup
	mirrorEnabled func() bool

	presigner      storage.Presigner
	presignEnabled func() bool
	presignTTL     time.Duration

	logger *slog.Logger
}

// Option configures New.
type Option func(*chain)

// WithMirror wires the peer-mirror (IPFS gateway) source. enabled is consulted
// per request — it is the master switch, and reading it here rather than at
// construction is what lets an operator turn the mirror off without a restart.
// A nil enabled means always on.
func WithMirror(lookup MirrorLookup, enabled func() bool) Option {
	return func(c *chain) {
		c.mirror = lookup
		c.mirrorEnabled = enabled
	}
}

// WithPresign wires the presigned-URL source.
//
// The presigner MUST be the RAW primary backend, never a storage.Fallback: a
// Fallback is a dual-read view of two stores during a migration, and a URL
// signed against one store for an object that currently lives in the other is a
// 404 the API can no longer fall back from (see storage.Fallback's doc comment
// — it deliberately fails every capability detection for exactly this reason).
// The wiring in cmd/api therefore declines to pass a presigner at all while a
// migration target is configured.
//
// enabled is consulted per request (the runtime admin toggle); a nil enabled
// means always on. ttl <= 0 uses PresignTTL.
func WithPresign(p storage.Presigner, ttl time.Duration, enabled func() bool) Option {
	return func(c *chain) {
		c.presigner = p
		c.presignTTL = ttl
		c.presignEnabled = enabled
	}
}

// WithLogger sets the logger used for the fail-open warnings. A nil logger
// falls back to the default one.
func WithLogger(l *slog.Logger) Option {
	return func(c *chain) { c.logger = l }
}

// New builds the delivery resolver. With no options it is a valid resolver that
// always answers "api-proxy" — which is precisely the behaviour of an install
// with no mirror and no object store, and the behaviour every unit-test server
// gets for free.
func New(opts ...Option) Resolver {
	c := &chain{presignTTL: PresignTTL}
	for _, opt := range opts {
		opt(c)
	}
	if c.logger == nil {
		c.logger = slog.Default()
	}
	if c.presignTTL <= 0 {
		c.presignTTL = PresignTTL
	}
	return c
}

// Resolve returns the ordered sources for req, best first.
//
// The mirror comes before the presigned URL deliberately: an IPFS gateway hit
// is served from an immutable CID and costs the instance neither bandwidth nor
// object-store egress, and it is the behaviour that shipped first — folding
// presign in must not silently demote it.
func (c *chain) Resolve(ctx context.Context, req Request) []Source {
	sources := make([]Source, 0, 3)
	if src, ok := c.mirrorSource(ctx, req); ok {
		sources = append(sources, src)
	}
	if src, ok := c.presignedSource(ctx, req); ok {
		sources = append(sources, src)
	}
	return append(sources, Source{
		Kind:         SourceAPIProxy,
		CacheControl: CacheControl(req.Class, req.Versioned, req.Credentialed),
	})
}

// Purge is the day-one CDN invalidation hook. Nothing implements it yet, and
// nothing may emit a shared-cacheable byte response until something does.
// Returning nil is honest here: with no shared cache configured, there is
// nothing holding a stale copy to invalidate.
func (c *chain) Purge(_ context.Context, _ string) error { return nil }

// mirrorSource asks the pin ledger for a public gateway URL. Every failure mode
// — mirror off, no mirror class for this route, not eligible, lookup error, not
// pinned — returns "no source", so the caller serves the authoritative bytes.
func (c *chain) mirrorSource(ctx context.Context, req Request) (Source, bool) {
	if c.mirror == nil || req.ObjectKey == "" || req.MirrorClass == "" || !req.Eligible {
		return Source{}, false
	}
	if !Redirectable(req.Class) {
		return Source{}, false
	}
	if c.mirrorEnabled != nil && !c.mirrorEnabled() {
		return Source{}, false
	}
	u, ok, err := c.mirror(ctx, req.ObjectKey, req.MirrorClass)
	if err != nil {
		c.logger.WarnContext(ctx, "ipfs asset lookup failed; serving authoritative storage",
			"error", err, "media_class", req.MirrorClass, "object_key", req.ObjectKey)
		return Source{}, false
	}
	if !ok {
		return Source{}, false
	}
	return Source{Kind: SourceIPFSGateway, URL: u, CacheControl: CacheMirrorRedirect}, true
}

// presignedSource mints a signed object-store URL when every fence passes.
//
// The fences, in the order they are cheapest to check: a backend that can
// presign at all (the local backend cannot — it has no HTTP surface), a class
// whose relative references survive being served from elsewhere, the caller's
// public-and-published eligibility assertion, no credential on the request, and
// the runtime toggle. A presign failure is logged WITHOUT the URL (it is a
// credential) and degrades to the authoritative path.
func (c *chain) presignedSource(ctx context.Context, req Request) (Source, bool) {
	if c.presigner == nil || req.ObjectKey == "" {
		return Source{}, false
	}
	if !Redirectable(req.Class) || !req.Eligible || req.Credentialed {
		return Source{}, false
	}
	if c.presignEnabled != nil && !c.presignEnabled() {
		return Source{}, false
	}
	overrides := storage.PresignResponse{
		ContentType:        req.ContentType,
		ContentDisposition: req.ContentDisposition,
		// The object store must not hand the BODY to a shared cache either. The
		// stored objects carry no cache metadata of their own, so without this
		// the redirect target answers with no directives at all.
		CacheControl: CachePresignedRedirect,
	}
	u, err := presign(ctx, c.presigner, req.ObjectKey, c.presignTTL, overrides)
	if err != nil {
		// The key names media and the error may not; log the class only. Never
		// the URL — it is a signed credential.
		c.logger.WarnContext(ctx, "presigned delivery URL unavailable; serving authoritative storage",
			"error", err, "media_class", string(req.Class))
		return Source{}, false
	}
	return Source{Kind: SourcePresigned, URL: u, CacheControl: CachePresignedRedirect}, true
}

// presign picks the richest presign the backend supports.
//
// Response-header overrides are not a nicety here. Vidra uploads objects with
// no Content-Type, and derives download filenames from the video row rather
// than from the key — so a backend that can only sign a bare GET would turn an
// inline MP4 into an octet-stream download and "My Holiday.mp4" into
// "<uuid>.mp4". A redirect that is not header-equivalent to the byte path is
// not a delivery of the same resource, so this declines rather than degrades.
func presign(ctx context.Context, p storage.Presigner, key string, ttl time.Duration, o storage.PresignResponse) (string, error) {
	if o.IsZero() {
		return p.PresignGet(ctx, key, ttl)
	}
	rp, ok := p.(storage.ResponsePresigner)
	if !ok {
		return "", errNoResponseOverrides
	}
	return rp.PresignGetAs(ctx, key, ttl, o)
}
