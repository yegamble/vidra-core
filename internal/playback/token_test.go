package playback

import (
	"encoding/base64"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
)

// signV1 mints a PRE-phase-4 token: payload "<videoID>:<expUnix>", no version
// tag, no session, no scope. Production no longer has a v1 mint path — the only
// reason to build one is to prove the compatibility window below, so it lives
// here rather than in the package where something could start calling it.
func (s *Signer) signV1(videoID uuid.UUID, ttl time.Duration) string {
	payload := videoID.String() + ":" + strconv.FormatInt(s.now().Add(ttl).Unix(), 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(s.mac([]byte(payload)))
}

// TestSignVerifyRoundTrip proves a freshly minted token verifies for its own
// video and reports the session and scope it was minted with — the claims the
// session API (phase-4 item 1) and QoE correlation (item 4) are built on.
func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid, sid := uuid.New(), uuid.New()
	tok := s.Sign(vid, sid, ScopePlayback, time.Hour)
	if !s.Verify(tok, vid, ScopePlayback) {
		t.Fatalf("fresh token failed to verify for its own video")
	}
	claims, ok := s.VerifyClaims(tok, vid, ScopePlayback)
	if !ok {
		t.Fatalf("VerifyClaims rejected a fresh token")
	}
	if claims.Version != 2 {
		t.Errorf("minted token version = %d, want 2 (only v2 is minted)", claims.Version)
	}
	if claims.SubjectID != vid {
		t.Errorf("claims.SubjectID = %s, want %s", claims.SubjectID, vid)
	}
	if claims.SessionID != sid {
		t.Errorf("claims.SessionID = %s, want %s", claims.SessionID, sid)
	}
	if claims.Scope != ScopePlayback {
		t.Errorf("claims.Scope = %q, want %q", claims.Scope, ScopePlayback)
	}
}

// TestUnknownScopeMintsNothing proves an unrecognised scope produces no token
// rather than one that silently grants the same authority as a known scope.
func TestUnknownScopeMintsNothing(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()
	if tok := s.Sign(vid, uuid.New(), Scope("drm-license"), time.Hour); tok != "" {
		t.Fatalf("Sign with an unknown scope returned %q, want the empty string", tok)
	}
}

// TestV1TokenStillVerifies is the deploy-window guard: the tokens outstanding
// when this change ships were minted in the old grammar and must keep working
// for the rest of their 6h life. They report version 1, no session, and the
// playback scope they have always granted.
func TestV1TokenStillVerifies(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()
	tok := s.signV1(vid, 6*time.Hour)
	claims, ok := s.VerifyClaims(tok, vid, ScopePlayback)
	if !ok {
		t.Fatalf("a v1 token must still verify during the compatibility window")
	}
	if claims.Version != 1 {
		t.Errorf("claims.Version = %d, want 1", claims.Version)
	}
	if claims.SessionID != uuid.Nil {
		t.Errorf("claims.SessionID = %s, want the zero UUID (v1 predates sessions)", claims.SessionID)
	}
	if claims.Scope != ScopePlayback {
		t.Errorf("claims.Scope = %q, want %q", claims.Scope, ScopePlayback)
	}
	// Still video-scoped and still expiring.
	if s.Verify(tok, uuid.New(), ScopePlayback) {
		t.Errorf("a v1 token must not verify for another video")
	}
	if s.Verify(s.signV1(vid, -time.Second), vid, ScopePlayback) {
		t.Errorf("an expired v1 token must be rejected")
	}
}

// TestTokenVersionsAreNotInterchangeable proves the two grammars cannot be
// replayed as one another. Adding claims to an unversioned payload is a grammar
// change, so the risk this pins is a parser that reads a v2 payload as a v1 one
// (dropping the scope and session it was supposed to enforce) or the reverse.
func TestTokenVersionsAreNotInterchangeable(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()

	v1 := s.signV1(vid, time.Hour)
	v2 := s.Sign(vid, uuid.New(), ScopePlayback, time.Hour)
	if v1 == v2 {
		t.Fatalf("the two grammars produced the same token")
	}

	// A v1 token is never read as v2 — it has no version tag, so it can never be
	// mistaken for one carrying a scope.
	if claims, ok := s.VerifyClaims(v1, vid, ScopePlayback); !ok || claims.Version != 1 {
		t.Errorf("v1 token parsed as version %d (ok=%v), want version 1", claims.Version, ok)
	}
	// A v2 token is never read as v1 — a parser that fell back to the old
	// grammar would silently discard the scope and session.
	if claims, ok := s.VerifyClaims(v2, vid, ScopePlayback); !ok || claims.Version != 2 {
		t.Errorf("v2 token parsed as version %d (ok=%v), want version 2", claims.Version, ok)
	}
	// The v1 parser, reached directly, must reject a v2 payload outright rather
	// than returning a lossy reading of it.
	v2Payload := payloadV2Prefix + vid.String() + ":" + uuid.New().String() + ":" +
		string(ScopePlayback) + ":" + strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	if _, ok := parsePayloadV1(v2Payload); ok {
		t.Errorf("the v1 parser accepted a v2 payload")
	}
	// And the v2 parser must reject a v1 payload rather than inventing claims.
	if _, ok := parsePayloadV2(vid.String() + ":0"); ok {
		t.Errorf("the v2 parser accepted a v1 payload")
	}
}

