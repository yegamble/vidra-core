# Fix Stale Redis AUTH Integration Test

Created: 2026-02-27
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Bugfix

## Summary

**Goal:** Update the stale integration test `TestServiceConnections_Redis_PasswordBug` to correctly assert the AUTH behavior that the handler already implements.

**Root Cause:** `tests/integration/service_connections_test.go:140-158` — The test documents a "KNOWN BUG" that `HandleTestRedis` ignores the password field, but the production code at `wizard_test_connections.go:176-194` already sends AUTH when a password is provided. The test asserts `success: true` but the handler now returns `success: false` (AUTH fails against a no-password Redis).

**Bug Condition (C):** Integration test `TestServiceConnections_Redis_PasswordBug` sends `password: "anypassword"` to a Redis instance with no password configured, then asserts `success: true`.

**Postcondition (P):** Test correctly asserts `success: false` when AUTH is sent to a no-password Redis, and the test name/comments accurately reflect the current behavior.

**Symptom:** The integration test either fails (because AUTH is now sent and rejected) or is misleading (documents a bug that no longer exists).

## Behavior Contract

### Must Change (C ⟹ P)

- WHEN password is provided to HandleTestRedis against a no-password Redis THEN the test asserts `success: false` (AUTH correctly rejected)
- **Regression test:** `TestServiceConnections_Redis_AuthRejectsWrongPassword` — verifies AUTH is sent and fails against no-password Redis

### Must NOT Change (¬C ⟹ unchanged)

- Existing test suite covers preservation. The production code (`HandleTestRedis`) is not modified — only the integration test is updated.

## Scope

**Change:** `tests/integration/service_connections_test.go` (test file only)
**Test:** Same file — update existing test
**Out of scope:** Production handler code (already correct)

## Context for Implementer

- **Root cause:** `tests/integration/service_connections_test.go:140-158` — stale test documents a bug that was already fixed in production code
- **Production code:** `internal/setup/wizard_test_connections.go:176-194` — AUTH is already implemented correctly (sends AUTH when password != "", checks for +OK response)
- **Pattern to follow:** See `TestServiceConnections_PostgreSQL` which has sub-tests for success and failure cases
- **Gotchas:** Test Redis on port 16379 has no password. AUTH against it returns an error (not +OK), so the handler correctly returns `success: false`.
- **Test location:** `tests/integration/service_connections_test.go`

## Progress Tracking

- [x] Task 1: Update stale integration test
- [x] Task 2: Verify

**Tasks:** 2 | **Done:** 2

## Implementation Tasks

### Task 1: Update stale integration test

**Objective:** Rename `TestServiceConnections_Redis_PasswordBug` to `TestServiceConnections_Redis_AuthRejectsWrongPassword`, remove stale bug documentation comments, and assert `success: false` with a meaningful error message check.

**Files:**
- `tests/integration/service_connections_test.go`

**TDD Flow:**
1. Rename test function from `TestServiceConnections_Redis_PasswordBug` to `TestServiceConnections_Redis_AuthRejectsWrongPassword`
2. Remove all stale "KNOWN BUG" comments
3. Change assertion from `assert.Equal(t, true, resp["success"], ...)` to `assert.Equal(t, false, resp["success"], ...)`
4. Add assertion that error message mentions authentication failure
5. Remove the `t.Log("KNOWN BUG documented: ...")` line

**Verify:** `go test ./tests/integration/... -run TestServiceConnections_Redis -short -count=1` (will skip due to no TEST_INTEGRATION, but confirms compilation). Also run unit tests: `go test ./internal/setup/... -short -count=1`

---

### Task 2: Verify

**Objective:** Run full validation to ensure no regressions.

**Verify:** `make validate-all`

## Open Questions

- None

### Deferred Ideas

- Add a password-protected Redis to Docker Compose test profile so integration tests can verify successful AUTH (currently only tests AUTH rejection)
