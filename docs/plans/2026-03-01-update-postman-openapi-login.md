# Update Postman E2E and OpenAPI for Username Login Implementation Plan

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

**Goal:** Add Postman E2E test requests for username-based login to the `athena-auth` collection, covering happy path and negative cases for the recently implemented login-with-username feature.

**Architecture:** The OpenAPI spec (`api/openapi.yaml`) is already fully updated from the prior implementation spec — it has the `username` field on `LoginRequest`, both email and username examples, and the updated endpoint description. No OpenAPI changes needed. The Postman auth collection currently only has email-based login requests and needs new requests for: (1) login by username, (2) missing both email and username, and (3) both fields simultaneously (email priority).

**Tech Stack:** Postman Collection v2.1.0 JSON format, Newman test runner

## Scope

### In Scope

- Add "Login with Username" request to `athena-auth` Postman collection
- Add "Login - Missing Both Email and Username (400)" negative test
- Add "Login - Both Email and Username Uses Email (200)" edge case test
- Update "Login" request description to mention username option

### Out of Scope

- OpenAPI spec changes (already complete from prior spec)
- `athena-frontend-api-gaps` collection (setup requests use email for backward compat, still work fine)
- Other Postman collections (no login requests)
- Environment variable changes (already have `username` variable)
- Code changes (implementation already complete and verified)

## Prerequisites

- The username login feature is already implemented and verified (see `docs/plans/2026-03-01-login-username-or-email.md`)
- Postman environment files already have a `username` variable

## Context for Implementer

- **Patterns to follow:** Existing Login request at `postman/athena-auth.postman_collection.json:117` — follow the same JSON structure for request objects with `name`, `request`, `event` (prerequest + test scripts), and `description`
- **Conventions:** All requests use `{{baseUrl}}` for the host. Test scripts use `pm.test()` with descriptive messages. Response envelope is `{ data: ..., error: ..., success: ... }` (see Register test at line 96-109 for the unwrapping pattern). Negative tests check status code and verify error structure.
- **Key files:**
  - `postman/athena-auth.postman_collection.json` — The auth Postman collection (only file to modify)
  - `postman/test-env.json` — Environment with `username`, `email`, `password` variables
- **Gotchas:**
  - The collection JSON uses escaped newlines in raw body strings (e.g., `"{\n  \"email\": ...}"`)
  - The "Login" request at line 117 has prerequest scripts that set default email/password from environment — the new "Login with Username" request needs similar prerequest logic for the `username` variable
  - The existing "Login" test script handles both 200 and 401 (lines 166-196) — the username login test should follow the same pattern since the test user may not exist when running individual tests
  - Test assertions on the response must unwrap from `wrap.data` (the envelope) not `wrap` directly
  - New requests should be inserted after "Login - Invalid Password (401)" (line 204) to keep login-related tests grouped together in the Auth folder
- **Domain context:** The server uses field-based resolution: if `email` field is provided, lookup by email (takes priority); if only `username` provided, lookup by username. Same `ErrInvalidCredentials` for all failures (no user enumeration).

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Add username login requests to Postman auth collection

**Total Tasks:** 1 | **Completed:** 1 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add username login requests to Postman auth collection

**Objective:** Add three new requests to the Auth folder in `athena-auth.postman_collection.json`: a username-based login happy path, a missing-both-identifiers negative test, and a both-fields-present edge case test. Also update the existing "Login" request description.

**Dependencies:** None

**Files:**

- Modify: `postman/athena-auth.postman_collection.json`

**Key Decisions / Notes:**

- Insert new requests after "Login - Invalid Password (401)" (line ~241) in the `item[0].item` array (the Auth folder)
- "Login with Username" request:
  - Body: `{"username": "{{username}}", "password": "{{password}}"}`
  - Prerequest: set default username/password if not in environment (same pattern as Login prerequest)
  - Test script: handle 200 (save tokens) and 401 (user may not exist), same pattern as existing Login test at line 164-196
- "Login - Missing Both Email and Username (400)" request:
  - Body: `{"password": "{{password}}"}` — no email or username
  - Test: expect 400 status, assert `pm.expect(j.error.code).to.equal("MISSING_CREDENTIALS")` (not just error presence)
- "Login - Both Email and Username Uses Email (200)" request:
  - Body: `{"email": "{{email}}", "username": "nonexistent_user", "password": "{{password}}"}`
  - Test: handle 200 (login succeeds using email, ignoring wrong username) and 401 (test user may not exist). On 200, verify `user.email` matches `{{email}}`
- Update existing "Login" description from "Authenticates an existing user and returns tokens." to "Authenticates an existing user by email and returns tokens. See also 'Login with Username'."
- TDD note: These are Postman collection JSON files, not Go code. TDD does not apply (JSON config files). Verify by checking JSON is valid and structure matches existing patterns.

**Definition of Done:**

- [ ] `postman/athena-auth.postman_collection.json` has "Login with Username" request that sends `{"username": ..., "password": ...}`
- [ ] Collection has "Login - Missing Both Email and Username (400)" request expecting 400 status
- [ ] Collection has "Login - Both Email and Username Uses Email (200)" request sending both fields
- [ ] Existing "Login" request description updated to mention username option
- [ ] JSON file is valid (parseable with `python3 -m json.tool`)
- [ ] New requests follow existing patterns (prerequest scripts, test assertions, envelope unwrapping)

**Verify:**

- `python3 -m json.tool postman/athena-auth.postman_collection.json > /dev/null` — JSON valid
- `grep -c '"Login with Username"' postman/athena-auth.postman_collection.json` — returns 1
- `grep -c '"Login - Missing Both Email and Username"' postman/athena-auth.postman_collection.json` — returns 1
- `grep -c '"Login - Both Email and Username Uses Email"' postman/athena-auth.postman_collection.json` — returns 1

## Testing Strategy

- Unit tests: N/A (Postman JSON, not Go code)
- Integration tests: JSON validity check via `python3 -m json.tool`
- Manual verification: Grep for new request names, verify structure matches existing patterns

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Invalid JSON after manual edit of large file | Med | Med | Validate with `python3 -m json.tool` after every edit |
| New requests don't match existing patterns | Low | Low | Follow exact structure of existing Login request (line 117) |

## Goal Verification

### Truths (what must be TRUE for the goal to be achieved)

- The Postman auth collection has a request that tests login by username (body contains `username` field, not `email`)
- The Postman auth collection has a negative test for missing both email and username (expects 400)
- The Postman auth collection has an edge case test for both fields present (expects email priority)
- The JSON file is valid and parseable

### Artifacts (what must EXIST to support those truths)

- `postman/athena-auth.postman_collection.json` — Updated with 3 new requests in the Auth folder

### Key Links (critical connections that must be WIRED)

- "Login with Username" request body uses `{{username}}` environment variable → which is already defined in `test-env.json`
- New test scripts follow the same envelope unwrapping pattern (`wrap.data`) as existing Login test

## Open Questions

- None
