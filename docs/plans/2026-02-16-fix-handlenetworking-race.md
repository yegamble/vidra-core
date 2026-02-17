# Fix HandleNetworking Race / findProjectRoot Fragility / NGINX_ENABLED Hardcoded

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

**Goal:** Fix three bugs/design issues found during the nginx reverse proxy verification: (1) race condition in `HandleNetworking` where defaults are set under lock but config pointer is read without lock during template rendering, (2) `findProjectRoot()` walks directory tree to locate `nginx/templates/` which is fragile in CI, and (3) `NGINX_ENABLED=true` is hardcoded in `writer.go` regardless of actual user configuration.

**Architecture:** Three surgical fixes to existing files. No new files needed. Each fix is independently testable.

**Tech Stack:** Go 1.24, testify, httptest

## Scope

### In Scope

- Fix HandleNetworking race by moving nginx defaults to `NewWizard()` initialization
- Replace `findProjectRoot()` with `go:embed` for nginx templates in `nginx_config.go`
- Add `NginxEnabled bool` field to `WizardConfig` and use it in `writer.go`
- Update all affected tests

### Out of Scope

- Other wizard handlers (they don't have the same race pattern)
- Nginx template content changes
- Docker Compose or CI workflow changes

## Prerequisites

- Existing nginx templates at `nginx/templates/` (already present)
- Go 1.24 with `embed` support (already used in wizard.go for HTML templates)

## Context for Implementer

- **Patterns to follow:** `wizard.go:16-17` already uses `//go:embed templates/*.html` for HTML templates — apply the same pattern for nginx templates in `nginx_config.go`
- **Conventions:** Table-driven tests with testify. Config defaults set in `NewWizard()` constructor (`wizard.go:87-102`).
- **Key files:**
  - `internal/setup/wizard.go` — Wizard struct, config, handlers, `NewWizard()` constructor
  - `internal/setup/nginx_config.go` — `GenerateNginxConfig()`, `findProjectRoot()`
  - `internal/setup/writer.go` — `WriteEnvFile()` with hardcoded `NGINX_ENABLED=true`
  - `internal/setup/wizard_forms.go` — Form processing, `processReviewForm()` calls `GenerateNginxConfig()`
- **Gotchas:**
  - `gofmt` is aliased to `gofmt -w .` on this machine; use `command gofmt` to bypass
  - The file_checker hook runs golangci-lint on single files causing typecheck false positives; full-package lint always passes
  - `nginx/templates/` is at project root, NOT inside `internal/setup/` — the embed directive needs to reference relative path from `nginx_config.go`'s package directory. Since `go:embed` only allows paths relative to the source file's directory, we must use a different approach (see Task 2 notes).

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix HandleNetworking race condition
- [x] Task 2: Replace findProjectRoot with go:embed
- [x] Task 3: Make NGINX_ENABLED configurable

**Total Tasks:** 3 | **Completed:** 3 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix HandleNetworking Race Condition

**Objective:** Move nginx default values (domain, port, protocol) from `HandleNetworking` into `NewWizard()` initialization so they are set once at construction time, eliminating the lock-then-unlock-then-read pattern.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` — Move defaults to `NewWizard()`, remove lock/default logic from `HandleNetworking`
- Modify: `internal/setup/wizard_test.go` — Add test verifying defaults are set after `NewWizard()`
- Test: `internal/setup/wizard_test.go`

**Key Decisions / Notes:**

- Currently `HandleNetworking` (line 183-193) acquires lock, sets defaults if empty, releases lock, then passes `w.config` pointer to `TemplateData` without lock. If a concurrent POST modifies config between unlock and template execution, stale/torn data could be rendered.
- Fix: Set `NginxDomain: "localhost"`, `NginxPort: 80`, `NginxProtocol: "http"` in the `NewWizard()` constructor (line 89-101), alongside existing defaults like `PostgresMode: "docker"`.
- Remove the lock/default block from `HandleNetworking` entirely. The pattern matches other handlers (`HandleDatabase`, `HandleStorage`, etc.) which don't set defaults in the handler.

**Definition of Done:**

- [ ] `NewWizard()` sets NginxDomain="localhost", NginxPort=80, NginxProtocol="http"
- [ ] `HandleNetworking` no longer acquires mutex or sets defaults
- [ ] New test `TestNewWizardNginxDefaults` verifies defaults are set
- [ ] All existing tests pass (`go test ./internal/setup/ -count=1`)

**Verify:**

- `go test ./internal/setup/ -count=1 -run TestNewWizard` — new defaults test passes
- `go test ./internal/setup/ -count=1` — all setup tests pass

### Task 2: Replace findProjectRoot with go:embed

**Objective:** Eliminate the fragile `findProjectRoot()` directory-walking function by embedding nginx templates directly into the binary, making `GenerateNginxConfig` work regardless of working directory.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/nginx_config.go` — Replace `findProjectRoot()` + file reads with embedded templates
- Modify: `internal/setup/nginx_config_test.go` — Tests should still pass (they test generated output, not file paths)
- Test: `internal/setup/nginx_config_test.go`

**Key Decisions / Notes:**

- `go:embed` only allows paths relative to the source file's directory. Since `nginx_config.go` is in `internal/setup/` and templates are in `nginx/templates/` at project root, we CANNOT use `//go:embed ../../nginx/templates/*` (Go forbids `..` in embed paths).
- **Solution:** Add a thin `embed.go` file at project root (or `nginx/` directory) that exports an `embed.FS`, then import it from `nginx_config.go`. Alternatively, copy the approach used for HTML templates — but those are already inside `internal/setup/templates/`.
- **Chosen approach:** Create `nginx/embed.go` in package `nginxtemplates` that exports `var TemplatesFS embed.FS`. Then `nginx_config.go` imports `athena/nginx/nginxtemplates` and reads from the embedded FS. This keeps templates where they are (used by both Go code and Docker builds) while eliminating the directory walk.
- Remove `findProjectRoot()` entirely.
- Change `template.ParseFiles(path)` to `template.ParseFS(fs, name)`.
- Change `os.ReadFile(srcPath)` for includes to `fs.ReadFile(name)`.

**Definition of Done:**

- [ ] `findProjectRoot()` is removed from `nginx_config.go`
- [ ] New `nginx/embed.go` exports embedded `TemplatesFS`
- [ ] `GenerateNginxConfig` reads templates from embedded FS
- [ ] All 6 nginx config tests pass regardless of working directory
- [ ] `go vet ./...` reports no issues

**Verify:**

- `go test ./internal/setup/ -count=1 -run TestGenerateNginx` — all nginx config tests pass
- `go vet ./...` — clean

### Task 3: Make NGINX_ENABLED Configurable

**Objective:** Replace the hardcoded `NGINX_ENABLED=true` in `WriteEnvFile` with a value driven by `WizardConfig.NginxEnabled`, and default it to `true` in `NewWizard()`.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` — Add `NginxEnabled bool` field to `WizardConfig`, set default `true` in `NewWizard()`
- Modify: `internal/setup/writer.go` — Use `config.NginxEnabled` instead of hardcoded `true`
- Modify: `internal/setup/writer_test.go` — Add test for `NginxEnabled=false`, update existing tests to set `NginxEnabled: true`
- Test: `internal/setup/writer_test.go`

**Key Decisions / Notes:**

- Add `NginxEnabled bool` to `WizardConfig` struct (wizard.go line 49 area, alongside other Nginx fields).
- In `NewWizard()`, set `NginxEnabled: true` as default (matches current behavior).
- In `writer.go` line 72, change `"NGINX_ENABLED=true"` to `fmt.Sprintf("NGINX_ENABLED=%t", config.NginxEnabled)`.
- Conditionally skip nginx config lines when `NginxEnabled` is false (wrap lines 71-80 in an `if config.NginxEnabled` block). When disabled, still write `NGINX_ENABLED=false` but skip domain/port/protocol/TLS lines.
- Existing writer tests set nginx config fields but don't set `NginxEnabled` — since Go zero-values bools to `false`, these tests will break. Update them to explicitly set `NginxEnabled: true`.

**Definition of Done:**

- [ ] `WizardConfig` has `NginxEnabled bool` field
- [ ] `NewWizard()` defaults `NginxEnabled: true`
- [ ] `WriteEnvFile` uses `config.NginxEnabled` for `NGINX_ENABLED` value
- [ ] When `NginxEnabled=false`, nginx config section writes only `NGINX_ENABLED=false` (no domain/port/protocol)
- [ ] New test `TestWriteEnvFileNginxDisabled` verifies disabled behavior
- [ ] Existing writer tests updated with `NginxEnabled: true` and still pass

**Verify:**

- `go test ./internal/setup/ -count=1 -run TestWriteEnvFile` — all writer tests pass
- `go test ./internal/setup/ -count=1` — full suite passes

## Testing Strategy

- Unit tests: Each task has focused unit tests (wizard defaults, nginx config generation, env file output)
- Integration tests: Not needed — all changes are pure logic with no external dependencies
- Manual verification: `go test ./internal/setup/ -count=1` after all tasks

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| `go:embed` path issue with nginx templates outside package | Med | High | Create separate `nginx/embed.go` package; test confirms templates are readable |
| Existing writer tests break due to `NginxEnabled` zero-value | High | Low | Explicitly set `NginxEnabled: true` in all existing tests |
| `processReviewForm` calls `GenerateNginxConfig` with wrong signature | Low | Med | `GenerateNginxConfig` signature stays the same (`*WizardConfig, string`) |

## Open Questions

- None — all three fixes are well-defined.
