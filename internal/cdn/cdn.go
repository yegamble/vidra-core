// Package cdn is the provider behind delivery.SourceCDN: it turns one of this
// API's own media route paths into an edge URL, and invalidates that URL at the
// edge.
//
// It is a package of its own for a structural reason rather than a tidiness
// one. internal/delivery imports context, errors, log/slog, time and
// internal/storage and NOTHING else, and that import list is the enforcement of
// phase-4-delivery.md item 2's "no CDN vendor in core media logic". Everything
// that knows about base URLs, HTTP, purge endpoints and auth headers lives
// here; it reaches the resolver only as the two opaque func types
// delivery.CDNLookup and delivery.CDNPurge. That is the same separation
// internal/ipfsmirror already has from the gateway source.
//
// VENDOR-NEUTRAL IS A CLAIM ABOUT THE CODE, NOT A SLOGAN. There is no
// per-provider branch anywhere below, and no provider is named in an
// identifier. A CDN is described entirely by operator configuration:
//
//   - BaseURL — a public base under which the CDN serves THIS API's media
//     routes. The edge URL of a media request is exactly BaseURL + the request's
//     own path and query, plus the delivery.EdgeOriginParam marker.
//   - a purge request — a method, a URL template and at most one auth header,
//     which is what every CDN's single-URL invalidation API reduces to.
//
// # What the CDN's origin has to be
//
// THE CDN'S ORIGIN IS THE VIDRA API. Not the bucket, not a static server over
// the media directory: the api host, serving the same /api/v1/videos/... routes
// a viewer would reach directly. That is a reversal of what this package
// required before, and the reasons are four measured defects of the
// key-addressed model rather than a preference (docs/release-readiness.md,
// "A32/A33 delivery — presigned S3 and CDN edge simulator"):
//
//   - A key-addressed origin has to be readable by the edge, and granting that
//     the obvious way — a public-read bucket policy — makes EVERY object in the
//     store world-readable, private videos included, because public and private
//     media share web-videos/, thumbnails/ and streaming-playlists/ and no key
//     prefix separates them. A correct purge was then undone by the very next
//     request, which re-pulled the object from an origin still serving it. With
//     the api as origin, every miss and every revalidation runs the route's own
//     authorization, and the bucket needs no public policy at all.
//   - A stored object carries no Content-Type, no Content-Disposition and no
//     Cache-Control, so an edge in front of the bucket answered
//     application/octet-stream with no cache policy and lost the creator's
//     filename on official downloads. The api sets all three.
//   - The ?v= generation tag lives in the URL's query, which a key-addressed
//     edge URL threw away — so a re-transcode's new generation arrived at the
//     edge as the same URL and the edge kept serving the old bytes.
//   - Purge is by URL, and the URL an edge holds an entry under is the one it
//     was asked for.
//
// The operator therefore points the CDN at the api host and lets it forward
// Range and the query string; see docs/operations.md. Nothing here can verify
// it — a 404 from a third-party edge is indistinguishable from a cold cache —
// so the resolver's fail-open discipline is what keeps a wrong answer from
// becoming a broken instance: a viewer follows a 307 to an edge that 404s and
// the operator sees it immediately, on the first request, rather than as
// corrupted state.
//
// # Telling the edge apart from a viewer
//
// With the api as origin, the edge fetches the same route a viewer does, so the
// api has to recognise the edge's own request or it answers it with a redirect
// back to the edge. It does that with delivery.EdgeOriginParam, a marker this
// package mints into every URL it hands out and that the edge carries back to
// the origin as part of the request it was asked for. See that constant for why
// a minted parameter rather than a trusted header or a Host match.
//
// # What the CDN can be handed at all
//
// Only what delivery.Request.Eligible admits: public AND published AND
// uncredentialed media. Private, unlisted, password-gated, scheduled and
// quarantined bytes are structurally origin-only, and CDN-fronted private
// playback needs signed-URLs-at-the-edge, which is a different mechanism.
package cdn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/vidra/vidra-core/internal/delivery"
)

