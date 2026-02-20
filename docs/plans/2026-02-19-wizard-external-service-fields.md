# Wizard External Service Fields & Test Connections Implementation Plan

Created: 2026-02-19
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

**Goal:** Fix the setup wizard so that selecting "External Service" reveals appropriate input fields with working test connection features. Fix PostgreSQL-specific bugs (Docker info shown in external mode, broken test connection). Add individual connection fields for external PostgreSQL. Add test connection handlers for all services.

**Architecture:** Client-side JavaScript fixes in layout.html + database.html templates. New backend HTTP handlers for test connections. Individual PostgreSQL connection fields with server-side URL construction.

**Tech Stack:** Go (Chi router), HTML templates, vanilla JavaScript, database/sql + net for connection testing

## Scope

### In Scope

- Fix layout.html toggle JS: hidden input name case mismatch and external-fields sibling lookup
- Fix database.html: Docker info box shown when external selected, testConnection() reads stale mode
- Add individual PostgreSQL external fields (host, port, user, password, database, SSL mode)
- Add WizardConfig fields and form processing for individual PostgreSQL params
- Add test connection handlers: PostgreSQL, Redis, IPFS, IOTA
- Add test connection buttons to services.html for Redis, IPFS, IOTA
- Register new routes in server.go
- Tests for all new handlers and form processing

### Out of Scope

- Individual fields for Redis (URL field is sufficient)
- Individual fields for IPFS/IOTA (URL fields already exist)
- Email test connection changes (already works)
- Any changes to the wizard flow/navigation
- Downstream config readers (`internal/config/`) — `writer.go` changes ARE in scope for outputting individual PG fields

## Prerequisites

- None - all changes are within `internal/setup/`

## Context for Implementer

- **Patterns to follow:** Email page (`templates/email.html:122-146`) has a working toggle JS pattern that correctly handles mode switching. Follow this pattern for database and services pages.
- **Conventions:** Test connection handlers follow the same JSON request/response pattern as `HandleTestEmail` in `wizard.go:331-402`. Response: `{success: bool, message: string}` or `{success: bool, error: string}`.
- **Key files:**
  - `internal/setup/templates/layout.html` — Global toggle JS (broken, lines 294-320)
  - `internal/setup/templates/database.html` — PostgreSQL config page
  - `internal/setup/templates/services.html` — Redis/IPFS/IOTA config page
  - `internal/setup/wizard.go` — Handler functions, WizardConfig struct
  - `internal/setup/wizard_forms.go` — Form processing (processDatabaseForm, processServicesForm)
  - `internal/setup/server.go` — Route registration
  - `internal/setup/validate.go` — URL validation functions (ValidateDatabaseURL, ValidateRedisURL)
- **Gotchas:**
  - The email page has its own custom toggle JS (`email.html:122-146`) that works independently of the layout JS. Both fire on email toggle clicks but don't conflict because they target different elements.
  - The layout JS hidden input selector uses `${serviceName}_MODE` but actual input names are `POSTGRES_MODE`, `REDIS_MODE`, etc. (uppercase prefix).
  - `group.nextElementSibling` in layout JS finds the `<input type="hidden">` not the `.external-fields` div — the `.external-fields` div is a sibling of the parent `.form-group`.
  - Database URL construction must URL-encode the password to handle special characters.

## Runtime Environment

- **Start command:** `go run cmd/server/main.go` (setup mode auto-detected)
- **Port:** 8080 (default)
- **Health check:** `curl http://localhost:8080/health` returns `{"status":"setup_required"}`

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix layout.html toggle JavaScript
- [x] Task 2: Fix database.html PostgreSQL external mode UI
- [x] Task 3: Update WizardConfig and form processing for PostgreSQL fields
- [x] Task 4: Add test connection backend handlers and routes
- [x] Task 5: Add test connection buttons to services.html
- [x] Task 6: Add tests

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix layout.html Toggle JavaScript