// TestTokenIsVideoScoped proves a token minted for video A is rejected on video
// B — the core security property (the token grants access to exactly one video).
func TestTokenIsVideoScoped(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	a, b := uuid.New(), uuid.New()
	tok := s.Sign(a, uuid.New(), ScopePlayback, time.Hour)
	if s.Verify(tok, b, ScopePlayback) {
		t.Fatalf("token for video A must not verify for video B")
	}
	if !s.Verify(tok, a, ScopePlayback) {
		t.Fatalf("token must still verify for its own video A")
	}
}

// TestTokenIsScopeBound proves the scope is enforced rather than merely
// recorded. Videos and live streams are different tables, and a live token names
// a live stream id — so without this check the two credentials would be
// interchangeable for any pair of rows that happened to share a UUID, and the
// closed scope set would be decoration.
func TestTokenIsScopeBound(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	id := uuid.New()
	vod := s.Sign(id, uuid.New(), ScopePlayback, time.Hour)
	stream := s.Sign(id, uuid.New(), ScopeLive, time.Hour)

	if s.Verify(vod, id, ScopeLive) {
		t.Errorf("a video playback token must not open a live stream")
	}
	if s.Verify(stream, id, ScopePlayback) {
		t.Errorf("a live token must not open a video")
	}
	// Each still opens its own.
	if !s.Verify(vod, id, ScopePlayback) || !s.Verify(stream, id, ScopeLive) {
		t.Errorf("a token must verify for the scope it was minted with")
	}
	// The v1 compatibility window covers video playback and nothing else: a
	// pre-phase-4 token predates scopes and must never be read as granting one
	// that did not exist when it was minted.
	if s.Verify(s.signV1(id, time.Hour), id, ScopeLive) {
		t.Errorf("a v1 token must not verify as a live token")
	}
}

// TestLicenseScopeIsClosedAndDistinct. ScopeLicense is a different authority
// from ScopePlayback — it asks for the KEY rather than for the bytes — so the
// two credentials must not be interchangeable in either direction. Nothing
// mints a license token yet (see the constant's comment); adding the scope to
// the closed set now is what lets a future gate name it without verification
// rejecting it as unknown, and this test is what keeps it from quietly
// collapsing into an alias for playback.
func TestLicenseScopeIsClosedAndDistinct(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	id := uuid.New()
	sid := uuid.New()

	license := s.Sign(id, sid, ScopeLicense, time.Hour)
	if license == "" {
		t.Fatal("ScopeLicense minted nothing; it is not in the closed set")
	}
	claims, ok := s.VerifyClaims(license, id, ScopeLicense)
	if !ok {
		t.Fatal("a license token does not verify for its own scope")
	}
	if claims.Scope != ScopeLicense || claims.SessionID != sid || claims.Version != 2 {
		t.Errorf("claims = %+v, want scope %q, session %s, version 2", claims, ScopeLicense, sid)
	}

	for _, other := range []Scope{ScopePlayback, ScopeLive} {
		if s.Verify(license, id, other) {
			t.Errorf("a license token verified as %q", other)
		}
		if s.Verify(s.Sign(id, sid, other, time.Hour), id, ScopeLicense) {
			t.Errorf("a %q token verified as a license token", other)
		}
	}
	// A pre-scope token predates the license authority entirely and must never
	// be read as carrying it.
	if s.Verify(s.signV1(id, time.Hour), id, ScopeLicense) {
		t.Error("a v1 token must not verify as a license token")
	}
}

// TestTokenExpires proves a token past its TTL no longer verifies.
func TestTokenExpires(t *testing.T) {
	base := time.Now()
	s := NewSigner([]byte("test-secret-material"))
	s.now = func() time.Time { return base }
	vid := uuid.New()
	tok := s.Sign(vid, uuid.New(), ScopePlayback, 6*time.Hour)

	// Just before expiry: still valid.
	s.now = func() time.Time { return base.Add(6*time.Hour - time.Second) }
	if !s.Verify(tok, vid, ScopePlayback) {
		t.Fatalf("token should be valid just before expiry")
	}
	// After expiry: rejected.
	s.now = func() time.Time { return base.Add(6*time.Hour + time.Second) }
	if s.Verify(tok, vid, ScopePlayback) {
		t.Fatalf("expired token must be rejected")
	}
}

// TestTamperedTokenRejected proves any bit-flip in the payload or signature is
// rejected (constant-time signature check), and that a token signed with a
// different key does not verify.
func TestTamperedTokenRejected(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()
	tok := s.Sign(vid, uuid.New(), ScopePlayback, time.Hour)

	cases := []string{
		"",
		".",
		"notbase64.notbase64",
		tok + "x",               // trailing garbage on the signature
		"x" + tok,               // leading garbage on the payload
		tok[:len(tok)-2] + "AA", // corrupted signature tail
	}
	for _, bad := range cases {
		if s.Verify(bad, vid, ScopePlayback) {
			t.Fatalf("tampered/malformed token %q must be rejected", bad)
		}
	}

	// A token from a signer with a different secret must not verify here.
	other := NewSigner([]byte("a-completely-different-secret"))
	if s.Verify(other.Sign(vid, uuid.New(), ScopePlayback, time.Hour), vid, ScopePlayback) {
		t.Fatalf("token signed with a different key must be rejected")
	}
	// Same for the v1 grammar: an outstanding token from another instance is not
	// grandfathered in by the compatibility window.
	if s.Verify(other.signV1(vid, time.Hour), vid, ScopePlayback) {
		t.Fatalf("v1 token signed with a different key must be rejected")
	}
}
