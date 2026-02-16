# Setup Script & Wizard Test Suite Implementation Plan

Created: 2026-02-16
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

**Goal:** Create a comprehensive test suite for `scripts/install.sh` and the setup wizard HTTP flow, covering all user personas — from zero-config beginner ("script kiddie") to power user with pre-set credentials and custom `INSTALL_DIR`.

**Architecture:** Two test layers:
1. **Shell script tests** (`scripts/install_test.sh`) — Tests `install.sh` logic by sourcing individual functions in isolated temp directories, verifying .env generation, directory detection, error handling.
2. **Go integration tests** (`internal/setup/integration_test.go`) — Tests the full wizard HTTP flow end-to-end: POST through all wizard steps, verify .env output, check config mode detection with various env var combinations.

**Tech Stack:** POSIX shell (for install script tests), Go standard testing + httptest (for wizard flow tests)

## Scope

### In Scope

- Shell tests for `scripts/install.sh` (all functions, all scenarios)
- Go tests for setup wizard HTTP flow (POST form data through each step)
- Go tests for config mode detection (`config.Load()` with various env var combinations)
- Go tests for `docker-compose.yml` compatibility (empty JWT_SECRET allowed)
- Go tests for the setup server routing (setup mode health endpoint vs normal mode)

### Out of Scope

- Actually starting Docker containers in tests (mock/stub Docker commands)
- Testing the Docker entrypoint script (`scripts/docker-entrypoint.sh`) — separate concern
- UI/browser testing of wizard HTML templates
- Changes to production code (test-only PR)

## Prerequisites

- Go 1.24 installed
- `bash` or `sh` available for shell tests
- No Docker required (shell tests mock Docker commands)

## Context for Implementer

- **Patterns to follow:** Table-driven tests in `internal/setup/validate_test.go:9` and `internal/config/config_test.go:113`
- **Conventions:** Use `testify/assert` and `testify/require`. Guard integration tests with `testing.Short()`.
- **Key files:**
  - `scripts/install.sh` — The install script being tested
  - `internal/setup/server.go` — Setup mode HTTP server and routing
  - `internal/setup/wizard.go` — Wizard handler implementations
  - `internal/setup/wizard_forms.go` — POST form processing
  - `internal/setup/writer.go` — .env file generation
  - `internal/setup/detect.go` — Setup detection logic (`RequiresSetup`, `IsSetupCompleted`)
  - `internal/setup/validate.go` — Input validation (DB URL, Redis URL, JWT secret)
  - `internal/config/config.go:280-295` — SetupMode decision logic
  - `docker-compose.yml:130` — JWT_SECRET now uses `${JWT_SECRET:-}` (optional)
- **Gotchas:**
  - `config.Load()` reads from `os.Getenv` — tests must use `t.Setenv()` to isolate
  - The wizard stores state in-memory (`Wizard.config`) — each test needs a fresh `NewWizard()`
  - `WriteEnvFile` uses atomic write (write to `.tmp`, rename) — test in temp dirs
  - Install script uses `$(dirname "$0")` for repo detection — tests must set up directory structures

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Shell tests for install.sh functions
- [x] Task 2: Go tests for config mode detection matrix
- [x] Task 3: Go tests for wizard HTTP flow end-to-end
- [x] Task 4: Go tests for setup server routing and health endpoint

**Total Tasks:** 4 | **Completed:** 4 | **Remaining:** 0

## Implementation Tasks

### Task 1: Shell tests for install.sh functions

**Objective:** Test every function in `install.sh` in isolation using temp directories. Cover all user scenarios: fresh install, existing clone, non-empty directory, INSTALL_DIR override, pre-existing .env, Docker missing.

**Dependencies:** None

**Files:**

- Create: `scripts/install_test.sh`

**Key Decisions / Notes:**

- Source `install.sh` functions without executing `main` by adding a guard: tests will extract and source individual functions
- Use temp directories (`mktemp -d`) for each test case
- Mock `docker` and `git` commands with shell functions that return predictable results
- Mock `docker compose` to avoid actually starting containers
- Test the `setup_athena` function's 4 branches: `docker-compose.yml` exists, `.git` exists, non-empty dir, empty dir
- Test `INSTALL_DIR` auto-detection when run from `scripts/` subdirectory
- Test `.env` generation: verify `SETUP_COMPLETED=false`, `PORT=8080`, `REQUIRE_IPFS=false` are present
- Test that pre-existing `.env` is preserved (not overwritten)

