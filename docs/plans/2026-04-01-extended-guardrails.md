# Extended Guardrails Implementation Plan

Created: 2026-04-01
Author: yegamble@gmail.com
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Add domain-specific guardrails as rules files and enforcement hooks covering IPFS, ATProto, IOTA, PeerTube parity, and Go concurrency patterns — filling gaps not covered by the 7 existing hooks.

**Architecture:** 5 new rules files in `.claude/rules/` (one per domain) plus 2 new PreToolUse hooks in `.claude/settings.json` for hard-block patterns. Rules files provide advisory guidance loaded into Claude's context; hooks enforce blocking checks on every edit.

**Tech Stack:** Markdown rules files, bash hook commands

## Scope

### In Scope

- 5 rules files: `ipfs-guardrails.md`, `atproto-guardrails.md`, `iota-guardrails.md`, `peertube-parity.md`, `go-concurrency.md`
- 2 new hooks: context.Background() in handlers (block), raw json.NewEncoder in handlers (warn)
- Each rules file covers: required patterns, forbidden patterns, validation requirements, testing expectations

### Out of Scope

- Modifying production Go code (rules/hooks only)
- golangci-lint configuration changes
- CI/CD pipeline changes

## Approach

**Chosen:** One file per domain + targeted hooks

**Why:** Domain-scoped files are easy to find and update. Hooks catch the patterns most likely to cause real bugs (wrong context in handlers, bypassing response envelope). Low overhead, no false positive risk on rules files.

**Alternatives considered:**
- Single comprehensive file — rejected: too large, hard to maintain
- Hooks only — rejected: can't express nuanced guidance via grep patterns

## Context for Implementer

- **Existing hooks:** `.claude/settings.json` has 6 PreToolUse + 1 PostToolUse hooks
- **Existing rules:** 9 files in `.claude/rules/` covering stop-hooks, testing, error-handling, etc.
- **Key patterns to reference:** `internal/ipfs/cid_validation.go` (ValidateCID), `internal/payments/iota_client.go` (Ed25519 signing), `internal/usecase/atproto_service.go` (ATProto DID/session), `internal/httpapi/shared/response.go` (WriteJSON/WriteError envelope)

## Assumptions

- Rules files are loaded into context automatically by Claude Code — no registration needed beyond placing them in `.claude/rules/`
- Hooks in `settings.json` support only `PreToolUse`, `PostToolUse`, `Notification` event types
- Bash hook commands must complete in <1 second to avoid slowing edits

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Hook false positives slow down development | Medium | Medium | Use narrow file path matchers (only handler dirs for context hook) and test patterns before deploying |
| Too many rules files bloat context | Low | Low | Each file is <100 lines; 5 files add ~2K tokens total |

## Goal Verification

### Truths

1. 5 new rules files exist in `.claude/rules/` and are syntactically valid markdown
2. 2 new hooks in `settings.json` detect their target patterns correctly
3. Settings.json remains valid JSON with no `Invalid key` errors
4. Existing hooks still function (no regressions)
5. Rules cover the specific patterns identified in the codebase exploration

### Artifacts

1. `.claude/rules/ipfs-guardrails.md`
2. `.claude/rules/atproto-guardrails.md`
3. `.claude/rules/iota-guardrails.md`
4. `.claude/rules/peertube-parity.md`
5. `.claude/rules/go-concurrency.md`
6. `.claude/settings.json` (updated with 2 new hooks)

## Progress Tracking

- [x] Task 1: IPFS guardrails rules file
- [x] Task 2: ATProto guardrails rules file
- [x] Task 3: IOTA guardrails rules file
- [x] Task 4: PeerTube parity rules file
- [x] Task 5: Go concurrency rules file
- [x] Task 6: New settings.json hooks + validation

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: IPFS Guardrails Rules File

**Objective:** Create `.claude/rules/ipfs-guardrails.md` covering CID validation, pinning safety, and gateway URL construction.
**Dependencies:** None

**Files:**

- Create: `.claude/rules/ipfs-guardrails.md`

**Key Decisions / Notes:**

- Reference existing `internal/ipfs/cid_validation.go` — `ValidateCID()` must be called at all IPFS boundaries
- CIDv0 (Qm...) is rejected by the codebase — only CIDv1 (bafy...) allowed
- Pinning: never unpin without checking video references (existing hook covers this — reinforce in rules)
- Gateway URLs: always use `cfg.IPFSGatewayURLs` ([]string, rotated via health-check in `ipfs_streaming/`), never hardcode `ipfs.io` or other public gateways
- Path traversal: CIDs must not contain `/`, `..`, or URL-encoded variants

