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
