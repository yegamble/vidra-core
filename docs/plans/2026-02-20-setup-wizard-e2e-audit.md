# Setup Wizard End-to-End Audit Implementation Plan

Created: 2026-02-20
Status: COMPLETE
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

**Goal:** Make the setup wizard a complete environment initializer: generate `docker-compose.override.yml` and `COMPOSE_PROFILES` in `.env` so only user-selected Docker services start, replace all blank-page `http.Error()` responses with inline AJAX error alerts that keep the user on the current page, and fix the PostgreSQL test connection for external mode (currently shows incorrect "Docker mode" message).

**Architecture:** New `compose_override.go` generates a `docker-compose.override.yml` that disables containers for external services (via `profiles: ["disabled"]`) and adjusts `app` service `depends_on`. `COMPOSE_PROFILES` is added to `.env` by the existing `WriteEnvFile`. For UX, a single JavaScript interceptor in `layout.html` catches all `#wizardForm` submissions via `fetch()`, detects redirects (success) vs error status codes, and shows inline red alert banners — zero server-side changes needed for error handling.

**Tech Stack:** Go (html/template, YAML generation), JavaScript (fetch API), Docker Compose override files.

## Scope

### In Scope

- Generate `docker-compose.override.yml` based on wizard config (disable external services, fix depends_on)
- Add `COMPOSE_PROFILES` to `.env` output for profiled services (IPFS, IOTA, ClamAV, Whisper, Mailpit)
- AJAX form submission interception with inline error alerts (all wizard forms)
- Call compose override generation from both `processReviewForm` and `processQuickInstallForm`
- Fix PostgreSQL test connection: external mode should test the connection, Docker mode should show informational message
- Verify all external service test connections work correctly (PostgreSQL, Redis, IPFS, IOTA)
- Tests for compose override generation, AJAX error behavior, and test connection fixes

### Out of Scope

- Changing the base `docker-compose.yml` structure (profiles stay as-is)
- Modifying docker-compose for test/CI stacks
- Adding new wizard pages or steps
- Server-side JSON error responses (not needed — the JS interceptor handles plain text errors)

## Prerequisites

- None — all required infrastructure exists

## Context for Implementer

> This section is critical for cross-session continuity. Write it for an implementer who has never seen the codebase.

- **Patterns to follow:** `WriteEnvFile` in `internal/setup/writer.go` is the model for `WriteComposeOverride` — same atomic-write pattern (write to `.tmp`, rename). The YAML output is simple enough to generate as string lines (no need for a YAML library).
- **Conventions:** Templates use `{{define "content"}}` blocks in `layout.html`. Form POST handlers are in `wizard_forms.go`. Each handler calls `http.Error()` for validation failures and `http.Redirect()` for success.
- **Key files:**
  - `internal/setup/writer.go` — `WriteEnvFile` writes `.env` from `WizardConfig` (add `COMPOSE_PROFILES` here)
  - `internal/setup/wizard_forms.go` — All form processors. `processReviewForm` and `processQuickInstallForm` are the finalization handlers that call `WriteEnvFile`/`GenerateNginxConfig`.
  - `internal/setup/wizard.go` — `WizardConfig` struct, `NewWizard()` defaults, all handler methods, `TemplateData` struct.
  - `internal/setup/templates/layout.html` — Shared layout with breadcrumb, action buttons, existing toggle JS. The AJAX interceptor goes here.
  - `docker-compose.yml` — Base compose file. `postgres` and `redis` have NO profiles (always start). `ipfs`, `clamav`, `whisper`, `iota-node`, `mailpit` use profiles. `app` depends_on `postgres` and `redis` with health conditions.
- **Gotchas:**
  - Docker Compose merges override files automatically — `depends_on` in the override REPLACES the base (not merges).
  - `COMPOSE_PROFILES` in `.env` is automatically read by `docker compose up`. Multiple profiles are comma-separated.
  - Services WITHOUT profiles always start. Adding `profiles: ["disabled"]` in the override prevents them from starting since `disabled` is never activated.
  - The `fetch()` API follows 303 redirects automatically. `response.redirected === true` detects successful form submissions. Error responses (4xx/5xx) are NOT redirected and return the plain text body.
  - `FormData` constructor reads the form element and sends `multipart/form-data`. The Go handlers use `r.ParseForm()` which handles both `multipart/form-data` and `application/x-www-form-urlencoded`, so this just works.
  - The `app` service must always start. `nginx` must always start. Only infrastructure services (postgres, redis) and optional services (IPFS, IOTA, etc.) are conditional.
  - The `mailpit` service (docker email) uses profile `["full", "mail"]` — it should be included when email mode is "docker".

## Runtime Environment

