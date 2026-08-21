package setupweb

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This file is the containment model's test, and it is deliberately exhaustive:
// every rule in guard.go is a REFUSAL, and a refusal that is not tested is a
// refusal that will be removed by the next person who finds it inconvenient.

func TestParseListenRefusesAnythingButLoopback(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{
		"0.0.0.0:8321",
		":8321",
		"192.168.1.10:8321",
		"[::]:8321",
		"203.0.113.5:8321",
		"video.example.org:8321",
		// A name that resolves to loopback today is still a name, and what it
		// resolves to is not a property of what was typed.
		"localhost.localdomain:8321",
		"8321",
		"127.0.0.1",
		"127.0.0.1:",
	} {
		if got, err := ParseListen(addr); err == nil {
			t.Errorf("ParseListen(%q) = %q, want a refusal", addr, got)
		}
	}
}

func TestParseListenRefusalNamesTheTunnel(t *testing.T) {
	t.Parallel()
	_, err := ParseListen("0.0.0.0:8321")
	if err == nil {
		t.Fatal("want a refusal")
	}
	// The refusal has to carry the way OUT, not just the way barred: an operator
	// on a remote host who is told "no" and nothing else reaches for --listen
	// 0.0.0.0 again with a firewall rule beside it.
	for _, want := range []string{"ssh -L 8321:127.0.0.1:8321", "loopback only"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal does not mention %q:\n%s", want, err)
		}
	}
}

func TestParseListenAcceptsLoopback(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:8321", "127.0.0.53:9000", "[::1]:8321", "localhost:8321", "LOCALHOST:1"} {
		if _, err := ParseListen(addr); err != nil {
			t.Errorf("ParseListen(%q): %v", addr, err)
		}
	}
	got, err := ParseListen("")
	if err != nil || got != DefaultListen {
		t.Errorf("ParseListen(\"\") = %q, %v; want %q", got, err, DefaultListen)
	}
}

func TestRunRefusesToBindANonLoopbackAddress(t *testing.T) {
	t.Parallel()
	// The refusal has to happen in New, BEFORE anything binds: a server that
	// listened first and validated second would have been on the network for
	// however long that took.
	if _, err := New(Options{Listen: "0.0.0.0:0"}); err == nil {
		t.Fatal("New accepted a wildcard bind address")
	}
}

// newTestServer is a wizard behind a real loopback HTTP server, which is what
// makes the Host header a real one.
func newTestServer(t *testing.T, opt Options) (*Server, *httptest.Server) {
	t.Helper()
	if opt.Listen == "" {
		opt.Listen = "127.0.0.1:0"
	}
	if opt.IdleTimeout == 0 {
		opt.IdleTimeout = -1 // no timer unless a test asks for one
	}
	s, err := New(opt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return s, ts
}

func do(t *testing.T, ts *httptest.Server, method, path string, mutate func(*http.Request)) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if mutate != nil {
		mutate(req)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func TestShellNeedsTheTokenInTheQuery(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})

	if resp := do(t, ts, "GET", "/", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET / with no token = %d, want 403", resp.StatusCode)
	}
	if resp := do(t, ts, "GET", "/?t=wrong", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET / with a wrong token = %d, want 403", resp.StatusCode)
	}
	// A token that is a PREFIX of the real one: the comparison is constant-time
	// and total, not a prefix match.
	if resp := do(t, ts, "GET", "/?t="+s.Token()[:8], nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET / with a truncated token = %d, want 403", resp.StatusCode)
	}
	resp := do(t, ts, "GET", "/?t="+s.Token(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / with the token = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("X-Frame-Options"); got != "DENY" {
		t.Errorf("X-Frame-Options = %q, want DENY", got)
	}
	if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("CSP does not forbid framing: %q", resp.Header.Get("Content-Security-Policy"))
	}
}

func TestAPINeedsTheTokenInTheHeaderAndNotTheQuery(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})

	// The query string is accepted on the SHELL and nowhere else. If /api/ took
	// it too, a cross-site <img src> or a top-level navigation would be a request
	// that carries the credential — which is the whole thing the custom header
	// buys.
	if resp := do(t, ts, "POST", "/api/finish?t="+s.Token(), nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/finish with the token in the query = %d, want 403", resp.StatusCode)
	}
	if resp := do(t, ts, "POST", "/api/finish", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("POST /api/finish with no token = %d, want 403", resp.StatusCode)
	}
	resp := do(t, ts, "POST", "/api/finish", func(r *http.Request) {
		r.Header.Set(tokenHeader, s.Token())
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/finish with the header = %d, want 200", resp.StatusCode)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("Finish did not stop the wizard")
	}
	if s.Reason() != ReasonFinished {
		t.Errorf("Reason = %q, want %q", s.Reason(), ReasonFinished)
	}
}

