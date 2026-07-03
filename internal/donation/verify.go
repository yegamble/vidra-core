package donation

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/decred/dcrd/dcrec/secp256k1/v4"
	"github.com/decred/dcrd/dcrec/secp256k1/v4/ecdsa"
	"golang.org/x/crypto/sha3"
)

// verifySignature dispatches proof-of-control verification by network.
//
// Return contract:
//   - (true, nil):  the signature proves control of address over message.
//   - (false, nil): a well-formed signature that recovers a DIFFERENT address
//     (a genuine mismatch — the caller does not control the address).
//   - (_, ErrBadSignature): the signature bytes are malformed.
//   - (_, ErrVerificationUnsupported): the network has no verification path.
//
// It NEVER fabricates a positive result: networks without a vetted signing
// standard are reported as unsupported, not silently "verified".
func verifySignature(network, address, message, signature string) (bool, error) {
	switch network {
	case "ethereum":
		return verifyEthereumPersonalSign(address, message, signature)
	default:
		// bitcoin (BIP-137) is intentionally deferred rather than implemented
		// half-safely; monero/litecoin have no practical message-signing
		// standard. See SupportsVerification.
		return false, ErrVerificationUnsupported
	}
}

// verifyEthereumPersonalSign verifies an EIP-191 personal_sign signature: it
// recovers the secp256k1 public key from the 65-byte [r||s||v] signature over
// keccak256 of the personal-sign-prefixed message, derives the Ethereum address
// (last 20 bytes of keccak256(pubkey)), and compares it case-insensitively to
// the claimed address.
//
// A minimal, well-vetted secp256k1 library (decred/dcrd) is used deliberately
// in place of the heavyweight go-ethereum dependency: we need only public-key
// recovery plus keccak256 (via golang.org/x/crypto/sha3), nothing from the full
// Ethereum client. That keeps the dependency surface small and auditable.
func verifyEthereumPersonalSign(address, message, signature string) (bool, error) {
	want := strings.TrimSpace(address)
	if !networkAddressRe["ethereum"].MatchString(want) {
		return false, ErrBadSignature
	}
	raw, err := hex.DecodeString(strings.TrimPrefix(strings.TrimSpace(signature), "0x"))
	if err != nil || len(raw) != 65 {
		return false, ErrBadSignature
	}
	// Ethereum lays the signature out as r(32) || s(32) || v(1); decred's
	// RecoverCompact expects the recovery byte FIRST: [27+recid] || r || s.
	v := raw[64]
	if v >= 27 {
		v -= 27
	}
	if v > 1 {
		return false, ErrBadSignature
	}
	compact := make([]byte, 65)
	compact[0] = 27 + v
	copy(compact[1:33], raw[0:32])
	copy(compact[33:65], raw[32:64])

	pub, _, err := ecdsa.RecoverCompact(compact, ethPersonalHash(message))
	if err != nil {
		// A recovery failure means garbage/non-recoverable signature bytes: a
		// wrong signature, not a server fault.
		return false, nil
	}
	return strings.EqualFold(ethereumAddressFromPubKey(pub), want), nil
}

// ethPersonalHash implements the EIP-191 personal_sign digest:
// keccak256("\x19Ethereum Signed Message:\n" + len(message) + message).
func ethPersonalHash(message string) []byte {
	prefix := fmt.Sprintf("\x19Ethereum Signed Message:\n%d", len(message))
	h := sha3.NewLegacyKeccak256()
	h.Write([]byte(prefix))
	h.Write([]byte(message))
	return h.Sum(nil)
}

// ethereumAddressFromPubKey derives the 0x-prefixed lowercase hex Ethereum
// address from a recovered public key: last 20 bytes of keccak256 of the
// 64-byte uncompressed key (X||Y, dropping the 0x04 prefix byte).
func ethereumAddressFromPubKey(pub *secp256k1.PublicKey) string {
	uncompressed := pub.SerializeUncompressed() // 0x04 || X(32) || Y(32)
	h := sha3.NewLegacyKeccak256()
	h.Write(uncompressed[1:])
	sum := h.Sum(nil)
	return "0x" + hex.EncodeToString(sum[12:])
}
