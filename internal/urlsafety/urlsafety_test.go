package urlsafety

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateURLAccepts(t *testing.T) {
	for _, raw := range []string{
		"http://example.com",
		"https://example.com/path?q=1",
		"https://sub.example.com:8443/x",
		"http://8.8.8.8/robots.txt", // a public literal IP is fine
		"https://[2606:4700:4700::1111]/",
	} {
		if _, err := ValidateURL(raw); err != nil {
			t.Errorf("ValidateURL(%q) = %v, want nil", raw, err)
		}
	}
}

func TestValidateURLRejects(t *testing.T) {
	for _, raw := range []string{
		"",
		"ftp://example.com",
		"file:///etc/passwd",
		"gopher://example.com",
		"data:text/plain,hi",
		"http://",                      // no host
		"https://user:pw@example.com/", // embedded credentials
		"http://127.0.0.1/",            // loopback
		"http://169.254.169.254/latest/meta-data/", // cloud metadata (link-local)
		"http://10.0.0.5/",                         // private
		"http://192.168.1.1/",                      // private
		"http://[::1]/",                            // ipv6 loopback
		"http://[fc00::1]/",                        // ipv6 ULA
		"http://100.100.0.1/",                      // CGNAT
		"http://0.0.0.0/",                          // unspecified
	} {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) = nil, want error", raw)
		}
	}
}

func TestIsBlockedIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.0.1",
		"169.254.169.254", "fe80::1", "fc00::1", "fd12:3456::1",
		"0.0.0.0", "::", "224.0.0.1", "ff02::1", "100.64.0.1", "100.127.255.255",
		"::ffff:127.0.0.1", // IPv4-mapped loopback
	}
	for _, s := range blocked {
		if !IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("IsBlockedIP(%s) = false, want true", s)
		}
	}
	allowed := []string{"8.8.8.8", "1.1.1.1", "93.184.216.34", "2606:4700:4700::1111", "100.63.255.255", "100.128.0.1"}
	for _, s := range allowed {
		if IsBlockedIP(net.ParseIP(s)) {
			t.Errorf("IsBlockedIP(%s) = true, want false", s)
		}
	}
	if !IsBlockedIP(nil) {
		t.Error("IsBlockedIP(nil) = false, want true (fail closed)")
	}
}

// IPv6 transition and embedding ranges carry an IPv4 address inside an IPv6
// one; a guard that only looks at the outer address lets a private IPv4
// through them. Each is either blocked outright or has its embedded IPv4
// checked against the private ranges.
func TestIsBlockedIPv6TransitionRanges(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		// NAT64 well-known prefix 64:ff9b::/96: embedded IPv4 decides.
		{"64:ff9b::7f00:1", true},    // 127.0.0.1
		{"64:ff9b::a00:1", true},     // 10.0.0.1
		{"64:ff9b::a9fe:a9fe", true}, // 169.254.169.254
		{"64:ff9b::808:808", false},  // 8.8.8.8
		// NAT64 local-use 64:ff9b:1::/48: blocked outright.
		{"64:ff9b:1::1", true},
		{"64:ff9b:1:ffff::808:808", true},
		// 6to4 2002::/16: embedded IPv4 in bytes 2-5.
		{"2002:7f00:1::1", true},   // 127.0.0.1
		{"2002:a00:1::1", true},    // 10.0.0.1
		{"2002:c0a8:101::1", true}, // 192.168.1.1
		{"2002:808:808::1", false}, // 8.8.8.8
		// Teredo 2001::/32: blocked outright.
		{"2001:0:4136:e378:8000:63bf:3fff:fdd2", true},
		{"2001:0::1", true},
		// IPv4-compatible ::/96 (deprecated): blocked outright.
		{"::7f00:1", true},
		{"::a00:1", true},
		{"::808:808", true},
		// Neighbouring public space stays reachable.
		{"2001:4860:4860::8888", false},
		{"2003::1", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("bad test address %q", tc.ip)
		}
		if got := IsBlockedIP(ip); got != tc.blocked {
			t.Errorf("IsBlockedIP(%s) = %v, want %v", tc.ip, got, tc.blocked)
		}
	}
}

