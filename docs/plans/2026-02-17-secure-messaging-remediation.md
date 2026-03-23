# Secure Messaging Remediation Implementation Plan

Created: 2026-02-17
Status: VERIFIED
Approved: Yes
Iterations: 1
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

**Goal:** Remediate 10 security findings (MSG-SEC-001 through MSG-SEC-010) in the E2EE messaging system by redesigning the architecture from server-mediated encryption to true client-side E2EE (server as ciphertext transport only), fixing DB schema contradictions, hardening chat rate limiting and IP trust, and creating comprehensive Postman E2E tests covering full sender and recipient flows.

**Architecture:** Transform the E2EE service from a server-side crypto engine (password-derived keys, server encrypt/decrypt) to a ciphertext transport layer where clients handle all cryptographic operations. Server stores public identity keys, facilitates key exchange by relaying X25519 public keys, and stores/delivers encrypted message blobs without ever seeing plaintext. This resolves MSG-SEC-002 (server-side decryption), MSG-SEC-005 (shared secret storage bug), and MSG-SEC-007 (session cache concurrency) by eliminating server-side key material entirely.

> **SCOPE NOTE:** This plan implements Signal-aligned primitives (X25519 ECDH + XChaCha20-Poly1305 + Ed25519) but defers the Double Ratchet mechanism. Forward secrecy is per-session (one ECDH exchange per conversation), not per-message. Full Signal protocol (Double Ratchet for per-message forward secrecy and break-in recovery) is deferred to a future plan. This is a deliberate scope reduction — the foundation is Signal-compatible but the ratcheting layer is not yet present.

**Tech Stack:** Go, Chi router, PostgreSQL (SQLX), XChaCha20-Poly1305, X25519 ECDH, Ed25519 signing, Postman/Newman for E2E tests

## Scope

### In Scope

- Fix DB schema contradictions: messages.content nullable, message_type constraint, conversation state machine (MSG-SEC-003, MSG-SEC-004)
- Redesign E2EE service as ciphertext transport (MSG-SEC-002)
- Redesign domain models and DTOs for client-side crypto
- Update secure message handlers to accept pre-encrypted ciphertext (MSG-SEC-006)
- Wire E2EE routes in production routes.go (MSG-SEC-001)
- Fix chat WebSocket rate limiter ZSET member uniqueness (MSG-SEC-008)
- Fix client IP trust to prefer RemoteAddr over spoofable headers (MSG-SEC-009)
- Align session/unlock timeout between docs and code (MSG-SEC-010)
- Update OpenAPI spec to reflect true E2EE model
- Create Postman E2E test collection: full sender+recipient secure messaging flows

### Out of Scope

- Signal protocol Double Ratchet (forward secrecy per-message) — deferred to future plan
- Multi-device key sync — deferred
- Key transparency / device verification UI — deferred
- Live stream chat E2EE — chat remains plaintext
- Plain messaging changes — `/api/v1/messages` untouched
- Client-side SDK/library implementation — server-side changes only

## Prerequisites

- PostgreSQL running for migration testing
- Redis running for rate limiter testing
- Newman installed for Postman E2E test execution
- Existing plain messaging tests must continue passing

## Context for Implementer

> This section is critical for cross-session continuity.

- **Patterns to follow:** Handler pattern in `internal/httpapi/handlers/messaging/secure_messages.go:31` (decode → validate → call service → respond). Route wiring pattern in `internal/httpapi/routes.go:245` (Chi router groups with middleware).
- **Conventions:** Domain models in `internal/domain/message.go`, DTOs as request/response structs with `validate` tags. Error handling via string matching in handlers (see `secure_messages.go:150`). Migrations are Goose SQL in `migrations/` directory. Postman collections in `postman/` with Newman for CI.
- **Key files the implementer must read first:**
  - `internal/usecase/e2ee_service.go` — Current E2EE service (904 lines, server-side crypto — being rewritten)
  - `internal/httpapi/handlers/messaging/secure_messages.go` — E2EE HTTP handlers
  - `internal/httpapi/handlers/messaging/e2ee_interface.go` — Service interface used by handlers
  - `internal/domain/message.go` — Message, Conversation, and E2EE domain models + DTOs
  - `migrations/015_create_messages_table.sql` — Original messages/conversations schema
  - `migrations/016_add_e2ee_messaging.sql` — E2EE schema additions (has contradictory constraints)
  - `internal/httpapi/routes.go:245` — Where messaging routes are wired (E2EE routes missing)
  - `internal/chat/websocket_server.go:476` — Chat rate limiter (ZSET member bug)
