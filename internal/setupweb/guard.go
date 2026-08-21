package setupweb

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// This file is the containment model, and it is the reason the rest of the
// package is allowed to be as powerful as it is. Four rules, each of which is a
// refusal rather than a warning:
//
//  1. LOOPBACK ONLY. ParseListen refuses any bind address that is not a loopback
//     host, and names the SSH tunnel as the remote path. There is no flag to
//     override it: a wizard on 0.0.0.0 is an unauthenticated root shell for
//     whoever finds the port first, and a "yes I know" gate for that would be a
//     gate operators learn to pass.
//
//  2. A ONE-TIME TOKEN, IN A CUSTOM HEADER. 256 bits from crypto/rand, minted at
//     startup, printed once in the opening URL. The HTML shell takes it in the
//     query string (that is how a browser can be handed a link at all); every
//     /api/ request must repeat it in X-Setup-Token. The header is the CSRF
//     defence and it is a complete one: a cross-site form post cannot set a
//     custom header, and a cross-site fetch that tries is stopped by the
//     preflight it now needs — which this server answers to nobody.
//
//  3. A HOST-HEADER ALLOWLIST. DNS rebinding is the attack that survives rule 1:
//     a page on evil.example re-resolves its own name to 127.0.0.1 and then talks
//     to this server from the browser of the operator who is running it. What it
//     cannot change is the Host header it sends, so anything that is not a
//     loopback literal or `localhost` is refused before it reaches a handler.
//
//  4. ONE MESSAGE FOR ALL THREE. A refusal never says which rule caught it, and
//     never whether a token was absent or wrong. There is nothing to learn from
//     probing, and the sentence an operator sees points at the terminal that has
//     the real link.

// tokenBytes is the entropy behind the one-time token: 256 bits, well past the
// 128 the threat model asks for, and cheap — it is typed by nobody, only pasted
// or clicked.
const tokenBytes = 32

// tokenHeader is where every /api/ request carries the token. The name matters
// less than the fact that it is a CUSTOM header: that is what puts a
// cross-origin request behind a preflight this server never approves.
const tokenHeader = "X-Setup-Token"

// tokenQuery is the query parameter on the HTML shell, and the ONLY place the
// token is ever accepted outside the header — a browser cannot be handed an
// opening link any other way.
const tokenQuery = "t"

// forbidden is the single body every refusal returns. It says nothing about
// which check failed, and points at the one place the real link exists.
const forbidden = "403 — this page is served by `vidra setup --web`, and it only answers requests that carry the one-time link that command printed in your terminal.\n"

// mintToken draws the one-time credential. base64url so it survives a query
// string, an address bar and a copy-paste out of a terminal without escaping.
func mintToken(r io.Reader) (string, error) {
	if r == nil {
		r = rand.Reader
	}
	buf := make([]byte, tokenBytes)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", fmt.Errorf("setup: draw %d random bytes for the wizard's one-time token: %w", tokenBytes, err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// ParseListen validates a --listen address and returns it as the server will
// bind it.
//
// It is the first rule of the containment model and it is a hard refusal. The
// wizard writes an env file full of freshly minted secrets and execs a deploy;
// it has no login, because the one-time token in the operator's terminal IS the
// login. On a public interface that token is the only thing between a stranger
// and the deployment, over plain http, before the TLS this very wizard is
// configuring exists. So the answer to "I need to reach it from my laptop" is
// the SSH tunnel named in the refusal, which moves the credential over a
// transport that is already authenticated.
func ParseListen(addr string) (string, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return DefaultListen, nil
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("setup: --listen %q is not a host:port address — write it as 127.0.0.1:8321 (an IPv6 host needs brackets: [::1]:8321)", addr)
	}
	if strings.TrimSpace(port) == "" {
		return "", fmt.Errorf("setup: --listen %q names no port — write it as 127.0.0.1:8321", addr)
	}
	if !isLoopbackHost(host) {
		return "", fmt.Errorf(`setup: --listen %s would put the setup wizard on an address other than this host's loopback, and it binds to loopback only.
The wizard writes the env file (with every secret in it) and runs the deploy, and its one-time token is its ONLY authentication — over plain http, before the TLS it is configuring exists. Reachable from the network, that is the deployment handed to whoever finds the port.
To drive it from your own machine, forward the port over SSH instead:
  ssh -L 8321:127.0.0.1:8321 user@server
then open the http://127.0.0.1:8321/... link this command prints, on your laptop. Valid --listen hosts are 127.0.0.1, ::1 and localhost`, addr)
	}
	return addr, nil
}

