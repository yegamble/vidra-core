// Package urlsafety provides SSRF-safe outbound URL handling for vidra-core: any
// feature that fetches a user-supplied URL (video URL import, link previews,
// federation fetches, webhooks) must go through here. It enforces the SSRF rules
// from .ralph/specs/security.md:
//
//   - only http/https schemes (no file://, gopher://, ftp://, data:, …);
//   - never connect to a non-public IP — loopback, private (RFC1918 / ULA),
//     link-local, CGNAT/shared (RFC 6598), multicast, or unspecified;
//   - the IP check runs at DIAL time, per resolved candidate IP, so it also
//     defeats DNS rebinding (a hostname that resolves to a public IP for the
//     pre-flight check but a private IP at connect time) and blocks redirects
//     that land on an internal address;
//   - slow responses are bounded by a timeout: a whole-request deadline for the
//     small RPC-style fetches NewClient serves, or, for DOWNLOADS, the per-read
//     idle timeout plus total budget NewBudgetClient serves — a transfer that is
//     merely slow is a success in progress, and only silence is a failure;
//   - oversized responses are the caller's job to bound (io.LimitReader /
//     http.MaxBytesReader).
package urlsafety

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"syscall"
	"time"
)

// Sentinel errors. Callers can errors.Is on these; they never carry the URL/host
// in a way that would be logged with PII (the caller decides what to log).
var (
	// ErrInvalidURL means the URL is malformed, has no host, carries userinfo,
	// or uses a scheme other than http/https.
	ErrInvalidURL = errors.New("urlsafety: invalid or disallowed URL")
	// ErrBlockedAddress means a connection was attempted to a non-public IP.
	ErrBlockedAddress = errors.New("urlsafety: blocked non-public address")
	// ErrTooManyRedirects means the redirect chain exceeded the limit.
	ErrTooManyRedirects = errors.New("urlsafety: too many redirects")
	// ErrIdleTimeout means no bytes arrived within a budget client's per-read
	// idle timeout — the source went quiet, not merely slow.
	ErrIdleTimeout = errors.New("urlsafety: idle timeout (no data received)")
	// ErrBudgetExceeded means a budget client's TOTAL transfer wall clock ran out
	// while the transfer was still making progress.
	ErrBudgetExceeded = errors.New("urlsafety: total transfer budget exceeded")
)

const maxRedirects = 5

// transportWrapper, when non-nil, wraps the outbound http.RoundTripper of every
// client NewClient builds. It is the seam through which OpenTelemetry client-span
// creation + W3C traceparent injection is added WITHOUT this security-critical
// package importing the OTel SDK (the wrapper is a plain stdlib signature). The
// wrapper only ever decorates the SSRF-guarded transport, so the dial-time IP
// guard and redirect re-validation still run underneath it. Set once at startup
// (see cmd/api wiring) before any outbound fetch; nil leaves clients untouched
// (the zero-cost default when OpenTelemetry is off).
var transportWrapper func(http.RoundTripper) http.RoundTripper

// SetTransportWrapper installs the outbound-transport decorator described on
// transportWrapper. Passing nil clears it. Not safe for concurrent use with
// NewClient; call it once during startup wiring.
func SetTransportWrapper(w func(http.RoundTripper) http.RoundTripper) { transportWrapper = w }

// Guard applies the SSRF policy. The zero value is the secure default: block
// every non-public address. Guard{AllowPrivate: true} relaxes ONLY the
// private/loopback/link-local/CGNAT IP checks — a DEV/TEST escape hatch (e.g.
// importing from a loopback origin in backed e2e), gated by config and never
// enabled in production. Scheme/host/userinfo validation and fail-closed on an
// unparseable IP still apply even when AllowPrivate is set.
type Guard struct {
	AllowPrivate bool
}

// blockedIP applies the guard's policy to a single IP.
func (g Guard) blockedIP(ip net.IP) bool {
	if ip == nil {
		return true // unparseable → fail closed regardless of AllowPrivate
	}
	if g.AllowPrivate {
		return false
	}
	return IsBlockedIP(ip)
}

// ValidateURL parses raw and returns it only if it is a fetchable URL: an
// http/https scheme, a non-empty host, and no embedded credentials. When the host
// is a literal IP, it must pass the guard's policy. Hostnames are NOT resolved
// here — that check happens at dial time (see NewClient), which is what actually
// defeats DNS rebinding; this pre-flight just rejects the obvious cases.
func (g Guard) ValidateURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidURL, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("%w: scheme %q", ErrInvalidURL, u.Scheme)
	}
	if u.User != nil {
		return nil, fmt.Errorf("%w: embedded credentials", ErrInvalidURL)
	}
	host := u.Hostname()
	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidURL)
	}
	if ip := net.ParseIP(host); ip != nil && g.blockedIP(ip) {
		return nil, fmt.Errorf("%w: non-public address", ErrInvalidURL)
	}
	return u, nil
}

