package httpsig

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"strings"
	"testing"
	"time"
)

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	return k
}

func newReq(t *testing.T, body []byte) *http.Request {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, "https://videos.example/inbox", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	return req
}

func resolver(pub *rsa.PublicKey) func(context.Context, string) (*rsa.PublicKey, error) {
	return func(context.Context, string) (*rsa.PublicKey, error) { return pub, nil }
}

func TestSignVerifyRoundTrip(t *testing.T) {
	key := testKey(t)
	body := []byte(`{"type":"Create"}`)
	req := newReq(t, body)

	signer := Signer{KeyID: "https://videos.example/accounts/ada#main-key", Priv: key}
	if err := signer.Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if req.Header.Get("Signature") == "" || req.Header.Get("Digest") == "" || req.Header.Get("Date") == "" {
		t.Fatal("Sign did not set the expected headers")
	}

	v := Verifier{ResolveKey: resolver(&key.PublicKey)}
	keyID, err := v.Verify(context.Background(), req, body)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if keyID != signer.KeyID {
		t.Errorf("keyID = %q, want %q", keyID, signer.KeyID)
	}
}

func TestVerifyRejectsTamperedBody(t *testing.T) {
	key := testKey(t)
	body := []byte(`{"type":"Create"}`)
	req := newReq(t, body)
	if err := (Signer{KeyID: "k", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	v := Verifier{ResolveKey: resolver(&key.PublicKey)}
	if _, err := v.Verify(context.Background(), req, []byte(`{"type":"Delete"}`)); err == nil {
		t.Error("Verify accepted a tampered body")
	}
}

func TestVerifyRejectsTamperedSignature(t *testing.T) {
	key := testKey(t)
	body := []byte("hi")
	req := newReq(t, body)
	if err := (Signer{KeyID: "k", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	sig := req.Header.Get("Signature")
	// Corrupt the signature value's last base64 char.
	req.Header.Set("Signature", sig[:len(sig)-2]+"A\"")
	v := Verifier{ResolveKey: resolver(&key.PublicKey)}
	if _, err := v.Verify(context.Background(), req, body); err == nil {
		t.Error("Verify accepted a tampered signature")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	key := testKey(t)
	other := testKey(t)
	body := []byte("hi")
	req := newReq(t, body)
	if err := (Signer{KeyID: "k", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	v := Verifier{ResolveKey: resolver(&other.PublicKey)}
	if _, err := v.Verify(context.Background(), req, body); err == nil {
		t.Error("Verify accepted a signature under the wrong key")
	}
}

func TestVerifyRejectsStaleDate(t *testing.T) {
	key := testKey(t)
	body := []byte("hi")
	req := newReq(t, body)
	// Pre-set an old Date so the signature covers it and Sign won't overwrite.
	req.Header.Set("Date", time.Now().Add(-1*time.Hour).UTC().Format(http.TimeFormat))
	if err := (Signer{KeyID: "k", Priv: key}).Sign(req, body); err != nil {
		t.Fatalf("Sign: %v", err)
	}
	v := Verifier{ResolveKey: resolver(&key.PublicKey), MaxSkew: 5 * time.Minute}
	if _, err := v.Verify(context.Background(), req, body); err == nil {
		t.Error("Verify accepted a stale Date")
	}
}

func TestVerifyRequiresDigestCoverageWithBody(t *testing.T) {
	// A signature that covers only (request-target) host date, but the request has
	// a body — must be rejected before any key work (an attacker could swap the body).
	req := newReq(t, []byte("payload"))
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("Signature", `keyId="k",algorithm="rsa-sha256",headers="(request-target) host date",signature="AAAA"`)
	v := Verifier{ResolveKey: func(context.Context, string) (*rsa.PublicKey, error) {
		t.Fatal("resolver should not be reached")
		return nil, nil
	}}
	if _, err := v.Verify(context.Background(), req, []byte("payload")); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Errorf("want a digest-coverage error, got %v", err)
	}
}

func TestVerifyRejectsUnsupportedAlgorithm(t *testing.T) {
	req := newReq(t, nil)
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("Signature", `keyId="k",algorithm="hs2019",headers="(request-target) host date",signature="AAAA"`)
	v := Verifier{ResolveKey: resolver(&testKey(t).PublicKey)}
	if _, err := v.Verify(context.Background(), req, nil); err == nil || !strings.Contains(err.Error(), "algorithm") {
		t.Errorf("want an algorithm error, got %v", err)
	}
}

func TestVerifyRejectsMissingSignatureHeader(t *testing.T) {
	req := newReq(t, nil)
	v := Verifier{ResolveKey: resolver(&testKey(t).PublicKey)}
	if _, err := v.Verify(context.Background(), req, nil); err == nil {
		t.Error("Verify accepted a request with no Signature header")
	}
}

func TestParseSignatureHeader(t *testing.T) {
	p, err := parseSignatureHeader(`keyId="https://h/a#main-key",algorithm="rsa-sha256",headers="(request-target) host date",signature="ab==cd/+"`)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if p["keyId"] != "https://h/a#main-key" {
		t.Errorf("keyId = %q", p["keyId"])
	}
	if p["signature"] != "ab==cd/+" {
		t.Errorf("signature = %q (base64 padding/chars must survive)", p["signature"])
	}
	if _, err := parseSignatureHeader(""); err == nil {
		t.Error("empty header should error")
	}
}