- **Start command:** `docker compose up -d` (reads `.env` and `docker-compose.override.yml` automatically)
- **Port:** 8080 (app), 80/443 (nginx)
- **Health check:** `curl http://localhost:8080/health`
- **Setup mode:** When `SETUP_COMPLETED=false`, the Go binary runs the setup wizard server instead of the main app.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Generate docker-compose.override.yml
- [x] Task 2: Add COMPOSE_PROFILES to .env output
- [x] Task 3: AJAX form error handling in layout.html
- [x] Task 4: Fix PostgreSQL test connection for external mode
- [x] Task 5: Tests for compose override, AJAX behavior, and test connections

**Total Tasks:** 5 | **Completed:** 5 | **Remaining:** 0

## Implementation Tasks

### Task 1: Generate docker-compose.override.yml

**Objective:** Create a new function `WriteComposeOverride` that generates a `docker-compose.override.yml` file to disable Docker containers for services configured as "external" and fix the `app` service's `depends_on` accordingly.

**Dependencies:** None

**Files:**

- Create: `internal/setup/compose_override.go`
- Modify: `internal/setup/wizard_forms.go` (call `WriteComposeOverride` from `processReviewForm` and `processQuickInstallForm`)

**Key Decisions / Notes:**

- Generate YAML as string lines (same pattern as `WriteEnvFile` in `writer.go:23`). No YAML library needed — the output is simple and predictable.
- For each external service, add `profiles: ["disabled"]` to prevent it from starting. This works because Docker Compose override replaces profiles, and `disabled` is never activated.
- The `app` service's `depends_on` must be overridden to only include docker-mode services. If both postgres and redis are docker, `depends_on` has both. If postgres is external, only redis. If both external, no `depends_on` at all.
- Use the same atomic write pattern as `WriteEnvFile`: write to `.tmp` then rename.
- Call `WriteComposeOverride("docker-compose.override.yml", w.config)` from both `processReviewForm` (line 345-349 area) and `processQuickInstallForm` (line 435-439 area), right before `WriteEnvFile`.

**Logic:**

```
services_to_disable = []
app_depends_on = {}

if config.PostgresMode == "external":
    services_to_disable += ["postgres"]
else:
    app_depends_on["postgres"] = {condition: service_healthy}

if config.RedisMode == "external":
    services_to_disable += ["redis"]
else:
    app_depends_on["redis"] = {condition: service_healthy}

# Only generate override if something changed from defaults
if len(services_to_disable) > 0:
    write docker-compose.override.yml
```

**Definition of Done:**

