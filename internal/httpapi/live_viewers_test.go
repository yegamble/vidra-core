package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/live"
	"github.com/vidra/vidra-core/internal/pseudonym"
)

// The anonymous live-viewer principal. A26 measured the count collapsing every
// viewer behind one address into a single member — and, because hls.js sends no
// session credential with a public stream's playlist, that is nearly every
// viewer on the instance.

// digestFor runs one anonymous playlist fetch through liveViewerDigest.
func digestFor(t *testing.T, s *Server, ip, ua string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/live/x/hls/master.m3u8", nil)
	req.Header.Set("User-Agent", ua)
	req.RemoteAddr = ip + ":54321"
	rec := httptest.NewRecorder()
	c := echo.New().NewContext(req, rec)
	return s.liveViewerDigest(c, time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
}

func viewerDigestServer() *Server {
	// An obviously fake key: this test asserts relationships between digests,
	// never a literal value.
	return &Server{liveViewers: pseudonym.New([]byte("test-only-live-viewer-key-0000000"), live.ViewerDigestDomain)}
}

// TestAnonymousViewersBehindOneAddressAreDistinguishedByUserAgent is SC5's
// measurable half: a phone and a laptop on one household NAT are two viewers,
// where before they were one.
func TestAnonymousViewersBehindOneAddressAreDistinguishedByUserAgent(t *testing.T) {
	s := viewerDigestServer()
	phone := digestFor(t, s, "203.0.113.7", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)")
	laptop := digestFor(t, s, "203.0.113.7", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)")
	if phone == "" || laptop == "" {
		t.Fatal("a configured digester produced no pseudonym")
	}
	if phone == laptop {
		t.Error("two devices on one address digest identically; every household, office and carrier NAT counts as one viewer")
	}
	// The same device is still ONE viewer across its ~2 s playlist refreshes —
	// a count that grew with every refetch would be worse than no count.
	if again := digestFor(t, s, "203.0.113.7", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"); again != phone {
		t.Error("the same viewer digests differently on a second playlist fetch; the count would grow with every refresh")
	}
	// And the address still matters: the same browser from two networks is two.
	if other := digestFor(t, s, "198.51.100.9", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X)"); other == phone {
		t.Error("the address dropped out of the principal; two networks collapsed into one viewer")
	}
}

// TestViewerPrincipalCannotBeSpoofedAcrossAddresses: the separator is the whole
// defence. Without it a crafted User-Agent could produce another address's
// principal by moving bytes across the join.
func TestViewerPrincipalCannotBeSpoofedAcrossAddresses(t *testing.T) {
	s := viewerDigestServer()
	a := digestFor(t, s, "203.0.113.7", "x")
	b := digestFor(t, s, "203.0.113.", "7x")
	if a == b {
		t.Error("two different (address, agent) pairs digest identically; the principal's halves are not separated")
	}
}

// TestViewerDigestAbsentWithoutAKey: no key, no pseudonym — and the counter
// ignores an empty digest rather than counting every viewer under one member.
func TestViewerDigestAbsentWithoutAKey(t *testing.T) {
	s := &Server{}
	if got := digestFor(t, s, "203.0.113.7", "curl/8"); got != "" {
		t.Errorf("digest = %q on an instance with no digester, want empty", got)
	}
}

// TestViewerUserAgentIsBounded: the agent is attacker-controlled and reaches a
// MAC, so it is truncated rather than passed through at header size.
func TestViewerUserAgentIsBounded(t *testing.T) {
	s := viewerDigestServer()
	long := make([]byte, 4096)
	for i := range long {
		long[i] = 'a'
	}
	if got := digestFor(t, s, "203.0.113.7", string(long)); got == "" {
		t.Error("an oversized User-Agent produced no digest at all")
	}
	// Two agents that differ only past the cap are the same viewer.
	if digestFor(t, s, "203.0.113.7", string(long)) != digestFor(t, s, "203.0.113.7", string(long)+"tail") {
		t.Error("the User-Agent is not bounded; a client can mint viewers by appending bytes")
	}
}
