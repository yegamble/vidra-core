// Package playback mints and verifies short-lived, video-scoped playback tokens.
//
// A playback token authorises reading exactly ONE video's media and detail for a
// bounded time (6h) and carries NO account identity. Every video read endpoint
// accepts it as `Authorization: Bearer <token>` or, because Safari native-HLS and
// progressive <video src> playback cannot set request headers, as a `?pt=` query
// parameter (the only token type ever honoured in a query string).
//
// Two mint paths exist, and both go through a playback SESSION:
// POST /videos/{id}/playback-session (phase-4 item 1) hands one to a caller who
// has already cleared the video's authorization, and POST /videos/{id}/unlock
// (CORE-17 / W1.C2) hands one to a caller who just proved the password. A token
// is only ever minted for a video that actually needs one — see
// httpapi/playback_session.go for why minting one per viewer would be a delivery
// regression rather than a feature.
//
// The token is a SECRET: it is never logged, never placed in an error body, and
// compared in constant time.
//
// # Payload versions
//
// v1 (pre-phase-4) is "<videoID>:<expUnix>": no scope, no session, no version
// discriminator. v2 is "v2:<videoID>:<sessionID>:<scope>:<expUnix>". Only v2 is
// minted; v1 is still VERIFIED so the tokens outstanding at deploy time keep
// working for the rest of their 6h lifetime. The two grammars cannot be confused
// for one another in either direction: v1's parser reads everything before the
// last ':' as a UUID (which "v2:<id>:<sess>:<scope>" is not), and v2's parser
// requires the literal "v2:" prefix (which a v1 payload does not have). The
// version tag is inside the MAC'd bytes, so it cannot be edited either.
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
//
// The trailing "/v1" is this LABEL's version, not the payload's: changing it
// would re-derive the key and invalidate every outstanding token at once, which
// is the opposite of what a payload-grammar change should do. Payload versions
// are carried inside the signed bytes (payloadV2Prefix).
const domainSeparation = "vidra/playback-token/v1"

// payloadV2Prefix tags a v2 payload. It is part of the MAC'd bytes, so it is the
// version discriminator v1 payloads never had.
const payloadV2Prefix = "v2:"

// Scope names what a token authorises. It is a closed set: an unrecognised scope
// fails verification rather than being treated as "no restriction", so widening
// the set is always a deliberate act.
type Scope string

// ScopePlayback authorises reading one video's media and detail — exactly the
// authority the pre-phase-4 token carried, now named. Later scopes (a DRM
// license request, a live segment pull) join it here.
const ScopePlayback Scope = "playback"

func (sc Scope) valid() bool { return sc == ScopePlayback }

// Claims is a verified token's contents.
type Claims struct {
	// Version is the payload grammar the token was minted in (1 or 2).
	Version int
	// VideoID is the single video this token authorises.
	VideoID uuid.UUID
	// SessionID correlates this token with the playback session that minted it
	// (the QoE correlation key, phase-4 item 4). Zero for a v1 token: they were
	// minted before sessions existed.
	SessionID uuid.UUID
	// Scope is what the token authorises. A v1 token reports ScopePlayback,
	// which is what it has always granted.
	Scope Scope
	// ExpiresAt is the instant the token stops verifying.
	ExpiresAt time.Time
}

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