// isLoopbackHost reports whether a host part names this machine and nothing
// else.
//
// A NAME other than localhost is refused even though it might resolve to
// 127.0.0.1 today: what a name resolves to is not a property of the address the
// operator typed, and "it pointed at loopback when we looked" is exactly the
// assumption DNS rebinding exists to break. An empty host is refused because it
// is the wildcard — `:8321` binds every interface, which is the one outcome this
// function exists to prevent.
func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	addr, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	return addr.IsLoopback()
}

// guard wraps the mux in the whole containment model. Nothing is routed until
// every rule above has passed, so a handler in this package may assume it is
// talking to the operator's own browser.
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set before any refusal, so even a 403 is not cached and not framed. The
		// CSP is as tight as a page with inline assets can be: nothing is fetched
		// from anywhere, no form may post anywhere, and the page may not be
		// embedded — a clickjacked install wizard would be an install somebody
		// else configured.
		h := w.Header()
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Content-Security-Policy",
			"default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src data:; form-action 'none'; frame-ancestors 'none'; base-uri 'none'")

		if !allowedHost(r.Host) || !hostTargetOK(r) || !allowedOrigin(r.Header.Get("Origin")) || !s.authorized(r) {
			http.Error(w, forbidden, http.StatusForbidden)
			return
		}
		// Only now: an unauthenticated probe must not be able to hold the wizard
		// open past its idle timeout.
		s.touch()
		next.ServeHTTP(w, r)
	})
}

// hostTargetOK is belt to rule 3's braces, for a client that is not a browser.
//
// A browser always sends an ORIGIN-FORM request target — a bare path — and puts
// the authority in the Host header, which net/http promotes to r.Host, the value
// allowedHost has already vetted. A raw socket can instead send an ABSOLUTE-form
// target, `GET http://127.0.0.1/ HTTP/1.1`, whose authority Go reads into r.Host
// (so it passes the check above) while a mismatched `Host: evil.example` rides
// alongside — and Go, having taken the authority from the URI, drops that header
// from r.Header entirely, so allowedHost never sees it. Nothing a browser does
// looks like this, and a loopback wizard has no proxy in front of it that
// legitimately would, so an absolute-form request target is refused outright.
// Any Host header still present (Go keeps it for an origin-form target that also
// repeats it) is held to the same loopback allowlist.
func hostTargetOK(r *http.Request) bool {
	if r.URL != nil && r.URL.IsAbs() {
		return false
	}
	if h := r.Header.Get("Host"); h != "" && !allowedHost(h) {
		return false
	}
	return true
}

// allowedHost is rule 3. The port is ignored — the operator may have moved it
// with --listen — and only the host part is tested, against the same
// loopback-literal-or-localhost rule the bind address obeys.
func allowedHost(host string) bool {
	if host == "" {
		// HTTP/1.1 requires a Host header. A request without one is not a browser
		// this server has any business answering.
		return false
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return isLoopbackHost(h)
	}
	// No port: the header is the bare host.
	return isLoopbackHost(host)
}

// allowedOrigin is belt to rule 2's braces. A same-origin fetch from the wizard's
// own page sends this server's own loopback origin; a cross-site attempt sends
// somebody else's, and is refused here before the token comparison rather than
// after it. An ABSENT Origin is allowed: curl sends none, and the operator
// driving the API from a terminal is a first-class user of it.
func allowedOrigin(origin string) bool {
	if strings.TrimSpace(origin) == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" {
		return false
	}
	return allowedHost(u.Host)
}

// authorized is rule 2: the token, compared in constant time.
//
// WHERE it is read from is the whole CSRF story. Everything under /api/ demands
// the custom header, which no cross-site form post can set and no simple
// cross-site fetch can carry. The query string is accepted for the HTML shell
// and for nothing else, because a link is the only way to hand a browser its
// first credential — and a page loaded that way leaks no more than the address
// bar of the operator's own machine.
func (s *Server) authorized(r *http.Request) bool {
	got := r.Header.Get(tokenHeader)
	if got == "" && r.Method == http.MethodGet && isShellPath(r.URL.Path) {
		got = r.URL.Query().Get(tokenQuery)
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) == 1
}

// isShellPath is the one path the query-string token is accepted on.
func isShellPath(p string) bool { return p == "/" }