// DefaultPurgeMethod is the method used when the operator names none.
//
// PURGE rather than POST or DELETE because it is the one spelling that is
// approximately universal: it is what Varnish, the nginx cache-purge module and
// the single-URL purge of the common commercial CDNs all accept against the
// asset's own URL, so `DELIVERY_CDN_PURGE_URL={url}` with no method at all is a
// working configuration for most operators. It is not in any RFC; it is a
// convention, which is exactly why it is a default and not a constant.
const DefaultPurgeMethod = "PURGE"

// DefaultPurgeTimeout bounds one purge request. Ten seconds is chosen against
// the failure that matters: a purge is a best-effort side effect of an
// authorization change, and an edge that has stopped answering must not hold
// the request that triggered it open. It is long enough for a cross-region API
// call and far too short to be mistaken for a retry policy.
const DefaultPurgeTimeout = 10 * time.Second

// DefaultPurgeAuthHeader is the header a purge token rides in when the operator
// names no header. Bearer-style APIs want Authorization; the ones that use a
// bespoke header (an API-key header of their own naming) say so via
// Config.PurgeHeader.
const DefaultPurgeAuthHeader = "Authorization"

// ErrPurgeNotConfigured is returned by Purge when a CDN is configured and no
// purge endpoint is. It is an ERROR and not a nil because those two states have
// different postconditions: with no CDN there is provably no shared copy, and
// with an unpurgeable one there may well be. Whatever eventually promotes a
// cache header from private to shared has to be able to tell them apart.
var ErrPurgeNotConfigured = errors.New("cdn: no purge endpoint configured")

// ErrInvalidMediaPath rejects a path that must never be turned into an edge
// URL: empty, not rooted, protocol-relative, dot-segmented (encoded or not),
// carrying a fragment or a control character, or already carrying the edge
// marker. A "../" segment escaping the edge base is the same class of bug
// storage.ErrInvalidKey exists for, and this package validates it independently
// rather than trusting the caller — the caller here is a delivery resolver
// whose whole contract is to be handed paths from elsewhere.
var ErrInvalidMediaPath = errors.New("cdn: invalid media path")

// Config is the operator's whole description of a CDN.
type Config struct {
	// BaseURL is the public edge base, e.g. https://cdn.example.com or
	// https://cdn.example.com/media. Empty means no CDN: New returns a nil
	// provider and the resolver gets no CDN source at all. Its ORIGIN must be
	// this Vidra API — see the package doc.
	BaseURL string
	// PurgeURL is the purge endpoint template. Empty means the CDN cannot be
	// invalidated, which Purge reports rather than hides. Three placeholders are
	// substituted, and between them they spell every single-URL purge API the
	// field actually has:
	//
	//	{url}         the full edge URL, verbatim
	//	              — "PURGE https://cdn.example.com/key" style
	//	{url_encoded} the full edge URL, percent-encoded for a query value
	//	              — "POST https://api.example/purge?url=…" style
	//	{key}         the edge URL's path and query with no leading slash
	//	              — "POST https://api.example/zones/1/purge/…" style
	PurgeURL string
	// PurgeMethod is the HTTP method for the purge request; empty means
	// DefaultPurgeMethod.
	PurgeMethod string
	// PurgeHeader is the name of the header the token rides in; empty with a
	// token set means DefaultPurgeAuthHeader.
	PurgeHeader string
	// PurgeToken is the header's VALUE, verbatim — so a Bearer API is spelled
	// `Bearer <token>` and a bare API-key header is spelled `<token>`. It is a
	// SECRET: held unexported, sent header-only, never logged, never returned
	// in an error, and on the observability.IsSensitiveKey denylist.
	PurgeToken string
	// PurgeTimeout bounds one purge request; <= 0 means DefaultPurgeTimeout.
	PurgeTimeout time.Duration
	// HTTPClient overrides the client used for purge requests (tests).
	HTTPClient *http.Client
}

// Provider implements the two capabilities internal/delivery consumes.
type Provider struct {
	base         string // normalised, no trailing slash
	purgeURL     string
	purgeMethod  string
	purgeHeader  string
	purgeToken   string // SECRET — never logged, never in an error
	purgeTimeout time.Duration
	client       *http.Client
	logger       *slog.Logger
}