// ValidateURL with the secure default policy (block all non-public addresses).
func ValidateURL(raw string) (*url.URL, error) { return Guard{}.ValidateURL(raw) }

// IsBlockedIP reports whether ip is one an outbound fetch must never reach.
// A nil/unparseable IP is treated as blocked (fail closed).
func IsBlockedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	// Not global unicast → loopback, unspecified, link-local (uni/multi), multicast.
	if !ip.IsGlobalUnicast() {
		return true
	}
	// Private ranges: RFC1918 (10/8, 172.16/12, 192.168/16) and ULA (fc00::/7).
	if ip.IsPrivate() {
		return true
	}
	// CGNAT / shared address space, RFC 6598: 100.64.0.0/10.
	if v4 := ip.To4(); v4 != nil && v4[0] == 100 && v4[1]&0xc0 == 0x40 {
		return true
	}
	return false
}

// control is the net.Dialer.Control hook. It runs after DNS resolution, once per
// candidate IP, with address as "ip:port" — so rejecting a blocked IP here is the
// dial-time guard that defeats DNS rebinding.
func (g Guard) control(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: non-IP dial address %q", ErrBlockedAddress, host)
	}
	if g.blockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	return nil
}

// NewClient returns an http.Client for outbound fetches of user-supplied URLs.
// It refuses to connect to a blocked IP at dial time (covering redirects and DNS
// rebinding), never honours proxy environment variables, caps the redirect chain,
// and applies timeout as the overall deadline (bounding slow responses). Callers
// must still bound the response body size themselves.
//
// timeout is the WHOLE-REQUEST deadline, which is the right shape for the small
// RPC-style fetches this helper serves (federation activity pulls, atproto XRPC,
// link previews, the PeerTube migration's bounded image pulls): their responses
// are small, so a response that takes longer than the deadline IS a failure. It
// is the WRONG shape for a download, where a slow-but-progressing transfer is a
// success in progress — see NewBudgetClient.
func (g Guard) NewClient(timeout time.Duration) *http.Client {
	return g.newClient(timeout, 0)
}

// NewClient with the secure default policy (block all non-public addresses).
func NewClient(timeout time.Duration) *http.Client { return Guard{}.NewClient(timeout) }

// Budget bounds an outbound DOWNLOAD in the two ways one actually goes wrong,
// which a single whole-request deadline cannot tell apart.
type Budget struct {
	// Total is the whole-transfer wall clock: headers plus every body byte. It
	// bounds how long one download may hold a worker slot even while it is making
	// honest progress. 0 means no total cap (the house 0-disables convention,
	// cf. YTDLP_MAX_HEIGHT).
	Total time.Duration
	// Idle is the PER-READ timeout: the transfer fails only when no bytes arrive
	// for this long. A drip-feed origin that keeps delivering never trips it, and
	// a hung origin trips it promptly. 0 means no idle cap (not recommended — it
	// leaves a stalled socket bounded only by Total).
	Idle time.Duration
}

// NewBudgetClient returns a download client with the same SSRF policy as
// NewClient — the identical dial-time Control hook, the same nil proxy, the same
// per-redirect re-validation — and download-shaped timeouts:
//
//   - b.Idle is enforced on the CONNECTION, by resetting the read deadline before
//     every read, so it measures silence rather than duration. It therefore also
//     covers the TLS handshake and the wait for response headers. Expiry is
//     reported as ErrIdleTimeout.
//   - b.Total is enforced as a per-request context deadline whose expiry is
//     reported as ErrBudgetExceeded, so a caller can tell "the source went quiet"
//     from "this took longer than the instance allows" and name the knob that
//     fired. It is applied per redirect hop; the chain is capped at maxRedirects.
//
// Callers must still bound the response body SIZE themselves.
func (g Guard) NewBudgetClient(b Budget) *http.Client {
	return g.newClient(0, b.Idle, b.Total)
}

// NewBudgetClient with the secure default policy (block all non-public addresses).
func NewBudgetClient(b Budget) *http.Client { return Guard{}.NewBudgetClient(b) }