**Objective:** Fix the global toggle handler in layout.html so that clicking "External Service" correctly updates the hidden input and shows/hides the external-fields container for all services.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/templates/layout.html`

**Key Decisions / Notes:**

- Fix 1 (line 315): Change `${serviceName}_MODE` to `${serviceName.toUpperCase()}_MODE` so the selector matches `POSTGRES_MODE`, `REDIS_MODE`, `IPFS_MODE`, `IOTA_MODE`
- Fix 2 (lines 307-311): Replace `group.nextElementSibling` with a robust traversal: walk siblings from `group.closest('.form-group')` and check each for `classList.contains('external-fields')` before acting. This is resilient to future template changes (e.g., elements inserted between form-group and external-fields).
- The email page has its own toggle JS that handles `SMTP_MODE` separately — the layout JS won't find an `EMAIL_MODE` input (doesn't exist) so the `if (modeInput)` check prevents issues
- Guard the entire toggle body (including external-fields manipulation) behind the `if (modeInput)` check, not just the hidden input update. This ensures the layout JS skips the external-fields toggle for email (which has its own handler) and prevents double-toggle issues

**Definition of Done:**

- [ ] Clicking "External Service" on database page updates `POSTGRES_MODE` hidden input to "external"
- [ ] Clicking "External Service" reveals the `.external-fields` div on database, services pages
- [ ] Clicking "Local Docker" hides the `.external-fields` div
- [ ] Email page toggle still works correctly (no regression)

**Verify:**

- `go test ./internal/setup/ -run TestWizardHandler -v` — existing tests still pass
- Visual: load database page, click External → fields appear; click Docker → fields hide

### Task 2: Fix database.html PostgreSQL External Mode UI

**Objective:** Fix the database page so that: (1) Docker info box hides when external is selected, (2) individual PostgreSQL connection fields appear in external mode, (3) testConnection() works correctly with mode.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/setup/templates/database.html`
- Modify: `internal/setup/templates/review.html` (fix password exposure in external mode)

**Key Decisions / Notes:**

