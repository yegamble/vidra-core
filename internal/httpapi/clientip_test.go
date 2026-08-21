package httpapi

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
)

// postFrom posts to path from a specific socket address, optionally claiming a
// client address in X-Forwarded-For — i.e. exactly what an attacker or a
// reverse proxy sends.
func postFrom(srv *Server, path, body, remoteAddr, xff string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	req.RemoteAddr = remoteAddr
	if xff != "" {
		req.Header.Set(echo.HeaderXForwardedFor, xff)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// counterKeys lists the rate-limit keys the fake counter has seen, so a test can
// assert WHICH identity a request was charged to — the thing that actually
// matters here, independent of the status code.
func counterKeys(fc *fakeCounter) []string {
	keys := make([]string, 0, len(fc.counts))
	for k := range fc.counts {
		keys = append(keys, k)
	}
	return keys
}

// TestSpoofedForwardedForDoesNotResetAuthBudget is the regression test for the
// header-spoofing hole: Echo's DEFAULT RealIP() takes the first X-Forwarded-For
// entry from ANY caller, so before the IPExtractor was installed an attacker
// bought a fresh 10/min credential-stuffing budget by incrementing a header, and
// the contact-form and video-password limiters fell the same way.
//
// The request here arrives straight from a PUBLIC address (httptest's default
// 192.0.2.1, TEST-NET-1) — an untrusted hop — so its X-Forwarded-For must be
// ignored entirely and every attempt charged to the real socket address.
func TestSpoofedForwardedForDoesNotResetAuthBudget(t *testing.T) {
	var buf bytes.Buffer
	fc := &fakeCounter{}
	srv := newAuthLimitedServer(t, &buf, 2, fc)
	body := `{"email":"ada@example.test"}`

	// Two attempts inside the budget, each claiming to be a different client.
	for i := 1; i <= 2; i++ {
		rec := postFrom(srv, "/api/v1/auth/password-reset", body, "192.0.2.1:41000", "203.0.113.10")
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt #%d throttled too early", i)
		}
	}
	// A third attempt with yet another invented client address must still be
	// denied — the budget belongs to the socket address, not to the header.
	rec := postFrom(srv, "/api/v1/auth/password-reset", body, "192.0.2.1:41000", "198.51.100.99")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: a spoofed X-Forwarded-For must not buy a fresh budget", rec.Code)
	}

	keys := counterKeys(fc)
	if len(keys) != 1 || keys[0] != "auth:192.0.2.1" {
		t.Errorf("rate-limit keys = %v, want exactly [auth:192.0.2.1] (the real peer)", keys)
	}
}

// TestForwardedForFromTrustedProxyIsHonoured is the other half: the compose
// network and the reverse proxy ARE trusted, so the client address they append
// must be used. Without this, every server-rendered page view shares the Next.js
// container's single bucket and the instance 429s at roughly two renders/second
// no matter who is browsing.
func TestForwardedForFromTrustedProxyIsHonoured(t *testing.T) {
	var buf bytes.Buffer
	fc := &fakeCounter{}
	srv := newAuthLimitedServer(t, &buf, 2, fc)
	body := `{"email":"ada@example.test"}`

	// Two different visitors relayed by the same private-network hop: each keeps
	// its own budget, so neither is throttled.
	for _, visitor := range []string{"203.0.113.10", "198.51.100.20"} {
		for i := 1; i <= 2; i++ {
			rec := postFrom(srv, "/api/v1/auth/password-reset", body, "10.0.0.5:39000", visitor)
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("visitor %s attempt #%d throttled — the trusted hop's forwarded address must key the budget", visitor, i)
			}
		}
	}
	// The third attempt by the FIRST visitor is denied: their own budget is spent.
	rec := postFrom(srv, "/api/v1/auth/password-reset", body, "10.0.0.5:39000", "203.0.113.10")
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 once one visitor's own budget is spent", rec.Code)
	}

	for _, k := range counterKeys(fc) {
		if k == "auth:10.0.0.5" {
			t.Error("requests were charged to the proxy, not the visitor it forwarded for")
		}
	}
}

// TestForwardedForChainStopsAtFirstUntrustedHop proves the walk is right-to-left
// and stops at the nearest untrusted address: a client that prepends fake hops
// before the real proxy chain cannot make itself look like any of them.
func TestForwardedForChainStopsAtFirstUntrustedHop(t *testing.T) {
	var buf bytes.Buffer
	fc := &fakeCounter{}
	srv := newAuthLimitedServer(t, &buf, 5, fc)

	// The client at 203.0.113.7 sent "1.2.3.4" itself; the proxy appended the
	// client's real address. The nearest untrusted hop is 203.0.113.7.
	postFrom(srv, "/api/v1/auth/password-reset", `{"email":"a@b.test"}`,
		"10.0.0.5:39000", "1.2.3.4, 203.0.113.7")

	keys := counterKeys(fc)
	if len(keys) != 1 || keys[0] != "auth:203.0.113.7" {
		t.Errorf("rate-limit keys = %v, want [auth:203.0.113.7] (nearest untrusted hop, not the client-supplied 1.2.3.4)", keys)
	}
}

// TestPublicTrustedProxyCIDRIsHonoured is the fourth case, and the one the other
// three could not express: a TLS terminator on a PUBLIC address.
//
// A cloud load balancer, a CDN edge or the operator-run proxy of
// VIDRA_TLS_MODE=external forwards from a routable address, which the built-in
// loopback/RFC1918/link-local trust set correctly refuses to believe. The
// consequence is not a security hole — the proxy becomes the client, which is
// fail-safe — it is that EVERY visitor behind it shares one 10/min auth budget,
// so the instance locks itself out at the edge. TRUSTED_PROXY_CIDRS is the
// deliberate statement that fixes it, and this proves the statement is read.
func TestPublicTrustedProxyCIDRIsHonoured(t *testing.T) {
	var buf bytes.Buffer
	fc := &fakeCounter{}
	cfg := testConfig()
	cfg.TrustedProxyCIDRs = []string{"198.51.100.10/32"}
	srv := newAuthLimitedServerWith(t, &buf, 2, fc, cfg)
	body := `{"email":"ada@example.test"}`

	// Two visitors relayed by the public terminator: each keeps its own budget.
	for _, visitor := range []string{"203.0.113.10", "203.0.113.20"} {
		for i := 1; i <= 2; i++ {
			rec := postFrom(srv, "/api/v1/auth/password-reset", body, "198.51.100.10:44300", visitor)
			if rec.Code == http.StatusTooManyRequests {
				t.Fatalf("visitor %s attempt #%d throttled — the configured terminator's forwarded address must key the budget", visitor, i)
			}
		}
	}
	for _, k := range counterKeys(fc) {
		if k == "auth:198.51.100.10" {
			t.Error("requests were charged to the terminator, not the visitor it forwarded for: TRUSTED_PROXY_CIDRS was not applied")
		}
	}

	// The same request WITHOUT the CIDR configured is charged to the proxy — the
	// default, and the reason this variable has to be set deliberately.
	var defaultBuf bytes.Buffer
	defaultCounter := &fakeCounter{}
	defaultSrv := newAuthLimitedServer(t, &defaultBuf, 2, defaultCounter)
	postFrom(defaultSrv, "/api/v1/auth/password-reset", body, "198.51.100.10:44300", "203.0.113.10")
	keys := counterKeys(defaultCounter)
	if len(keys) != 1 || keys[0] != "auth:198.51.100.10" {
		t.Errorf("rate-limit keys = %v, want [auth:198.51.100.10]: an unlisted public hop must NOT be trusted", keys)
	}
}