// New builds a provider from cfg. An empty BaseURL is not an error and not a
// provider: it returns (nil, nil), which is the "no CDN configured" case every
// install has by default.
func New(cfg Config, logger *slog.Logger) (*Provider, error) {
	base := strings.TrimSpace(cfg.BaseURL)
	if base == "" {
		return nil, nil
	}
	base = strings.TrimRight(base, "/")
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("cdn: base URL %q must be an absolute http(s) URL", cfg.BaseURL)
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("cdn: base URL %q must be a path base with no query or fragment", cfg.BaseURL)
	}
	if logger == nil {
		logger = slog.Default()
	}
	p := &Provider{
		base:         base,
		purgeURL:     strings.TrimSpace(cfg.PurgeURL),
		purgeMethod:  strings.ToUpper(strings.TrimSpace(cfg.PurgeMethod)),
		purgeHeader:  strings.TrimSpace(cfg.PurgeHeader),
		purgeToken:   cfg.PurgeToken,
		purgeTimeout: cfg.PurgeTimeout,
		client:       cfg.HTTPClient,
		logger:       logger,
	}
	if p.purgeMethod == "" {
		p.purgeMethod = DefaultPurgeMethod
	}
	if p.purgeHeader == "" {
		p.purgeHeader = DefaultPurgeAuthHeader
	}
	if p.purgeTimeout <= 0 {
		p.purgeTimeout = DefaultPurgeTimeout
	}
	if p.client == nil {
		p.client = &http.Client{Timeout: p.purgeTimeout}
	}
	return p, nil
}

// EdgeURL has the shape of delivery.CDNLookup: it maps one of this API's media
// route paths to the URL a viewer should fetch it from.
//
// It never consults the network. An edge URL is a pure function of the base and
// the path — the CDN either has that URL cached or pulls it from its origin
// (this api), and asking it in advance would put a synchronous third-party
// round trip on every media request in exchange for an answer that is stale by
// the time it arrives. ok=false is reserved for "this provider cannot address
// that path".
func (p *Provider) EdgeURL(_ context.Context, mediaPath string) (string, bool, error) {
	if p == nil {
		return "", false, nil
	}
	suffix, err := edgeSuffix(mediaPath)
	if err != nil {
		return "", false, err
	}
	return p.base + suffix, true, nil
}

// CanPurge reports whether an invalidation path is configured. It exists so
// cmd/api can say so ONCE at boot, in a log line an operator reads before the
// first takedown rather than during one.
func (p *Provider) CanPurge() bool { return p != nil && p.purgeURL != "" }

// Describe is the boot-log summary: the edge base (public — it is in every
// redirect this instance emits) and whether purge is wired. It never contains
// the token.
func (p *Provider) Describe() string {
	if p == nil {
		return "none"
	}
	if p.CanPurge() {
		return p.base + " (purge " + p.purgeMethod + ")"
	}
	return p.base + " (no purge endpoint)"
}

// Purge has the shape of delivery.CDNPurge: it invalidates one object at the
// edge.
//
// TWO NON-OBVIOUS OUTCOMES, both deliberate:
//
//   - 404 IS SUCCESS. The nginx cache-purge module (and several APIs modelled
//     on it) answers 404 for a URL it holds no entry for. The postcondition
//     being asserted is "no stale copy of this URL survives at the edge", and
//     "there was never one" satisfies it exactly.
//   - THE RESPONSE BODY NEVER APPEARS IN THE ERROR. Neither does the request
//     URL, which is why the transport error is unwrapped out of its *url.Error
//     before being reported. A purge template is operator-supplied and some
//     APIs want the credential in the query string; an error that echoed either
//     would put it in the logs of a system whose whole point is that it does
//     not hold credentials. Callers get a status code, which is what they can
//     act on anyway.
func (p *Provider) Purge(ctx context.Context, mediaPath string) error {
	if p == nil {
		return nil
	}
	if p.purgeURL == "" {
		return ErrPurgeNotConfigured
	}
	suffix, err := edgeSuffix(mediaPath)
	if err != nil {
		return err
	}
	// The URL built here MUST be the one EdgeURL hands a viewer, marker and all
	// — an edge holds its entry under the URL it was asked for, so a purge that
	// differed by a single query parameter would answer 404 (which this package
	// treats as success) while the stale copy stayed exactly where it was.
	edge := p.base + suffix
	target := strings.NewReplacer(
		"{url_encoded}", url.QueryEscape(edge),
		"{url}", edge,
		"{key}", strings.TrimPrefix(suffix, "/"),
	).Replace(p.purgeURL)

	ctx, cancel := context.WithTimeout(ctx, p.purgeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, p.purgeMethod, target, nil)
	if err != nil {
		return fmt.Errorf("cdn: purge request could not be built (check DELIVERY_CDN_PURGE_URL and _METHOD): %w", stripURL(err))
	}
	if p.purgeToken != "" {
		req.Header.Set(p.purgeHeader, p.purgeToken)
	}
	req.Header.Set("Accept", "*/*")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("cdn: purge request failed: %w", stripURL(err))
	}
	defer func() { _ = resp.Body.Close() }()
	// Drain a bounded amount so the connection can be reused. The body is read
	// and discarded, never inspected: a per-vendor success grammar is exactly
	// the vendor coupling this package exists to not have.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300, resp.StatusCode == http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("cdn: purge rejected with status %d", resp.StatusCode)
	}
}

