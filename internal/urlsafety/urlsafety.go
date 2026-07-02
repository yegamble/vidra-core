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
//   - slow responses are bounded by the client timeout; oversized responses are
//     the caller's job to bound (io.LimitReader / http.MaxBytesReader).
package urlsafety

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
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
)

const maxRedirects = 5

// ValidateURL parses raw and returns it only if it is a fetchable public URL:
// an http/https scheme, a non-empty host, and no embedded credentials. When the
// host is a literal IP, it must not be in a blocked range. Hostnames are NOT
// resolved here — that check happens at dial time (see NewClient), which is what
// actually defeats DNS rebinding; this pre-flight just rejects the obvious cases.
func ValidateURL(raw string) (*url.URL, error) {
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
	if ip := net.ParseIP(host); ip != nil && IsBlockedIP(ip) {
		return nil, fmt.Errorf("%w: non-public address", ErrInvalidURL)
	}
	return u, nil
}

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

// safeControl is the net.Dialer.Control hook. It runs after DNS resolution, once
// per candidate IP, with address as "ip:port" — so rejecting a blocked IP here
// is the dial-time guard that defeats DNS rebinding.
func safeControl(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrBlockedAddress, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("%w: non-IP dial address %q", ErrBlockedAddress, host)
	}
	if IsBlockedIP(ip) {
		return fmt.Errorf("%w: %s", ErrBlockedAddress, ip)
	}
	return nil
}

// NewClient returns an http.Client for outbound fetches of user-supplied URLs.
// It refuses to connect to a blocked IP at dial time (covering redirects and DNS
// rebinding), never honours proxy environment variables, caps the redirect chain,
// and applies timeout as the overall deadline (bounding slow responses). Callers
// must still bound the response body size themselves.
func NewClient(timeout time.Duration) *http.Client {
	dialer := &net.Dialer{Timeout: 10 * time.Second, Control: safeControl}
	transport := &http.Transport{
		DialContext:           dialer.DialContext,
		Proxy:                 nil, // ignore HTTP(S)_PROXY — could route around the guard
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          10,
		IdleConnTimeout:       30 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return ErrTooManyRedirects
			}
			// Re-validate each redirect target's scheme/host (the dial guard
			// still blocks the IP, but this rejects a redirect to a non-http
			// scheme early with a clear error).
			if _, err := ValidateURL(req.URL.String()); err != nil {
				return err
			}
			return nil
		},
	}
}
