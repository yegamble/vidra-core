# Login with Username or Email Implementation Plan

Created: 2026-03-01
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

> **Status Lifecycle:** PENDING → COMPLETE → VERIFIED
> **Iterations:** Tracks implement→verify cycles (incremented by verify phase)
>
> - PENDING: Initial state, awaiting implementation
> - COMPLETE: All tasks implemented
> - VERIFIED: All checks passed
>
> **Approval Gate:** Implementation CANNOT proceed until `Approved: Yes`
> **Worktree:** Set at plan creation (from dispatcher). `Yes` uses git worktree isolation; `No` works directly on current branch (default)
> **Type:** `Feature` or `Bugfix` — set at planning time, used by dispatcher for routing

## Summary

**Goal:** Allow users to log in using either their username or email address in the same login field.

**Architecture:** The login handler currently only accepts `email`. We'll add a `username` field to the request. Resolution is field-based: if the `email` field is provided, look up by email; if only the `username` field is provided, look up by username; if both are provided, `email` takes priority (backward compat). No content-based `@` detection — the field name determines the lookup type. This maintains backward compatibility (existing clients sending `email` still work).

**Tech Stack:** Go, Chi router, OpenAPI 3.0 spec, oapi-codegen

## Scope

### In Scope

- Modify production `Login` handler to accept username OR email
- Update OpenAPI spec `LoginRequest` schema to add optional `username` field
- Update error messages to reflect "username or email"
- Update existing tests for new behavior
- Add new test cases for login-by-username
- Update test helper `Login` method for consistency

### Out of Scope

- Changing registration flow
- Adding a unified "identifier" field (we keep `email` + `username` as separate fields for backward compatibility)
- Frontend/UI changes
- Rate limiting changes
- OAuth password grant handler (`internal/httpapi/handlers/auth/oauth.go`) — already supports username login via `@`-detection on the form-encoded `username` field. This plan does not change that path. The two paths use different resolution strategies by design: the JSON API uses field presence, the OAuth endpoint uses `@`-content detection on a single field.

## Prerequisites

- None — all required repository methods (`GetByEmail`, `GetByUsername`) already exist

## Context for Implementer

- **Patterns to follow:** The production Login handler is at `internal/httpapi/handlers.go:75`. It uses `map[string]interface{}` for JSON decoding (not the generated type). The same pattern should be used for extracting the new `username` field.
- **Conventions:** Error messages use `domain.NewDomainError()` with codes like `"MISSING_CREDENTIALS"`. Error mapping is in `internal/httpapi/shared/response.go`.
- **Key files:**
  - `internal/httpapi/handlers.go` — Production Login handler (line 75)
  - `internal/httpapi/security_test.go` — Tests for Login on the `Server` struct with mock repo (line 169)
  - `internal/httpapi/handlers/auth/test_helpers_test.go` — Test-only Login on `AuthHandlers` struct (line 89)
  - `internal/httpapi/handlers/auth/auth_handlers_unit_test.go` — Unit tests (line 525)
  - `api/openapi.yaml` — OpenAPI spec, `LoginRequest` at line 4100, `/auth/login` at line 103
  - `internal/generated/types.go` — Generated `LoginRequest` type (line 1000)
  - `internal/port/user.go` — `UserRepository` interface with `GetByEmail` (line 12) and `GetByUsername` (line 13)
- **Gotchas:**
  - The generated `LoginRequest` type uses `openapi_types.Email` for the email field, but the handler parses into `map[string]interface{}` instead of the generated type. The handler must continue using map parsing to support both `email` and `username` fields flexibly.
  - The `email` field must remain the primary/backward-compatible field. If a client sends `{"email": "user@example.com", "password": "..."}` it must still work exactly as before.
  - The test helper Login at `test_helpers_test.go:89` is in the `auth` package (test only) and is separate from the production `Server.Login`.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Update OpenAPI spec and regenerate types
- [x] Task 2: Modify production Login handler
- [x] Task 3: Update tests

**Total Tasks:** 3 | **Completed:** 3 | **Remaining:** 0

## Implementation Tasks

### Task 1: Update OpenAPI spec and regenerate types

**Objective:** Add an optional `username` field to the `LoginRequest` schema and regenerate Go types. Change required fields so that either `email` or `username` must be provided (but not enforced at schema level — handler validates).

**Dependencies:** None

**Files:**

- Modify: `api/openapi.yaml` (LoginRequest schema at line ~4100, /auth/login description at line ~103)
- Regenerate: `internal/generated/types.go` (via `make generate-openapi`)

**Key Decisions / Notes:**

- Add `username` as an optional string field to `LoginRequest`
- Keep `email` as optional too (remove from `required` list) — the handler validates that at least one is provided
- Update the endpoint description from "Login with email and password" to "Login with username or email and password"
- After spec edit, run `make generate-openapi` to regenerate types

**Definition of Done:**

- [ ] `LoginRequest` schema has both `email` (optional) and `username` (optional) fields
- [ ] Endpoint description updated to mention username
- [ ] `make generate-openapi` succeeds without errors
- [ ] `make verify-openapi` passes (no drift)

**Verify:**

- `make generate-openapi && make verify-openapi`

### Task 2: Modify production Login handler

