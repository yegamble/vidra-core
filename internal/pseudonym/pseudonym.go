// Package pseudonym derives keyed, day-scoped pseudonyms for request
// principals: the construction vidra uses whenever a per-visitor identifier
// must be aggregated over without becoming a durable tracking identifier.
//
// It exists because the obvious alternative is a privacy regression. A bare
// sha256("ip:"+addr) is trivially reversible — IPv4 is 2^32 and a rainbow table
// of the whole space is minutes of work — so anything derived that way must
// never be persisted or shipped to another service. Three properties make
// persistence and cross-service delivery defensible:
//
//  1. KEYED. The key is HMAC(secret, domain), so someone holding a dump of the
//     values and the whole IPv4 space still cannot invert one without the
//     secret.
//  2. DOMAIN-SEPARATED. The label binds a key to exactly one purpose, so a
//     value from one dataset can never be joined against another's — the same
//     reason internal/playback derives its signing key from a label.
//  3. DAY-SCOPED. The UTC date is inside the MAC'd bytes, so the same principal
//     is a different, unlinkable subject tomorrow.
//
// Rotation policy: callers key these from JWT_SECRET, so rotating it re-derives
// every key — the same principal becomes two unrelated subjects across the
// boundary and no historical value can be recomputed. That is intended, not a
// bug to fix with key versioning.
package pseudonym

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"
)

// Digester turns a principal string into a pseudonym. The key never leaves the
// type. A nil *Digester is a valid receiver yielding "", so a caller wired
// without a secret emits no field rather than failing or falling back to an
// unkeyed hash.
type Digester struct {
	key []byte
}

// New derives a Digester for one domain-separation label. An empty secret or an
// empty label yields nil: a weak or unlabelled key is worse than no value.
func New(secret []byte, domain string) *Digester {
	if len(secret) == 0 || domain == "" {
		return nil
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(domain))
	return &Digester{key: mac.Sum(nil)}
}

// Of returns the day-scoped pseudonym of principal as of now. principal is the
// caller's own namespaced string (e.g. "ip:203.0.113.7"); prefix distinct kinds
// differently so they cannot collide. It is never stored or logged here.
func (d *Digester) Of(now time.Time, principal string) string {
	if d == nil {
		return ""
	}
	if principal = strings.TrimSpace(principal); principal == "" {
		return ""
	}
	mac := hmac.New(sha256.New, d.key)
	// The day goes in FIRST so the MAC'd bytes cannot be split ambiguously:
	// "2026-08-23|ip:..." has exactly one reading.
	mac.Write([]byte(now.UTC().Format(time.DateOnly)))
	mac.Write([]byte{'|'})
	mac.Write([]byte(principal))
	return hex.EncodeToString(mac.Sum(nil))
}