**Test scenarios:**

| # | Scenario | Expected |
|---|----------|----------|
| 1 | Empty INSTALL_DIR, docker-compose.yml present | Skips clone, creates .env |
| 2 | Empty INSTALL_DIR, .git present | Runs git pull, creates .env |
| 3 | Non-empty dir without .git or docker-compose.yml | Exits with error |
| 4 | Empty directory | Runs git clone |
| 5 | INSTALL_DIR explicitly set by user | Uses user's path |
| 6 | Script run from scripts/ subdirectory | Auto-detects repo root |
| 7 | Pre-existing .env file | Skips .env creation |
| 8 | Docker not installed (macOS) | Prints install Docker Desktop message, exits 1 |
| 9 | Docker not installed (Linux) | Attempts install via get.docker.com |
| 10 | Native mode requested | Exits with "not yet implemented" |
| 11 | Generated .env contents | Contains SETUP_COMPLETED=false, PORT=8080, REQUIRE_IPFS=false |

**Definition of Done:**

- [ ] All 11 test scenarios pass
- [ ] Tests run without Docker (mocked)
- [ ] Tests clean up temp directories
- [ ] `bash scripts/install_test.sh` exits 0

**Verify:**

- `bash scripts/install_test.sh` — all tests pass

### Task 2: Go tests for config mode detection matrix

**Objective:** Exhaustively test the SetupMode decision logic in `config.Load()` with all meaningful combinations of `SETUP_COMPLETED`, `DATABASE_URL`, `REDIS_URL`, and `JWT_SECRET`.

**Dependencies:** None

**Files:**

- Modify: `internal/config/config_test.go`

**Key Decisions / Notes:**

- Extend the existing `TestLoad_SetupModeAllowsPartialConfig` with additional cases
- Use `t.Setenv()` for env var isolation (already the pattern in this file)
- Test the power user scenario: all vars pre-set + `SETUP_COMPLETED=true` → normal mode
- Test the beginner scenario: nothing set → setup mode
- Test edge cases: `SETUP_COMPLETED` with unusual values (whitespace, mixed case)

**Test scenarios:**

| # | SETUP_COMPLETED | DATABASE_URL | REDIS_URL | JWT_SECRET | Expected Mode |
|---|-----------------|--------------|-----------|------------|---------------|
| 1 | (unset) | (unset) | (unset) | (unset) | Setup |
| 2 | (unset) | set | (unset) | (unset) | Setup |
| 3 | (unset) | set | set | (unset) | Setup |
| 4 | (unset) | set | set | set | Normal |
| 5 | "false" | set | set | set | Setup |
| 6 | "0" | set | set | set | Setup |
| 7 | "true" | set | set | set | Normal |
| 8 | "1" | set | set | set | Normal |
| 9 | "true" | (unset) | set | set | Error (DB required) |
| 10 | "true" | set | (unset) | set | Error (Redis required) |
| 11 | "true" | set | set | (unset) | Error (JWT required) |
| 12 | "  false  " | set | set | set | Setup (whitespace trimmed) |
| 13 | "FALSE" | set | set | set | Setup (case insensitive) |
| 14 | "True" | set | set | set | Normal (case insensitive) |

**Definition of Done:**

- [ ] All 14 config detection scenarios pass
- [ ] No regressions in existing config tests
- [ ] `go test ./internal/config/ -run TestLoad` passes

**Verify:**

- `go test ./internal/config/ -run TestLoad -v` — all pass, 0 failures

### Task 3: Go tests for wizard HTTP flow end-to-end

**Objective:** Test the complete wizard flow by POST-ing form data through each step and verifying state changes, redirects, validation errors, and final .env output.

**Dependencies:** None

**Files:**

- Create: `internal/setup/wizard_flow_test.go`

**Key Decisions / Notes:**

