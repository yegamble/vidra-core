package atproto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
)

// parseProof splits a DPoP proof JWT into its decoded header and claims maps and
// the raw signing input + signature, failing the test on any structural problem.
func parseProof(t *testing.T, proof string) (header, claims map[string]any, signingInput string, sig []byte) {
	t.Helper()
	parts := strings.Split(proof, ".")
	if len(parts) != 3 {
		t.Fatalf("proof has %d segments, want 3", len(parts))
	}
	hb, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatalf("decode header: %v", err)
	}
	cb, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode claims: %v", err)
	}
	sig, err = base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatalf("decode signature: %v", err)
	}
	if err := json.Unmarshal(hb, &header); err != nil {
		t.Fatalf("unmarshal header: %v", err)
	}
	if err := json.Unmarshal(cb, &claims); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	return header, claims, parts[0] + "." + parts[1], sig
}

// verifyProofSig reconstructs the public key from the proof header jwk and
// verifies the raw R||S ES256 signature over the signing input.
func verifyProofSig(t *testing.T, header map[string]any, signingInput string, sig []byte) {
	t.Helper()
	jwk, ok := header["jwk"].(map[string]any)
	if !ok {
		t.Fatal("header carries no jwk object")
	}
	if jwk["kty"] != "EC" || jwk["crv"] != "P-256" {
		t.Fatalf("jwk kty/crv = %v/%v, want EC/P-256", jwk["kty"], jwk["crv"])
	}
	xb, err := base64.RawURLEncoding.DecodeString(jwk["x"].(string))
	if err != nil {
		t.Fatalf("decode x: %v", err)
	}
	yb, err := base64.RawURLEncoding.DecodeString(jwk["y"].(string))
	if err != nil {
		t.Fatalf("decode y: %v", err)
	}
	pub := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(xb), Y: new(big.Int).SetBytes(yb)}
	if len(sig) != 64 {
		t.Fatalf("signature is %d bytes, want 64 (raw R||S)", len(sig))
	}
	r := new(big.Int).SetBytes(sig[:32])
	s := new(big.Int).SetBytes(sig[32:])
	digest := sha256.Sum256([]byte(signingInput))
	if !ecdsa.Verify(pub, digest[:], r, s) {
		t.Fatal("ES256 signature does not verify against the header jwk")
	}
}

func TestDPoPProofStructureAndSignature(t *testing.T) {
	key, err := newDPoPKey()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := key.Proof("POST", "https://auth.example/token?foo=bar#frag", "")
	if err != nil {
		t.Fatal(err)
	}
	header, claims, signingInput, sig := parseProof(t, proof)

	if header["typ"] != "dpop+jwt" || header["alg"] != "ES256" {
		t.Errorf("header typ/alg = %v/%v, want dpop+jwt/ES256", header["typ"], header["alg"])
	}
	if claims["htm"] != "POST" {
		t.Errorf("htm = %v, want POST", claims["htm"])
	}
	// htu must be stripped of query and fragment.
	if claims["htu"] != "https://auth.example/token" {
		t.Errorf("htu = %v, want the URL without query/fragment", claims["htu"])
	}
	if claims["jti"] == nil || claims["jti"] == "" {
		t.Error("proof must carry a jti")
	}
	if _, ok := claims["iat"]; !ok {
		t.Error("proof must carry an iat")
	}
	// No nonce was supplied → the claim must be absent (not empty).
	if _, present := claims["nonce"]; present {
		t.Error("nonce claim must be omitted when no server nonce is known")
	}
	verifyProofSig(t, header, signingInput, sig)
}

func TestDPoPProofIncludesNonceWhenGiven(t *testing.T) {
	key, err := newDPoPKey()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := key.Proof("POST", "https://auth.example/par", "server-nonce-xyz")
	if err != nil {
		t.Fatal(err)
	}
	_, claims, _, _ := parseProof(t, proof)
	if claims["nonce"] != "server-nonce-xyz" {
		t.Errorf("nonce = %v, want server-nonce-xyz", claims["nonce"])
	}
}

func TestDPoPKeyJWKRoundTrip(t *testing.T) {
	key, err := newDPoPKey()
	if err != nil {
		t.Fatal(err)
	}
	jwk, err := key.marshalPrivateJWK()
	if err != nil {
		t.Fatal(err)
	}
	// The serialised private key must carry the d scalar (it is what makes the
	// round-trip usable) and the public coordinates.
	if !strings.Contains(jwk, `"d"`) || !strings.Contains(jwk, `"x"`) {
		t.Fatalf("marshalled JWK missing coordinates: %s", jwk)
	}
	restored, err := parseDPoPKey(jwk)
	if err != nil {
		t.Fatalf("parseDPoPKey: %v", err)
	}
	// A proof signed by the restored key still verifies against its own public jwk.
	proof, err := restored.Proof("POST", "https://auth.example/token", "n")
	if err != nil {
		t.Fatal(err)
	}
	header, _, signingInput, sig := parseProof(t, proof)
	verifyProofSig(t, header, signingInput, sig)
}

func TestParseDPoPKeyRejectsTampered(t *testing.T) {
	for name, jwk := range map[string]string{
		"not json":        "{",
		"wrong curve":     `{"kty":"EC","crv":"P-384","x":"AA","y":"AA","d":"AA"}`,
		"bad base64":      `{"kty":"EC","crv":"P-256","x":"!!","y":"AA","d":"AA"}`,
		"point off curve": `{"kty":"EC","crv":"P-256","x":"AQAB","y":"AQAB","d":"AQAB"}`,
	} {
		if _, err := parseDPoPKey(jwk); err == nil {
			t.Errorf("%s: parseDPoPKey accepted a tampered JWK", name)
		}
	}
}
