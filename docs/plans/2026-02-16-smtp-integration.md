# SMTP Integration Implementation Plan

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
> **Worktree:** Set at plan creation (from dispatcher). `No` works directly on current branch (default)

## Summary

**Goal:** Fully wire SMTP email support into Vidra Core with PeerTube-compatible config fields, Docker dev setup (Mailpit), external relay support, setup wizard integration, and application-level email service wiring.

**Architecture:** The `internal/email` package already has a working SMTP service with plain/TLS/STARTTLS sending. The config struct and env loading already include all PeerTube-compatible fields. The gaps are: (1) `email.Config` doesn't expose the full set of PeerTube fields (TLS, STARTTLS, CA, transport), (2) the email service isn't wired in `app.go`, (3) `.env.example` lacks SMTP docs, (4) the setup wizard has no email step, (5) email verification routes are registered with `nil` service.

**Tech Stack:** Go (existing email package), Mailpit (Docker dev SMTP), HTML templates (setup wizard)

## Scope

### In Scope

- Extend `email.Config` to include TLS, DisableSTARTTLS, CAFile, Transport, SendmailPath fields
- Update `email.Service.sendEmail()` to use config-driven TLS logic instead of port-based
- Add `SendTestEmail()` method for the setup wizard
- Wire `email.Service` and `EmailVerificationService` in `app.go`
- Pass email verification service through to auth route handlers
- Add SMTP section to `.env.example`
- Add missing SMTP env vars to `docker-compose.yml` app service
- Add email configuration step to the setup wizard (template, handler, form, breadcrumb)
- Add SMTP test endpoint to setup wizard (AJAX-based "Send Test Email")
- Update wizard `WriteEnvFile()` and review template to include email config

### Out of Scope

- Sendmail transport implementation (config field added, actual sendmail binary execution deferred)
- Custom CA certificate file loading for TLS connections (config field added, implementation deferred)
- Production self-hosted SMTP documentation (docker-mailserver setup guide)
- Email template customization/theming
- New email types beyond what exists (verification, password reset, resend)

## Prerequisites

- Mailpit already in `docker-compose.yml` under `full`/`mail` profiles (confirmed present)
- All SMTP config fields already in `config.Config` struct and loaded from env (confirmed)
- `email.Service`, `email.EmailService` interface, and `EmailVerificationService` already exist

## Context for Implementer

- **Patterns to follow:** The setup wizard step pattern is in `internal/setup/wizard.go` — each step has a `Handle<Step>` method (GET renders template, POST calls `process<Step>Form`), a template in `internal/setup/templates/<step>.html`, and a route pair in `internal/setup/server.go`. Follow the Services step as the closest analog.
- **Conventions:** Config fields use `SMTP_*` env var prefix. Email service uses constructor DI (`NewService(config)`). Handler dependencies flow through `shared.HandlerDependencies` struct in `internal/httpapi/shared/dependencies.go`.
- **Key files:**
  - `internal/email/service.go` — SMTP sending logic, `Config` struct, `EmailSender` interface
  - `internal/email/interface.go` — `EmailService` interface
  - `internal/config/config.go:271-283` — SMTP fields on main Config struct
  - `internal/config/config_load.go:260-271` — SMTP env var loading
  - `internal/app/app.go:237-416` — `initializeDependencies()` where email service should be created
  - `internal/httpapi/routes.go:71-82` — auth handler creation passes `nil` for verificationService
  - `internal/httpapi/shared/dependencies.go:25-80` — `HandlerDependencies` struct (no email fields yet)
  - `internal/setup/wizard.go` — wizard config and handlers
  - `internal/setup/wizard_forms.go` — form processing for each step
  - `internal/setup/writer.go` — `WriteEnvFile()` writes .env from wizard config
  - `internal/setup/server.go` — setup routes
  - `internal/setup/templates/layout.html` — breadcrumb and layout
  - `internal/setup/templates/services.html` — closest pattern for new email step
- **Gotchas:**
  - `authHandlers` at `routes.go:75` passes `nil` for verification service — this is the wiring gap
  - The `email.Config` and `config.Config` are separate structs — need a bridge function
  - The review template form action is `/setup/complete` (not `/setup/review`)
  - Existing sender tests (`service_sender_test.go`) assert port-based TLS selection — these need updating for config-driven behavior
