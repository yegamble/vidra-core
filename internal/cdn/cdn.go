// Package cdn is the provider behind delivery.SourceCDN: it turns a storage
// object key into an edge URL, and invalidates that object at the edge.
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
//   - BaseURL — a public base under which the CDN serves this instance's
//     stored objects AT THEIR OWN KEYS. The edge URL of an object is exactly
//     BaseURL + "/" + objectKey, with each key segment percent-encoded.
//   - a purge request — a method, a URL template and at most one auth header,
//     which is what every CDN's single-URL invalidation API reduces to.
//
// # What the CDN's origin has to be
//
// The resolver is per-object-key (it has no video, no route and no manifest),
// so the edge is addressed by key. That fixes the supported topology: the CDN's
// ORIGIN must be key-addressed — the object store bucket, or a static server
// rooted at the media directory. Pointing BaseURL at the Vidra API origin does
// not work and cannot be made to work here: the API addresses media by route
// (/api/v1/videos/{id}/hls/...), not by storage key, so every edge request
// would 404. There is no runtime check for this — a 404 from a third-party
// edge is indistinguishable from a cold cache — so it is stated here, in
// .env.example and in docs/operations.md, and the resolver's fail-open
// discipline is what keeps a wrong answer from becoming a broken instance:
// a viewer follows a 307 to an edge that 404s and the operator sees it
// immediately, on the first request, rather than as corrupted state.
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

// ErrInvalidObjectKey rejects a key that must never be turned into a URL: empty,
// absolute, dot-segmented, or carrying control characters. A "../" key escaping
// the edge base is the same class of bug storage.ErrInvalidKey exists for, and
// this package validates it independently rather than trusting the caller —
// the caller here is a delivery resolver whose whole contract is to be handed
// keys from elsewhere.
var ErrInvalidObjectKey = errors.New("cdn: invalid object key")

// Config is the operator's whole description of a CDN.
type Config struct {
	// BaseURL is the public edge base, e.g. https://cdn.example.com or
	// https://cdn.example.com/media. Empty means no CDN: New returns a nil
	// provider and the resolver gets no CDN source at all.
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
	//	{key}         the percent-encoded object key, no leading slash
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

// EdgeURL has the shape of delivery.CDNLookup: it maps an object key to the URL
// a viewer should fetch it from.
//
// It never consults the network. An edge URL is a pure function of the base and
// the key — the CDN either has the object cached or pulls it from its origin,
// and asking it in advance would put a synchronous third-party round trip on
// every media request in exchange for an answer that is stale by the time it
// arrives. ok=false is reserved for "this provider cannot address that key".
func (p *Provider) EdgeURL(_ context.Context, objectKey string) (string, bool, error) {
	if p == nil {
		return "", false, nil
	}
	escaped, err := escapeKey(objectKey)
	if err != nil {
		return "", false, err
	}
	return p.base + "/" + escaped, true, nil
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
//     being asserted is "no stale copy of this object survives at the edge",
//     and "there was never one" satisfies it exactly.
//   - THE RESPONSE BODY NEVER APPEARS IN THE ERROR. Neither does the request
//     URL, which is why the transport error is unwrapped out of its *url.Error
//     before being reported. A purge template is operator-supplied and some
//     APIs want the credential in the query string; an error that echoed either
//     would put it in the logs of a system whose whole point is that it does
//     not hold credentials. Callers get a status code, which is what they can
//     act on anyway.
func (p *Provider) Purge(ctx context.Context, objectKey string) error {
	if p == nil {
		return nil
	}
	if p.purgeURL == "" {
		return ErrPurgeNotConfigured
	}
	escaped, err := escapeKey(objectKey)
	if err != nil {
		return err
	}
	edge := p.base + "/" + escaped
	target := strings.NewReplacer(
		"{url_encoded}", url.QueryEscape(edge),
		"{url}", edge,
		"{key}", escaped,
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

// escapeKey validates a storage key and percent-encodes it segment by segment.
//
// Segment by segment rather than whole, because url.PathEscape escapes "/" —
// the separator has to survive. The rejections mirror storage.ErrInvalidKey's:
// empty, absolute, any "." or ".." segment (a key that walks out of the edge
// base addresses somebody else's object), an empty segment (a "//" that some
// origins collapse and others do not), and control characters including NUL.
func escapeKey(objectKey string) (string, error) {
	if objectKey == "" || strings.HasPrefix(objectKey, "/") {
		return "", fmt.Errorf("%w: %q", ErrInvalidObjectKey, objectKey)
	}
	for _, r := range objectKey {
		if r < 0x20 || r == 0x7f {
			return "", fmt.Errorf("%w: control character in key", ErrInvalidObjectKey)
		}
	}
	segments := strings.Split(objectKey, "/")
	for i, seg := range segments {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrInvalidObjectKey, objectKey)
		}
		segments[i] = url.PathEscape(seg)
	}
	return strings.Join(segments, "/"), nil
}
