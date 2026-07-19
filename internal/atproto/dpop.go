package atproto

// DPoP (Demonstrating Proof of Possession, RFC 9449) support for the ATProto
// identity-login flow (see resolve.go / oauth_client.go). A fresh P-256 keypair
// is minted per login attempt; every request to the auth server (PAR, token)
// carries a DPoP proof JWT signed by that key, binding the request to the key.
//
// Security posture:
//   - The key is EPHEMERAL: it exists only for the ~10 minute lifetime of one
//     login attempt and is discarded once identity is verified. We never call the
//     PDS with an access token, so the proofs never need an `ath` claim.
//   - The PRIVATE key round-trips between Begin and Complete inside the signed,
//     httpOnly state cookie (marshalPrivateJWK / parseDPoPKey). It is useless
//     after the single-use exchange and its only holder is the resource owner's
//     own browser, so this carries no standing secret.
//   - The private key material (the `d` scalar) is NEVER logged; only the public
//     JWK (x, y) is ever emitted, inside the proof header where it belongs.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"time"
)

// p256UncompressedLen is the length of an uncompressed SEC1 P-256 public point
// (0x04 || X(32) || Y(32)), the form ecdsa.PublicKey.Bytes emits.
const p256UncompressedLen = 65

// dpopKey is the per-flow P-256 signing key. The zero value is unusable; build
// one with newDPoPKey or parseDPoPKey.
type dpopKey struct {
	priv *ecdsa.PrivateKey
}

// newDPoPKey mints a fresh P-256 keypair for one login attempt.
func newDPoPKey() (*dpopKey, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("atproto: generate DPoP key: %w", err)
	}
	return &dpopKey{priv: priv}, nil
}

// GenerateDPoPKeyJWK mints a fresh per-flow DPoP key and returns its private JWK
// serialisation. The login service calls this at Begin, stashes the JWK in the
// signed state cookie, and passes it back into PushAuthRequest/ExchangeCode — so
// the concrete key type never leaves this package.
func GenerateDPoPKeyJWK() (string, error) {
	k, err := newDPoPKey()
	if err != nil {
		return "", err
	}
	return k.marshalPrivateJWK()
}

// b64url is the JOSE/JWK base64url (no padding) encoding used throughout DPoP.
func b64url(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// p256Coord left-pads an ECDSA signature integer (R or S) to the fixed 32-byte
// P-256 width, as required for the raw R||S JWS signature encoding. The `r`/`s`
// values come from ecdsa.Sign (fresh outputs, not key material).
func p256Coord(i *big.Int) []byte {
	b := i.Bytes()
	if len(b) >= 32 {
		return b
	}
	out := make([]byte, 32)
	copy(out[32-len(b):], b)
	return out
}

// pubXY returns the 32-byte big-endian X and Y coordinates of the key's public
// point via the supported ecdsa.PublicKey.Bytes API (no deprecated raw big.Int
// field access). The point is the uncompressed SEC1 form 0x04 || X || Y.
func (k *dpopKey) pubXY() (x, y []byte, err error) {
	pub, err := k.priv.PublicKey.Bytes()
	if err != nil {
		return nil, nil, err
	}
	if len(pub) != p256UncompressedLen || pub[0] != 0x04 {
		return nil, nil, errors.New("atproto: unexpected P-256 public key encoding")
	}
	return pub[1:33], pub[33:65], nil
}

// publicJWK returns the public key as the JWK map embedded in the DPoP proof
// header. Only the public coordinates are exposed — never the `d` scalar.
func (k *dpopKey) publicJWK() (map[string]string, error) {
	x, y, err := k.pubXY()
	if err != nil {
		return nil, err
	}
	return map[string]string{
		"kty": "EC",
		"crv": "P-256",
		"x":   b64url(x),
		"y":   b64url(y),
	}, nil
}

// privateJWK is the serialised private key threaded through the signed, httpOnly
// state cookie between Begin and Complete. It carries the `d` scalar and so is a
// secret for the (single-use, ~10 min) lifetime of the attempt.
type privateJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Y   string `json:"y"`
	D   string `json:"d"`
}