- **Domain context:** PeerTube SMTP config supports `smtp` and `sendmail` transports, explicit TLS toggle (not just port-based), STARTTLS control, and custom CA files. Vidra Core's `config.Config` already has all these fields loaded from env. The email service just doesn't use them yet.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Extend email.Config and update sendEmail() for config-driven TLS
- [x] Task 2: Wire email service in app.go and route registration
- [x] Task 3: Add SMTP section to .env.example and docker-compose.yml
- [x] Task 4: Add email step to setup wizard
- [x] Task 5: Add SMTP test endpoint to setup wizard
- [x] Task 6: Update wizard WriteEnvFile and review template for email config

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: Extend email.Config and update sendEmail() for config-driven TLS

**Objective:** Add PeerTube-compatible config fields to `email.Config` and update `sendEmail()` to use explicit TLS/STARTTLS config instead of port-based heuristics.

**Dependencies:** None

**Files:**

- Modify: `internal/email/service.go`
- Test: `internal/email/service_sender_test.go`
- Test: `internal/email/service_test.go`

**Key Decisions / Notes:**

- Add fields to `email.Config`: `Transport` (string: "smtp"/"sendmail"), `TLS` (bool), `DisableSTARTTLS` (bool), `CAFile` (string), `SendmailPath` (string)
- Create `NewConfigFromAppConfig(cfg *config.Config) *email.Config` bridge function in `internal/email/service.go` — maps all 12 config.Config SMTP fields to email.Config fields. This is the ONLY way to create email.Config from app config (app.go must use this, never construct directly).
- Update `sendEmail()` decision logic:
  - If `Transport == "sendmail"`: return `ErrSendmailNotImplemented` (stub for future)
  - If `TLS == true`: use implicit TLS (SendTLS), regardless of port
  - If `DisableSTARTTLS == false` and port == 587: use STARTTLS
  - If `DisableSTARTTLS == true` or port not 587 and TLS not set: use plain
  - Fallback: for port 465 use TLS, for port 587 use STARTTLS, else plain (backward compatible)
- Add `SendTestEmail(ctx, toAddress)` method that sends a simple test message
- Update existing tests to cover new config-driven paths
- Keep backward compatibility: if TLS/DisableSTARTTLS are zero-values, fall back to port-based logic
- **Known limitation:** Auth uses smtp.PlainAuth (works for Mailgun, Postmark, ImprovMX, most relays). Gmail XOAUTH2 and custom LOGIN mechanisms are deferred.

**Definition of Done:**

- [ ] `email.Config` has Transport, TLS, DisableSTARTTLS, CAFile, SendmailPath fields
- [ ] `NewConfigFromAppConfig()` bridge function maps all 12 SMTP fields with unit test
- [ ] `sendEmail()` respects TLS=true to force implicit TLS on any port
- [ ] `sendEmail()` respects DisableSTARTTLS=true to skip STARTTLS on port 587
- [ ] `SendTestEmail()` method sends a basic test email to a given address
- [ ] All existing sender tests pass (backward compatible port-based behavior preserved)
- [ ] New tests cover: TLS=true on non-465 port, DisableSTARTTLS=true on 587, Transport=sendmail returns error
- [ ] New tests cover: TLS=false+DisableSTARTTLS=false+port=465 falls back to TLS, TLS=false+DisableSTARTTLS=false+port=2525 falls back to plain

**Verify:**

- `go test ./internal/email/ -v -count=1` — all email tests pass
- `go vet ./internal/email/` — no issues

---

### Task 2: Wire email service in app.go and route registration

**Objective:** Create the email service in `app.go` when `EnableEmail` is true, add it to dependencies, and pass it through to auth handlers so email verification routes work.

**Dependencies:** Task 1

**Files:**

