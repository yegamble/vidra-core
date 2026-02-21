# Audit Findings Fixes Implementation Plan

Created: 2026-02-21
Status: COMPLETE
Approved: Yes
Iterations: 1
Worktree: No

## Summary

**Goal:** Address bugs and gaps identified in the Phase 1 integration audit findings (`docs/audit/2026-02-21-integration-audit-findings.md`) and unfinished items from existing plans.

## Progress Tracking

- [x] Task 1: Fix `TestUnit_Follow_SuccessWithPDS` and all PDS unit tests (Bug 3.1)
- [x] Task 2: Verify COMPOSE_PROFILES bugs already fixed (Bugs 3.3, 3.5, 3.6)
- [x] Task 3: Fix Redis AUTH in HandleTestRedis (Bug 2.3)
- [x] Task 4: Fix AJAX interceptor stopImmediatePropagation (Bug 3.4)
- [x] Task 5: Add GetNodeStatus() test coverage (Bug 3.2)
- [x] Task 6: Replace hardcoded health check queue depth values (Bug 2.4)

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Details

### Task 1: Fix PDS Unit Tests (Bug 3.1 — P1)

**Root cause:** The `newTestSocialHandlerWithPDS()` function in `social_handlers_unit_test.go` created a social service with the default SSRF validator, which blocks connections to private IPs (127.0.0.1). Since httptest servers bind to localhost, all PDS-related tests failed with HTTP 500.

**Fix applied:**
- Added `SetURLValidator()` method to `internal/usecase/social/service.go` to allow test-time injection of a permissive URL validator
- Updated `newTestSocialHandlerWithPDS()` to use `security.NewURLValidatorAllowPrivate()` and set `ATProtoAppPassword`
- Added `createSession` endpoint to `newFakePDS()` for completeness
- Created `newTestSocialHandlerWithPDSStrict()` for the SSRF-verification test (`TestUnit_IngestFeed_SSRFBlocksLocalPDS`)
- All 6 PDS tests now pass (Follow, Unfollow, Like, Unlike, CreateComment, DeleteComment)

**Files modified:**
- `internal/usecase/social/service.go`
- `internal/httpapi/handlers/social/social_handlers_unit_test.go`

### Task 2: Verify COMPOSE_PROFILES (Bugs 3.3, 3.5, 3.6)

**Finding:** All three COMPOSE_PROFILES bugs documented in the audit were already fixed in the current codebase:
- Bug 3.3: `writer.go:187` checks `config.SMTPMode == "docker"` (not `SMTPHost == "mailpit"`)
- Bug 3.5: Lines 201-207 always write `COMPOSE_PROFILES=` even when empty
- Bug 3.6: Line 191 adds `letsencrypt` profile when `NginxTLSMode == "letsencrypt"`

**No code changes required.**

### Task 3: Fix Redis AUTH (Bug 2.3 — P3)

**Root cause:** `HandleTestRedis` parsed the `password` field from the request but never sent an `AUTH` command before `PING`. Password-protected Redis instances would fail silently.

**Fix applied:** Added Redis RESP AUTH command (`*2\r\n$4\r\nAUTH\r\n$N\r\n<password>\r\n`) before PING when a password is provided. Validates the `+OK` response before proceeding.

**File modified:** `internal/setup/wizard_test_connections.go`

### Task 4: Fix AJAX Interceptor (Bug 3.4 — P4)

**Fix applied:** Added `e.stopImmediatePropagation()` after `e.preventDefault()` in the AJAX form submission handler to prevent other listeners from triggering native form submission.

**File modified:** `internal/setup/templates/layout.html`

### Task 5: Add GetNodeStatus() Tests (Bug 3.2 — P8)

**Fix applied:** Added 3 table-driven tests covering:
- Healthy node → no error
- Unhealthy node → error containing "not healthy"
- Connection error → error propagated

**File modified:** `internal/payments/iota_client_test.go`

### Task 6: Fix Health Check Queue Depth (Bug 2.4 — P6)

**Root cause:** `NewHealthHandlers` hardcoded queue depth values (`return 5, nil` and `return 10, nil`), making the `/ready` endpoint always report healthy regardless of actual queue state.

**Fix applied:**
- Updated `NewHealthHandlers` signature to accept `QueueDepthFunc` parameters for encoding and activity queue depth
- Queue health check is only added when both functions are provided (nil = skip)
- Cleaned up standalone `checkQueueDepth()` stub to remove misleading hardcoded values

**File modified:** `internal/httpapi/health.go`

## Remaining Items (Deferred)

The following items from the audit are deferred to future phases:

- **P4 — IOTA Transaction Signing:** `BuildTransaction`, `SignTransaction`, `SubmitTransaction` (Phase 2 feature)
- **P5 — Custom Platform Token:** Move module on IOTA Rebased (Phase 3 feature)
- **P7 — Rate Limiter Redis Backing:** Low priority, wizard is single-use per deployment
