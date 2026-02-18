# Messaging & Secure Messaging Security Review (Athena)

Date: 2026-02-17
Scope: Direct messaging (`/api/v1/messages`), secure messaging/E2EE code paths, and live stream chat WebSocket messaging.

## Executive Summary

Current production messaging is **not Messenger-style secure messaging**. Plain messaging is active, but the E2EE implementation is mostly disconnected from runtime routing and contains protocol/schema inconsistencies that prevent it from operating safely as designed. Even if fully wired, the current design is server-centric crypto (server unlocks and decrypts), which is materially different from Messenger’s client-side Signal-protocol model.

## Critical Findings

### [MSG-SEC-001] E2EE endpoints are documented but not registered in production routes

- Severity: Critical
- Location: `internal/httpapi/routes.go:245`, `internal/httpapi/routes.go:253`, `api/openapi.yaml:1570`, `api/openapi.yaml:1703`, `internal/httpapi/handlers/messaging/secure_messages.go:24`
- Evidence: Runtime routes register `/api/v1/messages` and `/api/v1/conversations`, but there is no `/api/v1/e2ee/*` or `/api/v1/messages/secure` route wiring. E2EE handler constructor appears only in unit tests.
- Impact: System appears to support secure messaging in API docs, but users cannot actually access those paths in production. This creates a false security posture.
- Fix: Wire E2EE routes explicitly in `internal/httpapi/routes.go` only after protocol/storage issues below are fixed.
- Mitigation: Remove/flag E2EE endpoints in OpenAPI until implemented end-to-end.

### [MSG-SEC-002] Server-side decryption model is not end-to-end encryption

- Severity: Critical
- Location: `internal/usecase/e2ee_service.go:95`, `internal/usecase/e2ee_service.go:193`, `internal/usecase/e2ee_service.go:649`, `internal/httpapi/handlers/messaging/secure_messages.go:291`
- Evidence: Server derives keys from user password, stores master key material (encrypted at rest), holds decrypted master key in server session memory, and exposes a decrypt endpoint returning plaintext.
- Impact: This is not equivalent to Messenger’s E2EE trust model where clients encrypt/decrypt and servers are ciphertext routers.
- Fix: Move crypto operations to clients (or trusted device agents) and treat backend as ciphertext transport/storage only.
- Mitigation: Clearly label current model as “server-mediated encryption” if retained temporarily.

### [MSG-SEC-003] Message schema constraints are internally contradictory for encrypted messages

- Severity: Critical
- Location: `migrations/015_create_messages_table.sql:8`, `migrations/015_create_messages_table.sql:9`, `migrations/016_add_e2ee_messaging.sql:116`, `migrations/016_add_e2ee_messaging.sql:117`, `internal/usecase/e2ee_service.go:635`
- Evidence:
  - `messages.content` is `NOT NULL`.
  - E2EE check requires `content IS NULL` when `is_encrypted=true`.
  - Message type check allows only `text|system`, while E2EE code writes `secure`.
- Impact: Encrypted message persistence cannot satisfy constraints reliably.
- Fix: Align schema and code: nullable plaintext field strategy or ciphertext-only schema, and update message_type constraint.
- Mitigation: Block E2EE writes until migration/schema contract is corrected.

### [MSG-SEC-004] Conversation E2EE state transition violates DB constraint

- Severity: Critical
- Location: `migrations/016_add_e2ee_messaging.sql:145`, `internal/usecase/e2ee_service.go:374`, `internal/usecase/e2ee_service.go:375`
- Evidence: DB constraint requires `is_encrypted=true` => `key_exchange_complete=true`; initiation sets `is_encrypted=true` and `key_exchange_complete=false`.
- Impact: Key exchange initiation will conflict with persisted constraints when migration is active.
- Fix: Redesign state machine/constraint (e.g., pending state column or deferred two-phase transition).
- Mitigation: Disable conversation encrypted-flag write until handshake completion.

## High Findings

### [MSG-SEC-005] Shared secret storage bug breaks one side of key exchange

- Severity: High
- Location: `internal/usecase/e2ee_service.go:483`, `internal/usecase/e2ee_service.go:510`, `internal/usecase/e2ee_service.go:570`
- Evidence: Recipient path stores `nonce+ciphertext`, but sender update stores only `ciphertext` (nonce dropped). Later decrypt path assumes `nonce+ciphertext` format.
- Impact: Sender may be unable to decrypt conversation shared secret, causing cryptographic failure and unreliable secure messaging.
- Fix: Persist sender shared secret in the same serialized format as recipient (`nonce+ciphertext`).
- Mitigation: Add integration test for full handshake from both sender/recipient encryption/decryption paths.

### [MSG-SEC-006] Client-provided secure messaging fields are ignored or reinterpreted