func TestRefusalsSayTheSameThing(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})

	bodies := map[string]string{}
	for name, mutate := range map[string]func(*http.Request){
		"no token":    func(r *http.Request) {},
		"wrong token": func(r *http.Request) { r.Header.Set(tokenHeader, "nope") },
		"bad host":    func(r *http.Request) { r.Host = "evil.example"; r.Header.Set(tokenHeader, s.Token()) },
		"bad origin": func(r *http.Request) {
			r.Header.Set("Origin", "https://evil.example")
			r.Header.Set(tokenHeader, s.Token())
		},
	} {
		resp := do(t, ts, "POST", "/api/finish", mutate)
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", name, resp.StatusCode)
		}
		var buf bytes.Buffer
		_, _ = buf.ReadFrom(resp.Body)
		bodies[name] = buf.String()
	}
	// One message for every rule: a probe must not be able to tell "your token is
	// wrong" from "your Host header is wrong", because the difference is a map of
	// what to try next.
	var first string
	for name, body := range bodies {
		if first == "" {
			first = body
			continue
		}
		if body != first {
			t.Errorf("%s answered differently from the first refusal:\n%q\nvs\n%q", name, body, first)
		}
	}
	if strings.Contains(first, s.Token()) {
		t.Error("the refusal echoes the token")
	}
}

func TestHostHeaderAllowlistDefeatsDNSRebinding(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})

	// The shape of a rebinding attack: the browser has been made to resolve a
	// name the attacker controls to 127.0.0.1, so the connection really does
	// arrive here — but the Host header still says the attacker's name, and it is
	// the one thing they cannot forge from a page.
	for _, host := range []string{
		"evil.example",
		"evil.example:8321",
		"127.0.0.1.nip.io:8321",
		"vidra.local",
	} {
		resp := do(t, ts, "GET", "/?t="+s.Token(), func(r *http.Request) {
			r.Host = host
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Host %q = %d, want 403", host, resp.StatusCode)
		}
	}
	for _, host := range []string{"127.0.0.1:8321", "localhost:8321", "[::1]:8321", "localhost", "127.0.0.1"} {
		resp := do(t, ts, "GET", "/?t="+s.Token(), func(r *http.Request) {
			r.Host = host
		})
		if resp.StatusCode != http.StatusOK {
			t.Errorf("Host %q = %d, want 200", host, resp.StatusCode)
		}
	}
	// A request with NO Host at all cannot be made through net/http's client (it
	// fills the header in from the URL), so it is driven straight at the handler.
	// HTTP/1.1 requires the header; anything that omits it is not a browser this
	// server has business answering.
	req := httptest.NewRequest("GET", "/?t="+s.Token(), nil)
	req.Host = ""
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Errorf("no Host header = %d, want 403", rec.Code)
	}
}

// TestAbsoluteFormRequestTargetIsRefusedOverARawSocket closes the belt-and-
// suspenders gap the audit noted: an ABSOLUTE-form request target,
// `GET http://127.0.0.1/ HTTP/1.1`, makes Go read the authority into r.Host (so
// the Host allowlist passes) while a mismatched `Host: evil.example` rides
// alongside and is dropped from r.Header. No browser sends an absolute-form
// target to a loopback server, so it is refused outright.
//
// It has to be driven over a RAW SOCKET: net/http's client will not emit an
// absolute-form target for a plain GET, which is exactly why the vector is not
// reachable by a browser — this test is proving the last mile shut anyway.
func TestAbsoluteFormRequestTargetIsRefusedOverARawSocket(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})
	addr := strings.TrimPrefix(ts.URL, "http://")

	send := func(raw string) string {
		conn, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		defer func() { _ = conn.Close() }()
		if _, err := conn.Write([]byte(raw)); err != nil {
			t.Fatalf("write: %v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		buf := make([]byte, 128)
		n, _ := conn.Read(buf)
		return string(buf[:n])
	}

	// The exact attack: authority in the URI is loopback (to slip past r.Host),
	// a forged Host header beside it, and the real token so ONLY the host check
	// stands between the request and a 200.
	status := send("GET http://127.0.0.1/?t=" + s.Token() + " HTTP/1.1\r\nHost: evil.example\r\nConnection: close\r\n\r\n")
	if !strings.Contains(status, "403") {
		t.Errorf("absolute-form target with a forged Host was not refused:\n%s", status)
	}
	// And the ordinary origin-form request the browser actually sends still works
	// over the same raw socket, so the rule refuses the attack and nothing else.
	ok := send("GET /?t=" + s.Token() + " HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
	if !strings.Contains(ok, "200") {
		t.Errorf("origin-form request was refused:\n%s", ok)
	}
}

func TestCrossSiteOriginIsRefusedEvenWithTheToken(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})
	for _, origin := range []string{"https://evil.example", "http://evil.example", "null", "http://127.0.0.1.evil.example"} {
		resp := do(t, ts, "POST", "/api/finish", func(r *http.Request) {
			r.Header.Set("Origin", origin)
			r.Header.Set(tokenHeader, s.Token())
		})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("Origin %q = %d, want 403", origin, resp.StatusCode)
		}
	}
	// curl sends no Origin at all, and driving this API from a terminal is a
	// first-class use of it.
	resp := do(t, ts, "POST", "/api/finish", func(r *http.Request) {
		r.Header.Set(tokenHeader, s.Token())
	})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("no Origin = %d, want 200", resp.StatusCode)
	}
}

