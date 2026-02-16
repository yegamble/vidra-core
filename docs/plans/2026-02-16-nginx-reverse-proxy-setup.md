# Nginx Reverse Proxy Setup Implementation Plan

Created: 2026-02-16
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
> **Worktree:** No — direct implementation on current branch

## Summary

**Goal:** Add an Nginx Docker container as a production-grade reverse proxy with setup wizard integration. Users configure domain, port, and HTTP/HTTPS protocol during first-run setup. Follows FAANG best practices for security headers, caching, gzip compression, rate limiting, and WebSocket proxying.

**Architecture:** Nginx sits in front of the Go app container, handling TLS termination, static file serving, security headers, gzip compression, and request proxying. The setup wizard gets a new "Networking" step (after Services, before Storage) that generates nginx config from templates. Self-signed certificates for dev/local, Let's Encrypt via Certbot for production.

**Tech Stack:** Nginx 1.27 (stable-alpine), Certbot for Let's Encrypt, Go `text/template` for nginx config generation, embedded HTML templates for wizard step.

## Scope

### In Scope

- Nginx Docker service in docker-compose.yml (default profile)
- Certbot Docker service for Let's Encrypt (optional profile)
- Nginx config templates (HTTP, HTTPS self-signed, HTTPS Let's Encrypt)
- Setup wizard "Networking" step (domain, port, protocol, TLS mode)
- Static file serving (storage/processed volumes) directly from Nginx
- WebSocket proxy support for live chat
- Security headers (HSTS, X-Frame-Options, X-Content-Type-Options, CSP, etc.)
- Gzip compression for text/JSON/CSS/JS
- Proxy buffering and connection keepalive to upstream
- Response caching for static assets (thumbnails, HLS segments)
- Self-signed certificate generation script
- `PUBLIC_BASE_URL` populated from wizard config
- Env vars for all nginx-related settings (changeable post-install)
- Config validation for domain and port inputs

### Out of Scope

- Load balancing (single upstream for now)
- HTTP/3 / QUIC support
- Nginx Plus features
- Custom Nginx modules
- WAF rules beyond security headers
- CDN integration

## Prerequisites

- Docker and Docker Compose installed
- Existing Athena docker-compose.yml with app service
- Port 80 and/or 443 available on host

## Context for Implementer

> This section is critical for cross-session continuity.

- **Patterns to follow:**
  - Docker service pattern: See `postgres:` service in `docker-compose.yml` (healthcheck, networks, volumes, restart policy)
  - Wizard step pattern: See `HandleServices` + `processServicesForm` in `internal/setup/wizard.go:145` and `wizard_forms.go:30`
  - Template pattern: See `internal/setup/templates/services.html` for toggle buttons and form layout
  - Env writer pattern: See `internal/setup/writer.go:10` `WriteEnvFile` for how to add new config sections
  - Config loading: See `internal/config/config_load.go:170` for `getEnvOrDefault` pattern
  - Validation: See `internal/setup/validate.go` for `ValidateDatabaseURL` pattern

- **Conventions:**
  - Wizard steps use `toggle-btn` with `data-mode` for docker/external toggles
  - Hidden `input` fields track mode state; form POSTs redirect to next step via `http.StatusSeeOther`
  - All config fields use `SCREAMING_SNAKE_CASE` env var names
  - Docker services on `athena-network` bridge

- **Key files the implementer must read first:**
  - `docker-compose.yml` — existing service definitions, volumes, networks
  - `internal/setup/wizard.go` — wizard struct, handlers, step flow, breadcrumb logic
  - `internal/setup/wizard_forms.go` — form processing pattern
  - `internal/setup/writer.go` — .env file generation
  - `internal/setup/server.go` — route registration
  - `internal/setup/templates/layout.html` — breadcrumb step list (needs "Networking" added)
  - `internal/config/config.go` — Config struct, `PublicBaseURL` field
  - `internal/config/config_load.go` — env var loading

- **Gotchas:**
  - The `PublicBaseURL` field already exists in Config but is sparsely used. We populate it from the new wizard step.
  - Breadcrumb items are hardcoded in `layout.html` — must add "Networking" in correct position
  - The wizard `CompletedSteps` map in each handler must be updated to include "networking" for steps after it
  - Docker Compose exposes app:8080 to host — Nginx needs to proxy to `app:8080` internally but we should stop exposing 8080 to host when Nginx is enabled
  - Self-signed certs need to exist before Nginx starts, so the cert generation script runs as a Docker entrypoint or init container

- **Domain context:**
  - Athena is a PeerTube-compatible video platform. Nginx must handle large file uploads (chunked, up to 32MB per chunk), HLS video streaming segments, and WebSocket connections for live chat.
  - The `storage` and `processed` Docker volumes contain static video files, thumbnails, and HLS segments that Nginx can serve directly.

## Runtime Environment

- **Start command:** `docker compose up` (starts all services including nginx)
- **Port:** 80 (HTTP) or 443 (HTTPS) via Nginx; app remains on 8080 internally
- **Health check:** `curl http://localhost/nginx-health` (nginx direct), `curl http://localhost/health` (proxied to app)
- **Restart procedure:** `docker compose restart nginx` after config changes

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete.**

- [x] Task 1: Nginx configuration templates
- [x] Task 2: Self-signed certificate generation script
- [x] Task 3: Docker Compose nginx + certbot services
- [x] Task 4: Wizard networking step (backend)
- [x] Task 5: Wizard networking step (template + breadcrumb)
- [x] Task 6: Env writer + config loader integration
- [x] Task 7: Nginx config generator (Go)
- [x] Task 8: Bash script tests for nginx scripts
- [x] Task 9: GitHub Actions CI workflow update

**Total Tasks:** 9 | **Completed:** 9 | **Remaining:** 0

## Implementation Tasks

### Task 1: Nginx Configuration Templates

**Objective:** Create production-grade nginx.conf templates for HTTP-only, HTTPS self-signed, and HTTPS Let's Encrypt modes.

**Dependencies:** None

**Files:**

- Create: `nginx/templates/nginx-http.conf.tmpl`
- Create: `nginx/templates/nginx-https-selfsigned.conf.tmpl`
- Create: `nginx/templates/nginx-https-letsencrypt.conf.tmpl`
- Create: `nginx/templates/common-security.conf`
- Create: `nginx/templates/common-proxy.conf`
- Test: `internal/setup/nginx_config_test.go` (tested in Task 7, templates validated here structurally)

**Key Decisions / Notes:**

- Use Go `text/template` syntax with placeholders: `{{.Domain}}`, `{{.Port}}`, `{{.UpstreamAddr}}`
- Common includes for security headers and proxy settings to avoid duplication
- Security headers following OWASP/FAANG best practices:
  - `X-Frame-Options: DENY`
  - `X-Content-Type-Options: nosniff`
  - `X-XSS-Protection: 1; mode=block`
  - `Referrer-Policy: strict-origin-when-cross-origin`
  - `Content-Security-Policy` (initial restrictive policy, customizable via `NGINX_CSP_POLICY` env var): `default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; media-src 'self' blob:; connect-src 'self' ws: wss:`
  - `Permissions-Policy: camera=(), microphone=(), geolocation=()`
  - HSTS with 1 year max-age (HTTPS only)
- Gzip: compress `text/plain`, `text/css`, `application/json`, `application/javascript`, `text/xml`, `application/xml`, `video/mp2t` (HLS segments), `application/vnd.apple.mpegurl`
- Proxy: `proxy_http_version 1.1`, keepalive connections, WebSocket upgrade headers for `/ws/` paths
- Caching: Cache-Control for static files (thumbnails 7d, HLS segments 1h, video files 30d)
- Client max body size: 0 (unlimited — app handles chunked upload limits)
- Rate limiting: 10 req/s per IP for API endpoints, burst 20
- Static file serving: `/storage/` and `/processed/` location blocks serving from shared volumes. **CRITICAL:** Nginx volume mount paths MUST match app mount paths exactly (`storage:/app/storage:ro`, `processed:/app/processed:ro`) — nginx config `root` directives must reference these same paths
- Let's Encrypt template includes ACME challenge location block (`/.well-known/acme-challenge/` with root at certbot webroot volume)
- All templates include `/nginx-health` location block returning 200 directly (used for Docker healthcheck, not proxied to app)

**Definition of Done:**

- [ ] Three nginx conf templates created with correct Go template syntax
- [ ] Common security headers in shared include file
- [ ] Common proxy settings in shared include file
- [ ] Templates pass `template.Must(template.ParseGlob(...))` in test
- [ ] Gzip configuration present for text, JSON, HLS content types
- [ ] WebSocket upgrade support configured for `/ws/` paths
- [ ] Static file locations configured with appropriate cache headers

**Verify:**

- `go test ./internal/setup/ -run TestNginx -v -short` — template parsing tests pass
- Nginx syntax validation automated in Task 8 bash tests via `docker run --rm -v ./nginx/conf:/etc/nginx/conf.d:ro nginx:1.27-alpine nginx -t`

---

### Task 2: Self-Signed Certificate Generation Script

**Objective:** Create a script that generates self-signed TLS certificates for local/dev HTTPS mode.

**Dependencies:** None

**Files:**

- Create: `nginx/scripts/generate-self-signed-cert.sh`
- Create: `nginx/scripts/entrypoint.sh`
- Create: `nginx/Dockerfile` — extends `nginx:1.27-alpine`, installs `openssl` and `curl` packages, copies scripts

**Key Decisions / Notes:**

- Use custom `nginx/Dockerfile` (FROM nginx:1.27-alpine) that installs `openssl` package explicitly — Alpine uses LibreSSL by default which has different flags. Installing the `openssl` package ensures full OpenSSL compatibility
- Script generates RSA 2048-bit cert + key using `openssl`
- Outputs to `/etc/nginx/ssl/self-signed.crt` and `/etc/nginx/ssl/self-signed.key`
- Also generates DH params at `/etc/nginx/ssl/dhparam.pem` if not present — use `openssl dhparam -dsaparam 2048` for fast generation (avoids 10+ minute generation time on slow hardware)
- Entrypoint script: if HTTPS enabled and certs don't exist, generate self-signed; then exec nginx
- Script is idempotent — skips generation if certs already exist
- Cert validity: 365 days, CN set to configured domain

**Definition of Done:**

- [ ] `generate-self-signed-cert.sh` generates valid cert/key pair
- [ ] `entrypoint.sh` runs cert generation conditionally then starts nginx
- [ ] Scripts are executable (chmod +x)
- [ ] Scripts work on Alpine (Dockerfile installs `openssl` package explicitly, not LibreSSL)
- [ ] Dockerfile created: `FROM nginx:1.27-alpine`, installs `openssl` and `curl`, copies scripts
- [ ] Self-signed cert includes configured domain as CN and SAN

**Verify:**

- `bash -n nginx/scripts/generate-self-signed-cert.sh` — syntax check passes
- `bash -n nginx/scripts/entrypoint.sh` — syntax check passes

---

### Task 3: Docker Compose Nginx + Certbot Services

**Objective:** Add nginx and certbot services to docker-compose.yml following existing patterns.

**Dependencies:** Task 1, Task 2

**Files:**

- Modify: `docker-compose.yml`

**Key Decisions / Notes:**

- `nginx` service: default profile (always runs), built from `nginx/Dockerfile` (extends `nginx:1.27-alpine` with `openssl` and `curl`)
- Depends on `app` (service_healthy), mounts nginx config volumes
- Ports: `"${NGINX_HTTP_PORT:-80}:80"` and `"${NGINX_HTTPS_PORT:-443}:443"`
- Volumes: `./nginx/conf:/etc/nginx/conf.d:ro`, `./nginx/ssl:/etc/nginx/ssl:ro`, `storage:/app/storage:ro`, `processed:/app/processed:ro`
- Custom entrypoint using `nginx/scripts/entrypoint.sh`
- Environment: `NGINX_DOMAIN`, `NGINX_PROTOCOL`, `NGINX_TLS_MODE`
- Healthcheck: `curl -sf http://localhost/nginx-health || exit 1` (served directly by nginx via `stub_status` or a static `/nginx-health` location returning 200, NOT proxied to app — avoids circular dependency with app healthcheck)
- Add `start_period: 60s` to nginx healthcheck to allow time for cert generation before health checks begin
- `certbot` service: profile `letsencrypt`, `certbot/certbot` image
- Certbot volumes shared with nginx for ACME challenge and certs
- **Let's Encrypt workflow:** (1) Nginx starts with HTTP-only config initially for ACME challenge serving, (2) Certbot runs `certonly --webroot -w /var/www/certbot -d $DOMAIN --email $EMAIL --agree-tos --non-interactive`, (3) On success, entrypoint.sh detects certs exist and switches to HTTPS config, (4) Post-renewal hook runs `nginx -s reload` to pick up new certs
- Certbot entrypoint: initial cert acquisition, then renew loop every 12 hours
- If cert acquisition fails (domain not pointing to server, rate limit), fall back to self-signed certs and log warning
- Remove host port mapping for app:8080 when nginx is present (use `expose` instead of `ports`)
- Add `nginx_ssl` and `nginx_conf` named volumes
- Add `certbot_conf` and `certbot_www` named volumes

**Definition of Done:**

- [ ] `nginx` service in docker-compose.yml with healthcheck, networks, volumes
- [ ] `certbot` service in docker-compose.yml with `letsencrypt` profile
- [ ] App service port changed from `ports` to `expose` (internal only)
- [ ] All new volumes declared in volumes section
- [ ] `docker compose config` validates without errors
- [ ] Nginx depends_on app with service_healthy condition

**Verify:**

- `docker compose config --quiet` — validates compose file
- `docker compose config --services` — lists nginx in output

---

### Task 4: Wizard Networking Step (Backend)

**Objective:** Add backend handlers and form processing for the new "Networking" wizard step, inserted after Services and before Storage.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` — add `HandleNetworking` handler, add fields to `WizardConfig`
- Modify: `internal/setup/wizard_forms.go` — add `processNetworkingForm`
- Modify: `internal/setup/validate.go` — add `ValidateDomain`, `ValidatePort`
- Modify: `internal/setup/server.go` — add GET/POST routes for `/setup/networking`
- Test: `internal/setup/wizard_flow_test.go` — add networking step tests
- Test: `internal/setup/validate_test.go` — add domain/port validation tests

**Key Decisions / Notes:**

- New `WizardConfig` fields:
  - `NginxDomain string` (e.g., "videos.example.com" or "localhost")
  - `NginxPort int` (default 80 for HTTP, 443 for HTTPS)
  - `NginxProtocol string` ("http" or "https")
  - `NginxTLSMode string` ("self-signed" or "letsencrypt", only when protocol=https)
  - `NginxEmail string` (for Let's Encrypt registration, only when TLS=letsencrypt)
- Default: HTTP, localhost, port 80
- `ValidateDomain`: reject empty, shell metacharacters, must be valid hostname or IP
- `ValidatePort`: range 1-65535, reject privileged ports <1024 unless 80/443
- Follow existing handler pattern: GET renders template, POST processes form and redirects
- Update redirect chain: services → networking → storage (was services → storage)
- Update `CompletedSteps` maps in storage, security, and review handlers to include "networking"

**Definition of Done:**

- [ ] `HandleNetworking` renders networking template on GET
- [ ] `processNetworkingForm` saves config and redirects to `/setup/storage` on POST
- [ ] Domain and port validation prevent invalid/dangerous inputs
- [ ] Routes registered in server.go for GET/POST `/setup/networking`
- [ ] Services POST redirects to `/setup/networking` (not `/setup/storage`)
- [ ] Unit tests for domain/port validation with edge cases
- [ ] Flow test verifies networking step in the wizard sequence
- [ ] `CompletedSteps` maps updated in ALL handlers after networking: HandleStorage, HandleSecurity, HandleReview, AND HandleComplete (grep for `CompletedSteps.*services.*true` to find all locations)

**Verify:**

- `go test ./internal/setup/ -run TestValidateDomain -v -short` — validation tests pass
- `go test ./internal/setup/ -run TestValidatePort -v -short` — validation tests pass
- `go test ./internal/setup/ -run TestNetworking -v -short` — handler tests pass

---

### Task 5: Wizard Networking Step (Template + Breadcrumb)

**Objective:** Create the HTML template for the networking wizard step and update the layout breadcrumb.

**Dependencies:** Task 4

**Files:**

- Create: `internal/setup/templates/networking.html`
- Modify: `internal/setup/templates/layout.html` — add "Networking" to breadcrumb
- Modify: `internal/setup/templates/services.html` — update note about next step if needed

**Key Decisions / Notes:**

- Template follows existing pattern from `services.html`: form groups with toggle buttons
- Fields:
  - **Domain/Hostname**: text input, default "localhost"
  - **Port**: number input, default 80 (updates to 443 when HTTPS selected)
  - **Protocol**: toggle buttons — HTTP / HTTPS
  - **TLS Mode** (shown only when HTTPS): toggle buttons — Self-Signed / Let's Encrypt
  - **Email** (shown only when Let's Encrypt): text input for cert registration
- JavaScript: toggle HTTPS fields visibility, auto-update port on protocol change
- Breadcrumb in layout.html: add "Networking" between "Services" and "Storage"
- Help text explaining each option:
  - HTTP: "For development or behind an existing reverse proxy"
  - HTTPS Self-Signed: "Quick TLS for internal/dev use. Browsers will show a warning."
  - HTTPS Let's Encrypt: "Free, trusted certificates. Requires a real domain pointing to this server."

**Definition of Done:**

- [ ] `networking.html` template renders with all form fields
- [ ] Protocol toggle shows/hides TLS mode section via JavaScript
- [ ] TLS mode toggle shows/hides email field for Let's Encrypt
- [ ] Port auto-updates between 80/443 on protocol toggle
- [ ] Breadcrumb in layout.html includes "Networking" in correct position
- [ ] Template uses consistent styling with other wizard steps
- [ ] Breadcrumb step count updated from 6 to 7 steps total (welcome, database, services, networking, storage, security, review)

**Verify:**

- `go test ./internal/setup/ -run TestWizard -v -short` — template parsing succeeds (all templates load)
- Verify breadcrumb lists all 7 steps in correct order: welcome, database, services, networking, storage, security, review

---

### Task 6: Env Writer + Config Loader Integration

**Objective:** Update the .env writer to output nginx-related settings and the config loader to read them. Wire `PUBLIC_BASE_URL` from the networking step.

**Dependencies:** Task 4

**Files:**

- Modify: `internal/setup/writer.go` — add nginx config section to `WriteEnvFile`
- Modify: `internal/config/config.go` — add nginx fields to Config struct
- Modify: `internal/config/config_load.go` — load nginx env vars
- Modify: `.env.example` — add documented nginx variables
- Test: `internal/setup/writer_test.go` — test nginx section written
- Test: `internal/config/config_test.go` — test nginx config loading

**Key Decisions / Notes:**

- New env vars:
  - `NGINX_ENABLED=true` (boolean, whether nginx is in use)
  - `NGINX_DOMAIN=localhost`
  - `NGINX_PORT=80`
  - `NGINX_PROTOCOL=http` (http or https)
  - `NGINX_TLS_MODE=` (self-signed or letsencrypt, empty when HTTP)
  - `NGINX_LETSENCRYPT_EMAIL=` (email for certbot)
  - `PUBLIC_BASE_URL=http://localhost:80` (auto-computed from domain+port+protocol)
- Config struct additions:
  - `NginxEnabled bool`
  - `NginxDomain string`
  - `NginxPort int`
  - `NginxProtocol string`
  - `NginxTLSMode string`
  - `NginxLetsEncryptEmail string`
- `PUBLIC_BASE_URL` is auto-computed: `{protocol}://{domain}` (omit port if 80/443, include otherwise)
- **CRITICAL:** Audit all existing `PUBLIC_BASE_URL` usage in codebase before implementation (grep for `PublicBaseURL` and `PUBLIC_BASE_URL` in `internal/activitypub/`, `internal/config/`, and all other packages). Ensure auto-computed format works with all consumers (ActivityPub actor IDs, federation, embed URLs). Add URL normalization helper if formats differ. If .env already has `PUBLIC_BASE_URL` set, preserve it and warn user.
- Writer generates env section after backup section, before security section
- Config loader defaults: `NginxEnabled=false`, `NginxProtocol=http`, `NginxPort=80`

**Definition of Done:**

- [ ] `WriteEnvFile` outputs nginx configuration section
- [ ] Config struct has all nginx fields
- [ ] Config loader reads nginx env vars with sensible defaults
- [ ] `PUBLIC_BASE_URL` auto-populated from nginx domain/protocol/port
- [ ] `.env.example` documents all nginx variables
- [ ] Unit tests verify .env output contains nginx section
- [ ] Unit tests verify config loading with various nginx env combinations
- [ ] Unit tests verify `PUBLIC_BASE_URL` generation for all cases: `http://localhost` (port 80), `https://example.com` (port 443), `http://example.com:8080` (custom port), `https://example.com:8443` (custom HTTPS port)

**Verify:**

- `go test ./internal/setup/ -run TestWriteEnv -v -short` — writer tests pass
- `go test ./internal/config/ -run TestNginx -v -short` — config loading tests pass
- `go test ./internal/config/ -run TestPublicBaseURL -v -short` — URL generation tests pass

---

### Task 7: Nginx Config Generator (Go)

**Objective:** Create a Go function that reads the nginx templates from Task 1, applies config values, and writes the final nginx.conf to the output directory.

**Dependencies:** Task 1, Task 6

**Files:**

- Create: `internal/setup/nginx_config.go` — `GenerateNginxConfig` function
- Test: `internal/setup/nginx_config_test.go`
- Modify: `internal/setup/wizard_forms.go` — call `GenerateNginxConfig` from `processReviewForm`

**Key Decisions / Notes:**

- `GenerateNginxConfig(config *WizardConfig, outputDir string) error`
  - Selects template based on `NginxProtocol` and `NginxTLSMode`
  - Applies config values to template
  - Writes to `outputDir/default.conf`
  - Also writes common includes to `outputDir/security.conf` and `outputDir/proxy.conf`
- Templates embedded via `//go:embed nginx/templates/*` or read from filesystem
- Called during `processReviewForm` alongside `WriteEnvFile`
- **Also callable standalone** via `make nginx-config` Makefile target that reads `.env` and regenerates `./nginx/conf/`. This enables post-wizard config changes: user edits `.env`, runs `make nginx-config`, then `docker compose restart nginx`
- Config output goes to `./nginx/conf/` directory (mounted into nginx container)
- **Atomic completion:** Generate nginx config FIRST, then write .env. If config generation fails, .env is not modified and user can retry. Show clear error message if template rendering fails.
- Table-driven tests verifying each mode produces valid output

**Definition of Done:**

- [ ] `GenerateNginxConfig` selects correct template based on protocol/TLS mode
- [ ] Generated config includes domain, port, upstream address
- [ ] Generated config writes to specified output directory
- [ ] Common security and proxy includes written alongside main config
- [ ] Called during review form processing (config generated BEFORE .env write — atomic completion)
- [ ] Standalone `make nginx-config` target reads `.env` and regenerates `./nginx/conf/` for post-wizard changes
- [ ] Table-driven tests for HTTP, HTTPS self-signed, and HTTPS Let's Encrypt modes
- [ ] Tests verify template placeholders are replaced with actual values
- [ ] Test verifies Let's Encrypt template includes `location /.well-known/acme-challenge/` block with correct root path matching certbot volume mount
- [ ] `GenerateNginxConfig` creates output directory if not present (`os.MkdirAll` with 0755 permissions)
- [ ] All tests pass: `go test ./internal/setup/ -run TestGenerateNginx -v -short`

**Verify:**

- `go test ./internal/setup/ -run TestGenerateNginx -v -short` — all tests pass
- `go test ./internal/setup/ -v -short` — full setup package tests pass

### Task 8: Bash Script Tests for Nginx Scripts

**Objective:** Create a comprehensive bash test suite for the nginx shell scripts (cert generation, entrypoint), following the established `scripts/install_test.sh` test pattern.

**Dependencies:** Task 2

**Files:**

- Create: `nginx/scripts/nginx_test.sh`

**Key Decisions / Notes:**

- Follow the exact test framework pattern from `scripts/install_test.sh`:
  - Colored output (RED/GREEN/YELLOW)
  - `test_pass()` / `test_fail()` helper functions
  - `TESTS_RUN` / `TESTS_PASSED` / `TESTS_FAILED` counters
  - `setup_test()` creates temp directory, `teardown_test()` cleans up
  - Mock functions for external commands (e.g., mock `openssl`, mock `nginx`)
- Test cases for `generate-self-signed-cert.sh`:
  - Generates cert and key files when none exist
  - Skips generation when certs already exist (idempotent)
  - Creates output directory if missing
  - Sets correct file permissions on generated certs
  - Cert includes correct CN/SAN from domain argument
  - Generates DH params when not present
  - Handles missing `openssl` gracefully (error message)
- Test cases for `entrypoint.sh`:
  - Runs cert generation when HTTPS enabled and no certs exist
  - Skips cert generation when HTTP mode
  - Skips cert generation when certs already exist
  - Falls back to HTTP if cert generation fails
  - Passes through to exec nginx after setup
  - Handles missing environment variables with defaults
- Use `mktemp -d` for isolated test environments
- Exit with non-zero if any tests fail
- **Two test tiers:** (1) Mocked tests for script logic (fast, no Docker required), (2) Integration tests that run scripts inside actual `nginx:1.27-alpine` container to verify OpenSSL compatibility and cert validity (`openssl x509 -text -noout -in cert.crt`). Integration tests guarded by `INTEGRATION=1` env var so CI can run them selectively.

**Definition of Done:**

- [ ] `nginx/scripts/nginx_test.sh` created with all test cases
- [ ] Test script follows `scripts/install_test.sh` framework exactly (counters, colors, setup/teardown)
- [ ] All cert generation scenarios tested (create, skip existing, error handling)
- [ ] All entrypoint scenarios tested (HTTP, HTTPS, fallback)
- [ ] Script is executable (`chmod +x`)
- [ ] Tests pass independently without Docker or external dependencies
- [ ] Tests produce clear pass/fail output with final summary

**Verify:**

- `bash -n nginx/scripts/nginx_test.sh` — syntax check passes
- `bash nginx/scripts/nginx_test.sh` — all tests pass with green output

---

### Task 9: GitHub Actions CI Workflow Update

**Objective:** Add nginx bash script tests to the existing CI pipeline's `shell-tests` job, following the established pattern for `install_test.sh`.

**Dependencies:** Task 8

**Files:**

- Modify: `.github/workflows/test.yml` — add nginx test step to `shell-tests` job
- Modify: `docker-compose.yml` — verify nginx service included in `ci` profile compose config validation (if applicable)

**Key Decisions / Notes:**

- Add a new step to the existing `shell-tests` job (not a new job) — keeps CI fast and parallel
- Pattern from existing workflow:
  ```yaml
  - name: Run nginx script tests
    run: bash nginx/scripts/nginx_test.sh
  ```
- Place after the existing `Run install.sh tests` step
- No additional setup needed — bash tests are self-contained with mocks
- Consider adding `docker compose config --quiet` validation step if not already present
- The `paths-ignore` already excludes `**/*.md` and `docs/**` — nginx scripts will trigger CI correctly since they're under `nginx/`

**Definition of Done:**

- [ ] `shell-tests` job in `.github/workflows/test.yml` includes nginx test step
- [ ] Step runs after `install_test.sh` step
- [ ] CI workflow YAML passes validation (`yamllint` or `actionlint`)
- [ ] Docker compose config validation step added as FIRST step in `shell-tests` job (before bash tests) for fast failure on YAML syntax errors

**Verify:**

- `cat .github/workflows/test.yml | grep nginx_test` — step exists in workflow
- `docker compose config --quiet` — compose validation succeeds

---

## Testing Strategy

- **Unit tests (Go):** Template parsing, validation functions, config loading, env writer output, nginx config generation (all table-driven, `go test -short`)
- **Bash script tests:** `nginx/scripts/nginx_test.sh` — tests cert generation, entrypoint logic, error handling with mock commands (follows `scripts/install_test.sh` framework pattern)
- **Integration tests (Go):** Full wizard flow with networking step (HTTP request sequence through all steps)
- **CI pipeline:** GitHub Actions `shell-tests` job runs both `scripts/install_test.sh` and `nginx/scripts/nginx_test.sh`; `unit` job runs all Go tests
- **Manual verification:** `docker compose up`, visit `http://localhost/setup/welcome`, complete wizard including networking step, verify nginx serves proxied responses

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Port 80/443 already in use on host | Medium | Medium | Use configurable `NGINX_HTTP_PORT`/`NGINX_HTTPS_PORT` env vars with defaults; wizard shows warning if ports likely conflict |
| Let's Encrypt fails (no real domain) | Medium | Low | Validate domain is not localhost/127.0.0.1 when Let's Encrypt selected; show clear error message with instructions |
| Nginx starts before app is healthy | Low | Medium | `depends_on: app: condition: service_healthy` in compose; nginx healthcheck verifies proxy works |
| Self-signed cert generation fails | Low | Medium | Entrypoint script has error handling; falls back to HTTP if cert generation fails |
| Breaking existing Docker setup | Medium | High | Docker Compose conditionally exposes app port: when `NGINX_ENABLED=true`, app uses `expose: ["8080"]` (internal only, nginx proxies); when `NGINX_ENABLED=false` (default for existing installs), app uses `ports: ["8080:8080"]` (host-accessible). Wizard sets `NGINX_ENABLED=true` in .env. Implementation: use Docker Compose profiles or separate service override file |
| WebSocket connections fail through proxy | Low | Medium | Explicit `Upgrade` and `Connection` headers in nginx proxy config for `/ws/` paths |
| Port privilege on Linux | Low | Low | Nginx inside container always listens on 80/443 (standard for official image). Docker Compose maps host ports via `NGINX_HTTP_PORT`/`NGINX_HTTPS_PORT` env vars. No `cap_add` needed for default setup |

## Open Questions

- None — all decisions made during clarification.

### Deferred Ideas

- HTTP/3 (QUIC) support for Nginx
- Nginx load balancing across multiple app instances
- Automatic Nginx config reload on .env change (currently requires container restart)
- CDN integration for static assets
- WAF rules (ModSecurity) integration