- Severity: High
- Location: `internal/httpapi/handlers/messaging/secure_messages.go:129`, `internal/httpapi/handlers/messaging/secure_messages.go:148`, `internal/httpapi/handlers/messaging/secure_messages.go:175`, `internal/httpapi/handlers/messaging/secure_messages.go:189`, `internal/httpapi/handlers/messaging/secure_messages.go:239`, `internal/httpapi/handlers/messaging/secure_messages.go:260`
- Evidence:
  - `public_key` and `signature` in initiate/accept requests are accepted by DTO but not used in service calls.
  - `pgp_signature` in send request is required but never verified.
  - `encrypted_content` request field is passed as plaintext input to server encryption.
- Impact: API contract suggests client-side cryptographic control that does not exist; risk of incorrect client assumptions and broken interoperability.
- Fix: Either remove these fields and document server-managed crypto, or implement true client-driven protocol semantics.
- Mitigation: Reject unused crypto fields until semantics are implemented.

### [MSG-SEC-007] E2EE session cache is process-local and concurrency-unsafe

- Severity: High
- Location: `internal/usecase/e2ee_service.go:92`, `internal/usecase/e2ee_service.go:254`, `internal/usecase/e2ee_service.go:265`, `internal/usecase/e2ee_service.go:273`
- Evidence: Global mutable map `userSessions` is accessed across handlers without mutex; code comment notes production should use Redis.
- Impact: Potential `concurrent map` panics/races; multi-instance deployments lose session consistency and can fail decrypt/encrypt flows unpredictably.
- Fix: Replace with synchronized session manager (mutex/sync.Map) plus shared store for distributed deployments.
- Mitigation: Restrict to single-instance and protect with lock immediately.

## Medium Findings

### [MSG-SEC-008] Chat WebSocket rate limiting can be bypassed with same-second bursts

- Severity: Medium
- Location: `internal/chat/websocket_server.go:480`, `internal/chat/websocket_server.go:489`, `internal/chat/websocket_server.go:500`
- Evidence: Redis ZSET member uses second-resolution timestamp (`Member: now`), so multiple messages in one second can overwrite same member and undercount.
- Impact: Attackers can exceed intended chat rate limits and spam.
- Fix: Use unique members per message (e.g., `<unix_nano>:<uuid>`), keep score as timestamp.
- Mitigation: Lower max message size and add server-side flood detection by per-connection counters.

### [MSG-SEC-009] Client IP in crypto audit logs can be spoofed

- Severity: Medium
- Location: `internal/httpapi/handlers/messaging/secure_messages.go:357`, `internal/httpapi/handlers/messaging/secure_messages.go:365`
- Evidence: `X-Forwarded-For` and `X-Real-IP` are trusted directly without trusted-proxy validation.
- Impact: Audit/forensics integrity is reduced; attackers can forge source IP values.
- Fix: Accept forwarded headers only from trusted proxy hops.
- Mitigation: Prefer `RemoteAddr` unless proxy trust is configured.

### [MSG-SEC-010] Session lifetime mismatch between docs and implementation

- Severity: Medium
- Location: `api/openapi.yaml:1620`, `internal/usecase/e2ee_service.go:251`
- Evidence: OpenAPI says unlock timeout is 15 minutes, service uses 24 hours.
- Impact: Security expectations mismatch; longer key-unlock exposure than documented.
- Fix: Align implementation and docs; shorten unlock window and enforce re-auth for sensitive operations.
- Mitigation: Update docs immediately and add explicit `expires_at` in responses.

## Additional Observations

- Plain direct messages are sanitized before persistence (`internal/usecase/message/service.go:64`) and are correctly auth-gated at route level (`internal/httpapi/routes.go:246`).
- Live chat still stores plaintext messages (`internal/repository/chat_repository.go:73`) and has no E2EE path.

## Messenger-Style Gap Analysis

To be similar to modern Messenger secure messaging, architecture should include:

1. Client-side Signal-style protocol flow (identity keys, signed pre-keys, session setup, Double Ratchet).
2. Backend as ciphertext router/storage; no server plaintext decrypt endpoint.
3. Device-level key verification and transparency protections.
4. Robust key/session state handling across multi-device and multi-instance deployments.
5. Honest API/docs alignment so security guarantees match runtime behavior.

## Recommended Remediation Order

1. Remove false E2EE claims from runtime/docs until working safely.
2. Fix schema/protocol contradictions (messages/conversation constraints and types).
3. Decide target model: true client-side E2EE (recommended) vs server-managed encryption.
4. If true E2EE: implement client key lifecycle and protocol state management first, then expose routes.
5. Harden chat anti-abuse (rate-limiter member uniqueness) and proxy-trust handling.
