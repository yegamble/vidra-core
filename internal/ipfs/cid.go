// Package ipfs is the Kubo RPC client + CID contract for hybrid IPFS media
// mirroring (fix_plan P19, .ralph/specs/ipfs-media.md). IPFS is a MIRROR SIDECAR:
// local/S3 stays authoritative; when enabled, eligible ALREADY-PUBLIC media is
// added+pinned here and its CID exposed additively.
//
// This file is the CID validation contract. A CID appears in gateway URLs, API
// responses, and DHT provider records — it is a capability-like public handle —
// so every CID that crosses a trust boundary (a URL we build, a pin/unpin/fetch
// argument) is validated first. The rules are the archived predecessor's contract
// (AUDIT §5), re-implemented clean, not copied: CIDv1-only (CIDv0 "Qm…" rejected),
// a codec whitelist, a length cap, path-traversal / control-char / URL-encoding
// rejection, and a SHA-256 / Blake2b multihash allowlist over lowercase base32.
package ipfs

import (
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"strings"
)

// maxCIDLen caps CID length to bound DoS via absurdly long inputs. A CIDv1 with a
// 32-byte digest is ~59 chars; 256 is generous headroom for larger digests.
const maxCIDLen = 256

// Multicodec + multihash constants (the subset we allow).
const (
	codecRaw     = 0x55 // raw binary leaf
	codecDagPB   = 0x70 // UnixFS / directory (dag-pb)
	codecDagCBOR = 0x71 // dag-cbor

	mhSHA2256    = 0x12   // sha2-256
	mhBlake2b256 = 0xb220 // blake2b-256
)

// allowedCodecs is the content-type whitelist. raw = file leaves, dag-pb = UnixFS
// files/directories (the HLS wrap tree), dag-cbor = structured DAGs. Anything else
// (dag-json, git, eth, …) is refused — we only mirror media bytes.
var allowedCodecs = map[uint64]bool{
	codecRaw:     true,
	codecDagPB:   true,
	codecDagCBOR: true,
}

// allowedMultihash maps an accepted multihash function code to its exact digest
// length in bytes. Only the two hashes Kubo emits for our adds are accepted.
var allowedMultihash = map[uint64]int{
	mhSHA2256:    32,
	mhBlake2b256: 32,
}

// base32Lower is multibase base32 ('b' prefix): RFC 4648 lowercase, no padding.
// CIDv1 in its canonical string form is always base32-lower, so we accept only
// that representation (an uppercase 'B'/base32-upper CID is rejected).
var base32Lower = base32.NewEncoding("abcdefghijklmnopqrstuvwxyz234567").WithPadding(base32.NoPadding)

// CID validation errors. They are intentionally coarse (no echoing the input) so a
// caller can log the class without leaking a would-be capability handle.
var (
	ErrCIDEmpty     = errors.New("ipfs: empty CID")
	ErrCIDTooLong   = errors.New("ipfs: CID exceeds max length")
	ErrCIDBadChar   = errors.New("ipfs: CID contains a non-printable or non-ASCII character")
	ErrCIDTraversal = errors.New("ipfs: CID contains a path separator, '..', or URL-encoding")
	ErrCIDv0        = errors.New("ipfs: CIDv0 is not supported (CIDv1 only)")
	ErrCIDNotBase32 = errors.New("ipfs: CID is not lowercase base32 (missing 'b' multibase prefix)")
	ErrCIDDecode    = errors.New("ipfs: CID is not decodable")
	ErrCIDVersion   = errors.New("ipfs: CID is not version 1")
	ErrCIDCodec     = errors.New("ipfs: CID codec is not in the whitelist")
	ErrCIDMultihash = errors.New("ipfs: CID multihash function is not allowed")
	ErrCIDDigestLen = errors.New("ipfs: CID multihash digest length is invalid")
)