// TestIsBlockedIPSpecialPurposeRanges pins the IANA special-purpose ranges that
// are not forwardable / not globally reachable, one representative address each
// (plus the range edges worth catching), with public neighbours as negatives.
func TestIsBlockedIPSpecialPurposeRanges(t *testing.T) {
	cases := []struct {
		name    string
		ip      string
		blocked bool
	}{
		{"this-network 0.0.0.0/8", "0.1.2.3", true},
		{"cgnat 100.64.0.0/10", "100.100.100.100", true},
		{"link-local 169.254.0.0/16", "169.254.169.254", true},
		{"ietf protocol 192.0.0.0/24", "192.0.0.8", true},
		{"ietf protocol 192.0.0.0/24 (last)", "192.0.0.255", true},
		{"test-net-1 192.0.2.0/24", "192.0.2.10", true},
		{"benchmarking 198.18.0.0/15", "198.18.0.1", true},
		{"benchmarking 198.18.0.0/15 (last)", "198.19.255.254", true},
		{"test-net-2 198.51.100.0/24", "198.51.100.7", true},
		{"test-net-3 203.0.113.0/24", "203.0.113.9", true},
		{"reserved 240.0.0.0/4", "240.0.0.1", true},
		{"reserved 240.0.0.0/4 (mid)", "250.1.2.3", true},
		{"broadcast 255.255.255.255/32", "255.255.255.255", true},
		{"v4-mapped benchmarking", "::ffff:198.18.0.1", true},
		{"site-local fec0::/10", "fec0::1", true},
		{"site-local fec0::/10 (last)", "feff:ffff::1", true},
		{"ipv4-translated ::ffff:0:0:0/96", "::ffff:0:808:808", true},
		{"discard 100::/64", "100::1", true},
		{"documentation 2001:db8::/32", "2001:db8::1", true},
		{"benchmarking 2001:2::/48", "2001:2::1", true},
		{"orchid 2001:10::/28", "2001:10::1", true},
		{"orchid 2001:10::/28 (last)", "2001:1f:ffff::1", true},
		{"documentation 3fff::/20", "3fff::1", true},
		{"srv6 sids 5f00::/16", "5f00::1", true},

		{"public 1.1.1.1", "1.1.1.1", false},
		{"public 8.8.8.8", "8.8.8.8", false},
		{"below benchmarking 198.17.255.255", "198.17.255.255", false},
		{"above benchmarking 198.20.0.0", "198.20.0.0", false},
		{"above ietf 192.0.1.1", "192.0.1.1", false},
		{"below reserved 239 is multicast-blocked, 223.255.255.255 public", "223.255.255.255", false},
		{"public 2606:4700::1111", "2606:4700::1111", false},
		{"public 2001:4860:4860::8888", "2001:4860:4860::8888", false},
		{"above orchid 2001:20::1 (ORCHIDv2, reachable)", "2001:20::1", false},
	}
	for _, tc := range cases {
		ip := net.ParseIP(tc.ip)
		if ip == nil {
			t.Fatalf("%s: bad test address %q", tc.name, tc.ip)
		}
		if got := IsBlockedIP(ip); got != tc.blocked {
			t.Errorf("%s: IsBlockedIP(%s) = %v, want %v", tc.name, tc.ip, got, tc.blocked)
		}
	}
}

// TestClientBlocksLoopback proves the dial-time guard actually refuses a
// connection to a loopback address even for a syntactically-valid http URL — the
// real SSRF defense (this is what stops a rebinding DNS name too).
func TestClientBlocksLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// srv.URL is http://127.0.0.1:<port> — a valid URL, but a blocked address.
	client := NewClient(5 * time.Second)
	resp, err := client.Get(srv.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatalf("client fetched a loopback URL %q; want a blocked-address error", srv.URL)
	}
	if !strings.Contains(err.Error(), "blocked") {
		t.Errorf("error = %v, want it to mention the blocked address", err)
	}
}

// TestClientReachesPublicIsBlockedByGuardNotDNS is a lightweight guard that the
// client is wired with our control hook: dialing an explicit private IP fails
// with ErrBlockedAddress surfaced through the transport.
func TestControlRejectsPrivateDial(t *testing.T) {
	if err := (Guard{}).control("tcp", "10.0.0.9:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("control(private) = %v, want ErrBlockedAddress", err)
	}
	if err := (Guard{}).control("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("control(public) = %v, want nil", err)
	}
}