**Definition of Done:**

- [ ] File exists at `.claude/rules/ipfs-guardrails.md`
- [ ] Covers: CID validation, pinning safety, gateway URLs, path traversal
- [ ] References actual code paths in the codebase

**Verify:**

- File exists and is valid markdown

---

### Task 2: ATProto Guardrails Rules File

**Objective:** Create `.claude/rules/atproto-guardrails.md` covering DID validation, lexicon compliance, and session handling.
**Dependencies:** None

**Files:**

- Create: `.claude/rules/atproto-guardrails.md`

**Key Decisions / Notes:**

- DID format: must match `did:plc:*` or `did:web:*` — reject arbitrary strings
- AT URI format: `at://{did}/{collection}/{rkey}` — must validate parts count before `strings.Split` parsing (reference: `internal/usecase/atproto_features.go:163-172`)
- Record keys: alphanumeric + hyphens, max 512 chars (AT Protocol spec)
- Lexicon NSIDs: `app.bsky.*` namespace, dot-separated reverse domain
- Session tokens: never log access/refresh tokens, always encrypt at rest via `AtprotoSessionStore`
- XRPC calls: always use `ctx` from the request, set timeouts
- Best-effort pattern: ATProto failures should never block video upload/processing (existing pattern in `AtprotoPublisher` interface)

**Definition of Done:**

- [ ] File exists at `.claude/rules/atproto-guardrails.md`
- [ ] Covers: DID format, record keys, lexicon NSIDs, session security, best-effort pattern
- [ ] References `internal/usecase/atproto_service.go` and `internal/domain/social.go`

**Verify:**

- File exists and is valid markdown

---

### Task 3: IOTA Guardrails Rules File

**Objective:** Create `.claude/rules/iota-guardrails.md` covering Ed25519 key safety, transaction signing, and amount validation.
**Dependencies:** None

**Files:**

- Create: `.claude/rules/iota-guardrails.md`

**Key Decisions / Notes:**

- Ed25519 private keys: never log, never serialize to JSON, always use `crypto/rand` for generation
- Amount validation: all amounts in nanos (int64); check for overflow before arithmetic; reject negative amounts
- Address format: IOTA Rebased addresses are derived by Blake2b-256 hashing `(0x00 flag || publicKey)`, hex-encoded with `0x` prefix. Total length: 66 chars. Use `IOTAClient.DeriveAddress()` and `ValidateAddress()` — never construct addresses manually
- Transaction signing: use the intent prefix pattern from `iota_client.go` (3-byte `[0x00, 0x00, 0x00]`)
- Gas budget: always use `DefaultGasBudget` constant, don't hardcode magic numbers
- Wallet encryption: keys at rest must use `security/wallet_encryption.go` — never store plaintext keys
- Existing hook covers secret logging — reinforce with specific IOTA patterns

**Definition of Done:**

- [ ] File exists at `.claude/rules/iota-guardrails.md`
- [ ] Covers: key safety, amount overflow, address format, signing pattern, wallet encryption
- [ ] References `internal/payments/iota_client.go` and `internal/security/wallet_encryption.go`

**Verify:**

- File exists and is valid markdown

---

### Task 4: PeerTube Parity Rules File

**Objective:** Create `.claude/rules/peertube-parity.md` covering response shapes, route conventions, and compatibility requirements.
**Dependencies:** None

**Files:**

- Create: `.claude/rules/peertube-parity.md`

**Key Decisions / Notes:**

- Response envelope: always use `shared.WriteJSON()` / `shared.WriteError()` / `shared.WriteJSONWithMeta()` — never raw `json.NewEncoder(w).Encode()`
- List endpoints: must include `Meta` with `Total`, `Limit`, `Offset` via `WriteJSONWithMeta()`
- PeerTube uses `{ total, data }` — Vidra maps this to `{ meta: { total }, data }` with `success` field
- Error envelope: `{ success: false, error: { code, message, details? } }`
- Route aliases: PeerTube-compatible routes must coexist with Vidra routes (e.g., `/api/v1/videos/upload-resumable`)
- Field naming: snake_case JSON fields to match PeerTube conventions
- Status codes: use `shared.MapDomainErrorToHTTP()` for consistent mapping
- Pagination: `start` and `count` query params (PeerTube convention) map to `offset` and `limit`