- Use `httptest.NewServer` with the setup server handler for realistic routing
- Follow the redirect chain: welcome → database → services → storage → security → review → complete
- Test both "docker mode" (defaults) and "external mode" (user provides URLs)
- Test validation rejection: invalid DB URL, short JWT secret, missing admin password
- Test .env output by writing to a temp dir and reading back
- Follow pattern from `internal/setup/wizard_test.go` but with full-flow integration

**Test scenarios:**

| # | Scenario | Key Checks |
|---|----------|------------|
| 1 | Default wizard flow (all docker mode) | Redirects through all steps, completes |
| 2 | External DB + external Redis | Validates URLs, stores in config |
| 3 | Invalid external DB URL (mysql://) | Returns 400, does not redirect |
| 4 | Invalid JWT secret (too short) | Returns 400 on security step |
| 5 | Missing admin password on review | Returns 400 |
| 6 | Full flow produces valid .env | .env contains SETUP_COMPLETED=true, JWT_SECRET, modes |
| 7 | Custom JWT secret accepted | Overrides auto-generated secret |
| 8 | IPFS enabled with external URL | IPFS_MODE=external and IPFS_API_URL in .env |
| 9 | All optional services disabled | ENABLE_CLAMAV=false, ENABLE_WHISPER=false in .env |
| 10 | Wizard state isolation | Two separate Wizard instances don't share config |

**Definition of Done:**

- [ ] All 10 wizard flow scenarios pass
- [ ] Tests write .env to temp directories (no side effects)
- [ ] `go test ./internal/setup/ -run TestWizardFlow` passes

**Verify:**

- `go test ./internal/setup/ -run TestWizardFlow -v` — all pass, 0 failures

### Task 4: Go tests for setup server routing and health endpoint

**Objective:** Test that the setup server correctly routes requests, returns `setup_required` health status, redirects root to `/setup/welcome`, and returns 503 for API routes.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/server_test.go`

**Key Decisions / Notes:**

- Extend existing tests with more scenarios
- Test root redirect: `GET /` → 303 to `/setup/welcome`
- Test all wizard routes return 200
- Test non-wizard routes return 503 with `setup_required` error
- Test Content-Type headers

**Test scenarios:**

| # | Scenario | Expected |
|---|----------|----------|
| 1 | GET / | 303 redirect to /setup/welcome |
| 2 | GET /health | 200, {"status":"setup_required"} |
| 3 | GET /setup/welcome | 200, HTML with "Welcome to Athena Setup" |
| 4 | GET /setup/database | 200, HTML with "Database" |
| 5 | GET /setup/services | 200, HTML with "Services" |
| 6 | GET /setup/storage | 200, HTML with "Storage" |
| 7 | GET /setup/security | 200, HTML with "Security" |
| 8 | GET /setup/review | 200, HTML with "Review" |
| 9 | GET /setup/complete | 200, HTML with "Setup Complete" |
| 10 | GET /api/v1/videos | 503, {"error":"setup_required"} |
| 11 | GET /api/v1/users/me | 503, {"error":"setup_required"} |
| 12 | Default port when empty | Port defaults to "8080" |
| 13 | Custom port | Port set to provided value |

**Definition of Done:**

- [ ] All 13 routing scenarios pass
- [ ] No regressions in existing server tests
- [ ] `go test ./internal/setup/ -run TestSetupServer` passes

**Verify:**

- `go test ./internal/setup/ -run TestSetupServer -v` — all pass, 0 failures

## Testing Strategy

- **Unit tests:** All Go tests are unit-level using httptest (no real servers, no Docker)
- **Shell tests:** All shell tests use mocked commands and temp directories
- **Integration tests:** None required — all scenarios can be tested without infrastructure
- **Manual verification:** After all tests pass, run `bash scripts/install.sh` and verify wizard loads at `http://localhost:8080/setup/welcome`

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Shell tests not portable across sh implementations | Low | Med | Use POSIX sh only, no bashisms. Test with `sh` not `bash`. |
| config.Load() env var leakage between tests | Med | High | Every test uses `t.Setenv()` which auto-restores after test |
| Wizard state shared between test cases | Low | Med | Create fresh `NewWizard()` per test case |
| .env file written to working directory in tests | Med | High | Always use `t.TempDir()` paths, never write to project root |

## Open Questions

None — all scenarios are well-defined.