- **Gotchas:**
  - `messages.content` is `NOT NULL` in migration 015, but E2EE constraint in migration 016 requires `content IS NULL` when `is_encrypted=true` — these are mutually exclusive and will cause INSERT failures
  - `message_type` CHECK allows only `text|system`, but E2EE code writes `secure` — INSERT will fail
  - Conversation constraint `is_encrypted=true => key_exchange_complete=true` prevents the key exchange initiation step from writing `is_encrypted=true, key_exchange_complete=false`
  - `userSessions` global map at `e2ee_service.go:92` has no mutex — concurrent access will panic
  - Sender's shared secret at `e2ee_service.go:510` stores only ciphertext (nonce dropped), but decrypt path at `e2ee_service.go:570` expects nonce+ciphertext format
  - The `ensure_conversation_order` trigger in migration 015 swaps participant IDs to maintain ordering — `GetByParticipants` must handle this
- **Domain context:** True E2EE means the server NEVER sees plaintext. Clients generate keypairs locally, compute shared secrets via X25519 ECDH, encrypt messages with XChaCha20-Poly1305, and send ciphertext to the server. The server's role is: store public keys, relay key exchange messages, store/deliver encrypted blobs. The `/decrypt` endpoint is removed entirely — decryption happens client-side only.

## Runtime Environment

- **Start command:** `docker compose up postgres redis -d && make run` (or `docker compose --profile test up -d`)
- **Port:** 8080 (configurable via PORT env var)
- **Health check:** `curl http://localhost:8080/health`
- **Newman test command:** `newman run postman/vidra-secure-messaging.postman_collection.json -e postman/test-env.json`
- **Prerequisites:** PostgreSQL running with migrations applied (`make migrate-up`), Redis running
- **postman/test-env.json:** Must set `baseUrl=http://localhost:8080`. Test users are registered by the collection itself (no pre-seeding needed).

## Feature Inventory — Files Being Modified

| File | Current Functions | Mapped to Task |
| ---- | ---------------- | -------------- |
| `migrations/016_add_e2ee_messaging.sql` | Schema constraints (content NOT NULL contradiction, conversation state machine) | Task 1 |
| `internal/domain/message.go` | E2EE DTOs: SetupE2EERequest, UnlockE2EERequest, SendSecureMessageRequest, etc. | Task 2 |
| `internal/httpapi/handlers/messaging/e2ee_interface.go` | E2EEServiceInterface (12 methods) | Task 2 |
| `internal/usecase/e2ee_service.go` | E2EEService (SetupE2EE, UnlockE2EE, EncryptMessage, DecryptMessage, etc.) | Task 3 |
| `internal/httpapi/handlers/messaging/secure_messages.go` | SecureMessagesHandler (SetupE2EE, UnlockE2EE, SendSecureMessage, DecryptMessage, etc.) | Task 4 |
| `internal/httpapi/routes.go` | No E2EE routes wired | Task 5 |
| `internal/chat/websocket_server.go` | `checkRateLimit` ZSET member uses `now` (not unique) | Task 6 |
| `internal/httpapi/handlers/messaging/secure_messages.go` | `GetClientIP` trusts X-Forwarded-For/X-Real-IP directly | Task 7 |
| `api/openapi.yaml` | E2EE endpoints documented but non-functional | Task 8 |
| `postman/` (new file) | No messaging Postman collection exists | Task 9 |

### Feature Mapping Verification

- [x] All files being modified listed above
- [x] All functions/classes identified
- [x] Every feature has a task number
- [x] No features accidentally omitted

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix E2EE database schema contradictions (migration 065)
- [x] Task 2: Redesign domain models and interface for true E2EE
- [x] Task 3: Rewrite E2EE service as ciphertext transport
- [x] Task 4: Update secure message handlers for client-side crypto
- [x] Task 5: Wire E2EE routes in production router
- [x] Task 6: Fix chat WebSocket rate limiter member uniqueness
- [x] Task 7: Fix client IP trust and align session timeout
- [x] Task 8: Update OpenAPI specification for true E2EE model
- [x] Task 9: Create Postman E2E test collection for secure messaging
- [x] Task 10: [FIX] Repository ErrNotFound mapping and missing public_identity_key column
- [x] Task 11: [FIX] Replace fragile string prefix error matching with sentinel errors

> Extended 2026-02-17: Tasks 10-11 added for quality findings from verification iteration 1

**Total Tasks:** 11 | **Completed:** 11 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix E2EE Database Schema Contradictions

