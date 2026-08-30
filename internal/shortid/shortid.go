// TWIN: a byte-compatible TypeScript implementation lives in vidra-user
// lib/short-id.ts — keep the golden vectors in both test suites identical.
//
// WHY the twin matters: the frontend mints and routes `/v/{sid}` share links,
// so core must decode a sid to exactly the UUID the frontend encoded from. If
// the two implementations ever disagree the breakage is asymmetric and silent
// — links keep working in the browser (the frontend redirects them itself)
// while every oEmbed unfurl of the same link 404s, or vice versa.
//
// Package shortid re-encodes a video's UUID as base58 using the alphabet
// Bitcoin uses (no 0/O/I/l, so a link survives being read aloud or typed by
// hand). 36 hyphenated hex characters become at most 22. It is a pure
// re-encoding of an id we already have: no new column, no migration, and every
// existing video gets a short link for free.
package shortid

import (
	"math/big"
	"strings"

	"github.com/google/uuid"
)

const alphabet = "123456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// 16 bytes never encode to more than 22 base58 characters (58^22 > 2^128) and
// never to fewer than 16 (all-zero bytes each cost one '1'). Bounding the
// length keeps a hostile URL from driving pointless big.Int work.
const (
	minLen = 16
	maxLen = 22
)

var base = big.NewInt(58)

// FromUUID encodes a UUID as its base58 short id. Leading zero BYTES carry no
// magnitude, so they are encoded positionally as leading '1's — otherwise
// 00…01 and 01 would collide.
func FromUUID(u uuid.UUID) string {
	leadingZeros := 0
	for leadingZeros < len(u) && u[leadingZeros] == 0 {
		leadingZeros++
	}

	n := new(big.Int).SetBytes(u[:])
	mod := new(big.Int)
	digits := make([]byte, 0, maxLen)
	for n.Sign() > 0 {
		n.DivMod(n, base, mod)
		digits = append(digits, alphabet[mod.Int64()])
	}

	var b strings.Builder
	b.Grow(leadingZeros + len(digits))
	for i := 0; i < leadingZeros; i++ {
		b.WriteByte('1')
	}
	for i := len(digits) - 1; i >= 0; i-- { // DivMod produced least-significant first
		b.WriteByte(digits[i])
	}
	return b.String()
}

// ToUUID decodes a base58 short id back to the UUID it names. It reports false
// for anything that is not the canonical encoding of exactly 16 bytes:
// off-alphabet characters, out-of-bounds lengths, and non-canonical spellings
// such as a valid id with an extra leading '1'. Canonical-only matters because
// two spellings of one video would be two URLs, two cache keys and two
// canonicals — and because the TypeScript twin rejects them too.
func ToUUID(sid string) (uuid.UUID, bool) {
	if len(sid) < minLen || len(sid) > maxLen {
		return uuid.Nil, false
	}

	leadingZeros := 0
	for leadingZeros < len(sid) && sid[leadingZeros] == '1' {
		leadingZeros++
	}

	n := new(big.Int)
	digit := new(big.Int)
	for i := leadingZeros; i < len(sid); i++ {
		// IndexByte also rejects the individual bytes of any multi-byte rune,
		// since the alphabet is pure ASCII.
		v := strings.IndexByte(alphabet, sid[i])
		if v < 0 {
			return uuid.Nil, false
		}
		n.Mul(n, base)
		n.Add(n, digit.SetInt64(int64(v)))
	}

	body := n.Bytes() // no leading zero bytes; those live in leadingZeros
	if leadingZeros+len(body) != 16 {
		return uuid.Nil, false
	}
	var out uuid.UUID
	copy(out[leadingZeros:], body)

	// Re-encode and compare: the length check above already implies canonical
	// form today, so this is the invariant stated structurally rather than
	// left as a happy accident of the arithmetic. Anyone who later loosens the
	// bounds still cannot smuggle in a second spelling of the same video.
	if FromUUID(out) != sid {
		return uuid.Nil, false
	}
	return out, true
}