- Modify: `internal/app/app.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- In `initializeDependencies()`, when `app.Config.EnableEmail` is true:
  - Create `email.Config` using `email.NewConfigFromAppConfig(app.Config)` (bridge function from Task 1)
  - Create `email.Service` with that config
  - Log warning if SMTP host is empty or looks misconfigured (defensive, don't block startup)
  - Create `EmailVerificationService` with userRepo, verifyRepo, emailService
- Add `EmailService email.EmailService` and `EmailVerificationService *usecase.EmailVerificationService` to both `app.Dependencies` and `shared.HandlerDependencies`
- In `routes.go:75`, replace `nil` with `deps.EmailVerificationService`
- Add email verification routes: `POST /auth/email/verify`, `POST /auth/email/resend`, `GET /auth/email/status` — only register when EmailVerificationService is non-nil
- When `EnableEmail` is false, verification service stays nil (existing behavior, no email routes registered)
- Need `EmailVerificationRepository` — check if it exists

**Definition of Done:**

- [ ] `email.Service` created in `app.go` when `ENABLE_EMAIL=true` using bridge function
- [ ] Warning logged if SMTP config looks invalid (empty host) but app still starts (degraded mode)
- [ ] `EmailVerificationService` created and wired through to auth handlers
- [ ] Email verification routes registered at `/auth/email/verify`, `/auth/email/resend`, `/auth/email/status`
- [ ] Email routes only registered when verification service is non-nil (nil guard in routes.go)
- [ ] When `ENABLE_EMAIL=false`, auth handlers still work (nil verification service, no email routes)
- [ ] `go build ./cmd/server` succeeds

**Verify:**

- `go build ./cmd/server` — builds successfully
- `go vet ./internal/app/ ./internal/httpapi/...` — no issues

---

### Task 3: Add SMTP section to .env.example and docker-compose.yml

**Objective:** Document all SMTP env vars in `.env.example` and add missing SMTP vars to `docker-compose.yml` app service.

**Dependencies:** None

**Files:**

- Modify: `.env.example`
- Modify: `docker-compose.yml`

**Key Decisions / Notes:**

- Add SMTP section to `.env.example` after the ClamAV section, matching PeerTube's config shape:
  - `ENABLE_EMAIL`, `SMTP_TRANSPORT`, `SMTP_SENDMAIL_PATH`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `SMTP_DISABLE_STARTTLS`, `SMTP_CA_FILE`, `SMTP_FROM`, `SMTP_FROM_NAME`
- Include comments explaining each field and the three modes (dev/relay/self-hosted)
- **Current state in docker-compose.yml app service (already present):** `ENABLE_EMAIL`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_FROM`, `SMTP_FROM_NAME` (7 vars)
- **Need to add to docker-compose.yml app service:** `SMTP_TRANSPORT`, `SMTP_SENDMAIL_PATH`, `SMTP_TLS`, `SMTP_DISABLE_STARTTLS`, `SMTP_CA_FILE` (5 vars)
- Default `SMTP_HOST` to `mailpit` in docker-compose (already done)
- Default `SMTP_PORT` to `1025` in docker-compose (already done)
- Full list of 12 env vars for both files: `ENABLE_EMAIL`, `SMTP_TRANSPORT`, `SMTP_SENDMAIL_PATH`, `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `SMTP_TLS`, `SMTP_DISABLE_STARTTLS`, `SMTP_CA_FILE`, `SMTP_FROM`, `SMTP_FROM_NAME`
- In `.env.example`, add comment: `# SMTP_HOST=mailpit (when app runs in Docker Compose) or localhost (when app runs locally)`

**Definition of Done:**

- [ ] `.env.example` has complete SMTP section with all 12 env vars documented
- [ ] `docker-compose.yml` app service has all 12 SMTP env vars passed through (7 existing + 5 new)
- [ ] Comments explain dev (Mailpit), relay (external SMTP), and self-hosted modes

**Verify:**

- `grep -c SMTP .env.example` — returns 12+ lines
- `grep SMTP docker-compose.yml | wc -l` — returns 12+ matches (all vars in app service)

---

### Task 4: Add email step to setup wizard

