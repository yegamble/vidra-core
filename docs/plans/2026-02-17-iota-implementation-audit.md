# IOTA Rebased Implementation Upgrade Plan

Created: 2026-02-17
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No

> **Status Lifecycle:** PENDING → COMPLETE → VERIFIED
> **Iterations:** Tracks implement→verify cycles (incremented by verify phase)
>
> - PENDING: Initial state, awaiting implementation
> - COMPLETE: All tasks implemented
> - VERIFIED: All checks passed
>
> **Approval Gate:** Implementation CANNOT proceed until `Approved: Yes`
> **Worktree:** Set at plan creation (from dispatcher). `Yes` uses git worktree isolation; `No` works directly on current branch (default)

## Summary

**Goal:** Upgrade Athena's IOTA payment integration from stub/mock code targeting the deprecated Stardust protocol to a fully functional implementation targeting IOTA Rebased (launched May 5, 2025) — a Move-based smart contract platform with JSON-RPC APIs, Ed25519 keypairs, and hex-encoded addresses.

**Architecture:** Build a native Go JSON-RPC client that communicates with the IOTA Rebased node API. Replace the old Bech32 address format with hex `0x`-prefixed addresses. Migrate from "seeds" to Ed25519 keypairs for wallet key storage. Add Docker containers for IOTA node (testnet for dev, mainnet for prod). Integrate IOTA node configuration into the Setup Wizard.

**Tech Stack:** Go (net/http JSON-RPC client), `iotaledger/iota-node` Docker image, Ed25519 (`crypto/ed25519`), existing AES-GCM encryption for key storage.

## Scope

### In Scope

- Replace stub IOTA client with real JSON-RPC client for IOTA Rebased
- Migrate address format from Bech32 (`iota1...`) to hex (`0x...`, 32 bytes)
- Migrate wallet key model from "seeds" to Ed25519 keypairs
- Add `iotaledger/iota-node` to docker-compose.yml (testnet for dev/test, mainnet for prod)
- Add IOTA node configuration to Setup Wizard (external URL vs Docker node)
- Write `.env` IOTA configuration from wizard
- Update database migration for new address format
- Update all tests
- Add validation scripts/bash for IOTA Docker setup

### Out of Scope

- Move smart contract deployment (Phase 2 — custom token contracts)
- IOTA staking integration
- Frontend wallet UI
- ATProto/ActivityPub federation changes
- Withdrawal/send transaction implementation (balance checking and payment detection only for now)

## Prerequisites