// TestGuardAllowPrivate: the dev/test escape hatch permits private/loopback IPs
// (validate + dial) while still rejecting non-http schemes and fail-closing on an
// unparseable dial address.
func TestGuardAllowPrivate(t *testing.T) {
	g := Guard{AllowPrivate: true}
	// A loopback literal-IP URL now passes ValidateURL.
	if _, err := g.ValidateURL("http://127.0.0.1:8080/clip.mp4"); err != nil {
		t.Errorf("AllowPrivate ValidateURL(loopback) = %v, want nil", err)
	}
	// A private dial address is now permitted.
	if err := g.control("tcp", "10.0.0.9:80", nil); err != nil {
		t.Errorf("AllowPrivate control(private) = %v, want nil", err)
	}
	// But scheme validation still applies...
	if _, err := g.ValidateURL("ftp://127.0.0.1/x"); err == nil {
		t.Error("AllowPrivate ValidateURL(ftp) = nil, want error")
	}
	// ...and an unparseable dial address still fails closed.
	if err := g.control("tcp", "not-an-ip", nil); err == nil {
		t.Error("AllowPrivate control(bad addr) = nil, want error")
	}
	// The secure default still blocks loopback.
	if _, err := (Guard{}).ValidateURL("http://127.0.0.1/x"); err == nil {
		t.Error("default ValidateURL(loopback) = nil, want error")
	}
}

// FuzzValidateURL asserts ValidateURL never panics and that any URL it accepts is
// an http/https URL with a host (the invariant callers rely on). Runs its seed
// corpus under `go test`; real fuzzing is on-demand via `go test -fuzz`.
func FuzzValidateURL(f *testing.F) {
	for _, s := range []string{
		"", "http://example.com", "https://a.b.c/d?e=f#g", "file:///x",
		"http://127.0.0.1", "://", "http://[::1]", "ht!tp://x", "%%%",
		"http://user:pw@host/", "https://100.64.0.1",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		u, err := ValidateURL(raw)
		if err != nil {
			return
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			t.Errorf("accepted URL %q with scheme %q", raw, u.Scheme)
		}
		if u.Hostname() == "" {
			t.Errorf("accepted URL %q with empty host", raw)
		}
		if u.User != nil {
			t.Errorf("accepted URL %q with userinfo", raw)
		}
	})
}

// dripServer serves a body in `chunks` writes spaced `gap` apart, flushing each
// one, so the transfer makes steady progress but takes chunks*gap overall. It is
// the shape a real slow origin (or a big file on a thin link) has, and the shape
// a whole-request timeout kills for no good reason.
func dripServer(t *testing.T, chunks int, gap time.Duration) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		flusher, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			_, _ = w.Write([]byte("x"))
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(gap)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// stallServer answers with headers and then sends no bytes at all until the test
// finishes — the shape a hung origin has, and the ONE shape an idle timeout must
// still kill.
func stallServer(t *testing.T) *httptest.Server {
	t.Helper()
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "video/mp4")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-done
	}))
	t.Cleanup(func() { close(done); srv.Close() })
	return srv
}

// TestNewClientTimeoutKillsAProgressingTransfer is the REPRODUCTION of the limit
// A27 recorded: NewClient's argument is http.Client.Timeout, the WHOLE-request
// deadline, so a transfer that is delivering bytes the entire time still dies on
// it. This is deliberately kept — the legacy helper's semantics are unchanged for
// its short-RPC callers (federation, atproto, link previews), where a slow
// response really is a failure.
func TestNewClientTimeoutKillsAProgressingTransfer(t *testing.T) {
	origin := dripServer(t, 20, 30*time.Millisecond) // ~600ms of steady progress
	client := Guard{AllowPrivate: true}.NewClient(200 * time.Millisecond)
	resp, err := client.Get(origin.URL)
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if err == nil {
		t.Fatal("whole-request client read the drip to completion; the reproduction no longer holds")
	}
}

