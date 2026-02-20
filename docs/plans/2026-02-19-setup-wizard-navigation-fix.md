# Setup Wizard Navigation Fix Implementation Plan

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

**Goal:** Fix the setup wizard so each step renders its own page content and the user can navigate through the entire wizard flow (Welcome → Database → Services → Email → Networking → Storage → Security → Review → Complete).

**Architecture:** The root cause is that `template.ParseFS` parses all `*.html` templates together. Since every page template defines `{{define "content"}}`, the last one alphabetically (`welcome.html`) overwrites all others. Every page renders the welcome content, creating an infinite loop. The fix changes the template system to parse each page individually with the layout, and also fixes incorrect template field references and form action bugs found during investigation.

**Tech Stack:** Go html/template, existing Chi router setup (no new dependencies)

## Scope

### In Scope

- Fix template loading to parse each page separately with layout
- Fix template field references in `database.html`, `services.html`, `storage.html`, `security.html`, `complete.html`
- Fix review form action (points to wrong URL)
- Fix welcome page toggle JavaScript (doesn't update hidden field)
- Fix admin password flow (processSecurityForm must save password to config for processReviewForm)
- Add tests that verify each wizard page renders its own content (GET renders correctly)
- Add tests that verify the full navigation flow (POST redirects work end-to-end)

### Out of Scope

- No changes to the wizard flow order (welcome → database → services → email → networking → storage → security → review → complete)
- No changes to form validation logic
- No changes to styling or layout
- No new wizard features

## Prerequisites

- None — all changes are in existing files

## Context for Implementer

- **Patterns to follow:** The `email.html` and `networking.html` templates are already correct — they use `.Config.` prefix for all WizardConfig fields. Use these as reference for fixing the other templates.
- **Conventions:** The `TemplateData` struct (wizard.go:81-98) is the template context. Config fields live on `TemplateData.Config` (a `*WizardConfig`). Direct fields on `TemplateData` are: Title, CurrentStep, ShowBreadcrumb, ShowActions, ShowBack, ShowContinue, DisableContinue, BackURL, CompletedSteps, Config, SystemResources, Recommendation, Error, Success, MigrationCount, Port.
- **Key files:**
  - `internal/setup/wizard.go` — Wizard struct, NewWizard(), handlers, renderTemplate()
  - `internal/setup/wizard_forms.go` — POST form processing, redirects
  - `internal/setup/templates/layout.html` — Shared layout with breadcrumb, actions, JavaScript
  - `internal/setup/templates/*.html` — Individual page templates
  - `internal/setup/wizard_test.go` — Handler GET tests
  - `internal/setup/wizard_flow_test.go` — Flow/POST tests
- **Gotchas:**
  - When using `template.ParseFS` or `template.ParseGlob` with multiple files defining the same named template (`{{define "content"}}`), the last file alphabetically wins. This is Go's documented behavior.
  - The `renderTemplate` function receives a `content` parameter (e.g., `"database.html"`) but never uses it — the layout always calls `{{template "content" .}}` which resolves to the last-defined "content".
  - Existing tests pass despite the bug because the welcome content happens to render successfully (no Config field references), and assertions check for words like "Database" that appear in the layout's breadcrumb.
- **Domain context:** The wizard is a first-run setup flow. Each step collects config values, POSTs them to the same handler which saves state and redirects to the next step.

## Runtime Environment

- **Start command:** `make run` or `go run ./cmd/server/...`
- **Port:** 8080
- **Health check:** `curl http://localhost:8080/health` (returns `{"status":"setup_required"}` in setup mode)

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix template loading to use per-page template sets
- [x] Task 2: Fix template field references in database.html, services.html, storage.html, security.html
- [x] Task 3: Fix review form action, admin password flow, and welcome page toggle
- [x] Task 4: Add tests verifying each page renders correctly and full navigation flow works

**Total Tasks:** 4 | **Completed:** 4 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix template loading to use per-page template sets

**Objective:** Change the Wizard struct to hold a map of templates (one per page) instead of a single combined template. Update renderTemplate to use the correct template set based on the content parameter.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go`

**Key Decisions / Notes:**

- Change `templates` field from `*template.Template` to `map[string]*template.Template`
- In `NewWizard()`, iterate over page names and parse each one individually with the layout:

  ```go
  pages := []string{"welcome", "database", "services", "email", "networking", "storage", "security", "review", "complete"}
  for _, page := range pages {
      t := template.Must(template.ParseFS(templatesFS, "templates/layout.html", "templates/"+page+".html"))
      templates[page] = t
  }
  ```

- In `renderTemplate`, derive the page name from the `content` parameter (strip `.html` suffix) and look up the template in the map
- This ensures each page's `{{define "content"}}` is the only "content" definition in its template set

**Definition of Done:**

- [ ] `Wizard.templates` is `map[string]*template.Template`
- [ ] `NewWizard()` creates per-page template sets
- [ ] `renderTemplate` looks up the correct template by page name
- [ ] Each handler's call to `renderTemplate` works without changes (they already pass the content file name)

**Verify:**

- `go build ./internal/setup/...` — compiles without errors
- `go test ./internal/setup/ -run "TestWizardHandler" -v -count=1` — existing handler tests still pass

### Task 2: Fix template field references in broken templates

**Objective:** Fix all templates that reference WizardConfig fields directly on TemplateData (e.g., `.PostgresMode`) to use the correct path through Config (`.Config.PostgresMode`).

**Dependencies:** Task 1

**Files:**

- Modify: `internal/setup/templates/database.html`
- Modify: `internal/setup/templates/services.html`
- Modify: `internal/setup/templates/storage.html`
- Modify: `internal/setup/templates/security.html`
- Modify: `internal/setup/templates/complete.html`

**Key Decisions / Notes:**

- **database.html** — Replace all occurrences: `.PostgresMode` → `.Config.PostgresMode`, `.DatabaseURL` → `.Config.DatabaseURL`, `.CreateDB` → `.Config.CreateDB`
- **services.html** — Replace: `.RedisMode` → `.Config.RedisMode`, `.RedisURL` → `.Config.RedisURL`, `.EnableIPFS` → `.Config.EnableIPFS`, `.IPFSMode` → `.Config.IPFSMode`, `.IPFSAPIUrl` → `.Config.IPFSAPIUrl`, `.EnableClamAV` → `.Config.EnableClamAV`, `.EnableWhisper` → `.Config.EnableWhisper`. Note: the IOTA section is already correct (uses `.Config.`).
- **storage.html** — Replace all: `.StoragePath` → `.Config.StoragePath`, `.BackupEnabled` → `.Config.BackupEnabled`, `.BackupTarget` → `.Config.BackupTarget`, `.BackupSchedule` → `.Config.BackupSchedule`, `.BackupRetention` → `.Config.BackupRetention`, `.BackupLocalPath` → `.Config.BackupLocalPath`
- **security.html** — Replace all: `.JWTSecret` → `.Config.JWTSecret`, `.AdminUsername` → `.Config.AdminUsername`, `.AdminEmail` → `.Config.AdminEmail`
- **complete.html line 47** — Fix `.Config.Port` → `.Port` (Port is on TemplateData, not WizardConfig)
- Reference `email.html` and `networking.html` as examples of correct usage

**Definition of Done:**

- [ ] No template references Config fields without `.Config.` prefix
- [ ] GET requests to each wizard page return 200 (not 500)
- [ ] Each page renders its own content (database page shows "Database Configuration", etc.)

**Verify:**

- `go test ./internal/setup/ -run "TestWizardHandler" -v -count=1` — all handler GET tests pass
- `go test ./internal/setup/ -run "TestWizardFullFlow" -v -count=1` — full flow tests pass

### Task 3: Fix review form action, admin password flow, and welcome page toggle

**Objective:** Fix three additional bugs: (1) the review page form submits to the wrong URL, (2) the admin password entered on the security page is never saved so processReviewForm always rejects with "Admin password is required", and (3) the welcome page deployment mode toggle doesn't update the hidden form field.

**Dependencies:** Task 2

**Files:**

- Modify: `internal/setup/templates/review.html`
- Modify: `internal/setup/templates/welcome.html`
- Modify: `internal/setup/wizard.go` (add AdminPassword to WizardConfig)
- Modify: `internal/setup/wizard_forms.go` (save password in processSecurityForm, read from config in processReviewForm)

**Key Decisions / Notes:**

- **review.html line 74:** Change `action="/setup/complete"` to `action="/setup/review"`. The `processReviewForm` handler at `/setup/review` (POST) writes the env file, creates the admin user, and redirects to `/setup/complete`. The current form action bypasses this entirely by posting to `/setup/complete` which has no POST route.
- **Admin password flow:** Add `AdminPassword string` to `WizardConfig`. In `processSecurityForm`, save the admin password: `w.config.AdminPassword = r.FormValue("ADMIN_PASSWORD")`. In `processReviewForm`, read from config instead of form: `adminPassword := w.config.AdminPassword`. This is needed because the security page collects the password but `processSecurityForm` doesn't save it, and review.html has no password field — so `processReviewForm` always fails with "Admin password is required".
- **welcome.html:** The `mode` parameter is purely cosmetic (HandleDatabase ignores it). The simplest fix is to rename the hidden input from `name="mode"` to `name="deployment_MODE"` so the existing JS toggle handler in layout.html can find and update it. This makes the URL show `?deployment_MODE=manual` when Manual is selected, which is acceptable for a cosmetic parameter.

**Definition of Done:**

- [ ] Review page form submits to `/setup/review` (POST), triggering env file generation and admin user creation
- [ ] Admin password entered on security page is preserved through to review submission
- [ ] Welcome page toggle updates the hidden field when "Manual" is selected

**Verify:**

- `go test ./internal/setup/ -run "TestWizardHandler" -v -count=1` — all tests pass
- `go test ./internal/setup/ -run "TestWizardFullFlow" -v -count=1` — full flow tests pass

### Task 4: Add tests verifying each page renders correctly and full navigation flow works

**Objective:** Add comprehensive tests that: (1) verify each wizard page renders its own unique content on GET (not the welcome content), and (2) verify the full navigation flow progresses through all pages correctly. These tests would have caught the original bug.

**Dependencies:** Task 3

**Files:**

- Modify: `internal/setup/wizard_test.go`
- Modify: `internal/setup/wizard_flow_test.go`

**Key Decisions / Notes:**

- **Page content tests (wizard_test.go):** Update existing handler tests to assert page-specific content, not just words that appear in the breadcrumb. For example, `TestWizardHandlerDatabase` should check for "Database Configuration" (from database.html h2), not just "Database" (which is in the breadcrumb). Add similar assertions for all pages.
- **Navigation progression test (wizard_flow_test.go):** Add a new test `TestWizardPageRendersOwnContent` that verifies each GET request renders page-specific content. Add `TestWizardFullNavigationFlow` that simulates the complete user journey: welcome GET → database POST (redirects to services) → services POST (redirects to email) → ... → security POST (redirects to review) → review GET. The review POST step involves I/O (WriteEnvFile, CreateAdminUser, GenerateNginxConfig) that would require mocking or tmpdir setup — follow the existing pattern in `TestWizardFullFlowDocker` which tests up to the security step and calls WriteEnvFile directly.
- Follow existing table-driven test patterns in the file

**Definition of Done:**

- [ ] Each wizard page has a test asserting its unique content renders (not just breadcrumb text)
- [ ] A navigation flow test verifies GET+POST progression through all pages
- [ ] All tests pass with `go test ./internal/setup/ -v -count=1`

**Verify:**

- `go test ./internal/setup/ -v -count=1` — all tests pass including new ones
- `go test ./internal/setup/ -count=1` — clean pass (no verbose noise)

## Testing Strategy

- **Unit tests:** Each page handler GET returns 200 with page-specific content
- **Integration tests:** Full wizard flow (POST through all pages) verifies redirects and state accumulation
- **No manual verification needed:** The tests themselves verify the fix

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Template parsing change breaks existing behavior | Low | High | Per-page parsing is the standard Go pattern; existing tests cover all handlers |
| Missed template field reference | Low | Medium | Systematic search for all `.FieldName` patterns in templates where FieldName is a WizardConfig field |

## Open Questions

- None — all bugs have clear fixes with no ambiguity