// marshalPrivateJWK serialises the key for the state round-trip. The private
// scalar comes from ecdsa.PrivateKey.Bytes (already the fixed 32-byte width) and
// the public coordinates from pubXY — both supported, non-deprecated APIs.
func (k *dpopKey) marshalPrivateJWK() (string, error) {
	x, y, err := k.pubXY()
	if err != nil {
		return "", err
	}
	d, err := k.priv.Bytes()
	if err != nil {
		return "", err
	}
	body, err := json.Marshal(privateJWK{
		Kty: "EC", Crv: "P-256",
		X: b64url(x),
		Y: b64url(y),
		D: b64url(d),
	})
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// parseDPoPKey rebuilds a key from a marshalPrivateJWK string. The key is
// reconstructed from the private scalar via ecdsa.ParseRawPrivateKey, which
// validates the scalar and DERIVES the public point — so a tampered x/y can never
// ride along. We additionally fail closed if the JWK's carried coordinates
// disagree with the ones the scalar actually derives.
func parseDPoPKey(s string) (*dpopKey, error) {
	var jwk privateJWK
	if err := json.Unmarshal([]byte(s), &jwk); err != nil {
		return nil, errors.New("atproto: malformed DPoP key JWK")
	}
	if jwk.Kty != "EC" || jwk.Crv != "P-256" {
		return nil, errors.New("atproto: unsupported DPoP key type")
	}
	xb, err1 := base64.RawURLEncoding.DecodeString(jwk.X)
	yb, err2 := base64.RawURLEncoding.DecodeString(jwk.Y)
	db, err3 := base64.RawURLEncoding.DecodeString(jwk.D)
	if err1 != nil || err2 != nil || err3 != nil {
		return nil, errors.New("atproto: malformed DPoP key coordinates")
	}
	priv, err := ecdsa.ParseRawPrivateKey(elliptic.P256(), db)
	if err != nil {
		return nil, errors.New("atproto: invalid DPoP private key")
	}
	k := &dpopKey{priv: priv}
	dx, dy, err := k.pubXY()
	if err != nil {
		return nil, errors.New("atproto: invalid DPoP public key")
	}
	if !bytes.Equal(xb, dx) || !bytes.Equal(yb, dy) {
		return nil, errors.New("atproto: DPoP public key does not match its private scalar")
	}
	return k, nil
}

// Proof builds a DPoP proof JWT for a single request: header
// {typ:"dpop+jwt", alg:"ES256", jwk:<public JWK>} and claims
// {jti, htm:<method>, htu:<url sans query/fragment>, iat}; the server nonce is
// included as the `nonce` claim once we have one (empty nonce omits it). The
// ES256 signature is the raw R||S (32+32 bytes) over base64url(header).base64url(claims).
func (k *dpopKey) Proof(method, rawURL, nonce string) (string, error) {
	jwk, err := k.publicJWK()
	if err != nil {
		return "", err
	}
	header := map[string]any{
		"typ": "dpop+jwt",
		"alg": "ES256",
		"jwk": jwk,
	}
	claims := map[string]any{
		"jti": randomJTI(),
		"htm": method,
		"htu": canonicalHTU(rawURL),
		"iat": time.Now().Unix(),
	}
	if nonce != "" {
		claims["nonce"] = nonce
	}
	hb, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	cb, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := b64url(hb) + "." + b64url(cb)
	digest := sha256.Sum256([]byte(signingInput))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, digest[:])
	if err != nil {
		return "", fmt.Errorf("atproto: sign DPoP proof: %w", err)
	}
	sig := append(p256Coord(r), p256Coord(s)...)
	return signingInput + "." + b64url(sig), nil
}

// canonicalHTU strips the query and fragment from a URL for the `htu` claim, per
// RFC 9449. A URL that will not parse is returned unchanged (the server rejects
// the proof; nothing sensitive leaks).
func canonicalHTU(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

// randomJTI returns a 128-bit URL-safe unique token for the proof `jti` claim.
func randomJTI() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("atproto: crypto/rand failed: %v", err))
	}
	return b64url(b)
}