**Objective:** Create migration 065 that resolves MSG-SEC-003 (contradictory message constraints) and MSG-SEC-004 (conversation state machine violation) so E2EE message persistence actually works.

**Dependencies:** None

**Files:**

- Create: `migrations/065_fix_e2ee_schema_contradictions.sql` (verify 065 is next available: `ls migrations/ | sort | tail -5`)
- Test: `migrations/065_fix_e2ee_schema_contradictions.sql` (verified via `make migrate-up` + `make migrate-down`)

**Key Decisions / Notes:**

- Drop the `check_encrypted_content` constraint from migration 016 and replace with a corrected version:
  - When `is_encrypted=true`: `encrypted_content IS NOT NULL AND content_nonce IS NOT NULL` (content can be NULL or placeholder)
  - When `is_encrypted=false`: `content IS NOT NULL` (encrypted_content/content_nonce can be NULL)
- Make `messages.content` nullable: `ALTER TABLE messages ALTER COLUMN content DROP NOT NULL`
- Update `message_type` CHECK constraint: add `'secure'` and `'key_exchange'` to allowed values
- Replace conversation `check_encrypted_key_exchange` constraint:
  - Drop existing constraint
  - Add `encryption_status` column: `VARCHAR(20) DEFAULT 'none' CHECK (encryption_status IN ('none', 'pending', 'active'))`
  - This replaces the boolean pair `is_encrypted + key_exchange_complete` with a proper state machine
  - Keep old columns for backward compatibility but remove the contradictory constraint