// TestBudgetClientAllowsSlowProgressingTransfer is the ruling: with an idle
// timeout and no total cap, a slow but PROGRESSING transfer completes even though
// it runs far longer than any single gap between reads.
func TestBudgetClientAllowsSlowProgressingTransfer(t *testing.T) {
	chunks := 20
	origin := dripServer(t, chunks, 30*time.Millisecond) // ~600ms overall
	client := Guard{AllowPrivate: true}.NewBudgetClient(Budget{Idle: 300 * time.Millisecond})
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	n, err := io.Copy(io.Discard, resp.Body)
	if err != nil {
		t.Fatalf("read drip: %v", err)
	}
	if n != int64(chunks) {
		t.Errorf("read %d bytes, want %d", n, chunks)
	}
}

// TestBudgetClientIdleTimeoutKillsAStall: headers then silence fails with
// ErrIdleTimeout — typed, so the caller can name the limit that fired.
func TestBudgetClientIdleTimeoutKillsAStall(t *testing.T) {
	origin := stallServer(t)
	client := Guard{AllowPrivate: true}.NewBudgetClient(Budget{Idle: 150 * time.Millisecond})
	start := time.Now()
	resp, err := client.Get(origin.URL)
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrIdleTimeout) {
		t.Fatalf("stalled transfer err = %v, want ErrIdleTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("stall took %s to fail; the idle timeout is not bounding it", elapsed)
	}
}

// TestBudgetClientTotalBudgetKillsALongTransfer: the OTHER limit. A drip that
// never goes quiet still fails once the total budget is spent, and with the other
// typed reason so an operator learns which knob to turn.
func TestBudgetClientTotalBudgetKillsALongTransfer(t *testing.T) {
	origin := dripServer(t, 100, 20*time.Millisecond) // ~2s overall
	client := Guard{AllowPrivate: true}.NewBudgetClient(Budget{
		Total: 200 * time.Millisecond,
		Idle:  5 * time.Second,
	})
	resp, err := client.Get(origin.URL)
	if err == nil {
		_, err = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
	if !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("over-budget transfer err = %v, want ErrBudgetExceeded", err)
	}
	if errors.Is(err, ErrIdleTimeout) {
		t.Error("over-budget transfer also reported the idle timeout; the two limits must not be confused")
	}
}

// TestBudgetClientKeepsTheSSRFPinning is the load-bearing one: the new client is
// built through the SAME dialer control hook, so it still refuses a private
// address at dial time. A budget knob must never be a way around the guard.
func TestBudgetClientKeepsTheSSRFPinning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	client := NewBudgetClient(Budget{Total: 5 * time.Second, Idle: time.Second}) // secure default guard
	resp, err := client.Get(srv.URL)                                             // http://127.0.0.1:<port>
	if err == nil {
		_ = resp.Body.Close()
		t.Fatalf("budget client fetched loopback %q; want a blocked-address refusal", srv.URL)
	}
	if !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("err = %v, want ErrBlockedAddress", err)
	}
}

// TestBudgetClientZeroTotalMeansNoCap: 0 is "no total cap" (the house convention,
// cf. YTDLP_MAX_HEIGHT=0), so only the idle timeout bounds the transfer.
func TestBudgetClientZeroTotalMeansNoCap(t *testing.T) {
	origin := dripServer(t, 30, 20*time.Millisecond) // ~600ms
	client := Guard{AllowPrivate: true}.NewBudgetClient(Budget{Total: 0, Idle: time.Second})
	resp, err := client.Get(origin.URL)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read: %v", err)
	}
}

// TestGuardKeepMetadataBlocked: an allow-private guard that keeps the
// cloud-metadata ranges blocked still reaches ordinary private addresses.
func TestGuardKeepMetadataBlocked(t *testing.T) {
	g := Guard{AllowPrivate: true, KeepMetadataBlocked: true}
	for ip, want := range map[string]bool{
		"169.254.169.254": true,
		"169.254.1.1":     true,
		"fd00:ec2::254":   true,
		"10.0.0.5":        false,
		"127.0.0.1":       false,
		"fd00:1::1":       false,
	} {
		if got := g.blockedIP(net.ParseIP(ip)); got != want {
			t.Errorf("blockedIP(%s) = %v, want %v", ip, got, want)
		}
	}
	if (Guard{AllowPrivate: true}).blockedIP(net.ParseIP("169.254.169.254")) {
		t.Error("plain AllowPrivate must keep its existing behavior")
	}
}