- [ ] `WriteComposeOverride` generates valid YAML that `docker compose config` accepts
- [ ] When PostgresMode=external, override contains `postgres: profiles: ["disabled"]` and app's depends_on excludes postgres
- [ ] When RedisMode=external, override contains `redis: profiles: ["disabled"]` and app's depends_on excludes redis
- [ ] When both are external, app has no depends_on section
- [ ] When both are docker (default), no override file is generated (or it's minimal/empty)
- [ ] processReviewForm calls WriteComposeOverride before WriteEnvFile
- [ ] processQuickInstallForm calls WriteComposeOverride before WriteEnvFile
- [ ] All existing tests still pass

**Verify:**

- `go test ./internal/setup/... -run TestWriteComposeOverride -v`
- `go test ./internal/setup/... -count=1`

### Task 2: Add COMPOSE_PROFILES to .env output

**Objective:** Extend `WriteEnvFile` to output a `COMPOSE_PROFILES=...` line that activates Docker Compose profiles for services the user wants to run in Docker mode.

**Dependencies:** None (can run in parallel with Task 1)

**Files:**

- Modify: `internal/setup/writer.go` (add COMPOSE_PROFILES generation to `WriteEnvFile`)

**Key Decisions / Notes:**

- Profile mapping (from base `docker-compose.yml`):
  - IPFS enabled + docker mode → profile `ipfs`
  - IOTA enabled + docker mode → profile `iota`
  - ClamAV enabled → profile `media` (shared with whisper)
  - Whisper enabled → profile `media` (shared with clamav)
  - Email docker mode → profile `mail`
  - Let's Encrypt TLS → profile `letsencrypt`
- Deduplicate profiles (e.g., if both ClamAV and Whisper are enabled, `media` only appears once)
- Place `COMPOSE_PROFILES=ipfs,mail,iota` in the `.env` file under a `# Docker Compose Profiles` section
- If no profiles are needed, write `COMPOSE_PROFILES=` (empty) to ensure any previous value is cleared
- Quick Install mode: all defaults are docker, so profiles are computed from defaults (postgres=docker, redis=docker, no IPFS/IOTA/ClamAV/Whisper by default, email=docker → `COMPOSE_PROFILES=mail`)

**Definition of Done:**

- [ ] `.env` contains `COMPOSE_PROFILES=...` with correct profiles for the configuration
- [ ] When IPFS is enabled in docker mode, `ipfs` profile is included
- [ ] When IOTA is enabled in docker mode, `iota` profile is included
- [ ] When ClamAV or Whisper is enabled, `media` profile is included (deduplicated)
- [ ] When email mode is docker, `mail` profile is included
- [ ] When TLS mode is letsencrypt, `letsencrypt` profile is included
- [ ] Quick Install writes appropriate profiles
- [ ] Existing writer tests still pass

**Verify:**

- `go test ./internal/setup/... -run TestWriteEnvFile -v`
- `go test ./internal/setup/... -run TestQuickInstallDockerModeWritesEnv -v`
- `go test ./internal/setup/... -count=1`

### Task 3: AJAX form error handling in layout.html

**Objective:** Add a JavaScript form submission interceptor in `layout.html` that catches all wizard form POSTs via `fetch()`, shows inline red alert banners on validation errors, and follows redirects on success — eliminating blank-page error responses.

**Dependencies:** None (independent of Tasks 1-2)

**Files:**

- Modify: `internal/setup/templates/layout.html` (add JS interceptor and error alert container)

**Key Decisions / Notes:**

- Add a hidden `<div id="form-error-alert" class="alert alert-error" style="display:none;"></div>` at the top of the `.content` div, before `{{template "content" .}}`.
- In the existing `<script>` block, replace the empty form validation handler with a `fetch()` interceptor:

  ```javascript
  form.addEventListener('submit', function(e) {
      e.preventDefault();
      const errorAlert = document.getElementById('form-error-alert');
      errorAlert.style.display = 'none';

      fetch(form.action, {
          method: 'POST',
          body: new FormData(form)
      }).then(response => {
          if (response.redirected) {
              window.location.href = response.url;
          } else if (!response.ok) {
              return response.text().then(text => {
                  errorAlert.textContent = text;
                  errorAlert.style.display = 'block';
                  errorAlert.scrollIntoView({ behavior: 'smooth', block: 'start' });
              });
          }
      }).catch(() => {
          errorAlert.textContent = 'An unexpected error occurred. Please try again.';
          errorAlert.style.display = 'block';
      });
  });
  ```

- This handles ALL wizard forms without per-page changes since they all use `id="wizardForm"`.
- The `response.redirected` flag is `true` when `fetch()` follows a 303 redirect (success case). Error responses (400, 500) are NOT redirects.
- The `quickinstall.html` has its own submit button and form — verify it also uses `id="wizardForm"` or add the same intercept for its form ID.
- The `scrollIntoView` ensures the error is visible even on long forms.

**Definition of Done:**

- [ ] Form validation errors on any wizard page show a red alert banner at the top of the content area
- [ ] No page navigation occurs on validation errors
- [ ] Successful form submissions still redirect correctly to the next page
- [ ] Error alert disappears when the user resubmits the form
- [ ] Network errors show a generic "unexpected error" message
- [ ] Quick Install form also uses the AJAX interceptor

**Verify:**

- `go test ./internal/setup/... -count=1` (all existing tests still pass — template changes don't break rendering)
- Manual: POST database form with empty host in external mode → red alert appears inline

### Task 4: Fix PostgreSQL test connection for external mode

**Objective:** Fix the database page so that: (1) When PostgreSQL is set to "external", the test connection button actually tests the external connection and returns success/failure. (2) When PostgreSQL is set to "docker", the Docker info area clearly states that Docker services will be set up automatically (not a misleading "Docker mode will be validated during startup" message). (3) Verify all external service test connections (Redis, IPFS, IOTA) work correctly.

**Dependencies:** None (independent of Tasks 1-3)

**Files:**

- Modify: `internal/setup/templates/database.html` (fix test connection UX for Docker vs external mode)

**Key Decisions / Notes:**

- The previous plan (`wizard-external-service-fields.md`) planned a "Docker mode will be validated during startup" message for Docker mode's test connection area, but this message is misleading — Docker mode doesn't need a test connection since containers are managed internally.
- For Docker mode: The Docker info box already shows default configuration details (User, Password, Database, Port). No test connection button is needed — Docker services are auto-configured.
- For external mode: The test connection button and handler (`HandleTestDatabase` in `wizard_test_connections.go`) already exist and work correctly. Verify the JS `testConnection()` function correctly reads individual field values and sends them to `/setup/test-database`.
- Investigate whether the layout.html toggle JS or database.html page-specific JS has any race condition or bug that could cause the wrong UI state (Docker info visible when external is selected, or vice versa).
- The Docker info box uses `id="docker-info"` and has a page-specific JS handler in `database.html:108-116` that toggles visibility on mode change. The layout.html global toggle JS handles showing/hiding `.external-fields`. Both should fire without conflict.
- Verify Redis test connection on services.html: the `testRedis()` JS function parses the URL to extract host/port, sends to `/setup/test-redis`. Check that the Redis URL field is properly validated before sending.

**Definition of Done:**

- [ ] External mode: clicking "Test Connection" sends POST to `/setup/test-database` with correct field values and shows success/error inline
- [ ] Docker mode: Docker info box visible with clear "managed automatically" messaging, no misleading test connection message
- [ ] Toggling between Docker and External properly shows/hides the correct UI sections with no stale state
- [ ] Redis, IPFS, IOTA test connections on services.html all work when external mode is selected
- [ ] All existing tests still pass

**Verify:**

- `go test ./internal/setup/... -count=1`
- Manual: Toggle PostgreSQL to External → fill fields → Test Connection → verify JSON response displayed inline
- Manual: Toggle PostgreSQL to Docker → verify Docker info box shown, no test connection button

### Task 5: Tests for compose override, AJAX behavior, and test connections

**Objective:** Add comprehensive tests for the compose override generation, COMPOSE_PROFILES in .env, verify the AJAX interceptor doesn't break existing form behavior, and verify test connection fixes.

**Dependencies:** Task 1, Task 2, Task 3, Task 4

**Files:**

- Create: `internal/setup/compose_override_test.go`
- Modify: `internal/setup/wizard_flow_test.go` (add COMPOSE_PROFILES tests)
- Modify: `internal/setup/verify_execution_test.go` (add E2E tests for compose override in review POST)

**Key Decisions / Notes:**

- Follow existing table-driven test pattern from `wizard_flow_test.go`
- Test cases for `WriteComposeOverride`:
  - All docker (default) → no override needed / minimal file
  - Postgres external only → postgres disabled, app depends on redis only
  - Redis external only → redis disabled, app depends on postgres only
  - Both external → both disabled, app has no depends_on
  - Validate YAML output contains expected strings
- Test cases for COMPOSE_PROFILES:
  - Default config → `COMPOSE_PROFILES=` (empty or just mail if email docker is default)
  - IPFS enabled docker → contains `ipfs`
  - Multiple services → contains comma-separated profiles
  - Deduplication → `media` only once when both ClamAV and Whisper enabled
- Test cases for test connections:
  - Verify database.html template renders test connection button only in external-fields section
  - Verify services.html template renders test connection buttons for Redis, IPFS, IOTA
  - Existing test connection handler tests in `wizard_test_connections_test.go` cover validation — add template rendering assertions
- E2E: POST to /setup/review through chi router → verify override file is generated in temp dir

**Definition of Done:**

- [ ] Unit tests for WriteComposeOverride cover all docker/external combinations
- [ ] Unit tests for COMPOSE_PROFILES in .env cover single, multiple, and deduplicated profiles
- [ ] E2E test verifies compose override generation during review POST flow
- [ ] Template rendering tests verify test connection UI elements
- [ ] All tests pass: `go test ./internal/setup/... -count=1`

**Verify:**

- `go test ./internal/setup/... -run TestWriteComposeOverride -v`
- `go test ./internal/setup/... -run TestComposeProfiles -v`
- `go test ./internal/setup/... -count=1 -v`

## Testing Strategy

- Unit tests: `WriteComposeOverride` output for each service mode combination, `WriteEnvFile` output contains COMPOSE_PROFILES
- Integration tests: Form POSTs through chi router verify override file + .env generation in temp directories
- Manual verification: Run `docker compose config` on generated override to validate YAML, test form error handling in browser

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Docker Compose override YAML syntax error breaks `docker compose up` | Med | High | Unit tests validate output with string matching; manual verification with `docker compose config` |
| `fetch()` interceptor breaks forms that don't have `id="wizardForm"` | Low | Med | Quick Install form checked explicitly; test all form IDs in templates match |
| `response.redirected` not supported in older browsers | Low | Low | Fallback: if no redirect detected and response.ok, redirect to response.url anyway |
| Override file left from previous run with different config | Low | Med | Always regenerate the override file during finalization, even if all services are docker (write minimal/empty override to clear previous state) |
| `COMPOSE_PROFILES` conflicts with user's manual profile settings | Low | Low | Document in .env comment that COMPOSE_PROFILES is wizard-managed |

## Open Questions

- None — requirements are clear from user clarification.

### Deferred Ideas

- Generate a `start.sh` convenience script that wraps `docker compose up` with status messages
- Add a "Download Configuration" button on the complete page (zip of .env + override + nginx conf)
- Validate generated docker-compose.override.yml with `docker compose config` as a post-generation check
