package unsubscribe

import (
	"net/url"
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

// Only one spelling of a token may verify. The id part is 16 bytes = 22 base64
// characters with 4 spare bits, the signature 32 bytes = 43 characters with 2;
// a lenient decoder accepts any value in those bits, so each token would have
// many valid spellings.
func TestVerifyRejectsNonCanonicalEncodings(t *testing.T) {
	s := New(secret)
	id := uuid.New()
	idPart, sigPart, _ := strings.Cut(s.Token(id), ".")
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"
	bump := func(p string) string { // same decoded bytes under a lenient decoder
		i := strings.IndexByte(alphabet, p[len(p)-1])
		return p[:len(p)-1] + string(alphabet[i^1])
	}
	for name, tok := range map[string]string{
		"id trailing bits":  bump(idPart) + "." + sigPart,
		"sig trailing bits": idPart + "." + bump(sigPart),
		"padded id":         idPart + "==." + sigPart,
		"std alphabet":      strings.NewReplacer("-", "+", "_", "/").Replace(idPart) + "." + sigPart,
	} {
		if tok == s.Token(id) {
			continue // the mutation happened to be a no-op for this alphabet char
		}
		if _, ok := s.Verify(tok); ok {
			t.Errorf("%s: non-canonical token accepted", name)
		}
	}
}

// encoding/base64 skips CR and LF, so an embedded line break would decode to
// the canonical bytes unless Verify refuses it outright.
func TestVerifyRejectsEmbeddedLineBreaks(t *testing.T) {
	s := New(secret)
	good := s.Token(uuid.New())
	for _, ins := range []string{"\n", "\r", "\r\n"} {
		for _, pos := range []int{3, strings.IndexByte(good, '.') + 2, len(good)} {
			if _, ok := s.Verify(good[:pos] + ins + good[pos:]); ok {
				t.Errorf("token with %q at %d accepted", ins, pos)
			}
		}
	}
}

func TestURL(t *testing.T) {
	s := New(secret)
	id := uuid.New()
	got := s.URL("https://vidra.example/", "/api/v1/notifications/unsubscribe", id)
	want := "https://vidra.example/api/v1/notifications/unsubscribe?token=" + url.QueryEscape(s.Token(id))
	if got != want {
		t.Errorf("URL = %q, want %q", got, want)
	}
	if s.URL("", "/x", id) != "" || (*Signer)(nil).URL("https://a.example", "/x", id) != "" {
		t.Error("URL must be empty without an origin or a signer")
	}
	// The built URL's token verifies after the query round trip.
	u, _ := url.Parse(got)
	if v, ok := s.Verify(u.Query().Get("token")); !ok || v != id {
		t.Error("token in the built URL does not verify")
	}
}