// newClient builds the shared guarded client. clientTimeout is http.Client.Timeout
// (the legacy whole-request form; 0 for budget clients); idle is the per-read
// deadline; an optional total installs the budget round-tripper.
func (g Guard) newClient(clientTimeout, idle time.Duration, total ...time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: g.control}
	transport := &http.Transport{
		// The Control hook lives on the dialer, so wrapping the CONN it returns
		// cannot weaken the SSRF guard: the address is already vetted by the time
		// there is a conn to wrap.
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			conn, err := dialer.DialContext(ctx, network, addr)
			if err != nil || idle <= 0 {
				return conn, err
			}
			return &idleConn{Conn: conn, idle: idle}, nil
		},
		Proxy:             nil, // ignore HTTP(S)_PROXY — could route around the guard
		ForceAttemptHTTP2: true,
		MaxIdleConns:      10,
		// A pooled idle conn is read by a background loop, which the idle deadline
		// will trip; that just evicts the conn, exactly as this timeout already did.
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	// Decorate the guarded transport with the outbound-span/traceparent wrapper
	// when one is installed (OpenTelemetry on); the SSRF guard still runs below it.
	var rt http.RoundTripper = transport
	if transportWrapper != nil {
		rt = transportWrapper(transport)
	}
	if len(total) > 0 && total[0] > 0 {
		rt = &budgetTransport{base: rt, total: total[0]}
	}
	return &http.Client{
		Timeout:   clientTimeout,
		Transport: rt,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return ErrTooManyRedirects
			}
			// Re-validate each redirect target's scheme/host (the dial guard
			// still blocks the IP, but this rejects a redirect to a non-http
			// scheme early with a clear error).
			if _, err := g.ValidateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}

// idleConn resets the read deadline before every Read, turning the connection's
// one-shot deadline into a rolling per-read idle timeout. A transfer that keeps
// delivering bytes never trips it however long it runs; one that goes quiet trips
// it after idle.
type idleConn struct {
	net.Conn
	idle time.Duration
}

func (c *idleConn) Read(p []byte) (int, error) {
	if err := c.Conn.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Read(p)
	if err != nil && errors.Is(err, os.ErrDeadlineExceeded) {
		return n, &idleTimeoutError{d: c.idle}
	}
	return n, err
}

// idleTimeoutError keeps the net.Error timeout contract (http.Transport reads it
// to decide whether a request may be retried on a fresh connection) while adding
// the typed ErrIdleTimeout identity callers match on. It never carries the URL.
type idleTimeoutError struct{ d time.Duration }

func (e *idleTimeoutError) Error() string {
	return fmt.Sprintf("%s: no data received for %s", ErrIdleTimeout.Error(), e.d)
}
func (e *idleTimeoutError) Timeout() bool   { return true }
func (e *idleTimeoutError) Temporary() bool { return false }
func (e *idleTimeoutError) Is(target error) bool {
	return target == ErrIdleTimeout || target == os.ErrDeadlineExceeded
}

// budgetTransport runs each request under its own deadline context and reports
// that deadline's expiry as ErrBudgetExceeded.
//
// http.Client.Timeout would be simpler but is exactly what this package is
// moving away from for downloads: its expiry surfaces as an untyped error a
// caller cannot distinguish from its own cancellation, so no failure message
// could name which limit fired. Deriving from the request context also keeps the
// caller's own cancellation working unchanged.
type budgetTransport struct {
	base  http.RoundTripper
	total time.Duration
}

func (t *budgetTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	parent := req.Context()
	ctx, cancel := context.WithTimeout(parent, t.total)
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel()
		return nil, budgetErr(parent, ctx, err)
	}
	resp.Body = &budgetBody{rc: resp.Body, parent: parent, ctx: ctx, cancel: cancel}
	return resp, nil
}

// budgetBody carries the request's deadline through the body stream — the half
// of a download where the budget actually matters — and releases the context
// when the caller closes.
type budgetBody struct {
	rc     io.ReadCloser
	parent context.Context
	ctx    context.Context
	cancel context.CancelFunc
}

func (b *budgetBody) Read(p []byte) (int, error) {
	n, err := b.rc.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		err = budgetErr(b.parent, b.ctx, err)
	}
	return n, err
}

func (b *budgetBody) Close() error {
	err := b.rc.Close()
	b.cancel()
	return err
}

// budgetErr names the budget as the cause only when OUR deadline is the one that
// fired: a parent that is already done means the caller cancelled, and an idle
// timeout is the other limit and keeps its own identity. The raw error is dropped
// rather than wrapped — at the http.Client layer it would carry the URL, which is
// attacker-controlled and must never reach a stored, client-visible reason.
func budgetErr(parent, ctx context.Context, err error) error {
	if errors.Is(err, ErrIdleTimeout) {
		return err
	}
	if parent.Err() == nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrBudgetExceeded
	}
	return err
}
