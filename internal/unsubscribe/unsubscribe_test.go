package unsubscribe

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

var secret = []byte("0123456789abcdef0123456789abcdef")

func TestRoundTrip(t *testing.T) {
	s := New(secret)
	id := uuid.New()
	got, ok := s.Verify(s.Token(id))
	if !ok || got != id {
		t.Fatalf("Verify(Token(id)) = %v, %v; want %v, true", got, ok, id)
	}
}

func TestTokenIsPerUser(t *testing.T) {
	s := New(secret)
	a, b := uuid.New(), uuid.New()
	if s.Token(a) == s.Token(b) {
		t.Fatal("two users share a token")
	}
}

func TestVerifyRejectsForgeries(t *testing.T) {
	s := New(secret)
	id := uuid.New()
	good := s.Token(id)
	body, sig, _ := strings.Cut(good, ".")
	other := s.Token(uuid.New())
	_, otherSig, _ := strings.Cut(other, ".")
	cases := map[string]string{
		"empty":           "",
		"no dot":          body,
		"garbage":         "!!!.???",
		"swapped sig":     body + "." + otherSig,
		"truncated sig":   body + "." + sig[:len(sig)-2],
		"extra segment":   good + ".x",
		"wrong key":       New([]byte("another-secret-another-secret-123")).Token(id),
		"oversized":       strings.Repeat("a", 4096),
		"empty signature": body + ".",
	}
	for name, tok := range cases {
		if got, ok := s.Verify(tok); ok {
			t.Errorf("%s: accepted forged token, user %v", name, got)
		}
	}
}

func TestEmptySecretYieldsNoSigner(t *testing.T) {
	// A nil Signer must mint nothing and verify nothing, so an instance wired
	// without a secret emits no header rather than a forgeable one.
	var s *Signer
	if New(nil) != nil || s.Token(uuid.New()) != "" {
		t.Fatal("empty secret must give a nil signer that mints no token")
	}
	if _, ok := s.Verify("x.y"); ok {
		t.Fatal("nil signer verified a token")
	}
}
