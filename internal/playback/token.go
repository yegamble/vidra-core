// Package playback mints and verifies short-lived, video-scoped playback tokens.
//
// A password-protected video (privacy=password, CORE-17 / W1.C2) is unlocked by
// POST /videos/{id}/unlock, which — on a correct password — returns a playback
// token. That token authorises reading exactly ONE video's media and detail for
// a bounded time (6h) and carries NO account identity. Every video read endpoint
// accepts it as `Authorization: Bearer <token>` or, because Safari native-HLS
// and progressive <video src> playback cannot set request headers, as a `?pt=`
// query parameter (the only token type ever honoured in a query string).
//
// The token is a SECRET: it is never logged, never placed in an error body, and
// compared in constant time.
package playback

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// domainSeparation binds the playback-token key to its single purpose. The key
// is derived from the JWT secret via HMAC over this label, so a playback token
// is cryptographically independent of — and can never be forged from or confused
// with — an account access token.
const domainSeparation = "vidra/playback-token/v1"

// Signer mints and verifies playback tokens. The signing key never leaves the
// type.
type Signer struct {
	key []byte
	now func() time.Time // injectable clock (tests override in-package)
}

// NewSigner derives a Signer's key from the given secret material (vidra-core
// passes the JWT secret) via HMAC domain separation.
func NewSigner(secret []byte) *Signer {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(domainSeparation))
	return &Signer{key: mac.Sum(nil), now: time.Now}
}

// Sign returns a token authorising reads of videoID until now+ttl. The format is
// base64url(payload) "." base64url(HMAC-SHA256(payload)) where payload is
// "<videoID>:<expUnix>"; both segments are RawURL-encoded so the token is a safe
// ?pt= query value.
func (s *Signer) Sign(videoID uuid.UUID, ttl time.Duration) string {
	exp := s.now().Add(ttl).Unix()
	payload := videoID.String() + ":" + strconv.FormatInt(exp, 10)
	sig := s.mac([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// Verify reports whether token is a valid, unexpired token scoped to videoID. It
// checks the signature in constant time BEFORE trusting any payload contents, and
// rejects tampering, a token minted for a different video, expiry, and any
// malformed input (it never panics).
func (s *Signer) Verify(token string, videoID uuid.UUID) bool {
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot >= len(token)-1 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return false
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return false
	}
	if !hmac.Equal(sig, s.mac(payload)) {
		return false
	}
	vid, exp, ok := parsePayload(string(payload))
	if !ok || vid != videoID {
		return false
	}
	return s.now().Unix() < exp
}

// mac computes HMAC-SHA256 of payload under the derived key.
func (s *Signer) mac(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// parsePayload splits "<videoID>:<expUnix>" into its parts.
func parsePayload(p string) (uuid.UUID, int64, bool) {
	i := strings.LastIndexByte(p, ':')
	if i <= 0 {
		return uuid.UUID{}, 0, false
	}
	vid, err := uuid.Parse(p[:i])
	if err != nil {
		return uuid.UUID{}, 0, false
	}
	exp, err := strconv.ParseInt(p[i+1:], 10, 64)
	if err != nil {
		return uuid.UUID{}, 0, false
	}
	return vid, exp, true
}
