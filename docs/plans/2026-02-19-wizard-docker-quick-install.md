# Wizard Docker Quick Install Implementation Plan

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

**Goal:** Add a "Quick Install (Docker)" button to the setup wizard welcome page. When clicked, it navigates to a streamlined page that collects only admin credentials (username, email, password) and domain name, then invokes the same finalization logic as the full wizard — writing `.env`, generating nginx config, and creating the admin user. All other configuration uses the existing Docker defaults from `NewWizard()`.

**Architecture:** New template (`quickinstall.html`) + new handler (`HandleQuickInstall`) + new form processor (`processQuickInstallForm`). The form processor reuses existing validation functions and calls the same `WriteEnvFile`/`GenerateNginxConfig`/`CreateAdminUser` chain as `processReviewForm`. A button on the welcome page links to the new route.

**Tech Stack:** Go (html/template, Chi router), existing validation and writer infrastructure.

## Scope

### In Scope

- Add "Quick Install (Docker)" button on welcome page
- Create `/setup/quickinstall` GET route (renders form with admin creds + domain)
- Create `/setup/quickinstall` POST route (validates, finalizes, redirects to complete)
- Reuse existing `WriteEnvFile`, `GenerateNginxConfig`, `CreateAdminUser`
- Password confirmation and client-side validation
- Tests for the new handler and E2E route

### Out of Scope

