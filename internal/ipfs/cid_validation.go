package ipfs

import (
	"fmt"
	"strings"

	"github.com/ipfs/go-cid"
)

const (
	// maxCIDLength prevents DoS attacks via excessively long strings
	maxCIDLength = 256
)

// allowedCodecs defines the whitelist of permitted IPFS codecs
// Per CLAUDE.md security requirements: raw, dag-pb, dag-cbor only
var allowedCodecs = map[uint64]bool{
	0x55: true, // raw
	0x70: true, // dag-pb
	0x71: true, // dag-cbor
}

// ValidateCID performs comprehensive validation of an IPFS CID
// This prevents path traversal, injection attacks, and enforces CIDv1-only policy
func ValidateCID(cidStr string) error {
	// Check for empty CID
	if cidStr == "" {
		return fmt.Errorf("CID cannot be empty")
	}

	// Check length to prevent DoS attacks
	if len(cidStr) > maxCIDLength {
		return fmt.Errorf("CID exceeds maximum length of %d characters", maxCIDLength)
	}

	// Check for path traversal attempts
	if strings.Contains(cidStr, "..") {
		return fmt.Errorf("invalid CID: path traversal detected")
	}

	// Check for null bytes and other control characters
	for _, ch := range cidStr {
		if ch < 32 || ch == 127 {
			return fmt.Errorf("invalid CID: control characters not allowed")
		}
	}

	// Check for whitespace
	if strings.TrimSpace(cidStr) != cidStr {
		return fmt.Errorf("invalid CID: whitespace not allowed")
	}

	// Check for URL encoding attempts
	if strings.Contains(cidStr, "%") {
		return fmt.Errorf("invalid CID: URL encoding not allowed")
	}

	// Check for case sensitivity - base32 CIDs (starting with 'b') should be lowercase
	if strings.HasPrefix(cidStr, "b") || strings.HasPrefix(cidStr, "B") {
		for _, ch := range cidStr {
			if ch >= 'A' && ch <= 'Z' {
				return fmt.Errorf("invalid CID: uppercase characters not allowed in base32 CIDs")
			}
		}
	}

	// Check if this looks like a CIDv0 (starts with Qm) before parsing
	// This provides a better error message
	if strings.HasPrefix(cidStr, "Qm") {
		// Check for path separators in CIDv0
		if strings.ContainsAny(cidStr, "/\\") {
			return fmt.Errorf("CIDv0 not supported, only CIDv1 allowed")
		}
		return fmt.Errorf("CIDv0 not supported, only CIDv1 allowed")
	}

	// Check for path separators (both Unix and Windows) after CIDv0 check
	if strings.ContainsAny(cidStr, "/\\") {
		return fmt.Errorf("invalid CID: path separators not allowed")
	}

	// Parse the CID using the official IPFS library
	parsed, err := cid.Decode(cidStr)
	if err != nil {
		return fmt.Errorf("invalid CID format: %w", err)
	}

	// Enforce CIDv1 only (per CLAUDE.md requirement)
	if parsed.Version() != 1 {
		return fmt.Errorf("only CIDv1 supported, got CIDv%d", parsed.Version())
	}

	// Validate codec is in whitelist
	codec := parsed.Prefix().Codec
	if !allowedCodecs[codec] {
		return fmt.Errorf("codec 0x%x not allowed (only raw, dag-pb, dag-cbor permitted)", codec)
	}

	return nil
}
