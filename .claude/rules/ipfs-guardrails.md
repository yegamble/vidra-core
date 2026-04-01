# IPFS Integration Guardrails

Rules for working with IPFS content addressing, pinning, and gateway access in Vidra Core.

## CID Validation

**Always call `ipfs.ValidateCID(cid)` before using a CID in any operation** — URL construction, pinning, fetching, or storage. The validator (`internal/ipfs/cid_validation.go`) enforces:

- CIDv1 only (base32 `bafy...` or base58). CIDv0 (`Qm...`) is rejected.
- Allowed codecs: `dag-pb`, `raw`, `dag-cbor`, `dag-json` only.
- Max length: 512 characters.
- Path traversal blocked: rejects `/`, `..`, `%2F`, `%2E%2E`, and other encoded variants.
- Multihash validation: SHA-256 or Blake2b only.

```go
// REQUIRED before any IPFS operation with external CID input
if err := ipfs.ValidateCID(cid); err != nil {
    return fmt.Errorf("invalid CID: %w", err)
}
```

**Known gap:** `internal/usecase/ipfs_streaming/gateway_client.go` `FetchCID()` constructs `/ipfs/%s` without calling `ValidateCID()` first. New code must not repeat this pattern.

## Gateway URL Construction

- Use `cfg.IPFSGatewayURLs` (a `[]string` with health-check rotation via `ipfs_streaming/`). Never hardcode `ipfs.io`, `dweb.link`, or other public gateways.
- Always validate CID before embedding in URLs.
- Use `url.PathEscape(cid)` when CID appears as a path component, `url.QueryEscape(cid)` for query params.

## Pinning Safety

- **Never unpin without reference checking.** Before calling `Unpin()` or `ClusterUnpin()`, verify the CID is not referenced by any active video, HLS manifest, or playlist.
- A PreToolUse hook already warns on unpin operations in `internal/ipfs/` — treat warnings seriously.
- Pin operations should be idempotent — pinning an already-pinned CID is a no-op.

## Storage Paths

- IPFS-stored content paths must use `storage.Paths` methods, never manual string concatenation.
- Hybrid storage (`internal/storage/`) determines local vs IPFS vs S3 — never bypass this layer to write directly.

## Testing

- Mock the IPFS client interface in unit tests — never connect to a real IPFS node.
- Integration tests for IPFS use the Docker mock service on port 15001 (see `docker-mock-services.md`).
- CID validation has comprehensive fuzz tests — run with `go test -fuzz ./internal/ipfs/...`.
