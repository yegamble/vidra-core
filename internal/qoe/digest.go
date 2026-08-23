package qoe

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Viewer identity in a PERSISTED telemetry row, and why it is not the one the
// codebase already has.
//
// httpapi.viewerKey is a bare, unsalted sha256("ip:" + RealIP). Against a known
// IP that is trivially reversible — the space of IPv4 addresses is 2^32 and a
// rainbow table of every one of them is a few minutes of work. It survives today
// only because it is NEVER PERSISTED: it exists as a Redis key fragment with a
// 1-hour TTL and nothing more. Copying that construction into a table with
// 7 days of retention would be a privacy regression dressed up as reuse, so this
// package does not reuse it.
//
// What is stored instead is a KEYED digest with three properties:
//
//  1. Keyed. The key is derived from the JWT secret by HMAC over a domain
//     separation label, exactly as internal/playback/token.go derives its
//     signing key. Someone holding a dump of qoe_events and the whole IPv4 space
//     still cannot invert a digest without the secret.
//
//  2. Domain-separated. The label binds the key to this one purpose, so a QoE
//     digest can never be confused with, or derived from, a playback token or an
//     account token — the same reason playback tokens do it.
//
//  3. DAY-SCOPED. The UTC date is inside the MAC'd bytes, so the same viewer
//     produces a different digest tomorrow. Within one day an investigation can
//     still answer the only question this field exists for — "was that rebuffer
//     spike one viewer or a thousand?" — and across days nothing links. A viewer
//     cannot be followed through the table, which is what a bare per-viewer hash
//     would have allowed for its entire retention window.
//
// # Rotation policy
//
// The key is derived from JWT_SECRET. Rotating that secret re-derives it, so
// every digest written after the rotation is unrelated to every digest written
// before it — the same viewer appears as two unrelated viewers across the
// boundary, and no historical digest can ever be recomputed again.
//
// That is a real, intended consequence and not a bug to be worked around: it
// means a secret rotation is also a telemetry-linkability reset. It does NOT
// affect any count, percentile or rollup, because nothing aggregates by viewer —
// the digest is only ever compared for equality inside one day, and 7-day raw
// retention bounds the confusion to the days spanning the rotation. Nobody
// should add a key-versioning scheme to "fix" this without first deciding they
// want cross-rotation viewer linkability, which is the opposite of the point.

// digestDomainSeparation binds the digest key to this single purpose. The
// trailing "/v1" is the LABEL's version: changing it re-derives the key and
// makes every existing digest incomparable, exactly as a secret rotation does.
const digestDomainSeparation = "vidra/qoe-viewer-digest/v1"

// Digester turns a request principal into a stored viewer digest. The key never
// leaves the type. A nil *Digester is a valid receiver that yields the empty
// digest, so a server wired without one records events with no viewer field
// rather than failing.
type Digester struct {
	key []byte
}

// NewDigester derives a Digester from the given secret material (cmd/api passes
// the JWT secret). An empty secret yields nil, which degrades to empty digests
// rather than to an unkeyed hash — a weak key here is worse than no field.
func NewDigester(secret []byte) *Digester {
	if len(secret) == 0 {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(digestDomainSeparation))
	return &Digester{key: mac.Sum(nil)}
}

// Viewer returns the day-scoped digest for one request.
//
// authed selects which principal is digested: the account id when the caller is
// signed in, the client IP otherwise. The two are prefixed differently so an
// account id that happened to look like an address could not collide with one.
//
// The IP is never stored, never logged, and never leaves this function.
func (d *Digester) Viewer(now time.Time, authed bool, userID uuid.UUID, clientIP string) string {
	if d == nil {
		return ""
	}
	var principal string
	switch {
	case authed && userID != uuid.Nil:
		principal = "u:" + userID.String()
	default:
		clientIP = strings.TrimSpace(clientIP)
		if clientIP == "" {
			return ""
		}
		principal = "ip:" + clientIP
	}
	mac := hmac.New(sha256.New, d.key)
	// The day goes in FIRST so that the value being MAC'd cannot be split
	// ambiguously: "2026-08-23|ip:..." has exactly one reading.
	mac.Write([]byte(now.UTC().Format(time.DateOnly)))
	mac.Write([]byte{'|'})
	mac.Write([]byte(principal))
	return hex.EncodeToString(mac.Sum(nil))
}