// stripURL removes the request URL from a transport error. net/http wraps every
// failure in a *url.Error carrying the full URL, and this package's URL can
// contain an operator-supplied credential.
func stripURL(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err
	}
	return err
}

// edgeSuffix validates a media route path and returns the suffix appended to the
// edge base: the path, its query, and the edge-origin marker.
//
// It does NOT re-encode. The path it is handed is either a live request's own
// RequestURI (already escaped by the client) or one built from a video id and a
// filename the media routes' own regexes constrain to [A-Za-z0-9._-], so
// escaping it again would turn "%20" into "%2520". What it does instead is
// REFUSE anything that could address something other than the media route it
// claims to be:
//
//   - not rooted, or rooted twice ("//host/x" is protocol-relative and would
//     leave the operator's base entirely);
//   - any "." or ".." segment, encoded or not — the origin decodes before it
//     routes, so "%2e%2e" walks just as far as "..";
//   - an empty segment ("//" mid-path, which some origins collapse and others
//     do not, so the edge and the origin could disagree on the cache key);
//   - a fragment, which cannot appear in a request URI at all;
//   - a control character, including NUL;
//   - the edge marker itself, which would mean the caller is trying to mint an
//     edge URL for a request that already came FROM the edge.
func edgeSuffix(mediaPath string) (string, error) {
	if mediaPath == "" || !strings.HasPrefix(mediaPath, "/") || strings.HasPrefix(mediaPath, "//") {
		return "", fmt.Errorf("%w: %q", ErrInvalidMediaPath, mediaPath)
	}
	for _, r := range mediaPath {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: control character in path", ErrInvalidMediaPath)
		}
	}
	if strings.ContainsRune(mediaPath, '#') {
		return "", fmt.Errorf("%w: %q", ErrInvalidMediaPath, mediaPath)
	}
	rawPath, query, _ := strings.Cut(mediaPath, "?")
	for _, seg := range strings.Split(strings.TrimPrefix(rawPath, "/"), "/") {
		if seg == "" {
			return "", fmt.Errorf("%w: %q", ErrInvalidMediaPath, mediaPath)
		}
		decoded, err := url.PathUnescape(seg)
		if err != nil {
			return "", fmt.Errorf("%w: %q", ErrInvalidMediaPath, mediaPath)
		}
		if decoded == "." || decoded == ".." {
			return "", fmt.Errorf("%w: %q", ErrInvalidMediaPath, mediaPath)
		}
	}
	if hasEdgeMarker(query) {
		return "", fmt.Errorf("%w: already carries the edge-origin marker", ErrInvalidMediaPath)
	}
	marker := delivery.EdgeOriginParam + "=" + delivery.EdgeOriginValue
	if query == "" {
		return rawPath + "?" + marker, nil
	}
	return rawPath + "?" + query + "&" + marker, nil
}

// hasEdgeMarker reports whether a raw query string already carries the marker,
// by NAME: a second value for the same parameter would still be read back as
// the marker by the api, so the name alone is the thing to refuse.
func hasEdgeMarker(rawQuery string) bool {
	for rawQuery != "" {
		var pair string
		pair, rawQuery, _ = strings.Cut(rawQuery, "&")
		name, _, _ := strings.Cut(pair, "=")
		if name == delivery.EdgeOriginParam {
			return true
		}
	}
	return false
}