- Add `public_identity_key TEXT` column to `user_signing_keys` table for storing X25519 ECDH public keys (needed by Task 3's `RegisterIdentityKey`)
- Make `encrypted_private_key` nullable: `ALTER TABLE user_signing_keys ALTER COLUMN encrypted_private_key DROP NOT NULL` — in true E2EE the server never holds private keys, so new rows store NULL for this column
- Follow Goose migration pattern from `migrations/CLAUDE.md`

**Definition of Done:**

- [ ] Migration 065 applies cleanly with `make migrate-up`
- [ ] `messages.content` accepts NULL when `is_encrypted=true`
- [ ] `message_type` accepts `'secure'` value
- [ ] Conversation allows `encryption_status='pending'` (key exchange in progress)
- [ ] `user_signing_keys` table has `public_identity_key` column for storing X25519 public keys
- [ ] Existing plain messages (non-encrypted) still satisfy constraints
- [ ] All existing tests pass

**Verify:**

- `make migrate-up` — migration applies cleanly
- Verify constraints with psql: `INSERT INTO messages (sender_id, recipient_id, content, message_type, is_encrypted) VALUES (gen_random_uuid(), gen_random_uuid(), NULL, 'secure', true)` succeeds; plain message with `content='hello', message_type='text', is_encrypted=false` still works
- Note: Migration follows project forward-only pattern (empty Down section). Reversibility tested via constraint correctness, not migrate-down cycle.
- `go test ./internal/domain/... -short -count=1` — domain tests pass
- `go test ./internal/usecase/message/... -short -count=1` — message service tests pass

---

### Task 2: Redesign Domain Models and Interface for True E2EE

**Objective:** Update domain models and the `E2EEServiceInterface` to reflect a ciphertext-transport architecture where clients handle all crypto operations and the server only stores/relays encrypted data.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/domain/message.go` (update DTOs, add new request types)
- Modify: `internal/httpapi/handlers/messaging/e2ee_interface.go` (new interface methods)
- Test: `internal/domain/message_test.go`

**Key Decisions / Notes:**

- **Remove server-crypto DTOs:** `SetupE2EERequest` (password-based) → replaced by `RegisterIdentityKeyRequest` (client sends public key only). `UnlockE2EERequest` → removed (no server-side key material to unlock). `SendSecureMessageRequest.EncryptedContent` field is now truly client-encrypted ciphertext (not plaintext input to server encryption).
- **New DTOs:**
  - `RegisterIdentityKeyRequest { PublicIdentityKey string, PublicSigningKey string }` — client registers public keys
  - `StoreEncryptedMessageRequest { RecipientID string, EncryptedContent string, ContentNonce string, Signature string }` — client sends pre-encrypted ciphertext. Validation: `EncryptedContent` max 8192 chars (base64, ~6KB plaintext), `ContentNonce` exactly 32 chars (24-byte base64 nonce), `Signature` max 128 chars. Prevents DoS via oversized payloads.
- **Updated interface methods:**
  - `RegisterIdentityKey(ctx, userID, publicIdentityKey, publicSigningKey string) error`
  - `GetPublicKeys(ctx, userID string) (*PublicKeyBundle, error)`
  - `InitiateKeyExchange(ctx, senderID, recipientID string) (*KeyExchangeMessage, error)` — simplified, no clientIP/userAgent needed for relay
  - `AcceptKeyExchange(ctx, keyExchangeID, userID, publicKey string) error`
  - `StoreEncryptedMessage(ctx, message *Message) error`
  - `GetEncryptedMessages(ctx, conversationID, userID string, limit, offset int) ([]*Message, error)`
  - Remove: `EncryptMessage`, `DecryptMessage`, `SetupE2EE`, `UnlockE2EE`, `LockE2EE`
- Add `encryption_status` field to `Conversation` struct matching new DB column
- Add `PublicKeyBundle` struct: `{ PublicIdentityKey string, PublicSigningKey string, KeyVersion int }`
- **Pattern:** Follow existing DTO conventions in `message.go:63` with `validate` struct tags

**Definition of Done:**

- [ ] `SetupE2EERequest` and `UnlockE2EERequest` removed or replaced
- [ ] `RegisterIdentityKeyRequest` and `StoreEncryptedMessageRequest` DTOs exist with proper validation tags
- [ ] `E2EEServiceInterface` reflects ciphertext-transport methods (no encrypt/decrypt)
- [ ] `Conversation` struct has `EncryptionStatus` field
- [ ] All domain tests pass

**Verify:**

- `go test ./internal/domain/... -short -count=1` — domain tests pass
- `go vet ./internal/domain/... ./internal/httpapi/handlers/messaging/...` — no type errors

---

### Task 3: Rewrite E2EE Service as Ciphertext Transport

**Objective:** Replace the server-side crypto E2EE service with a ciphertext transport service where the server stores public keys, relays key exchange messages, and stores/delivers encrypted message blobs — never seeing plaintext. This resolves MSG-SEC-002 (server-side decryption), MSG-SEC-005 (shared secret storage), and MSG-SEC-007 (session cache concurrency) by eliminating all server-side key material.

**Dependencies:** Task 1, Task 2

**Files:**

- Modify: `internal/usecase/e2ee_service.go` (major rewrite)
- Test: `internal/usecase/e2ee_service_test.go` (create or update)

**Key Decisions / Notes:**

- **Remove entirely:** `userSessions` global map, `SetupE2EE` (password-based), `UnlockE2EE`, `LockE2EE`, `IsUnlocked`, `EncryptMessage`, `DecryptMessage`, `getUserSigningKey` (server no longer holds private keys)
- **Replace with:**
  - `RegisterIdentityKey(ctx, userID, pubIdentityKey, pubSigningKey)` — stores public keys in `user_signing_keys` table (repurposed: signing key + identity key)
  - `GetPublicKeys(ctx, userID)` — retrieves public key bundle for a user
  - `InitiateKeyExchange(ctx, senderID, recipientID)` — creates key exchange record with sender's public key, sets conversation `encryption_status='pending'`
  - `AcceptKeyExchange(ctx, keyExchangeID, userID, recipientPublicKey)` — stores recipient's public key, sets conversation `encryption_status='active'`
  - `StoreEncryptedMessage(ctx, message)` — validates conversation has `encryption_status='active'`, stores the encrypted blob
  - `GetEncryptedMessages(ctx, conversationID, userID, limit, offset)` — resolves conversationID to (participant_one_id, participant_two_id) from conversations table, then queries messages WHERE `((sender_id=$1 AND recipient_id=$2) OR (sender_id=$2 AND recipient_id=$1)) AND is_encrypted=true` with pagination. Returns encrypted message blobs for authorized participants only.
- **Audit logging retained** but simplified: no clientIP/userAgent spoofing concern since we log RemoteAddr (see Task 7)
- **No dependency on `crypto.CryptoService`** — server does no crypto. Remove the `cryptoService` field.
- **Transaction usage:** `WithTransaction` pattern stays for atomic operations (key exchange + conversation update)
- **Race condition handling:** In `InitiateKeyExchange`, catch PostgreSQL error code 23505 (unique_violation) using `errors.As` with `pq.Error` and return `domain.ErrConflict` (not a raw wrapped error). This ensures the handler maps it to 409 instead of 500.
- **Audit logging:** Retain `clientIP` and `userAgent` parameters in methods that write to `crypto_audit_log` (InitiateKeyExchange, AcceptKeyExchange, RegisterIdentityKey). IP is extracted by handler via `GetClientIP()` and passed to service.

**Definition of Done:**

- [ ] `userSessions` map removed — no server-side key material
- [ ] No server-side encrypt/decrypt functions exist
- [ ] `RegisterIdentityKey` stores public keys correctly
- [ ] `InitiateKeyExchange` creates exchange record and sets `encryption_status='pending'`
- [ ] `AcceptKeyExchange` completes exchange and sets `encryption_status='active'`
- [ ] `StoreEncryptedMessage` validates conversation status and stores ciphertext
- [ ] Tests cover: register keys, key exchange flow, store/retrieve messages, authorization checks
- [ ] All tests pass

**Verify:**

- `go test ./internal/usecase/... -short -count=1` — all usecase tests pass
- `go vet ./internal/usecase/...` — clean

---

### Task 4: Update Secure Message Handlers for Client-Side Crypto

**Objective:** Rewrite the HTTP handlers to match the new ciphertext-transport service interface. Remove server-decrypt endpoint, add key registration endpoint, update send/receive to handle pre-encrypted ciphertext. Resolves MSG-SEC-006 (ignored client fields).

**Dependencies:** Task 2, Task 3

**Files:**

- Modify: `internal/httpapi/handlers/messaging/secure_messages.go` (rewrite handlers)
- Test: `internal/httpapi/handlers/messaging/secure_messages_test.go` (update tests)

**Key Decisions / Notes:**

- **Remove handlers:** `SetupE2EE` (password-based), `UnlockE2EE`, `LockE2EE`, `DecryptMessage` (client does this)
- **New handlers:**
  - `RegisterKeys` — POST, accepts `RegisterIdentityKeyRequest`, calls `RegisterIdentityKey`
  - `GetUserPublicKeys` — GET `/{userId}/keys`, returns public key bundle
- **Updated handlers:**
  - `InitiateKeyExchange` — client provides `{ RecipientID, SenderPublicKey }` where `SenderPublicKey` is the client-generated X25519 ephemeral public key (base64-encoded). Server stores it in the key exchange record and does NOT generate any key material. Requests without `SenderPublicKey` return 400 Bad Request.
  - `AcceptKeyExchange` — client provides their X25519 public key for ECDH
  - `SendSecureMessage` — takes `StoreEncryptedMessageRequest` with pre-encrypted ciphertext, nonce, signature. Server stores as-is, no re-encryption
  - `GetEncryptedMessages` — GET, verifies requesting user is a participant in the conversation (returns 403 otherwise), calls `GetEncryptedMessages` service method, returns paginated encrypted message blobs
  - `GetE2EEStatus` — updated to return key registration status instead of unlock status
- **MSG-SEC-006 resolution:** All client-provided crypto fields are now meaningful — `encrypted_content` is truly client-encrypted, `signature` is verified by recipients (not server), `public_key` is used in key exchange
- **Error handling:** Use domain sentinel errors instead of string matching (follow `internal/httpapi/shared/response.go` pattern with `MapDomainErrorToHTTP`). **Error-to-HTTP mapping:**
  | Service Error | HTTP Status |
  | `domain.ErrConflict` (duplicate key registration, race condition) | 409 |
  | `domain.ErrNotFound` (conversation not found, user not found) | 404 |
  | `domain.ErrForbidden` (not a participant) | 403 |
  | `domain.ErrValidation` (missing SenderPublicKey, bad format) | 400 |
  | New sentinel `ErrEncryptionNotReady` (conversation status != 'active') | 412 |
- **Audit logging:** Keep `clientIP` parameter in `InitiateKeyExchange` and `AcceptKeyExchange` service signatures for audit log entries. Handler extracts trusted IP via `GetClientIP()` (fixed in Task 7) and passes it to the service.

**Definition of Done:**

- [ ] No server-decrypt endpoint exists
- [ ] `RegisterKeys` handler stores public keys
- [ ] `SendSecureMessage` stores pre-encrypted ciphertext without re-encrypting
- [ ] All request DTO fields are meaningful (no unused/ignored fields)
- [ ] `GetEncryptedMessages` handler exists and returns 403 when authenticated user is not a participant
- [ ] `InitiateKeyExchange` request body requires `SenderPublicKey`; requests without it return 400 Bad Request
- [ ] Handler tests cover all endpoints with mock service
- [ ] All tests pass

**Verify:**

- `go test ./internal/httpapi/handlers/messaging/... -short -count=1` — handler tests pass
- `go vet ./internal/httpapi/...` — clean

---

### Task 5: Wire E2EE Routes in Production Router

**Objective:** Register E2EE endpoints in `routes.go` so they are accessible in production. Resolves MSG-SEC-001.

**Dependencies:** Task 4

**Files:**

- Modify: `internal/httpapi/routes.go` (add E2EE route group)
- Modify: `internal/app/app.go` (wire E2EEService into handler, if not already)
- Test: Integration test via route verification

**Key Decisions / Notes:**

- Add new route group under `/api/v1/e2ee`:

  ```
  POST   /api/v1/e2ee/keys              → RegisterKeys
  GET    /api/v1/e2ee/keys/{userId}     → GetUserPublicKeys
  GET    /api/v1/e2ee/status            → GetE2EEStatus
  POST   /api/v1/e2ee/key-exchange      → InitiateKeyExchange
  POST   /api/v1/e2ee/key-exchange/accept → AcceptKeyExchange
  GET    /api/v1/e2ee/key-exchange/pending → GetPendingKeyExchanges
  POST   /api/v1/e2ee/messages          → SendSecureMessage
  GET    /api/v1/e2ee/messages/{conversationId} → GetEncryptedMessages (NEW)
  ```

- All routes require `middleware.Auth(cfg.JWTSecret)`
- Follow the pattern at `routes.go:245` for route grouping
- **Major wiring task in `app.go`:** The Dependencies struct (lines 69-117) has NO E2EEService field. Requires:
  - (a) Add `E2EEService` field to `Dependencies` struct
  - (b) Add/instantiate `CryptoRepository` (or use existing repo) in `initializeDependencies()`
  - (c) Call `usecase.NewE2EEService(...)` with repo dependencies and assign to `deps.E2EEService`
  - (d) Construct `messaging.NewSecureMessagesHandler(deps.E2EEService, validator)` and register handlers

**Definition of Done:**

- [ ] All E2EE endpoints accessible via `/api/v1/e2ee/*`
- [ ] All E2EE routes require authentication
- [ ] `app.go` wires E2EEService and SecureMessagesHandler
- [ ] Build succeeds: `go build ./cmd/server`

**Verify:**

- `go build ./cmd/server` — compiles without errors
- `go test ./internal/httpapi/... -short -count=1` — route tests pass

---

### Task 6: Fix Chat WebSocket Rate Limiter Member Uniqueness

**Objective:** Fix MSG-SEC-008 where Redis ZSET rate limiter uses second-resolution timestamp as member, allowing same-second burst messages to overwrite and bypass the rate limit.

**Dependencies:** None

**Files:**

- Modify: `internal/chat/websocket_server.go` (~line 489)
- Test: `internal/chat/websocket_server_test.go`

**Key Decisions / Notes:**

- Current code: `pipe.ZAdd(ctx, key, redis.Z{Score: float64(now), Member: now})` — `Member: now` (int64 unix seconds) means multiple messages in the same second overwrite the same ZSET member
- Fix: Use unique member per message: `Member: fmt.Sprintf("%d:%s", time.Now().UnixNano(), uuid.New().String())`
- Keep `Score: float64(now)` (seconds) for the sliding window range query
- This is a one-line fix with high impact

**Definition of Done:**

- [ ] ZSET member is unique per message (uses nano timestamp + UUID)
- [ ] Score still uses seconds for sliding window cleanup
- [ ] Multiple messages in the same second are each counted individually
- [ ] New unit test sends N messages within the same second and asserts the ZSET contains N distinct members, verifying the burst bypass is closed
- [ ] Existing rate limit tests pass

**Verify:**

- `go test ./internal/chat/... -short -count=1` — chat tests pass (including new burst test)
- `go test ./internal/chat/... -run TestRateLimit -v -count=1` — rate limit tests specifically
- `go vet ./internal/chat/...` — clean

---

### Task 7: Fix Client IP Trust and Align Session Timeout

**Objective:** Fix MSG-SEC-009 (spoofable client IP in audit logs) and MSG-SEC-010 (session lifetime mismatch between docs and code).

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/messaging/secure_messages.go` (`GetClientIP` function, ~line 357)
- Modify: `api/openapi.yaml` (~line 1620, session timeout documentation)
- Test: `internal/httpapi/handlers/messaging/secure_messages_test.go`

**Key Decisions / Notes:**

- **IP Trust (MSG-SEC-009):** Change `GetClientIP` to prefer `r.RemoteAddr` as default. Only trust `X-Forwarded-For`/`X-Real-IP` when request comes through a trusted proxy. For now, since Vidra Core uses nginx as reverse proxy (`NginxEnabled` in config), check if the connection comes from a local/private IP range before trusting forwarded headers. If `r.RemoteAddr` is not from a trusted proxy network (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16), ignore forwarded headers.
- **Session Timeout (MSG-SEC-010):** The `e2ee_service.go:251` sets 24-hour session expiry but OpenAPI says 15 minutes. Since we're removing server-side sessions in Task 3, this specific bug goes away. But update the OpenAPI docs to document the new model (no server-side sessions at all). If any session concept remains (e.g., key exchange timeout), align to 1 hour (matching key exchange expiry at `e2ee_service.go:356`).

**Definition of Done:**

- [ ] `GetClientIP` only trusts forwarded headers from private/loopback source IPs
- [ ] Direct connections from public IPs use `RemoteAddr` regardless of forwarded headers
- [ ] Tests verify IP extraction with and without trusted proxy
- [ ] OpenAPI spec contains no reference to 24-hour or 15-minute server-side E2EE session/unlock timeout; any key exchange expiry documentation reads 1 hour matching the implementation constant

**Verify:**

- `go test ./internal/httpapi/handlers/messaging/... -short -count=1` — handler tests pass

---

### Task 8: Update OpenAPI Specification for True E2EE Model

**Objective:** Update `api/openapi.yaml` to remove false E2EE claims, document the ciphertext-transport model, and define the new `/api/v1/e2ee/*` endpoints accurately.

**Dependencies:** Task 4, Task 5

**Files:**

- Modify: `api/openapi.yaml` (E2EE endpoint section)

**Key Decisions / Notes:**

- Remove old E2EE endpoint definitions that reference server-side decrypt
- Add new endpoint definitions matching Task 5 routes
- Document that encryption/decryption happens client-side
- Document key exchange flow: register keys → initiate → accept → exchange messages
- Remove references to "unlock" / password-based key derivation
- Add security note: "Server acts as ciphertext transport. Plaintext is never transmitted to or stored on the server."
- Update session timeout references (no server-side crypto sessions)

**Definition of Done:**

- [ ] No `/decrypt` endpoint in OpenAPI spec
- [ ] All `/api/v1/e2ee/*` endpoints documented with request/response schemas
- [ ] Security model clearly labeled as client-side E2EE with server as ciphertext transport
- [ ] No references to server-side password-based key derivation

**Verify:**

- `grep -c '/decrypt' api/openapi.yaml` — should return 0 (no decrypt endpoint)
- `grep -c '/api/v1/e2ee/keys' api/openapi.yaml` — should return ≥ 1 (keys endpoints documented)
- `grep -ci 'password.*key\|key.*derivation\|unlock.*session' api/openapi.yaml` — should return 0 in E2EE section (no server-side crypto references)
- `grep -c 'ciphertext transport' api/openapi.yaml` — should return ≥ 1 (security model documented)

---

### Task 9: Create Postman E2E Test Collection for Secure Messaging

**Objective:** Create a comprehensive Postman collection testing the full sender and recipient secure messaging flow, including key registration, key exchange from both sides, encrypted message send/receive, and error cases.

**Dependencies:** Task 5 (routes must be wired)

**Files:**

- Create: `postman/vidra-secure-messaging.postman_collection.json`
- Modify: `postman/test-env.json` (add E2EE-related variables if needed)

**Key Decisions / Notes:**

- **Two-user test flow (User A = sender, User B = recipient):**
  1. Register User A (use existing auth collection pattern from `postman/vidra-auth.postman_collection.json`)
  2. Register User B
  3. User A: POST `/api/v1/e2ee/keys` — register identity key
  4. User B: POST `/api/v1/e2ee/keys` — register identity key
  5. User A: GET `/api/v1/e2ee/keys/{userB_id}` — get User B's public keys
  6. User A: POST `/api/v1/e2ee/key-exchange` — initiate with User B
  7. User B: GET `/api/v1/e2ee/key-exchange/pending` — see pending exchange
  8. User B: POST `/api/v1/e2ee/key-exchange/accept` — accept exchange
  9. User A: POST `/api/v1/e2ee/messages` — send encrypted message (simulated ciphertext)
  10. User B: GET `/api/v1/e2ee/messages/{conversationId}` — retrieve encrypted messages
  11. User B: POST `/api/v1/e2ee/messages` — reply with encrypted message
  12. User A: GET `/api/v1/e2ee/messages/{conversationId}` — retrieve reply
- **Session management tests (after step 6, before step 8):**
  13. User A: GET `/api/v1/e2ee/status` — assert `encryption_status='pending'`
  14. User A: POST `/api/v1/e2ee/messages` — attempt send while status is 'pending' → 412 Precondition Failed
  15. (After step 8) User A: GET `/api/v1/e2ee/status` — assert `encryption_status='active'`
  16. User B: GET `/api/v1/e2ee/status` — assert `encryption_status='active'`
- **Error cases:**
  - Send message without key exchange → 412 Precondition Failed
  - Send message while key exchange pending → 412 Precondition Failed
  - Accept someone else's key exchange → 403 Forbidden
  - Get messages from conversation you're not in → 403 Forbidden
  - Register keys twice → 409 Conflict
  - Initiate key exchange with yourself → 400 Bad Request
  - Send message without auth → 401 Unauthorized
- **Simulated ciphertext:** Use Postman pre-request scripts to generate valid base64-encoded random bytes for test data. Content nonce: 24 random bytes base64-encoded (~32 chars). Encrypted content: 32+ random bytes base64-encoded. Signature: 64 random bytes base64-encoded. The service treats ciphertext as opaque blobs without length/format validation beyond non-empty base64.
- **Test assertions:** Use Postman test scripts to validate response status codes, JSON structure, and that encrypted content is opaque (not plaintext)
- **Collection variables:** `baseUrl`, `userA_token`, `userB_token`, `userA_id`, `userB_id`, `keyExchangeId`, `conversationId`
- Follow existing collection pattern from `postman/vidra-auth.postman_collection.json`

**Definition of Done:**

- [ ] Postman collection covers full sender+recipient flow (12 happy-path requests + 4 session management requests)
- [ ] Session management scenarios tested: encryption_status transitions verified via status endpoint, send-before-complete rejected with 412
- [ ] At least 7 error cases tested (including send-while-pending)
- [ ] Each request has test scripts validating status codes and response shape
- [ ] Collection runs successfully with Newman: `newman run postman/vidra-secure-messaging.postman_collection.json -e postman/test-env.json`
- [ ] Collection variables properly chain responses (token from login → subsequent requests)

**Verify:**

- `newman run postman/vidra-secure-messaging.postman_collection.json -e postman/test-env.json --dry-run` — collection structure is valid
- Manual review of test scripts in collection

---

## Testing Strategy

- **Unit tests:** Each task has dedicated unit tests. E2EE service tests mock the repository layer. Handler tests mock the service interface. Rate limiter tests use Redis mock.
- **Integration tests:** Postman E2E collection (Task 9) tests the full HTTP flow with real server. Newman in CI validates collection.
- **Manual verification:** After all tasks, start server and run Postman collection against live instance. Verify that plain messaging (`/api/v1/messages`) still works unchanged.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| ---- | ---------- | ------ | ---------- |
| Migration 065 breaks existing data | Low | High | Migration only modifies constraints and adds columns with defaults. No data transformation. Test with `migrate-up` + `migrate-down` cycle. |
| E2EE service rewrite breaks existing handler tests | Med | Med | Update `e2ee_interface.go` first (Task 2), then update mock implementations in handler tests to match new interface. Run tests after each task. |
| Plain messaging regression | Low | High | Plain messaging routes and service (`/api/v1/messages`, `message/service.go`) are NOT modified. Run existing message tests after every task to verify no regression. |
| Postman tests depend on running server | Med | Low | Collection is designed to run against test profile (`docker compose --profile test`). Also support `--dry-run` for collection structure validation without live server. |
| Key exchange race condition (two users initiate simultaneously) | Low | Med | Use DB unique constraint on `(conversation_id, user_id, key_version)` in `conversation_keys` to prevent duplicate key records. Service returns 409 Conflict if exchange already exists. |
| Removing server-decrypt endpoint breaks existing clients | Low | Low | No clients currently use E2EE endpoints (MSG-SEC-001 confirmed routes are not wired). Breaking change is safe since the feature was never accessible. |

## Open Questions

- Should key exchange messages have a configurable expiry, or is 1 hour fixed? (Currently hardcoded to 1 hour)
- Should the `crypto_audit_log` table continue logging in the ciphertext-transport model? (Recommend yes — log key registrations, exchanges, message storage events)

### Deferred Ideas

- Signal protocol Double Ratchet for per-message forward secrecy
- Multi-device key sync (pre-key bundles per device)
- Key transparency log for public key verification
- Group encrypted messaging (multi-party key exchange)
- Client-side SDK/library for JS, Swift, Kotlin
- Live stream chat E2EE
