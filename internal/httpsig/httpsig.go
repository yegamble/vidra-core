// Package httpsig implements the "Signing HTTP Messages" (cavage draft) scheme
// used across the fediverse (Mastodon, PeerTube) for ActivityPub server-to-server
// authentication. It signs/verifies with RSA-SHA256 over the covered headers
// `(request-target) host date [digest]`, carries the body hash in a `Digest`
// header, and bounds the `Date` against clock skew.
//
// This is a standalone primitive: Sign is used for outbound delivery, Verify for
// inbound inbox requests. Verify takes a callback to resolve the signer's public
// key (fetched from its remote actor) so this package needs no network or DB.
package httpsig

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// algorithm is the only scheme we sign with and the only one we accept.
const algorithm = "rsa-sha256"

// defaultHeaders is the covered-header set used when signing a request with a body.
var defaultHeaders = []string{"(request-target)", "host", "date", "digest"}

// Signer signs outbound HTTP requests on behalf of a local actor.
type Signer struct {
	// KeyID is the actor's public key id, e.g.
	// https://videos.example/accounts/ada#main-key.
	KeyID string
	// Priv is the actor's RSA private key.
	Priv *rsa.PrivateKey
}

// Sign sets Date, Digest, and Signature headers on req. body is the exact request
// body (may be nil/empty; Digest is still set over the empty body so a receiver
// can bind it). Host is taken from req.Host (or the Host header) and must be set.
func (s Signer) Sign(req *http.Request, body []byte) error {
	if s.Priv == nil || s.KeyID == "" {
		return errors.New("httpsig: signer missing key")
	}
	if req.Header.Get("Date") == "" {
		req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	}
	req.Header.Set("Digest", digestHeader(body))

	signingString, err := buildSigningString(req, defaultHeaders)
	if err != nil {
		return err
	}
	hashed := sha256.Sum256([]byte(signingString))
	sig, err := rsa.SignPKCS1v15(rand.Reader, s.Priv, crypto.SHA256, hashed[:])
	if err != nil {
		return err
	}
	req.Header.Set("Signature", fmt.Sprintf(
		`keyId=%q,algorithm=%q,headers=%q,signature=%q`,
		s.KeyID, algorithm, strings.Join(defaultHeaders, " "),
		base64.StdEncoding.EncodeToString(sig),
	))
	return nil
}

// Verifier verifies inbound signed requests.
type Verifier struct {
	// ResolveKey returns the RSA public key for a signature's keyId (typically by
	// fetching the remote actor and parsing its publicKey).
	ResolveKey func(ctx context.Context, keyID string) (*rsa.PublicKey, error)
	// MaxSkew bounds how far the request Date may be from Now. Zero means 5 minutes.
	MaxSkew time.Duration
	// Now is the clock; nil means time.Now.
	Now func() time.Time
}

// Verify checks req's Signature header: the covered set must include
// (request-target), host and date (and digest when a body is present); the Date
// must be within skew; the Digest must match body; and the RSA signature must
// verify against the resolved key. It returns the verified keyId.
func (v Verifier) Verify(ctx context.Context, req *http.Request, body []byte) (string, error) {
	params, err := parseSignatureHeader(req.Header.Get("Signature"))
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(params["algorithm"], algorithm) && params["algorithm"] != "" {
		return "", fmt.Errorf("httpsig: unsupported algorithm %q", params["algorithm"])
	}
	keyID := params["keyId"]
	if keyID == "" || params["signature"] == "" || params["headers"] == "" {
		return "", errors.New("httpsig: signature missing keyId/headers/signature")
	}
	covered := strings.Fields(params["headers"])
	if err := requireCovered(covered, body); err != nil {
		return "", err
	}

	now := time.Now
	if v.Now != nil {
		now = v.Now
	}
	skew := v.MaxSkew
	if skew == 0 {
		skew = 5 * time.Minute
	}
	date, err := http.ParseTime(req.Header.Get("Date"))
	if err != nil {
		return "", fmt.Errorf("httpsig: bad Date header: %w", err)
	}
	if d := now().Sub(date); d > skew || d < -skew {
		return "", errors.New("httpsig: Date outside allowed skew")
	}

	if hasHeader(covered, "digest") {
		if req.Header.Get("Digest") != digestHeader(body) {
			return "", errors.New("httpsig: Digest does not match body")
		}
	}

	signingString, err := buildSigningString(req, covered)
	if err != nil {
		return "", err
	}
	sig, err := base64.StdEncoding.DecodeString(params["signature"])
	if err != nil {
		return "", fmt.Errorf("httpsig: bad signature base64: %w", err)
	}
	if v.ResolveKey == nil {
		return "", errors.New("httpsig: no key resolver configured")
	}
	pub, err := v.ResolveKey(ctx, keyID)
	if err != nil {
		return "", err
	}
	hashed := sha256.Sum256([]byte(signingString))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, hashed[:], sig); err != nil {
		return "", fmt.Errorf("httpsig: signature verification failed: %w", err)
	}
	return keyID, nil
}

// digestHeader returns the `SHA-256=<base64>` Digest header value for body.
func digestHeader(body []byte) string {
	sum := sha256.Sum256(body)
	return "SHA-256=" + base64.StdEncoding.EncodeToString(sum[:])
}

// buildSigningString assembles the cavage signing string for the covered headers.
func buildSigningString(req *http.Request, covered []string) (string, error) {
	var lines []string
	for _, h := range covered {
		name := strings.ToLower(h)
		switch name {
		case "(request-target)":
			target := req.URL.RequestURI()
			lines = append(lines, "(request-target): "+strings.ToLower(req.Method)+" "+target)
		case "host":
			host := req.Host
			if host == "" {
				host = req.Header.Get("Host")
			}
			if host == "" {
				return "", errors.New("httpsig: missing Host")
			}
			lines = append(lines, "host: "+host)
		default:
			val := req.Header.Get(name)
			if val == "" {
				return "", fmt.Errorf("httpsig: covered header %q not present", name)
			}
			lines = append(lines, name+": "+val)
		}
	}
	return strings.Join(lines, "\n"), nil
}

// requireCovered enforces the minimum covered-header set.
func requireCovered(covered []string, body []byte) error {
	for _, must := range []string{"(request-target)", "host", "date"} {
		if !hasHeader(covered, must) {
			return fmt.Errorf("httpsig: signature must cover %q", must)
		}
	}
	if len(body) > 0 && !hasHeader(covered, "digest") {
		return errors.New("httpsig: signature must cover \"digest\" when a body is present")
	}
	return nil
}

func hasHeader(covered []string, name string) bool {
	for _, h := range covered {
		if strings.EqualFold(h, name) {
			return true
		}
	}
	return false
}

// parseSignatureHeader parses a `k="v",k2="v2"` Signature header into a map. The
// base64 signature value may contain '=' padding but never a comma, so splitting
// on commas at the top level is safe.
func parseSignatureHeader(h string) (map[string]string, error) {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil, errors.New("httpsig: missing Signature header")
	}
	// Some senders prefix "Signature " (Authorization-style); tolerate it.
	h = strings.TrimPrefix(h, "Signature ")
	out := map[string]string{}
	for _, part := range strings.Split(h, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"`)
	}
	if len(out) == 0 {
		return nil, errors.New("httpsig: malformed Signature header")
	}
	return out, nil
}