**Definition of Done:**

- [ ] File exists at `.claude/rules/peertube-parity.md`
- [ ] Covers: response envelope, list pagination, error format, route aliases, field naming
- [ ] References `internal/httpapi/shared/response.go` patterns

**Verify:**

- File exists and is valid markdown

---

### Task 5: Go Concurrency Rules File

**Objective:** Create `.claude/rules/go-concurrency.md` covering goroutine safety, context propagation, and common Go pitfalls.
**Dependencies:** None

**Files:**

- Create: `.claude/rules/go-concurrency.md`

**Key Decisions / Notes:**

- **context.Background() in handlers:** NEVER use `context.Background()` or `context.TODO()` in HTTP handlers — always use `r.Context()` which carries request-scoped deadlines, cancellation, and auth
- **Goroutine recovery:** every `go func()` must have `defer func() { if r := recover(); r != nil { ... } }()` — unrecovered panics in goroutines crash the entire server
- **Goroutine leaks:** goroutines started in request handlers must respect `ctx.Done()` — never fire-and-forget without cancellation
- **Channel safety:** always use buffered channels or select with default to prevent goroutine leaks from blocked sends
- **Mutex vs sync.Map:** use `sync.Mutex` for complex state, `sync.Map` only for append-mostly caches with no read-modify-write
- **Channel-as-mutex pattern:** the codebase uses `chan struct{}` for session serialization (e.g., `atproto_service.go:80`). If using this pattern: always `defer` the release send, prefer `sync.Mutex` for simple cases, document tradeoffs (select-ability vs deadlock on panic)
- **Race conditions:** all shared state must be protected; use `-race` flag in tests
- **Timeouts:** all external calls (HTTP, DB, IPFS, IOTA node) must have timeouts via `context.WithTimeout` or `http.Client.Timeout`
- **defer in loops:** `defer` inside loops doesn't run until function returns — use closure or explicit cleanup

**Definition of Done:**

- [ ] File exists at `.claude/rules/go-concurrency.md`
- [ ] Covers: context propagation, goroutine recovery, leaks, channel safety, timeouts, defer pitfalls
- [ ] Patterns are specific to this codebase (references to actual packages)

**Verify:**

- File exists and is valid markdown

---

### Task 6: New Settings.json Hooks + Validation

**Objective:** Add 2 new hooks to `.claude/settings.json` and validate the complete configuration.
**Dependencies:** Tasks 1-5 (rules files should exist first)

**Files:**

- Modify: `.claude/settings.json`

**Key Decisions / Notes:**

- **Hook 1 (warn, not block):** `context.Background()` or `context.TODO()` in handler files (`internal/httpapi/handlers/**/*.go`, excluding `*_test.go`, `*/federation/*`, `*/auth/oauth*`). Warning because fire-and-forget goroutines legitimately use `context.Background()` (e.g., `server_following.go` emitFollowActivity).
- **Hook 2 (warn):** `json.NewEncoder(w).Encode` in handler files — should use `shared.WriteJSON()` instead. Excludes `*/federation/*` and `*/auth/oauth*` (ActivityPub/WebFinger/NodeInfo and OAuth2 return protocol-specific JSON, not the Vidra envelope). Note: existing violations exist in backup, messaging, import, and category handlers — these will be flagged on future edits.
- Both hooks must: check file path matches handler dirs, skip test files, exit 0 for non-matching files
- Validate final settings.json is valid JSON with no invalid keys
- Test each hook with sample inputs to verify no false positives

**Definition of Done:**

- [ ] 2 new hooks added to `.claude/settings.json`
- [ ] `python3 -c "import json; json.load(open('.claude/settings.json'))"` succeeds
- [ ] Hook 1 warns on `context.Background()` in handler files (excludes federation/oauth)
- [ ] Hook 2 detects `json.NewEncoder` in handler files
- [ ] No false positives on test files or non-handler files
- [ ] All existing hooks still present and functional

**Verify:**

- `python3 -c "import json; json.load(open('.claude/settings.json')); print('Valid JSON')"`

## Open Questions

None.

### Deferred Ideas

- golangci-lint custom rules for protocol compliance
- CI pipeline integration for guardrail validation
- Automated guardrail testing (hook regression tests)