// Sign returns a v2 token authorising scope on videoID, under sessionID, until
// now+ttl. The wire format is unchanged — base64url(payload) "."
// base64url(HMAC-SHA256(payload)), both RawURL-encoded so the token is a safe
// ?pt= query value — only the payload grammar is versioned.
//
// An invalid scope mints nothing (empty string) rather than a token that would
// fail its own verification: a caller passing a scope this package does not know
// is a programming error, and an empty token fails every gate loudly.
func (s *Signer) Sign(videoID, sessionID uuid.UUID, scope Scope, ttl time.Duration) string {
	if !scope.valid() {
		return ""
	}
	exp := s.now().Add(ttl).Unix()
	payload := payloadV2Prefix + videoID.String() + ":" + sessionID.String() + ":" +
		string(scope) + ":" + strconv.FormatInt(exp, 10)
	sig := s.mac([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(sig)
}

// Verify reports whether token is a valid, unexpired token scoped to videoID.
func (s *Signer) Verify(token string, videoID uuid.UUID) bool {
	_, ok := s.VerifyClaims(token, videoID)
	return ok
}

// VerifyClaims verifies token against videoID and returns what it carries. It
// checks the signature in constant time BEFORE trusting any payload contents,
// and rejects tampering, a token minted for a different video, an unknown
// scope, expiry, and any malformed input (it never panics).
//
// Both payload versions are accepted here and only here — every caller sees one
// Claims shape, so the v1 compatibility window is invisible above this line.
func (s *Signer) VerifyClaims(token string, videoID uuid.UUID) (Claims, bool) {
	dot := strings.IndexByte(token, '.')
	if dot <= 0 || dot >= len(token)-1 {
		return Claims{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(token[:dot])
	if err != nil {
		return Claims{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(token[dot+1:])
	if err != nil {
		return Claims{}, false
	}
	if !hmac.Equal(sig, s.mac(payload)) {
		return Claims{}, false
	}
	claims, ok := parsePayload(string(payload))
	if !ok || claims.VideoID != videoID {
		return Claims{}, false
	}
	if !s.now().Before(claims.ExpiresAt) {
		return Claims{}, false
	}
	return claims, true
}

// mac computes HMAC-SHA256 of payload under the derived key.
func (s *Signer) mac(payload []byte) []byte {
	mac := hmac.New(sha256.New, s.key)
	mac.Write(payload)
	return mac.Sum(nil)
}

// parsePayload reads either grammar. The version prefix decides which parser
// runs, and neither parser tolerates the other's shape.
func parsePayload(p string) (Claims, bool) {
	if strings.HasPrefix(p, payloadV2Prefix) {
		return parsePayloadV2(strings.TrimPrefix(p, payloadV2Prefix))
	}
	return parsePayloadV1(p)
}

// parsePayloadV1 splits "<videoID>:<expUnix>". A v2 payload cannot reach a valid
// result here: everything before the last ':' has to parse as a UUID, and
// "v2:<videoID>:<sessionID>:<scope>" does not.
func parsePayloadV1(p string) (Claims, bool) {
	i := strings.LastIndexByte(p, ':')
	if i <= 0 {
		return Claims{}, false
	}
	vid, err := uuid.Parse(p[:i])
	if err != nil {
		return Claims{}, false
	}
	exp, err := strconv.ParseInt(p[i+1:], 10, 64)
	if err != nil {
		return Claims{}, false
	}
	return Claims{
		Version:   1,
		VideoID:   vid,
		Scope:     ScopePlayback,
		ExpiresAt: time.Unix(exp, 0),
	}, true
}

// parsePayloadV2 splits "<videoID>:<sessionID>:<scope>:<expUnix>" (the "v2:"
// prefix already removed). Exactly four fields: a scope is drawn from a closed
// set that contains no ':', so a payload with more separators is malformed
// rather than "a scope with a colon in it".
func parsePayloadV2(p string) (Claims, bool) {
	parts := strings.Split(p, ":")
	if len(parts) != 4 {
		return Claims{}, false
	}
	vid, err := uuid.Parse(parts[0])
	if err != nil {
		return Claims{}, false
	}
	sid, err := uuid.Parse(parts[1])
	if err != nil {
		return Claims{}, false
	}
	scope := Scope(parts[2])
	if !scope.valid() {
		return Claims{}, false
	}
	exp, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return Claims{}, false
	}
	return Claims{
		Version:   2,
		VideoID:   vid,
		SessionID: sid,
		Scope:     scope,
		ExpiresAt: time.Unix(exp, 0),
	}, true
}