// ValidateCID reports whether cid is a well-formed, policy-conformant CIDv1. It is
// the single gate every CID passes before it is pinned, unpinned, fetched, or
// interpolated into a gateway URL.
func ValidateCID(cid string) error {
	if cid == "" {
		return ErrCIDEmpty
	}
	if len(cid) > maxCIDLen {
		return ErrCIDTooLong
	}
	// Printable ASCII only — rejects control chars, whitespace, NUL, and any
	// non-ASCII byte before we ever interpret the value.
	for i := 0; i < len(cid); i++ {
		if cid[i] < 0x21 || cid[i] > 0x7e {
			return ErrCIDBadChar
		}
	}
	// Defense in depth against a CID being spliced into a filesystem or URL path:
	// no separators, no parent-dir traversal, no percent-encoding.
	if strings.ContainsAny(cid, "/\\%") || strings.Contains(cid, "..") {
		return ErrCIDTraversal
	}
	// CIDv0 is base58btc and starts with "Qm" — explicitly unsupported.
	if strings.HasPrefix(cid, "Qm") {
		return ErrCIDv0
	}
	// CIDv1 canonical string is multibase base32-lower ('b' prefix).
	if cid[0] != 'b' {
		return ErrCIDNotBase32
	}
	raw, err := base32Lower.DecodeString(cid[1:])
	if err != nil {
		return ErrCIDDecode
	}

	// Parse <version><codec><mh-code><mh-len><digest>.
	version, n := binary.Uvarint(raw)
	if n <= 0 {
		return ErrCIDDecode
	}
	if version != 1 {
		return ErrCIDVersion
	}
	raw = raw[n:]

	codec, n := binary.Uvarint(raw)
	if n <= 0 {
		return ErrCIDDecode
	}
	if !allowedCodecs[codec] {
		return ErrCIDCodec
	}
	raw = raw[n:]

	mhCode, n := binary.Uvarint(raw)
	if n <= 0 {
		return ErrCIDDecode
	}
	wantLen, ok := allowedMultihash[mhCode]
	if !ok {
		return ErrCIDMultihash
	}
	raw = raw[n:]

	mhLen, n := binary.Uvarint(raw)
	if n <= 0 {
		return ErrCIDDecode
	}
	raw = raw[n:]
	if uint64(len(raw)) != mhLen || int(mhLen) != wantLen {
		return ErrCIDDigestLen
	}
	return nil
}

// RawLeafCIDv1 returns the CIDv1 of a raw (codec 0x55) sha2-256 leaf over data —
// the same shape Kubo produces for a single-file `add --cid-version=1
// --raw-leaves=true`. It is content-addressed: identical bytes ⇒ identical CID
// (the property the reference-checked unpin relies on). Deterministic and offline,
// so the fake client and tests can produce valid, ValidateCID-passing CIDs.
func RawLeafCIDv1(data []byte) string {
	sum := sha256.Sum256(data)
	return encodeCIDv1(codecRaw, mhSHA2256, sum[:])
}

// DirCIDv1 returns a CIDv1 with the dag-pb (UnixFS directory, codec 0x70) codec
// over a digest of data — a stand-in root CID for a wrapped directory add (the HLS
// tree). It is NOT a real UnixFS DAG hash; it is a deterministic, valid CIDv1 the
// fake uses so directory-add results content-address and pass ValidateCID.
func DirCIDv1(data []byte) string {
	sum := sha256.Sum256(data)
	return encodeCIDv1(codecDagPB, mhSHA2256, sum[:])
}

// encodeCIDv1 assembles a CIDv1 string from a codec, multihash code, and digest.
// It is the inverse of the parse in ValidateCID and the basis for the deterministic
// CIDs the fake client and tests produce.
func encodeCIDv1(codec, mhCode uint64, digest []byte) string {
	buf := make([]byte, 0, 8+len(digest))
	buf = binary.AppendUvarint(buf, 1) // CID version
	buf = binary.AppendUvarint(buf, codec)
	buf = binary.AppendUvarint(buf, mhCode)
	buf = binary.AppendUvarint(buf, uint64(len(digest)))
	buf = append(buf, digest...)
	return "b" + base32Lower.EncodeToString(buf)
}