- Convert Docker info box from server-rendered `{{if eq .Config.PostgresMode "docker"}}` to always-present but client-toggled (like `email.html`'s `docker-info` div). Use `id="docker-info"` with inline style toggle.
- Replace the single Database URL field with individual fields: Host, Port, Username, Password, Database Name, SSL Mode (dropdown: disable, require, verify-full, prefer)
- Keep the `DATABASE_URL` hidden field — it will be constructed server-side from individual fields (Task 3)
- Update `testConnection()` JS to read individual field values and POST them to `/setup/test-database`
- Move Test Connection button inside the external-fields div (only visible in external mode). Show Docker info message outside.
- Add default values: Port=5432, SSLMode=disable, Database=athena
- **Fix review.html:** Currently displays raw `DatabaseURL` which contains the password. Update to show individual fields (host, port, user, database, SSL mode) and mask/omit the password.

**Definition of Done:**

- [ ] Clicking "External Service" hides Docker info box and shows individual PostgreSQL fields
- [ ] Clicking "Local Docker" shows Docker info box and hides PostgreSQL fields
- [ ] External fields include: Host, Port, Username, Password, Database Name, SSL Mode
- [ ] Test Connection button sends individual field values to `/setup/test-database`
- [ ] Docker mode Test Connection shows "Docker mode will be validated during startup" (correct for Docker)

**Verify:**

- `go test ./internal/setup/ -run TestWizardHandlerDatabase -v` — page renders
- Visual: toggle between Docker and External, verify correct sections appear/hide

### Task 3: Update WizardConfig and Form Processing for PostgreSQL Fields

**Objective:** Add individual PostgreSQL connection fields to WizardConfig, update form processing to parse them, and construct DatabaseURL server-side.

**Dependencies:** Task 2

**Files:**

- Modify: `internal/setup/wizard.go` (WizardConfig struct)
- Modify: `internal/setup/wizard_forms.go` (processDatabaseForm)
- Modify: `internal/setup/writer.go` (WriteEnvFile — add individual fields to .env output)

**Key Decisions / Notes:**

- Add to WizardConfig: `PostgresHost string`, `PostgresPort int`, `PostgresUser string`, `PostgresPassword string`, `PostgresDB string`, `PostgresSSLMode string`
- Default values in NewWizard(): Port=5432, SSLMode="disable", DB="athena", User="athena"
- In `processDatabaseForm`, when mode is "external": parse individual fields, validate host is non-empty, construct `DatabaseURL` as `postgres://user:password@host:port/database?sslmode=sslmode`
- URL-encode the password using `url.QueryEscape()` to handle special characters
- Parse `POSTGRES_PORT` from string to int with `strconv.Atoi` and validate range (1-65535), following the SMTP_PORT pattern in `wizard_forms.go:98-106`
- Remove the old `ValidateDatabaseURL` call for external mode — replace with individual field validation
- When mode is "docker", clear individual PostgreSQL fields (similar to how `processEmailForm` clears SMTP fields for docker mode at `wizard_forms.go:90-95`)
- **CRITICAL: Use `net/url` package for URL construction**, not string formatting. Construct via `u := &url.URL{Scheme: "postgres", User: url.UserPassword(user, password), Host: net.JoinHostPort(host, portStr), Path: "/" + database}; u.RawQuery = "sslmode=" + sslmode`. This handles password encoding correctly (spaces, @, /, # etc.)
- Validate individual fields for shell metacharacters (host, user, database) using `containsShellMetachars()` from `validate.go`
- **CRITICAL: `WriteEnvFile` must continue writing `DATABASE_URL`** when mode is "external" — `internal/config/config.go` ONLY reads `DATABASE_URL`. Individual fields (POSTGRES_HOST etc.) are written as supplementary info. Without DATABASE_URL, the app fails at startup.
- Test constructed URL works with `extractDatabaseName()` and `replaceDatabaseName()` in `wizard_db.go` (these do naive string splitting on `/` — passwords containing `/` could break them if not properly encoded)

**Definition of Done:**

- [ ] WizardConfig has individual PostgreSQL fields with defaults
- [ ] processDatabaseForm parses individual fields when mode is "external"
- [ ] DatabaseURL is constructed from individual fields with URL-encoded password
- [ ] Validation rejects empty host when mode is "external"
- [ ] WriteEnvFile outputs individual PostgreSQL fields

**Verify:**

- `go test ./internal/setup/ -run TestWizardFullFlowExternal -v` — external flow test passes
- `go test ./internal/setup/ -run TestWizardInvalidDatabase -v` — validation tests pass

### Task 4: Add Test Connection Backend Handlers and Routes

**Objective:** Add HTTP handlers that test connectivity to external PostgreSQL, Redis, IPFS, and IOTA services. Register routes in server.go.

**Dependencies:** Task 3

**Files:**

- Create: `internal/setup/wizard_test_connections.go`
- Modify: `internal/setup/server.go` (register new routes)

**Key Decisions / Notes:**

- All handlers accept JSON POST, return `{success: bool, message: string}` or `{success: bool, error: string}`
- All connections use 5-second timeout via `context.WithTimeout`
- **Rate limiting (SECURITY):** Share the existing `testEmailLimit` rate limiter (or create a shared `testConnectionLimit`) across all test endpoints. Apply the same 3 requests per 5 minutes per IP pattern from `HandleTestEmail`. The setup wizard has no auth — test handlers are SSRF vectors without rate limiting.
- **SSRF protection:** Validate that host/URL does not resolve to private/loopback IP ranges (127.0.0.0/8, 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16, ::1). Reference `internal/security/` for existing SSRF patterns.
- Wrap timeout errors with user-friendly messages: "Connection timed out after 5 seconds. Verify host and port are correct."
- **HandleTestDatabase:** Accept `{host, port, user, password, database, sslmode}`. Construct URL, `sql.Open("postgres", url)`, `db.PingContext(ctx)`. Close after ping.
- **HandleTestRedis:** Accept `{url}`. Parse URL to extract host:port, `net.DialTimeout("tcp", host:port, 5s)`. After TCP connect, send Redis PING command (`*1\r\n$4\r\nPING\r\n`) and verify `+PONG\r\n` response. This confirms Redis is actually running (not just a TCP port open). No Redis client library needed.
- **HandleTestIPFS:** Accept `{url}`. HTTP POST to `url + "/api/v0/version"` with 5s timeout (IPFS Kubo requires POST for all /api/v0/ endpoints). Check for 200 response.
- **HandleTestIOTA:** Accept `{url}`. HTTP GET to `url` with 5s timeout to verify the endpoint is reachable and responds. Don't assume a specific JSON-RPC method name — IOTA Rebased (Move-based) nodes may use different RPC protocols than legacy IOTA. A successful HTTP response confirms the node is running.
- Routes: `POST /setup/test-database`, `POST /setup/test-redis`, `POST /setup/test-ipfs`, `POST /setup/test-iota`

**Definition of Done:**

- [ ] HandleTestDatabase connects to PostgreSQL and returns success/failure
- [ ] HandleTestRedis verifies Redis host:port is reachable
- [ ] HandleTestIPFS verifies IPFS API endpoint responds
- [ ] HandleTestIOTA verifies IOTA node endpoint responds
- [ ] All handlers use 5-second timeouts
- [ ] All four routes registered in server.go
- [ ] Rate limiting applied to all test connection handlers
- [ ] SSRF protection rejects private/loopback IPs

**Verify:**

- `go build ./internal/setup/...` — compiles without errors
- `go test ./internal/setup/ -run TestHandleTest -v` — handler tests pass (Task 6)

### Task 5: Add Test Connection Buttons to services.html

**Objective:** Add "Test Connection" buttons with JavaScript for Redis, IPFS, and IOTA external service fields on the services page.

**Dependencies:** Task 1, Task 4

**Files:**

- Modify: `internal/setup/templates/services.html`

**Key Decisions / Notes:**

- Each external-fields section gets a "Test Connection" button and a result div
- Redis: reads `redis_url` input value, POSTs to `/setup/test-redis`
- IPFS: reads `ipfs_api` input value, POSTs to `/setup/test-ipfs`
- IOTA: reads `iota_node_url` input value, POSTs to `/setup/test-iota`
- Follow the same JS pattern as database.html's testConnection() for consistency
- Use unique function names: `testRedisConnection()`, `testIPFSConnection()`, `testIOTAConnection()`

**Definition of Done:**

- [ ] Redis external fields section has a "Test Connection" button
- [ ] IPFS external fields section has a "Test Connection" button
- [ ] IOTA external fields section has a "Test Connection" button
- [ ] Each button sends the appropriate URL/params to the correct endpoint
- [ ] Results display in success/error alert divs below each button

**Verify:**

- `go test ./internal/setup/ -run TestWizardHandlerServices -v` — page renders with buttons
- Visual: select External for Redis, click Test Connection, verify request sent

### Task 6: Add Tests

**Objective:** Add comprehensive tests for test connection handlers, form processing with individual fields, and template rendering verification.

**Dependencies:** Tasks 3, 4, 5

**Files:**

- Modify: `internal/setup/wizard_test.go` (template rendering tests)
- Modify: `internal/setup/wizard_flow_test.go` (form processing tests)
- Create: `internal/setup/wizard_test_connections_test.go` (connection handler tests)

**Key Decisions / Notes:**

- **CRITICAL: Update existing tests broken by Task 3.** Three existing tests submit `DATABASE_URL` for external mode but Task 3 changes processDatabaseForm to use individual fields:
  - `TestWizardFullFlowExternal` (wizard_flow_test.go:104): Update to submit `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB`, `POSTGRES_SSLMODE` instead of `DATABASE_URL`. Verify DatabaseURL is correctly constructed from individual fields.
  - `TestWizardInvalidDatabaseURL` (wizard_flow_test.go:206): Replace with test submitting external mode with empty `POSTGRES_HOST` — expect 400 response (new validation).
  - `TestWizardStateIsolation` (wizard_flow_test.go:386): Update wizard1's external mode submission to use individual fields instead of `DATABASE_URL`.
- **Test connection handlers:** Test invalid JSON, missing required fields, valid request format (actual connections will fail in test env — verify error handling is clean)
- **New form processing tests:** Test processDatabaseForm with individual PostgreSQL fields → verify DatabaseURL constructed correctly. Test validation (empty host rejected). Test with special characters in password (URL encoding).
- **Template rendering:** Verify database page with external mode renders individual fields. Verify services page renders test connection buttons. Verify database page with docker mode renders Docker info box.
- Follow existing test patterns: `httptest.NewRequest`, `httptest.NewRecorder`, `assert`/`require`

**Definition of Done:**

- [ ] Update TestWizardFullFlowExternal to use individual PG fields
- [ ] Update TestWizardInvalidDatabaseURL to test empty host validation
- [ ] Update TestWizardStateIsolation to use individual PG fields for external mode
- [ ] TestHandleTestDatabase tests: invalid JSON, missing host, valid request
- [ ] TestHandleTestRedis tests: invalid JSON, empty URL, valid request
- [ ] TestHandleTestIPFS tests: invalid JSON, empty URL, valid request
- [ ] TestHandleTestIOTA tests: invalid JSON, empty URL, valid request
- [ ] TestProcessDatabaseFormExternalFields: individual fields parsed, URL constructed
- [ ] TestProcessDatabaseFormPasswordEncoding: special characters in password
- [ ] TestDatabasePageExternalMode: template renders individual PostgreSQL fields
- [ ] TestServicesPageTestConnectionButtons: template renders test buttons
- [ ] All tests pass: `go test ./internal/setup/ -v`

**Verify:**

- `go test ./internal/setup/ -v -count=1` — all tests pass
- `go test ./internal/setup/ -run TestHandleTest -v` — connection handler tests specifically
- `make lint` — no linting errors

## Testing Strategy

- **Unit tests:** Test connection handlers with mock/invalid inputs, form processing with individual fields, URL construction, validation
- **Integration tests:** Full wizard flow with external PostgreSQL individual fields (in wizard_flow_test.go)
- **Manual verification:** Run wizard, toggle between Docker/External, verify fields show/hide, click Test Connection buttons

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Layout JS fix breaks email page toggle | Low | Medium | Email page has its own independent JS; layout JS only affects services where `${SERVICE}_MODE` input exists. Email uses `SMTP_MODE` not `EMAIL_MODE`, so layout JS selector won't match. |
| Test connection handlers import heavy dependencies | Medium | Low | Use `database/sql` (already imported via wizard_db.go) for PostgreSQL, `net.DialTimeout` for Redis, `net/http` for IPFS/IOTA. No new dependencies. |
| Special characters in PostgreSQL password break URL | Medium | High | URL-encode password with `url.QueryEscape()` when constructing DatabaseURL. Test with special chars in test suite. |
| IOTA Rebased node uses different RPC than legacy IOTA | Medium | Low | Use generic HTTP GET health check instead of specific JSON-RPC method. Just verify the endpoint responds. |
| IPFS Kubo rejects GET on /api/v0/ endpoints | Medium | Low | Use HTTP POST for IPFS /api/v0/version endpoint (Kubo requires POST for all API v0 calls). |
| Test connection handlers used as SSRF proxy | High | High | Rate limit all handlers (3/5min per IP). Reject private/loopback IPs. Setup wizard has no auth. |
| Password with special chars breaks wizard_db.go URL parsing | Medium | High | Use `net/url.UserPassword()` for proper encoding. Test with passwords containing `/`, `@`, `#`. Verify compatibility with `extractDatabaseName()`. |
| WriteEnvFile drops DATABASE_URL breaking app startup | High | Critical | Always write DATABASE_URL (constructed from individual fields). Config.go only reads DATABASE_URL. Individual fields are supplementary. |
| Review page exposes password in plaintext | Medium | Medium | Update review.html to show individual fields, mask/omit password. |

## Open Questions

- None — all design decisions are clear from the bug analysis and user requirements.