**Objective:** Add an "Email" configuration step to the setup wizard between "Services" and "Networking", with a form to configure SMTP settings.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` — add email fields to `WizardConfig`, add `HandleEmail` handler
- Modify: `internal/setup/wizard_forms.go` — add `processEmailForm`
- Create: `internal/setup/templates/email.html` — email step template
- Modify: `internal/setup/server.go` — register email routes
- Modify: `internal/setup/templates/layout.html` — add "Email" to breadcrumb
- Modify: `internal/setup/templates/services.html` — update form action to redirect to email (not networking)

**Key Decisions / Notes:**

- Insert email step after Services, before Networking. Flow becomes: Welcome → Database → Services → **Email** → Networking → Storage → Security → Review
- `WizardConfig` gets: `EnableEmail bool`, `SMTPMode string` ("docker"/"external"), `SMTPHost`, `SMTPPort int`, `SMTPUsername`, `SMTPPassword`, `SMTPFromAddress`, `SMTPFromName`, `SMTPTLS bool`, `SMTPDisableSTARTTLS bool`
- Template offers three choices:
  1. "Built-in (Mailpit)" — for dev/testing, sets SMTPMode=docker, no credentials needed
  2. "External SMTP Relay" — for production (SES, Mailgun, etc.), shows host/port/username/password/from fields
  3. "Disabled" — no email (existing behavior)
- Default to "Built-in (Mailpit)" to match docker-compose defaults
- `processEmailForm` validates: if external mode, host and from_address are required
- Services form redirects to `/setup/email` instead of `/setup/networking`
- Email form redirects to `/setup/networking`
- Breadcrumb order: welcome > database > services > **email** > networking > storage > security > review
- All `CompletedSteps` maps in subsequent handlers need "email" added

**Definition of Done:**

- [ ] `WizardConfig` has email fields (EnableEmail, SMTPMode, SMTPHost, etc.)
- [ ] `HandleEmail` handler renders email.html on GET, calls `processEmailForm` on POST
- [ ] `email.html` template shows three mode options with appropriate fields
- [ ] Setup wizard flow: Services → Email → Networking (breadcrumb updated)
- [ ] All subsequent steps include "email" in CompletedSteps (verify: Networking has welcome+database+services+email, Storage adds networking, Security adds storage, Review has all 7)
- [ ] Form validation: external mode requires host and from_address

**Verify:**

- `go build ./cmd/server` — builds successfully
- `go test ./internal/setup/ -v -count=1` — setup tests pass

---

### Task 5: Add SMTP test endpoint to setup wizard

**Objective:** Add an AJAX endpoint `/setup/test-email` that sends a test email using the wizard's current SMTP config, so users can verify their SMTP setup during the wizard.

**Dependencies:** Task 1, Task 4

**Files:**

- Modify: `internal/setup/wizard.go` — add `HandleTestEmail` method
- Modify: `internal/setup/server.go` — register POST `/setup/test-email`
- Modify: `internal/setup/templates/email.html` — add "Send Test Email" button with AJAX
- Test: `internal/setup/wizard_test.go` — test the endpoint

**Key Decisions / Notes:**

- `HandleTestEmail` reads current `WizardConfig` SMTP fields, creates a temporary `email.Service`, calls `SendTestEmail(ctx, toAddress)` where `toAddress` comes from the request body
- Returns JSON `{"success": true/false, "message": "..."}` for AJAX consumption
- **Rate limit: max 3 test emails per 5 minutes per IP** — store counter in wizard struct (in-memory map with TTL). Prevents abuse since setup wizard runs pre-authentication on public port
- If `SMTPMode == "docker"`, use Mailpit defaults (host=localhost, port=1025, no auth)
- The test email button is optional UX — SMTP config saves even without testing
- Include a text field for "recipient email" in the template
- For docker mode, add a note: "Check Mailpit UI at <http://localhost:8025> to see the test email"

**Definition of Done:**

- [ ] `POST /setup/test-email` endpoint accepts `{"email": "user@example.com"}` and attempts to send a test email
- [ ] Rate limited to 3 requests per 5 minutes per IP (returns 429 when exceeded)
- [ ] Returns JSON success/failure response with descriptive message
- [ ] email.html has "Send Test Email" button that calls this endpoint via fetch()
- [ ] Works for both docker (Mailpit) and external SMTP modes
- [ ] Test covers: successful send mock, failed send error response, rate limit enforcement

**Verify:**

- `go build ./cmd/server` — builds successfully
- `go test ./internal/setup/ -v -count=1` — setup tests pass

---

### Task 6: Update wizard WriteEnvFile and review template for email config

**Objective:** Ensure the wizard's `.env` file writer includes all email config, and the review page displays the email configuration summary.

**Dependencies:** Task 4

**Files:**

- Modify: `internal/setup/writer.go` — add email section to `WriteEnvFile`
- Modify: `internal/setup/templates/review.html` — add email section to review page
- Test: `internal/setup/writer_test.go` — test email vars in output

**Key Decisions / Notes:**

- In `WriteEnvFile`, after the "Optional Services" section, add an "Email Configuration" section:
  - Always write `ENABLE_EMAIL`
  - If enabled, write all SMTP vars based on SMTPMode
  - For docker mode: host=mailpit, port=1025, no username/password
  - For external mode: write all user-provided values
- Review template: add an "Email" section between Services and Storage showing:
  - Mode (Built-in Mailpit / External SMTP / Disabled)
  - Host:Port (if enabled)
  - From address (if enabled)
  - Edit link to `/setup/email`

**Definition of Done:**

- [ ] `WriteEnvFile` writes ENABLE_EMAIL and all SMTP vars when email is enabled
- [ ] Docker mode writes correct Mailpit defaults (host=mailpit, port=1025)
- [ ] External mode writes user-provided SMTP host, port, credentials, from address
- [ ] Review template shows email configuration summary with Edit link
- [ ] Writer test verifies email vars are present in output for both modes

**Verify:**

- `go test ./internal/setup/ -v -count=1` — setup tests pass including writer tests

---

## Testing Strategy

- **Unit tests:** Email service config-driven TLS selection (Task 1), wizard form processing (Task 4), writer output (Task 6)
- **Integration tests:** SMTP test endpoint with mock sender (Task 5)
- **Manual verification:** `docker compose --profile mail up` starts Mailpit, wizard email step renders correctly, test email appears in Mailpit UI

## Runtime Environment

**Dev Mode (Mailpit):**

```bash
docker compose --profile mail up -d   # Start Mailpit
go run cmd/server/main.go             # Start Vidra Core (enters setup mode if not configured)
# Access setup wizard at http://localhost:8080/setup/welcome
# Access Mailpit UI at http://localhost:8025
```

**Verification checklist:**

- Setup wizard email step renders with 3 mode options (Built-in Mailpit / External SMTP / Disabled)
- Test email button sends to Mailpit (visible in Mailpit UI at :8025)
- Email verification routes respond when `ENABLE_EMAIL=true`
- App starts cleanly without email service when `ENABLE_EMAIL=false`

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Existing sender tests break when TLS logic changes | Medium | Medium | Preserve port-based fallback when TLS/DisableSTARTTLS are zero-values; update tests for new paths |
| Wizard step insertion breaks navigation flow | Low | Medium | Update all `CompletedSteps` maps and `BackURL` references; verify breadcrumb renders correctly |
| Email service nil when ENABLE_EMAIL=false causes panic in auth handlers | Medium | High | Auth handlers already receive nil verificationService; verify nil-safe behavior in existing handlers; add nil guard in routes.go to skip email route registration |
| Config bridge between config.Config and email.Config drifts | Low | Low | Create explicit `EmailConfigFromAppConfig(cfg *config.Config) *email.Config` bridge function in email package as single source of truth. app.go must use ONLY this function (never construct email.Config directly). Add test verifying all config.Config SMTP fields are mapped. |

## Open Questions

- None — all requirements are clear from the user's PeerTube config reference and the codebase exploration.

### Deferred Ideas

- Sendmail transport binary execution (config field added, implementation deferred to when needed)
- Custom CA certificate loading for TLS connections
- Production self-hosted SMTP documentation (docker-mailserver guide)
- Email template theming/customization system
- SMTP auth mechanism selection (XOAUTH2 for Gmail, CRAMMD5 for SES) — PlainAuth covers most relays
- Wizard state persistence across crashes (Redis-backed wizard sessions)
- Email service health check on /ready endpoint