func TestNoCORSHeadersAreEverSent(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})
	resp := do(t, ts, "GET", "/?t="+s.Token(), nil)
	for _, h := range []string{"Access-Control-Allow-Origin", "Access-Control-Allow-Headers", "Access-Control-Allow-Credentials"} {
		if v := resp.Header.Get(h); v != "" {
			t.Errorf("%s = %q; this server must never approve a cross-origin preflight — that approval is what the custom-header rule depends on not existing", h, v)
		}
	}
}

func TestIdleTimeoutStopsTheWizard(t *testing.T) {
	t.Parallel()
	s, err := New(Options{Listen: "127.0.0.1:0", IdleTimeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background()) }()

	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the idle timer never fired")
	}
	if !strings.HasPrefix(s.Reason(), ReasonIdle) {
		t.Errorf("Reason = %q, want the idle reason", s.Reason())
	}
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after the idle shutdown")
	}
	// And the port is free again: a wizard that has shut down but is still
	// holding the listener is a wizard the next `vidra setup --web` cannot start.
	ln, err := net.Listen("tcp", s.Addr())
	if err != nil {
		t.Fatalf("the listener was not released: %v", err)
	}
	_ = ln.Close()
}

func TestAnUnauthorizedRequestDoesNotHoldTheWizardOpen(t *testing.T) {
	t.Parallel()
	s, err := New(Options{Listen: "127.0.0.1:0", IdleTimeout: 150 * time.Millisecond})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	go func() { _ = s.Run(context.Background()) }()
	// Wait for the listener.
	base := ""
	for i := 0; i < 100; i++ {
		if c, err := net.Dial("tcp", s.Addr()); err == nil {
			_ = c.Close()
			base = "http://" + s.Addr()
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if base == "" {
		t.Fatal("the server never came up")
	}
	// Hammer it with unauthenticated requests for longer than the idle timeout.
	// If touch() lived in the mux rather than at the end of the guard, this would
	// keep the wizard alive for ever from outside.
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequest("GET", base+"/", nil)
		if resp, err := http.DefaultClient.Do(req); err == nil {
			_ = resp.Body.Close()
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-s.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("unauthenticated traffic kept the wizard alive past its idle timeout")
	}
}

func TestTokenIsUnguessableAndURLSafe(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		s, err := New(Options{Listen: "127.0.0.1:0"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		tok := s.Token()
		if len(tok) < 22 {
			t.Fatalf("token %q is shorter than 128 bits of base64url", tok)
		}
		if strings.ContainsAny(tok, "+/=&?# ") {
			t.Fatalf("token %q is not URL-safe", tok)
		}
		if seen[tok] {
			t.Fatalf("token %q was minted twice", tok)
		}
		seen[tok] = true
	}
}

func TestUnknownPathsAre404AndOnlyWithTheToken(t *testing.T) {
	t.Parallel()
	s, ts := newTestServer(t, Options{})
	if resp := do(t, ts, "GET", "/api/nope", nil); resp.StatusCode != http.StatusForbidden {
		t.Errorf("unknown path without a token = %d, want 403", resp.StatusCode)
	}
	resp := do(t, ts, "GET", "/api/nope", func(r *http.Request) { r.Header.Set(tokenHeader, s.Token()) })
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown path with the token = %d, want 404", resp.StatusCode)
	}
	// The shell's {$} pattern must not swallow every other path: a bare "/"
	// pattern would have served the whole page here.
	resp = do(t, ts, "GET", "/anything", func(r *http.Request) { r.Header.Set(tokenHeader, s.Token()) })
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("GET /anything = %d, want 404", resp.StatusCode)
	}
	// And the query-string token is shell-only, on every path: only GET / accepts
	// it, so a link to any other path carries no credential.
	resp = do(t, ts, "GET", "/anything?t="+s.Token(), nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /anything?t=… = %d, want 403 (the query token is shell-only)", resp.StatusCode)
	}
}
