// Package donation implements Vidra's simple, NON-CUSTODIAL crypto donation
// addresses (fix_plan P14). A creator lists public wallet addresses on their
// account or a channel; viewers see them (and whether ownership was proven) and
// send funds peer-to-peer, entirely outside Vidra. This package deliberately
// contains NO notion of a balance, payment, payout, invoice, or settlement:
// Vidra never custodies funds or processes money. It is HTTP-agnostic and
// testable without a server.
package donation

import "regexp"

// supportedNetworks is the curated, closed set of donation networks, in display
// order. Adding a network is a deliberate act: it needs an address validator
// (below) and, if it can be verified, a signature path in verify.go.
var supportedNetworks = []string{"bitcoin", "ethereum", "litecoin", "monero"}

// networkAddressRe maps each supported network to a shape validator for its
// public address string. These are pragmatic format checks (prefix + charset +
// length), NOT full checksum validators — enough to reject typos and
// cross-network paste errors before an address is stored and displayed.
var networkAddressRe = map[string]*regexp.Regexp{
	// Bitcoin: legacy base58 P2PKH/P2SH (1.../3...) or bech32/bech32m (bc1...).
	"bitcoin": regexp.MustCompile(`^(bc1[a-z0-9]{11,71}|[13][a-km-zA-HJ-NP-Z1-9]{25,39})$`),
	// Ethereum (and EVM chains): a 20-byte hex address with the 0x prefix.
	// Mixed-case (EIP-55 checksum) casing is accepted.
	"ethereum": regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`),
	// Litecoin: legacy base58 (L.../M.../3...) or bech32 (ltc1...).
	"litecoin": regexp.MustCompile(`^(ltc1[a-z0-9]{11,71}|[LM3][a-km-zA-HJ-NP-Z1-9]{26,39})$`),
	// Monero: standard/subaddress (95 chars, 4.../8...) or integrated (106 chars).
	"monero": regexp.MustCompile(`^[48][0-9AB][1-9A-HJ-NP-Za-km-z]{93,104}$`),
}

// SupportedNetworks returns a copy of the curated network list, in display order.
func SupportedNetworks() []string {
	out := make([]string, len(supportedNetworks))
	copy(out, supportedNetworks)
	return out
}

// IsKnownNetwork reports whether network is one of the curated, supported set.
func IsKnownNetwork(network string) bool {
	_, ok := networkAddressRe[network]
	return ok
}

// ValidAddress reports whether address has a plausible shape for network. It is
// a format guard, not a checksum verifier; a false result is always a reject,
// but a true result does not by itself prove control (see verify.go).
func ValidAddress(network, address string) bool {
	re, ok := networkAddressRe[network]
	if !ok {
		return false
	}
	return re.MatchString(address)
}

// SupportsVerification reports whether a network offers signed-challenge
// proof-of-control in this version. Only ethereum does today (EIP-191
// personal_sign). Bitcoin's BIP-137 path is intentionally deferred rather than
// implemented half-safely, and monero/litecoin have no practical message-
// signing standard, so both stay unverified-only. Verification is never faked:
// unsupported networks return ErrVerificationUnsupported.
func SupportsVerification(network string) bool {
	return network == "ethereum"
}
