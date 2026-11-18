package httpsig

import (
	"bytes"
	"context"
	"crypto/rsa"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// FuzzParseSignatureHeader asserts the hand-written Signature-header parser never
// panics on arbitrary input (the header is attacker-controlled on inbound
// federation requests) and, when it succeeds, always yields a non-empty map.
func FuzzParseSignatureHeader(f *testing.F) {
	f.Add(`keyId="https://h/a#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="ab==cd/+"`)
	f.Add("")
	f.Add("Signature garbage")
	f.Add("=,=,=")
	f.Add(`keyId=,signature="`)
	f.Add(`,,,,`)
	f.Add(`keyId="k" algorithm="rsa-sha256"`)

	f.Fuzz(func(t *testing.T, h string) {
		out, err := parseSignatureHeader(h)
		if err == nil && len(out) == 0 {
			t.Fatalf("parseSignatureHeader(%q) returned no error but an empty map", h)
		}
	})
}

// FuzzVerify drives the full inbound-verification path with an arbitrary
// Signature header and body: it must always return (never panic), regardless of
// how malformed the input is. The resolver always fails, so any parse that gets
// far enough still ends in a clean error.
func FuzzVerify(f *testing.F) {
	f.Add(`keyId="k#main-key",algorithm="rsa-sha256",headers="(request-target) host date digest",signature="AA=="`, []byte("{}"))
	f.Add("", []byte(""))
	f.Add("not-a-signature", []byte("body"))
	f.Add(`keyId="k",algorithm="hs2019",headers="(request-target) host date",signature="Zz"`, []byte(""))

	v := Verifier{ResolveKey: func(context.Context, string) (*rsa.PublicKey, error) {
		return nil, errors.New("no key")
	}}
	f.Fuzz(func(t *testing.T, sig string, body []byte) {
		req := httptest.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
		req.Header.Set("Signature", sig)
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
		// Must not panic; result is irrelevant.
		_, _ = v.Verify(context.Background(), req, body)
	})
}

// FuzzCheckKeyPolicy asserts the key-policy gate never panics on an arbitrary
// modulus/exponent pair and never accepts one outside its stated bounds. The
// seeds are the shapes it exists to refuse.
func FuzzCheckKeyPolicy(f *testing.F) {
	f.Add(bytes.Repeat([]byte{0xff}, 256), 65537)  // 2048-bit, in policy
	f.Add(bytes.Repeat([]byte{0xff}, 1025), 65537) // 8200-bit, oversize
	f.Add(bytes.Repeat([]byte{0xff}, 128), 65537)  // 1024-bit, undersize
	f.Add(bytes.Repeat([]byte{0xff}, 256), 1<<40+1)
	f.Add(bytes.Repeat([]byte{0xff}, 256), 1)
	f.Add([]byte{}, 0)

	f.Fuzz(func(t *testing.T, n []byte, e int) {
		pub := &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: e}
		if CheckKeyPolicy(pub) != nil {
			return
		}
		if b := pub.N.BitLen(); b < MinRSABits || b > MaxRSABits || e < 3 || e%2 == 0 || e > maxRSAExponent {
			t.Fatalf("accepted %d-bit modulus with e=%d", b, e)
		}
	})
}