**Objective:** Update `Server.Login` in `internal/httpapi/handlers.go` to accept either `username` or `email` for user lookup. Strategy: extract both fields from JSON, require at least one, then resolve the user using field-based logic — if `email` field is provided, call `GetByEmail`; if only `username` field is provided, call `GetByUsername`; if both are provided, `email` takes priority.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/httpapi/handlers.go` (Login method, lines 75-181)

**Key Decisions / Notes:**

- Extract `username` from `reqData` alongside existing `email` extraction (line 82-83)
- Change validation: require `(email != "" || username != "") && password != ""`
- Update error message from "Email and password are required" to "Email or username, and password are required"
- User resolution logic:
  1. If `email` is provided, try `GetByEmail` first
  2. Else if `username` is provided, try `GetByUsername`
  3. If neither field matched, return `ErrInvalidCredentials` (same as current behavior — no user enumeration)
- If both `email` and `username` are provided, prefer `email` (backward compat)
- Keep the same generic `ErrInvalidCredentials` error for failed lookups (no distinction between "not found by email" vs "not found by username" — prevents user enumeration)

**Definition of Done:**

- [ ] Login accepts `{"username": "...", "password": "..."}` and resolves user by username
- [ ] Login accepts `{"email": "...", "password": "..."}` and resolves user by email (backward compatible)
- [ ] Login rejects requests missing both `email` and `username`
- [ ] Login rejects requests missing `password`
- [ ] No user enumeration — same error for all lookup failures
- [ ] `make validate-all` passes (gofmt, goimports, golangci-lint, tests, build)

**Verify:**

- `make validate-all`

### Task 3: Update tests

**Objective:** Add unit tests for login-by-username and update existing tests. Also update the test helper `Login` in `test_helpers_test.go` for consistency.

**Dependencies:** Task 2

**Files:**

- Modify: `internal/httpapi/security_test.go` (add username-based login tests)
- Modify: `internal/httpapi/handlers/auth/test_helpers_test.go` (update test helper Login to support username)
- Modify: `internal/httpapi/handlers/auth/auth_handlers_unit_test.go` (add username login tests)

**Key Decisions / Notes:**

- Add test cases in `security_test.go`:
  - `TestLogin_WithUsername_Success` — login with username instead of email
  - `TestLogin_MissingBothEmailAndUsername` — returns 400
  - `TestLogin_BothEmailAndUsername_UsesEmail` — sends both fields, verifies email takes priority
- Update test helper `Login` at `test_helpers_test.go:89` to also check for `username` field and call `GetByUsername` when email is empty
- Rename `TestUnit_Login_MissingCredentials` → `TestUnit_Login_MissingPassword_WithEmailOnly` to clarify its specific scenario
- Add test cases in `auth_handlers_unit_test.go`:
  - `TestUnit_Login_WithUsername_Success` — login by username
  - `TestUnit_Login_MissingPassword_WithUsernameOnly` — sends `{"username": "someuser"}` (no password), expects 400
  - `TestUnit_Login_MissingBothEmailAndUsername` — sends only `{"password": "..."}`, expects 400
- Follow existing table-driven test patterns and mock setup from `security_test.go:169`

**Definition of Done:**

- [ ] Test for successful login by username passes
- [ ] Test for missing both email and username returns 400
- [ ] Test for missing password with username-only returns 400
- [ ] Test for simultaneous email+username verifies email takes priority
- [ ] Existing TestUnit_Login_MissingCredentials renamed to TestUnit_Login_MissingPassword_WithEmailOnly
- [ ] Existing email-based login tests still pass
- [ ] Test helper Login supports username field
- [ ] `go test ./internal/httpapi/... -short -count=1` passes all tests

**Verify:**

- `go test ./internal/httpapi/... -short -count=1`

## Testing Strategy

- Unit tests: Mock `UserRepository` with both `GetByEmail` and `GetByUsername` — test each login path
- Integration tests: Existing integration tests in `auth_integration_test.go` use email; they should still pass unmodified
- Manual verification: `make build` to confirm compilation

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Breaking backward compatibility for `email`-only clients | Low | High | `email` field still works exactly as before; existing tests verify this |
| User enumeration via different error messages | Low | Med | Same `ErrInvalidCredentials` returned regardless of whether lookup was by email or username |
| Username vs email ambiguity (email without `@`) | Low | Low | If `email` field is provided, use email lookup; if `username` field is provided, use username lookup — no guessing based on content |

## Goal Verification

### Truths (what must be TRUE for the goal to be achieved)

- Users can log in by sending `{"username": "myuser", "password": "..."}` and receiving tokens
- Users can log in by sending `{"email": "me@example.com", "password": "..."}` and receiving tokens (backward compatible)
- Requests missing both username and email are rejected with 400
- Failed lookups return the same generic error (no user enumeration)

### Artifacts (what must EXIST to support those truths)

- `internal/httpapi/handlers.go` — Updated Login handler with username extraction and dual-lookup logic
- `api/openapi.yaml` — Updated LoginRequest schema with optional `username` field
- `internal/httpapi/security_test.go` — Tests for username-based login on Server
- `internal/httpapi/handlers/auth/auth_handlers_unit_test.go` — Unit tests for username-based login

### Key Links (critical connections that must be WIRED)

- Login handler extracts `username` from JSON → calls `userRepo.GetByUsername()`
- OpenAPI `LoginRequest` schema includes `username` field → generated types reflect this
- Test mock `mockUserRepo.GetByUsername()` → returns user when username matches

## Open Questions

- None