- Docker available for running `iotaledger/iota-node` images
- Understanding of IOTA Rebased JSON-RPC API (documented at <https://docs.iota.org/developer/references/iota-api>)

## Context for Implementer

- **Patterns to follow:** The wizard uses a docker/external toggle pattern — see `internal/setup/templates/services.html:23-41` for Redis toggle, and `internal/setup/wizard_forms.go:32-49` for form processing. Follow this exact pattern for the IOTA node toggle.
- **Conventions:** All service modes follow `docker`/`external`/`disabled` convention. Docker services use profiles in `docker-compose.yml`. Writer outputs env vars in `internal/setup/writer.go`.
- **Key files:**
  - `internal/payments/iota_client.go` — Current stub client (to be rewritten)
  - `internal/usecase/payments/payment_service.go` — Business logic (interface changes needed)
  - `internal/domain/payment.go` — Domain models (address format, key storage changes)
  - `internal/httpapi/handlers/payments/payment_handlers.go` — HTTP handlers (minimal changes)
  - `internal/setup/wizard.go` — Wizard config struct + handlers
  - `internal/setup/wizard_forms.go` — Form processing
  - `internal/setup/writer.go` — .env file generation
  - `internal/setup/templates/services.html` — Services page template
  - `docker-compose.yml` — Docker service definitions
  - `internal/config/config.go` + `config_load.go` — App configuration
- **Gotchas:**
  - IOTA Rebased addresses are `0x`-prefixed hex strings (66 chars total: `0x` + 64 hex chars), NOT Bech32
  - The JSON-RPC API uses `iotax_` prefix for extended API methods and `iota_` prefix for core methods
  - Coin balances use the `iotax_getAllBalances` or `iotax_getBalance` RPC methods
  - Transaction status uses `iota_getTransactionBlock` with the transaction digest
  - Gas is required for all transactions (like Ethereum/Sui)
  - The node's JSON-RPC endpoint is on port 9000 internally, but **map to host port 14265** to avoid collision with Whisper (already on port 9000)
  - The `iotaledger/iota-node` image needs a `fullnode.yaml` config file and genesis snapshot — the entrypoint script downloads the snapshot on first boot
- **Domain context:** IOTA Rebased is an object-based ledger where coins are objects. Balances are queried per address. Transactions are "Programmable Transaction Blocks" that operate on objects. For our payment use case, we primarily need: address generation, balance checking, and transaction status polling.

## Runtime Environment

- **IOTA Node (Docker):** `iotaledger/iota-node:testnet` (dev) / `iotaledger/iota-node:mainnet` (prod)
- **JSON-RPC port:** 9000 internal, **14265 host** (dev), **19000 host** (test) — avoids collision with Whisper on port 9000
- **Health check:** `curl -X POST http://localhost:14265 -H 'Content-Type: application/json' -d '{"jsonrpc":"2.0","id":1,"method":"iota_getLatestCheckpointSequenceNumber","params":[]}'`
- **Faucet (testnet only):** `https://faucet.testnet.iota.cafe` or local `http://127.0.0.1:9123/gas`

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Update domain models for IOTA Rebased
- [x] Task 2: Build Go JSON-RPC client for IOTA Rebased
- [x] Task 3: Update payment service for Ed25519 keypairs
- [x] Task 4: Add IOTA node Docker configuration
- [x] Task 5: Add IOTA configuration to Setup Wizard
- [x] Task 6: Write database migration for address format
- [x] Task 7: Update config loading and .env generation
- [x] Task 8: Update HTTP handlers and integration wiring

**Total Tasks:** 8 | **Completed:** 8 | **Remaining:** 0

## Implementation Tasks

### Task 1: Update Domain Models for IOTA Rebased

**Objective:** Update domain models to reflect IOTA Rebased's address format (hex `0x...`), Ed25519 keypair storage, and object-based transaction model.

**Dependencies:** None

**Files:**

- Modify: `internal/domain/payment.go`
- Modify: `internal/domain/payment_test.go`
- Modify: `internal/usecase/payments/payment_service.go` (update `IOTARepository` and `IOTAClient` interfaces for new field names)

**Key Decisions / Notes:**

- Change `IOTAWallet` to store `EncryptedPrivateKey` + `PrivateKeyNonce` instead of `EncryptedSeed` + `SeedNonce`
- Add `PublicKey` field (hex-encoded Ed25519 public key)
- Address field stays but format changes: now `0x` + 64 hex chars (66 total)
- Keep `BalanceIOTA` as `int64` (IOTA Rebased NANOS fit in int64; changing to uint64 would break the `IOTARepository` interface in `payment_service.go`, `GetWalletBalance` return type, and `DetectPayment` comparison logic — a cascading breaking change not worth the churn since negative balances are impossible in practice)
- `IOTATransaction.TransactionHash` becomes `TransactionDigest` (IOTA Rebased terminology)
- Add `GasBudget` and `GasUsed` fields to `IOTATransaction`
- Keep existing error sentinels, add `ErrIOTANodeSyncing`, `ErrInsufficientGas`
- **Caller audit before removing symbols:** Before modifying domain errors or removing fields, run `grep -r 'ErrInvalidSeed\|EncryptedSeed\|SeedNonce\|GenerateSeed' ./internal` to find ALL callers. Replace `ErrInvalidSeed` with `ErrInvalidInput` rather than deleting if callers in test files reference it. List all callers explicitly before making changes.
- **Files also affected:** `internal/usecase/payments/payment_service.go` must be updated in this task for the `IOTARepository` interface (seed→private key field renames). The `IOTAClient` interface in `payment_service.go` also needs updating for new method signatures. Add these to the file list.

**Definition of Done:**

- [ ] `IOTAWallet` uses `EncryptedPrivateKey`/`PrivateKeyNonce`/`PublicKey` instead of `EncryptedSeed`/`SeedNonce`
- [ ] Address validation function accepts `0x`-prefixed 66-char hex strings
- [ ] `IOTARepository` interface in `payment_service.go` updated to use `EncryptedPrivateKey`/`PrivateKeyNonce` field names
- [ ] All callers of removed symbols (`ErrInvalidSeed`, `EncryptedSeed`, `SeedNonce`, `GenerateSeed`) identified and updated
- [ ] All tests in `domain/payment_test.go` pass with new model
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/domain/... -run TestPayment -v` — domain payment tests pass
- `go vet ./internal/domain/...` — no issues

### Task 2: Build Go JSON-RPC Client for IOTA Rebased

**Objective:** Replace the stub `IOTAClient` in `internal/payments/iota_client.go` with a real JSON-RPC client that communicates with an IOTA Rebased node.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/payments/iota_client.go`
- Modify: `internal/payments/iota_client_test.go`

**Key Decisions / Notes:**

- Use `net/http` + `encoding/json` for JSON-RPC calls (no external dependency)
- Implement a generic `callRPC(ctx, method, params)` helper
- Key RPC methods to implement:
  - `iota_getLatestCheckpointSequenceNumber` — node health/sync check
  - `iotax_getAllBalances(address)` — get all coin balances for an address
  - `iotax_getBalance(address, coinType)` — get specific coin balance
  - `iota_getTransactionBlock(digest, options)` — transaction status
  - `iota_dryRunTransactionBlock(txBytes)` — gas estimation (future use)
- Ed25519 key generation using Go's `crypto/ed25519`
- **Address derivation: Blake2b-256** hash of a flag byte (`0x00` for Ed25519) concatenated with the public key bytes, then `0x`-prefix the hex. This matches IOTA Rebased/Sui's scheme. **CRITICAL prerequisite:** Before marking this task done, verify the derived address by generating a known keypair, deriving its address, and confirming it matches the address shown on the IOTA testnet explorer or via `iota_getOwnedObjects` RPC call. If Blake2b-256 doesn't match, try SHA3-256 — but verify before any other task proceeds.
- Keep the `IOTANodeClient` interface for testability (mock in tests)
- Add configurable timeout (10s default as constructor parameter) and retry logic: retry up to 3 times with exponential backoff on connection errors (not on RPC error responses). Test with `httptest.Server` that returns errors on first N requests.
- Maintain backward compatibility with the `IOTAClient` interface used by `PaymentService` (update interface as needed for new method signatures)
- **Existing methods disposition:** The current `iota_client.go` has `WaitForConfirmation`, `SubmitTransaction`, `SignTransaction`, and `BuildTransaction` methods. These are out of scope for send transactions (Phase 2). Keep them as clearly-labelled stubs that return `ErrNotImplemented` with a TODO comment noting they require Programmable Transaction Block construction. Ensure no dead or unreachable code paths remain and the build is clean.
- **Go dependency:** Add `golang.org/x/crypto` for Blake2b-256 (`golang.org/x/crypto/blake2b`)

**Definition of Done:**

- [ ] `IOTAClient` makes real JSON-RPC calls when configured with a node URL
- [ ] `GenerateKeypair()` produces valid Ed25519 keypair
- [ ] `DeriveAddress(publicKey)` produces correct `0x`-prefixed hex address, **verified against IOTA testnet explorer or `iota_getOwnedObjects` RPC for a known test keypair**
- [ ] `ValidateAddress(address)` validates `0x` + 64 hex chars format
- [ ] `GetBalance(ctx, address)` calls `iotax_getBalance` via JSON-RPC
- [ ] `GetTransactionStatus(ctx, digest)` calls `iota_getTransactionBlock`
- [ ] `GetNodeInfo(ctx)` calls node health check endpoint
- [ ] JSON-RPC client retries up to 3 times with exponential backoff on connection errors (tested with httptest server returning errors on first N requests)
- [ ] Existing send-related methods (`WaitForConfirmation`, `SubmitTransaction`, `SignTransaction`, `BuildTransaction`) return `ErrNotImplemented` with TODO comments
- [ ] All tests pass with mock JSON-RPC server (httptest)
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/payments/... -v` — all client tests pass
- `go vet ./internal/payments/...` — no issues

### Task 3: Update Payment Service for Ed25519 Keypairs

**Objective:** Update `PaymentService` to use Ed25519 keypair generation/storage instead of seed-based wallets, and integrate with the new IOTA client interface.

**Dependencies:** Task 1, Task 2

**Files:**

- Modify: `internal/usecase/payments/payment_service.go`
- Modify: `internal/usecase/payments/payment_service_test.go`
- Modify: `internal/usecase/payments/helpers_test.go`
- Modify: `internal/usecase/payments/payment_service_extended_test.go`

**Key Decisions / Notes:**

- `CreateWallet` now calls `client.GenerateKeypair()` instead of `GenerateSeed()`
- Encrypt the private key (32 bytes) using existing AES-GCM, store as `EncryptedPrivateKey`
- Store the public key as hex in the `PublicKey` field
- Derive address from public key using `client.DeriveAddress(publicKey)`
- Rename `EncryptSeed`→`EncryptPrivateKey`, `DecryptSeed`→`DecryptPrivateKey`
- Update `IOTAClient` interface in this file to match new method signatures
- `DetectPayment` uses `GetBalance` with the new address format
- **Known limitation:** The balance-based `DetectPayment` approach (balance >= intent.AmountIOTA) is unreliable when multiple payment intents share the same wallet address — a single deposit can incorrectly mark multiple intents as paid. This is a pre-existing design flaw. For now, maintain the existing approach but add a TODO comment noting that production use should switch to transaction-based detection via `iota_getTransactionBlock` to correlate specific transactions to specific intents. Add a check that tracks previous balance and only triggers payment detection for the delta, not the total.
- Update `IOTARepository` interface: rename seed fields to private key fields (already done in Task 1)

**Definition of Done:**

- [ ] `CreateWallet` generates Ed25519 keypair and encrypts private key
- [ ] `GetWalletBalance` queries balance using new JSON-RPC client interface
- [ ] `DetectPayment` works with new balance-checking flow
- [ ] All encryption/decryption works with Ed25519 private keys
- [ ] All tests pass (unit tests with mocked client/repo)
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/usecase/payments/... -v` — all service tests pass
- `go vet ./internal/usecase/payments/...` — no issues

### Task 4: Add IOTA Node Docker Configuration

**Objective:** Add IOTA Rebased node containers to `docker-compose.yml` for development (testnet), testing, and production (mainnet) use.

**Dependencies:** None

**Files:**

- Modify: `docker-compose.yml`
- Create: `docker/iota/fullnode.yaml` (minimal fullnode config for local dev — testnet and mainnet variants)
- Create: `docker/iota/entrypoint.sh` (startup script that downloads genesis snapshot if not present before starting the node)
- Create: `scripts/iota-node-health.sh` (curl JSON-RPC health check, exits 0 on success, 1 on failure)
- Create: `scripts/iota-testnet-up.sh` (start IOTA testnet Docker node with snapshot download)
- Create: `scripts/iota-mainnet-up.sh` (start IOTA mainnet Docker node with snapshot download)

**Key Decisions / Notes:**

- Add `iota-node` service under `iota` profile (like IPFS uses `ipfs` profile)
- **Pin Docker image tags** to specific versions rather than floating `testnet`/`mainnet` tags. Use e.g. `iotaledger/iota-node:testnet-v1.x.y` (determine exact latest version at implementation time). Add a comment in docker-compose.yml with the date the tag was verified and the expected API version.
- **Port mapping:** Map IOTA node's internal port 9000 to **host port 14265** for dev (avoids collision with Whisper service which already binds port 9000). For test profile, map to host port 19000. Update `IOTA_NODE_URL` default in docker-compose app environment to `http://iota-node:14265` for host access, but internal container-to-container communication uses port 9000 (`http://iota-node:9000`). Add a comment in the service definition documenting the port choice.
- Add health check using JSON-RPC `iota_getLatestCheckpointSequenceNumber`
- Add `iota_data` volume for node data persistence
- Add `iota-node-test` service in the test profile
- Add appropriate network assignments
- **fullnode.yaml:** Create `docker/iota/fullnode.yaml` with minimal required config for local dev. Mount into the container via docker-compose volume mount.
- **Genesis snapshot handling:** Create `docker/iota/entrypoint.sh` that downloads the official IOTA genesis snapshot before starting the node. The script checks if the snapshot already exists (persistent volume) and skips download if present. Document expected first-boot sync time (minutes for testnet with snapshot, hours for mainnet).
- **Bash scripts (user-requested):** Create executable shell scripts under `/scripts/` for IOTA Docker operations. Each script should be executable (`chmod +x`) and include a usage comment.
- Add a comment noting the GraphQL API port (9125) is available but not currently exposed: `# GraphQL API port 9125 — expose when GraphQL support is added in Phase 2`

**Definition of Done:**

- [ ] `docker compose --profile iota up iota-node` starts an IOTA testnet fullnode
- [ ] `fullnode.yaml` is mounted into the container via docker-compose volume mount and the container starts without errors
- [ ] `docker/iota/entrypoint.sh` downloads genesis snapshot on first boot if not present
- [ ] Container starts and JSON-RPC port is open: `docker compose --profile iota up iota-node -d` exits 0 and `nc -z localhost 14265` succeeds within 60 seconds
- [ ] When fully synced, `iota_getLatestCheckpointSequenceNumber` returns a non-zero integer (note: full sync takes time, verified separately from container startup)
- [ ] Test profile has `iota-node-test` service on port 19000
- [ ] `app` service has `IOTA_NODE_URL` environment variable pointing to the Docker node
- [ ] Node data persists across restarts via `iota_data` volume
- [ ] Docker image tag is pinned to specific version (not floating `testnet`/`mainnet`)
- [ ] Port 9000 collision with Whisper is avoided (host port 14265 for dev, 19000 for test)
- [ ] `scripts/iota-node-health.sh` is executable and produces correct output when node is running
- [ ] `scripts/iota-testnet-up.sh` and `scripts/iota-mainnet-up.sh` are executable and include usage comments
- [ ] No port conflicts with existing services (`docker compose --profile iota --profile full config` validates cleanly)

**Verify:**

- `docker compose --profile iota config` — validates compose file with no port conflicts
- `docker compose --profile iota --profile full config` — validates no port conflicts with Whisper or other services
- `docker compose --profile iota up iota-node -d && sleep 60 && bash scripts/iota-node-health.sh` — node responds
- `bash scripts/iota-testnet-up.sh --help` — shows usage

### Task 5: Add IOTA Configuration to Setup Wizard

**Objective:** Add an IOTA node configuration section to the wizard's services page, allowing users to choose between Docker-managed or external IOTA node, following the same toggle pattern as Redis/IPFS.

**Dependencies:** Task 4

**Files:**

- Modify: `internal/setup/wizard.go` (add IOTA fields to `WizardConfig`)
- Modify: `internal/setup/wizard_forms.go` (add IOTA form processing to `processServicesForm`)
- Modify: `internal/setup/templates/services.html` (add IOTA toggle section)
- Modify: `internal/setup/templates/review.html` (display IOTA config in review)
- Create: `internal/setup/validate_iota.go` (IOTA URL validation)
- Create: `internal/setup/validate_iota_test.go`
- Modify: `internal/setup/wizard_test.go` (test IOTA wizard handler)
- Modify: `internal/setup/wizard_flow_test.go` (test IOTA in wizard flow)

**Key Decisions / Notes:**

- Add to `WizardConfig`: `EnableIOTA bool`, `IOTAMode string` (docker/external/disabled), `IOTANodeURL string`, `IOTANetwork string` (mainnet/testnet)
- Default: `EnableIOTA: false`, `IOTAMode: "docker"`, `IOTANetwork: "testnet"`
- Follow the exact same toggle pattern as Redis: docker/external toggle with conditional URL field
- Add network selector (mainnet/testnet) that appears for both docker and external modes
- **Validation:** For external mode, validate URL format strictly (must be a valid HTTP/HTTPS URL). Attempt a JSON-RPC ping with a short timeout (2s), but if it fails, show a **soft warning** ("Could not reach IOTA node — verify it is running") and **allow the user to proceed anyway**. This matches the existing Redis/Postgres pattern which does not block on connectivity, and avoids blocking setup in air-gapped or offline environments where the IOTA node may not be running yet during wizard setup.
- Add IOTA section to services page between IPFS and ClamAV sections
- Follow existing test patterns in `wizard_test.go` and `wizard_flow_test.go`

**Definition of Done:**

- [ ] Wizard services page shows IOTA section with enable checkbox
- [ ] Docker/External toggle works (shows URL field when external selected)
- [ ] Network selector (mainnet/testnet) is visible when IOTA enabled
- [ ] External URL validation checks format strictly; JSON-RPC connectivity is a soft warning (not blocking)
- [ ] Review page displays IOTA configuration
- [ ] All wizard tests pass including new IOTA tests
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/setup/... -v` — all setup tests pass
- `go vet ./internal/setup/...` — no issues

### Task 6: Write Database Migration for Address Format

**Objective:** Create a new Goose migration to update the IOTA tables for Rebased's address format and Ed25519 key storage.

**Dependencies:** Task 1

**Files:**

- Create: `migrations/064_update_iota_for_rebased.sql` (NOTE: 063 is already taken by `063_add_remote_video_support.sql` — verify at implementation time by listing `ls migrations/*.sql | tail -5` to confirm 064 is still available)

**Key Decisions / Notes:**

- Rename columns in `iota_wallets`: `encrypted_seed` → `encrypted_private_key`, `seed_nonce` → `private_key_nonce`
- Add column: `public_key VARCHAR(66)` (0x + 64 hex chars) to `iota_wallets`
- Keep `address` column at `VARCHAR(90)` — Rebased addresses are 66 chars max, so the existing width is fine (no change needed)
- Rename `transaction_hash` to `transaction_digest` in `iota_transactions`
- Add `gas_budget BIGINT` and `gas_used BIGINT` columns to `iota_transactions`
- **Review all three IOTA tables:** Explicitly enumerate which columns in `iota_payment_intents` are affected. Confirm that `payment_address VARCHAR(90)` is adequate for 66-char hex addresses (it is, no change needed). Verify that renaming `transaction_hash` to `transaction_digest` in `iota_transactions` does not break any FK or reference in `iota_payment_intents` (the `transaction_id` column in `iota_payment_intents` is a UUID FK, not related to the hash/digest rename).
- Down migration reverts all changes

**Definition of Done:**

- [ ] Migration file follows Goose format with Up and Down sections
- [ ] Column renames use `ALTER TABLE ... RENAME COLUMN`
- [ ] New columns added with appropriate defaults
- [ ] Down migration cleanly reverts all changes
- [ ] Migration SQL is syntactically valid
- [ ] All three IOTA tables (`iota_wallets`, `iota_transactions`, `iota_payment_intents`) have been reviewed; only columns explicitly listed are modified
- [ ] Migration number does not conflict with existing migrations (verified by listing migrations directory)

**Verify:**

- `cat migrations/064_update_iota_for_rebased.sql` — valid SQL
- `ls migrations/06*.sql` — no duplicate numbers

### Task 7: Update Config Loading and .env Generation

**Objective:** Add IOTA configuration to the app config system and the wizard's .env writer.

**Dependencies:** Task 5

**Files:**

- Modify: `internal/config/config.go` (update IOTA config fields)
- Modify: `internal/config/config_load.go` (load new IOTA env vars)
- Modify: `internal/setup/writer.go` (write IOTA config to .env)
- Modify: `internal/setup/writer_test.go` (test IOTA env var output)

**Key Decisions / Notes:**

- Config fields: `IOTAEnabled`, `IOTAMode` (docker/external), `IOTANodeURL`, `IOTANetwork` (mainnet/testnet), `IOTAWalletEncryptionKey` (keep existing)
- **Wallet encryption key:** Auto-generated by the wizard (similar to `JWTSecret`) and written to `.env` as `IOTA_WALLET_ENCRYPTION_KEY`. The user is not prompted for it but it is shown on the review page. The writer must output `IOTA_WALLET_ENCRYPTION_KEY` when `EnableIOTA` is true.
- Replace `IOTA_NODE_URL` with more structured: `ENABLE_IOTA`, `IOTA_MODE`, `IOTA_NODE_URL`, `IOTA_NETWORK`
- `config_load.go`: Load from env vars with sensible defaults (`ENABLE_IOTA=false`, `IOTA_MODE=docker`, `IOTA_NETWORK=testnet`)
- `writer.go`: Follow the exact pattern of IPFS section — conditional block based on `EnableIOTA`
- Docker mode default URL: `http://iota-node:9000` (container-to-container uses internal port 9000; host access uses port 14265)

**Definition of Done:**

- [ ] `config.go` has `IOTAEnabled`, `IOTAMode`, `IOTANodeURL`, `IOTANetwork` fields
- [ ] `config_load.go` loads all IOTA env vars with defaults
- [ ] `writer.go` outputs IOTA section in .env file, including `IOTA_WALLET_ENCRYPTION_KEY` when `EnableIOTA` is true
- [ ] Writer tests verify correct IOTA env output for docker and external modes
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/config/... -v` — config tests pass
- `go test ./internal/setup/... -run TestWrite -v` — writer tests pass

### Task 8: Update HTTP Handlers and Integration Wiring

**Objective:** Update payment HTTP handlers for the new domain model and wire everything together in the app initialization.

**Dependencies:** Task 1, Task 2, Task 3, Task 7

**Files:**

- Modify: `internal/httpapi/handlers/payments/payment_handlers.go`
- Modify: `internal/httpapi/handlers/payments/payment_handlers_test.go`
- Modify: `internal/app/app.go` (wire IOTA client with config)

**Key Decisions / Notes:**

- Handler changes are minimal — mostly field name changes in request/response structs
- `amount_iota` field stays the same in API (backward compatible)
- Wire `IOTAClient` creation in `app.go` using config values (node URL from config)
- Only create IOTA client if `IOTAEnabled` is true
- Add node health check to the `/ready` endpoint when IOTA is enabled
- Update handler test mocks to match new service interface

**Definition of Done:**

- [ ] Payment handlers work with updated domain models
- [ ] `app.go` wires `IOTAClient` using config-provided node URL
- [ ] IOTA client only initialized when `ENABLE_IOTA=true`
- [ ] Node health contributes to `/ready` endpoint
- [ ] All handler tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/payments/... -v` — handler tests pass
- `go build ./cmd/server` — binary builds cleanly
- `make validate-all` — full validation passes

## Testing Strategy

- **Unit tests:** Mock JSON-RPC server using `httptest.NewServer` for client tests. Mock client/repo interfaces for service tests. Table-driven tests for address validation and key derivation.
- **Integration tests:** Start `iota-node-test` Docker container, verify real JSON-RPC calls work (guarded with `testing.Short()` skip).
- **Wizard tests:** Follow existing `wizard_test.go` patterns — test GET renders form, POST processes form, validation rejects bad input.
- **Manual verification:** Start Docker stack with `--profile iota`, verify node syncs and responds to JSON-RPC.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| IOTA node takes long to sync from genesis | High | Med | The `fullnode.yaml` config (created in Task 4) includes the official IOTA testnet snapshot URL so the node bootstraps from a recent checkpoint rather than genesis. The `docker/iota/entrypoint.sh` script pre-downloads the snapshot before starting the node. For tests, use testnet which syncs faster. |
| No Go SDK means maintaining our own JSON-RPC client | Med | Med | Keep client thin — only implement the 5-6 RPC methods we actually need. Pin to specific API version. Add retry logic with exponential backoff (3 retries). |
| Ed25519 key derivation may differ from IOTA's exact scheme | Med | High | Use Blake2b-256 (IOTA Rebased uses same scheme as Sui). **Verify before any other task proceeds** by generating a keypair, deriving its address, and confirming on testnet explorer or via `iota_getOwnedObjects` RPC. |
| Docker image resource requirements (64GB RAM for mainnet) | Med | Med | Document requirements. Testnet needs less. For dev, use remote testnet RPC URL as fallback. |
| Breaking change in IOTA JSON-RPC API | Low | High | Pin Docker image to specific version tag (not floating `testnet`/`mainnet`). Add comment with date verified and expected API version. Abstract RPC calls behind interface for easy updates. |
| Balance-based payment detection unreliable for shared addresses | Med | Med | Known pre-existing limitation. Document in code with TODO for Phase 2 transition to transaction-based detection. Add delta-based check (previous balance vs current) as interim improvement. |

## Open Questions

- **RESOLVED:** Address derivation uses Blake2b-256 (same as Sui). Must be verified against testnet in Task 2 before proceeding.
- **RESOLVED:** Genesis snapshot IS required — handled by `docker/iota/entrypoint.sh` (Task 4).
- Whether we should support the IOTA GraphQL API as an alternative/fallback to JSON-RPC (GraphQL port 9125 noted in docker-compose as a comment for future use)

### Deferred Ideas

- Custom Move smart contract for Athena-specific payment tokens (Phase 2)
- IOTA staking integration for validator rewards
- Multi-sig wallet support
- Withdrawal/send transaction implementation (requires Programmable Transaction Block construction)
- GraphQL API support as alternative to JSON-RPC
- Transaction-based payment detection (replace balance-based approach for multi-intent reliability)
