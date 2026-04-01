# IOTA Payment Integration Guardrails

Rules for working with IOTA Rebased payments, Ed25519 cryptography, and transaction handling in Vidra Core.

## Ed25519 Private Key Safety

**Private keys must NEVER appear in logs, error messages, JSON responses, or debug output.**

- Tag private key fields with `json:"-"` to prevent serialization (see `domain.IOTAWallet`)
- Use `crypto/rand` for key generation — never `math/rand`
- A PreToolUse hook already blocks private key logging in `internal/payments/` — treat warnings seriously
- Keys at rest MUST be encrypted via `internal/security/wallet_encryption.go`

```go
// CORRECT: use WalletEncryptionService to encrypt seeds at rest
encrypted, err := walletEncService.EncryptSeed(ctx, seed)

// WRONG: plaintext storage
db.Exec("INSERT INTO wallets (private_key) VALUES ($1)", hex.EncodeToString(privateKey))
```

Reference: `internal/security/wallet_encryption.go` — `WalletEncryptionService.EncryptSeed(ctx, seed)`.

## Address Format

IOTA Rebased addresses are derived by:
1. Prepend `0x00` flag byte (Ed25519 scheme) to the 32-byte public key
2. Blake2b-256 hash the 33-byte result
3. Hex-encode with `0x` prefix

**Result:** 66 characters total (`0x` + 64 hex chars = 32 bytes)

- Use `IOTAClient.DeriveAddress()` to derive addresses — never construct manually
- Use `IOTAClient.ValidateAddress()` before passing addresses to RPC calls
- Validation checks: length == 66, `0x` prefix, valid hex encoding

## Amount Safety (int64 Overflow)

All IOTA amounts are in **nanos** (1 IOTA = 1,000,000,000 nanos). The `int64` type supports up to ~9.2e18 nanos — sufficient for IOTA's total supply (~4.6e18 nanos) but overflow is possible in summation.

```go
// REQUIRED: overflow check before addition
if amt > 0 && totalAmount > math.MaxInt64-amt {
    return fmt.Errorf("amount overflow: total would exceed int64 max")
}
totalAmount += amt
```

- Reject negative amounts at all API boundaries
- Use `strconv.ParseInt` (not `ParseUint`) to preserve sign checking
- **Known gap:** `iota_client.go` `QueryTransactionBlocks` sums amounts without overflow protection

## Transaction Signing

The IOTA Rebased signing protocol (`internal/payments/iota_client.go`):

1. Construct intent message: `intentPrefix(3 bytes) || bcsEncodedTxBytes`
2. Hash with Blake2b-256: `blake2b.Sum256(intentMessage)`
3. Sign the 32-byte hash with Ed25519: `ed25519.Sign(privateKey, hash[:])`
4. Assemble signature: `ed25519Flag(1 byte, 0x00) || signature(64 bytes) || publicKey(32 bytes)` = 97 bytes total

- Always use the `intentPrefix = []byte{0x00, 0x00, 0x00}` constant — never hardcode the bytes inline
- Always use `DefaultGasBudget` constant for gas — never hardcode magic numbers
- Verify the private key seed is exactly `ed25519.SeedSize` (32 bytes) before signing

## RPC Client Safety

- Always use the configured IOTA node URL (`cfg.IOTANodeURL`) — never hardcode endpoints
- Set HTTP client timeouts for all RPC calls
- Handle JSON-RPC error responses explicitly — don't assume success from HTTP 200
- Validate all addresses with `ValidateAddress()` before passing to RPC methods

## Testing

- Mock the IOTA node using the Docker mock service on port 19500 (see `docker-mock-services.md`)
- Unit tests must mock all HTTP calls — never connect to a real IOTA node
- Test edge cases: zero amounts, maximum int64 amounts, invalid addresses, expired gas objects
- Transaction signing tests must verify the exact byte layout (flag + sig + pubkey = 97 bytes)
