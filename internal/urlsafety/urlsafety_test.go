package urlsafety

import (
	"errors"
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
	if err := safeControl("tcp", "10.0.0.9:80", nil); !errors.Is(err, ErrBlockedAddress) {
		t.Errorf("safeControl(private) = %v, want ErrBlockedAddress", err)
	}
	if err := safeControl("tcp", "8.8.8.8:443", nil); err != nil {
		t.Errorf("safeControl(public) = %v, want nil", err)
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