- Changing the full wizard flow (all 8 steps remain intact)
- Modifying `NewWizard()` defaults
- Adding new validation logic beyond what exists
- Docker Compose orchestration (that's a separate concern)

## Prerequisites

- None — all required infrastructure exists (`WriteEnvFile`, `GenerateNginxConfig`, `CreateAdminUser`, `ValidateDomain`)

## Context for Implementer

> This section is critical for cross-session continuity. Write it for an implementer who has never seen the codebase.

- **Patterns to follow:** Each wizard page is an HTML template in `internal/setup/templates/` paired with a handler method on `*Wizard` in `wizard.go`. Templates are parsed in `NewWizard()` and rendered via `renderTemplate()`. Form POST handlers are in `wizard_forms.go`.
- **Conventions:** Templates use `{{define "content"}}` blocks rendered inside `layout.html`. Hidden inputs carry form values. The `TemplateData` struct controls breadcrumb, actions, title, etc.
- **Key files:**
  - `internal/setup/wizard.go` — Wizard struct, TemplateData, NewWizard(), all handlers, renderTemplate()
  - `internal/setup/wizard_forms.go` — All form processors (processSecurityForm, processReviewForm, etc.)
  - `internal/setup/writer.go` — WriteEnvFile writes .env from WizardConfig
  - `internal/setup/server.go` — Chi router, all route registrations
  - `internal/setup/validate.go` — ValidateDomain, ValidatePort, containsShellMetachars
  - `internal/setup/templates/layout.html` — Shared layout with breadcrumb, action buttons, toggle JS
  - `internal/setup/templates/welcome.html` — Current welcome page where button is added
  - `internal/setup/templates/security.html` — Admin account form fields (pattern to follow for quick install form)
- **Gotchas:**
  - Templates are parsed per-page in `NewWizard()` — each page template is combined with `layout.html` separately. A new template must be added to the `pages` slice.
  - `processReviewForm` requires `w.config.AdminPassword` to be set (checked at line 313). The quick install form processor must set this on the config before calling finalization.
  - `processReviewForm` also calls `CreateDatabaseIfNotExists` only for external mode with `CreateDB=true`. Quick install is always Docker mode, so this is skipped.
  - The `ShowBreadcrumb: false` option exists and is used by the complete page — quick install should also hide the breadcrumb since it's a shortcut path.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Add quick install template and handler
- [x] Task 2: Add quick install button to welcome page
- [x] Task 3: Add tests for quick install

**Total Tasks:** 3 | **Completed:** 3 | **Remaining:** 0

## Implementation Tasks

### Task 1: Add quick install template, handler, and route

**Objective:** Create the quick install page — a template with admin credentials (username, email, password, confirm password) and domain field, a GET handler to render it, a POST handler to validate and finalize, and register the routes.

**Dependencies:** None

**Files:**

- Create: `internal/setup/templates/quickinstall.html`
- Modify: `internal/setup/wizard.go` (add HandleQuickInstall, add "quickinstall" to pages slice)
- Modify: `internal/setup/wizard_forms.go` (add processQuickInstallForm)
- Modify: `internal/setup/server.go` (register GET and POST routes)

**Key Decisions / Notes:**

- The template follows the same pattern as `security.html` for admin fields, plus a domain input like `networking.html`
- `HandleQuickInstall` GET renders with `ShowBreadcrumb: false`, `ShowActions: true`, `ShowContinue: true`, `ShowBack: true`, `BackURL: "/setup/welcome"`
- `processQuickInstallForm` does:
  1. Parse form
  2. Validate admin fields (username, email, password >= 8 chars, password match)
  3. Validate domain via `ValidateDomain()`
  4. Set `w.config.AdminUsername`, `AdminEmail`, `AdminPassword`, `NginxDomain` on the wizard config
  5. Call `GenerateNginxConfig(w.config, "nginx/conf")`
  6. Call `WriteEnvFile(".env", w.config)`
  7. Call `CreateAdminUser(ctx, w.config.DatabaseURL, ...)` if username and email are provided
  8. Redirect to `/setup/complete`
- The continue button label should say "Install" instead of "Continue →" — this can be done by adding a `ContinueLabel` field to `TemplateData`, or by making the quick install template use its own form submit button instead of relying on the layout button.
- Simpler approach: The quick install template includes its own submit button in the content area and sets `ShowActions: false` (no layout-level action bar). This avoids modifying TemplateData.

**Definition of Done:**

- [ ] GET `/setup/quickinstall` returns 200 with admin username, email, password, confirm password, and domain fields
- [ ] POST `/setup/quickinstall` with valid data writes .env and redirects to `/setup/complete`
- [ ] POST with missing admin password returns 400
- [ ] POST with password < 8 chars returns 400
- [ ] POST with mismatched passwords returns 400
- [ ] POST with invalid domain returns 400
- [ ] All existing tests still pass

**Verify:**

- `go test ./internal/setup/... -run TestQuickInstall -v`
- `go test ./internal/setup/... -count=1`

### Task 2: Add quick install button to welcome page

**Objective:** Add a "Quick Install (Docker)" button/link on the welcome page that takes users directly to `/setup/quickinstall`.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/setup/templates/welcome.html`

**Key Decisions / Notes:**

- Add a second button/link below or alongside the current "Continue →" flow
- The button should be visually distinct — a prominent call-to-action (e.g., a large button styled differently from the existing flow)
- Position: Below the prerequisites check, above or alongside the existing form submit. A clear visual separation between "Quick Install" and "Advanced Setup" paths.
- Implementation: Add an `<a>` tag styled as a button pointing to `/setup/quickinstall`, placed in a new section between the prerequisites check and the existing form
- The existing "Continue →" button remains for the full wizard flow

**Definition of Done:**

- [ ] Welcome page contains a "Quick Install (Docker)" button/link
- [ ] Clicking the button navigates to `/setup/quickinstall`
- [ ] The full wizard "Continue →" button still works as before
- [ ] Button is visually distinguishable from the full wizard flow

**Verify:**

- `go test ./internal/setup/... -run TestWizardHandlerWelcome -v` (check body contains quick install link)
- `go test ./internal/setup/... -run TestProgramExecution -v`

### Task 3: Add tests for quick install

**Objective:** Add comprehensive tests for the quick install handler (GET, POST validation, POST success) and E2E route tests.

**Dependencies:** Task 1, Task 2

**Files:**

- Modify: `internal/setup/wizard_test.go` (add unit tests for HandleQuickInstall)
- Modify: `internal/setup/wizard_flow_test.go` (add form validation tests for processQuickInstallForm)
- Modify: `internal/setup/verify_execution_test.go` (add E2E route tests)

**Key Decisions / Notes:**

- Follow the existing table-driven test pattern used in `wizard_test.go` (see `TestWizardHandlerWelcome`)
- Follow the form validation test pattern in `wizard_flow_test.go` (see `TestProcessDatabaseFormPasswordEncoding`)
- E2E tests in `verify_execution_test.go` should test the route through the chi router (see existing `TestProgramExecution`)
- Test cases:
  - GET returns 200 with expected fields
  - POST with empty admin password → 400
  - POST with short password → 400
  - POST with mismatched passwords → 400
  - POST with invalid domain → 400
  - POST with valid data → redirect to complete (mock WriteEnvFile path using temp dir)
  - Welcome page contains quick install link
  - E2E: GET through chi router returns 200

**Definition of Done:**

- [ ] Unit test for GET handler returns 200 with expected form fields
- [ ] Unit tests for POST validation (empty password, short password, password mismatch, invalid domain)
- [ ] E2E test via chi router for GET /setup/quickinstall
- [ ] Test that welcome page body contains link to /setup/quickinstall
- [ ] All tests pass: `go test ./internal/setup/... -count=1`

**Verify:**

- `go test ./internal/setup/... -count=1 -v`
- `go test ./internal/setup/... -run TestQuickInstall -v`

## Testing Strategy

- Unit tests: Handler GET renders correct template, POST validates all required fields
- Integration tests: Form processor sets WizardConfig correctly and calls WriteEnvFile
- E2E: Route registered in chi, accessible through server handler, welcome page links to it

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Quick install bypasses validation that full wizard provides | Low | Med | Quick install reuses the same ValidateDomain(), password length check, and WriteEnvFile — no new validation paths needed |
| Template not added to pages slice in NewWizard | Low | High | Task 1 explicitly adds "quickinstall" to the pages slice; tests verify the template renders |
| processQuickInstallForm diverges from processReviewForm over time | Med | Low | processQuickInstallForm calls the same WriteEnvFile/GenerateNginxConfig/CreateAdminUser functions directly — no duplication of finalization logic |

## Open Questions

- None — requirements are clear from user clarification.
