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
	"strings"

	"github.com/google/uuid"
)

// domain labels the derived key. Never reuse it for another purpose.
const domain = "vidra/list-unsubscribe/v1"

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
	if s == nil || token == "" || len(token) > maxTokenLen {
		return uuid.Nil, false
	}
	idPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return uuid.Nil, false
	}
	idBytes, err := base64.RawURLEncoding.DecodeString(idPart)
	if err != nil || len(idBytes) != len(uuid.UUID{}) {
		return uuid.Nil, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(sigPart)
	if err != nil || !hmac.Equal(sig, s.sum(idBytes)) {
		return uuid.Nil, false
	}
	var id uuid.UUID
	copy(id[:], idBytes)
	return id, true
}
