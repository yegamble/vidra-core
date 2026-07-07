package playback

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestSignVerifyRoundTrip proves a freshly minted token verifies for its own
// video.
func TestSignVerifyRoundTrip(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()
	tok := s.Sign(vid, time.Hour)
	if !s.Verify(tok, vid) {
		t.Fatalf("fresh token failed to verify for its own video")
	}
}

// TestTokenIsVideoScoped proves a token minted for video A is rejected on video
// B — the core security property (the token grants access to exactly one video).
func TestTokenIsVideoScoped(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	a, b := uuid.New(), uuid.New()
	tok := s.Sign(a, time.Hour)
	if s.Verify(tok, b) {
		t.Fatalf("token for video A must not verify for video B")
	}
	if !s.Verify(tok, a) {
		t.Fatalf("token must still verify for its own video A")
	}
}

// TestTokenExpires proves a token past its TTL no longer verifies.
func TestTokenExpires(t *testing.T) {
	base := time.Now()
	s := NewSigner([]byte("test-secret-material"))
	s.now = func() time.Time { return base }
	vid := uuid.New()
	tok := s.Sign(vid, 6*time.Hour)

	// Just before expiry: still valid.
	s.now = func() time.Time { return base.Add(6*time.Hour - time.Second) }
	if !s.Verify(tok, vid) {
		t.Fatalf("token should be valid just before expiry")
	}
	// After expiry: rejected.
	s.now = func() time.Time { return base.Add(6*time.Hour + time.Second) }
	if s.Verify(tok, vid) {
		t.Fatalf("expired token must be rejected")
	}
}

// TestTamperedTokenRejected proves any bit-flip in the payload or signature is
// rejected (constant-time signature check), and that a token signed with a
// different key does not verify.
func TestTamperedTokenRejected(t *testing.T) {
	s := NewSigner([]byte("test-secret-material"))
	vid := uuid.New()
	tok := s.Sign(vid, time.Hour)

	cases := []string{
		"",
		".",
		"notbase64.notbase64",
		tok + "x",               // trailing garbage on the signature
		"x" + tok,               // leading garbage on the payload
		tok[:len(tok)-2] + "AA", // corrupted signature tail
	}
	for _, bad := range cases {
		if s.Verify(bad, vid) {
			t.Fatalf("tampered/malformed token %q must be rejected", bad)
		}
	}

	// A token from a signer with a different secret must not verify here.
	other := NewSigner([]byte("a-completely-different-secret"))
	if s.Verify(other.Sign(vid, time.Hour), vid) {
		t.Fatalf("token signed with a different key must be rejected")
	}
}
