// Package unsubscribe signs the per-user token behind the one-click
// List-Unsubscribe link (RFC 8058) in notification email.
//
// The token is "<user id>.<mac>", both base64url, the MAC an HMAC-SHA256 over
// the user id under a key DERIVED from the instance's JWT secret for this one
// purpose. Deriving (rather than keying with the raw secret) is the same
// domain separation internal/pseudonym and internal/playback use: a token
// minted here can never be replayed as, or forged from, anything else signed
// with JWT_SECRET, and no new secret has to be provisioned.
//
// It deliberately has NO expiry. A mailbox provider's unsubscribe button must
// keep working on a message that is months old, and the only thing the token
// can do is switch email OFF for the user it names — it grants no read access,
// no session and no way to turn anything on. The cost of a leaked link is one
// user's notification email stopping, which that user can undo in settings.
// Rotating JWT_SECRET invalidates every outstanding link (the old mail's
// buttons then do nothing; settings remain the fallback the body names).
package unsubscribe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

// domain labels the derived key. Never reuse it for another purpose.
const domain = "vidra/list-unsubscribe/v1"

// enc decodes STRICTLY: non-zero trailing bits are rejected, so exactly one
// string decodes to a given user id and MAC. Without it a second, differently
// spelled token for the same user would verify (malleable tokens).
var enc = base64.RawURLEncoding.Strict()

// maxTokenLen bounds what Verify will even decode: a real token is ~70 bytes.
const maxTokenLen = 256

// Signer mints and verifies unsubscribe tokens. A nil *Signer is valid and
// inert: it mints "" and verifies nothing.
type Signer struct{ key []byte }

// New derives a Signer from the instance secret. An empty secret yields nil,
// because a weak key is worse than no header.
func New(secret []byte) *Signer {
	if len(secret) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(domain))
	return &Signer{key: mac.Sum(nil)}
}

func (s *Signer) sum(id []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(id)
	return mac.Sum(nil)
}

// Token returns the signed token for a user, or "" on a nil Signer.
func (s *Signer) Token(userID uuid.UUID) string {
	if s == nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(userID[:]) + "." +
		base64.RawURLEncoding.EncodeToString(s.sum(userID[:]))
}

// Verify returns the user a token names, or ok=false for anything malformed or
// not signed by this instance. The comparison is hmac.Equal (constant time).
func (s *Signer) Verify(token string) (uuid.UUID, bool) {
	// encoding/base64 skips CR and LF even in strict mode, so a token with a line
	// break embedded would otherwise decode to the canonical bytes.
	if s == nil || token == "" || len(token) > maxTokenLen || strings.ContainsAny(token, "\r\n") {
		return uuid.Nil, false
	}
	idPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return uuid.Nil, false
	}
	idBytes, err := enc.DecodeString(idPart)
	if err != nil || len(idBytes) != len(uuid.UUID{}) {
		return uuid.Nil, false
	}
	sig, err := enc.DecodeString(sigPart)
	if err != nil || !hmac.Equal(sig, s.sum(idBytes)) {
		return uuid.Nil, false
	}
	var id uuid.UUID
	copy(id[:], idBytes)
	return id, true
}

// URL builds the one-click unsubscribe URL for a user: publicBase (the instance
// origin) + apiPath (the route, API base included) + the signed token. It
// returns "" when there is nothing safe to build — no signer, no origin — so a
// caller sends no header rather than a broken one.
func (s *Signer) URL(publicBase, apiPath string, userID uuid.UUID) string {
	base := strings.TrimRight(publicBase, "/")
	tok := s.Token(userID)
	if base == "" || tok == "" {
		return ""
	}
	return base + apiPath + "?token=" + url.QueryEscape(tok)
}
