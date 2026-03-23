# Codebase Full Audit - Phase 1: Docker Mock Infrastructure & Setup Wizard E2E

Created: 2026-02-21
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

**Goal:** Create Docker-based mock services for all external integrations (MinIO for S3, mock ATProto PDS, mock ActivityPub inbox) and build a comprehensive integration test suite that verifies the setup wizard and each external service connection works end-to-end. This establishes the test infrastructure that all future audit phases depend on.

**Architecture:** Add new Docker Compose services under a `test-integration` profile: MinIO (S3-compatible), a lightweight Go mock ATProto PDS, and a Go mock ActivityPub server. Create a Go integration test file (`tests/integration/setup_wizard_e2e_test.go`) that starts the app in setup mode, drives the wizard flow via HTTP, and verifies each service connection against real Docker containers. Add a `docker-compose.test-integration.yml` override for the integration test stack.

**Tech Stack:** Go (net/http/httptest for mocks, testing package), Docker Compose (MinIO, existing services), shell scripts for orchestration.

## Scope

### In Scope

- Add MinIO service to Docker Compose for S3-compatible storage testing
- Create a lightweight mock ATProto PDS server (Go binary in `tests/mocks/atproto-pds/`) that handles `createSession`, `refreshSession`, `createRecord`, and `uploadBlob` endpoints
- Create a lightweight mock ActivityPub server (Go binary in `tests/mocks/activitypub/`) that handles inbox POST, actor GET, and WebFinger
- Add both mock servers as Docker Compose services under the `test-integration` profile
- Create integration tests that verify the setup wizard E2E flow (welcome → database → services → email → networking → storage → security → review → complete)
- Test each external service connection handler (`HandleTestDatabase`, `HandleTestRedis`, `HandleTestIPFS`, `HandleTestIOTA`, `HandleTestEmail`) against real Docker services
- Verify the `.env` and `docker-compose.override.yml` generation from the wizard
- Create a `make test-integration` target that orchestrates the Docker stack and runs the tests
- Document gaps found during testing (broken features, missing functionality) in a findings file
- S3 backend integration test against MinIO
- ATProto service integration test against mock PDS

### Out of Scope

- Implementing IOTA payment stubs (BuildTransaction, SignTransaction, SubmitTransaction) — deferred to Phase 2 plan
- Custom platform token support — deferred to Phase 2
- Modifying existing unit tests
- Changes to the setup wizard UI or flow (tested as-is)
- Production deployment changes
- Full ActivityPub federation E2E (just inbox/outbox verification)

## Prerequisites

- Docker and Docker Compose installed on dev machine
- Go 1.24 installed
- `make` available

## Context for Implementer

> This section is critical for cross-session continuity. Write it for an implementer who has never seen the codebase.

- **Patterns to follow:** The existing Docker Compose `test` profile in `docker-compose.yml:284-445` shows the pattern for test-specific services (tmpfs, test ports, healthchecks). Follow this exact pattern for new test services.
- **Conventions:**
  - Test files use `_test.go` suffix and `testing.Short()` to skip infrastructure-dependent tests
  - Integration tests go in `tests/` directory (see `tests/` already exists)
  - Docker services use profiles to control startup (`profiles: ["test-integration"]`)
  - The setup wizard runs when `SETUP_COMPLETED=false` (see `scripts/docker-entrypoint.sh`)
  - All test connections go through SSRF validation via `security.NewURLValidator()` — mock servers must bind to non-private IPs or the SSRF check must be relaxed in test mode
- **Key files:**
  - `internal/setup/server.go` — Setup wizard HTTP server with all routes
  - `internal/setup/wizard.go` — Wizard struct, handlers, WizardConfig, NewWizard()
  - `internal/setup/wizard_test_connections.go` — Test connection handlers for DB, Redis, IPFS, IOTA
  - `internal/setup/wizard_forms.go` — Form processing handlers (processReviewForm, processQuickInstallForm)
  - `internal/setup/writer.go` — WriteEnvFile (generates .env from wizard config)
  - `internal/setup/compose_override.go` — WriteComposeOverride (generates docker-compose.override.yml)
  - `internal/usecase/atproto_service.go` — ATProto publisher service (createSession, publishVideo, uploadBlob)
  - `internal/storage/s3_backend.go` — S3 storage backend using AWS SDK v2
  - `internal/activitypub/httpsig.go` — HTTP signature signing/verification for ActivityPub
  - `docker-compose.yml` — Full Docker Compose stack with dev, test, and CI profiles
  - `internal/security/url_validator.go` — SSRF protection (`NewURLValidator()`) used by test connection handlers
- **Gotchas:**
  - The SSRF validator (`security.NewURLValidator()`) blocks connections to private IPs (127.0.0.1, 10.x, 172.16-31.x, 192.168.x). Docker container-to-container communication uses private IPs. Test connection handlers in the wizard use SSRF validation, so when testing against Docker services, the test must either: (a) use the Docker network hostname (which resolves to a container IP that passes SSRF), or (b) relax SSRF for the test environment.
  - MinIO defaults to port 9000 (API) and 9001 (console). Port 9000 conflicts with IOTA node and Whisper. Use different host port mappings (e.g., 19100:9000).
  - The `app` service in Docker Compose runs in setup mode when `SETUP_COMPLETED=false`. For integration tests, we can test against the wizard server directly (no Docker needed for the app itself — just start the Go HTTP handler in-process).
  - Mock ATProto PDS needs to handle: `POST /xrpc/com.atproto.server.createSession`, `POST /xrpc/com.atproto.server.refreshSession`, `POST /xrpc/com.atproto.repo.createRecord`, `POST /xrpc/com.atproto.repo.uploadBlob`, `GET /xrpc/app.bsky.feed.getAuthorFeed`.
  - Mock ActivityPub server needs to handle: `POST /inbox` (shared inbox), `GET /users/{username}` (actor), `GET /.well-known/webfinger` (discovery).
- **Domain context:**
  - This is a PeerTube-compatible backend written in Go. It supports ActivityPub federation (like Mastodon/PeerTube) and ATProto (like BlueSky). IOTA Rebased is used for crypto payments. S3-compatible storage is an option alongside local filesystem and IPFS.
  - The setup wizard is the first-run experience — it collects configuration (database, services, email, networking, storage, security) and generates `.env` and `docker-compose.override.yml`.

## Runtime Environment

- **Start command:** `docker compose --profile test-integration up -d` for mock services; Go test binary runs locally
- **Port mappings (test-integration profile):**
  - MinIO API: localhost:19100 (container 9000)
  - MinIO Console: localhost:19101 (container 9001)
  - Mock ATProto PDS: localhost:19200 (container 8080)
  - Mock ActivityPub: localhost:19300 (container 8080)
  - Mailpit Web UI: localhost:19400 (container 8025), SMTP: localhost:19401 (container 1025)
  - Mock IOTA RPC: localhost:19500 (container 8080)
- **Health check:** Each mock service exposes a `/health` or `/` endpoint
- **Test command:** `make test-integration`

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Refactor Wizard for testability (injectable validator + output paths)
- [x] Task 2: Add MinIO S3 mock to Docker Compose
- [x] Task 3: Create mock ATProto PDS server
- [x] Task 4: Create mock ActivityPub server and mock IOTA RPC server
- [x] Task 5: Add all mock services to Docker Compose (test-integration profile)
- [x] Task 6: Setup wizard E2E integration tests
- [x] Task 7: External service connection integration tests
- [x] Task 8: S3 backend integration test against MinIO
- [x] Task 9: ATProto service integration test against mock PDS
- [x] Task 10: Integration test orchestration (Makefile + script)
- [x] Task 11: Gap analysis and findings document

**Total Tasks:** 11 | **Completed:** 11 | **Remaining:** 0

## Implementation Tasks

### Task 1: Refactor Wizard for testability (injectable validator + output paths)

**Objective:** Add configurable output paths and an injectable URL validator to the Wizard struct so integration tests can: (a) write `.env` and `docker-compose.override.yml` to temp directories without overwriting real files, and (b) bypass SSRF validation when connecting to Docker containers (whose hostnames resolve to private IPs that the SSRF validator blocks).

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` (add fields to Wizard struct, update NewWizard constructor)
- Modify: `internal/setup/wizard_test_connections.go` (use injectable validator instead of hardcoded `security.NewURLValidator()`)
- Modify: `internal/setup/wizard_forms.go` (use configurable output paths for WriteEnvFile/WriteComposeOverride/GenerateNginxConfig)
- Modify: `internal/setup/writer.go` (accept output directory parameter)
- Modify: `internal/setup/compose_override.go` (accept output directory parameter)
- Modify: `internal/setup/nginx_config.go` (accept output directory parameter)

**Key Decisions / Notes:**

- Add these fields to the Wizard struct:

  ```go
  OutputDir    string                    // defaults to "." (current dir)
  URLValidator *security.URLValidator    // defaults to security.NewURLValidator()
  ```

- Update `NewWizard()` to use functional options or default values. For simplicity, set defaults in the constructor and allow override via exported fields after construction.
- The existing pattern in the codebase uses injectable validators: `internal/usecase/social/service_test.go` and `internal/usecase/activitypub/service_extended_test.go` already use `security.NewURLValidatorAllowPrivate()`. Check if this function exists in `internal/security/url_validator.go`; if not, add it.
- Update `WriteEnvFile(path, config)` → `WriteEnvFile(filepath.Join(w.OutputDir, path), config)`. Same for `WriteComposeOverride` and `GenerateNginxConfig`.
- In `wizard_test_connections.go`, replace all `security.NewURLValidator()` calls with `w.URLValidator`. There are 4 handlers that call it: HandleTestDatabase, HandleTestRedis, HandleTestIPFS, HandleTestIOTA.
- The `processReviewForm` also calls `GenerateNginxConfig(w.config, "nginx/conf")` — update to use `filepath.Join(w.OutputDir, "nginx/conf")`.
- All existing unit tests must still pass (they use the default validator/paths).

**Definition of Done:**

- [ ] Wizard struct has `OutputDir` and `URLValidator` fields with sensible defaults
- [ ] All 4 test connection handlers use `w.URLValidator` instead of `security.NewURLValidator()`
- [ ] `processReviewForm` and `processQuickInstallForm` write to `w.OutputDir` subdirectory
- [ ] Existing unit tests pass: `go test ./internal/setup/... -count=1`
- [ ] No changes to default behavior (production paths unchanged)

**Verify:**

- `go test ./internal/setup/... -count=1 -v`
- `go build ./cmd/server`

---

### Task 2: Add MinIO S3 mock to Docker Compose

**Objective:** Add a MinIO service to Docker Compose under the `test-integration` profile for S3-compatible storage testing.

**Dependencies:** None

**Files:**

- Modify: `docker-compose.yml` (add MinIO service under test-integration profile)

**Key Decisions / Notes:**

- Use `minio/minio:latest` image
- Map ports: `19100:9000` (API), `19101:9001` (console) to avoid conflicts with IOTA (14265) and Whisper (9000)
- Default credentials: `minioadmin`/`minioadmin`
- Create default bucket `vidra-test` on startup using `minio/mc` init container or entrypoint command
- Use tmpfs for data (no persistence needed for tests)
- Add healthcheck: `curl -f http://localhost:9000/minio/health/live`
- Network: `test-integration-network` (new network for integration tests)

**Definition of Done:**

- [ ] `docker compose --profile test-integration up minio` starts MinIO successfully
- [ ] MinIO API responds on `localhost:19100`
- [ ] Default bucket `vidra-test` exists after startup
- [ ] Healthcheck passes within 30 seconds

**Verify:**

- `docker compose --profile test-integration up -d minio && sleep 5 && curl -sf http://localhost:19100/minio/health/live`

### Task 3: Create mock ATProto PDS server

**Objective:** Build a lightweight Go HTTP server that mocks the ATProto PDS XRPC endpoints needed by `atproto_service.go`, allowing integration testing without a real PDS.

**Dependencies:** None

**Files:**

- Create: `tests/mocks/atproto-pds/main.go`
- Create: `tests/mocks/atproto-pds/main_test.go` (smoke tests for the mock itself)
- Create: `tests/mocks/atproto-pds/Dockerfile`

**Key Decisions / Notes:**

- Implement these endpoints (minimal, return valid JSON):
  - `POST /xrpc/com.atproto.server.createSession` — Accept identifier/password, return `{accessJwt: "test-access-token-<random>", refreshJwt: "test-refresh-token-<random>", did: "did:plc:test123"}`
  - `POST /xrpc/com.atproto.server.refreshSession` — **Validate Bearer token matches a previously issued refresh token**, return new tokens. Return 401 if token invalid.
  - `POST /xrpc/com.atproto.repo.createRecord` — **Validate Bearer auth header matches an issued access token**. Return 401 if missing/invalid. Accept record, return `{uri, cid}`
  - `POST /xrpc/com.atproto.repo.uploadBlob` — **Validate Bearer auth header**. Accept blob, return `{blob: {$type: "blob", ref: {$link: "<hash>"}, mimeType: "<detected>", size: <bytes>}}`
  - `GET /xrpc/app.bsky.feed.getAuthorFeed` — Return empty feed `{feed: [], cursor: ""}`
  - `GET /health` — Return 200
- **Auth token validation is critical** — the mock must track issued tokens and validate them on subsequent requests. This exercises the actual auth flow in `atproto_service.go` (session creation → token reuse → token refresh).
- Store created records in memory (slice) so tests can verify what was published
- Add `GET /test/records` debug endpoint to retrieve all created records (for test assertions)
- Add `GET /test/blobs` debug endpoint to list uploaded blobs
- Add `main_test.go` with httptest-based smoke tests to prevent false negatives from broken mocks
- Use standard library only (no external deps) — single `main.go` file
- Dockerfile: multi-stage build from `golang:1.24-alpine`, run from `alpine:3.18`
- Listen on port 8080 inside container

**Definition of Done:**

- [ ] `POST /xrpc/com.atproto.server.createSession` returns valid session JSON with accessJwt, refreshJwt, and did fields
- [ ] `POST /xrpc/com.atproto.repo.createRecord` validates Bearer token and stores the record
- [ ] Requests without valid Bearer token to createRecord/uploadBlob return 401
- [ ] `GET /test/records` returns all records created during the session
- [ ] `GET /health` returns 200
- [ ] Smoke tests pass: `go test ./tests/mocks/atproto-pds/... -v`
- [ ] Docker image builds: `docker build -t vidra-mock-atproto tests/mocks/atproto-pds/`

**Verify:**

- `cd tests/mocks/atproto-pds && go test -v && go build -o mock-atproto . && echo "Build OK"`
- `docker build -t vidra-mock-atproto tests/mocks/atproto-pds/`

### Task 4: Create mock ActivityPub server and mock IOTA RPC server

**Objective:** Build two lightweight Go HTTP servers: (1) a mock ActivityPub server for federation testing, and (2) a mock IOTA JSON-RPC server to avoid the 120s+ startup of a real IOTA node.

**Dependencies:** None

**Files:**

- Create: `tests/mocks/activitypub/main.go`
- Create: `tests/mocks/activitypub/main_test.go` (smoke tests)
- Create: `tests/mocks/activitypub/Dockerfile`
- Create: `tests/mocks/iota-rpc/main.go`
- Create: `tests/mocks/iota-rpc/main_test.go` (smoke tests)
- Create: `tests/mocks/iota-rpc/Dockerfile`

**Key Decisions / Notes:**

**Mock ActivityPub server:**

- Implement these endpoints:
  - `POST /inbox` — Accept Activity JSON (Follow, Create, Like, etc.), store in memory, return 202 Accepted. Log whether a valid `Signature` header was present (for assertion via `/test/inbox`).
  - `GET /users/{username}` — Return ActivityPub Actor JSON-LD with inbox, outbox, publicKey
  - `GET /.well-known/webfinger?resource=acct:{user}@{domain}` — Return WebFinger JRD with actor link
  - `GET /.well-known/nodeinfo` — Return NodeInfo discovery document
  - `GET /test/inbox` — Debug endpoint: return all received activities with `has_signature: bool` field
  - `GET /health` — Return 200
- Generate an RSA keypair at startup for the mock actor's `publicKey`
- Return proper `Content-Type: application/activity+json` headers

**Mock IOTA JSON-RPC server:**

- Implement a minimal JSON-RPC 2.0 server that responds to:
  - `iota_getChainIdentifier` → `{"jsonrpc":"2.0","id":1,"result":"35834a8a"}`
  - `iota_getLatestCheckpointSequenceNumber` → `{"jsonrpc":"2.0","id":1,"result":"1000"}`
  - `iotax_getBalance` → `{"jsonrpc":"2.0","id":1,"result":{"coinType":"0x2::iota::IOTA","totalBalance":"1000000000"}}`
  - `iota_getTransactionBlock` → valid transaction block response
  - Any unknown method → `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"Method not found"}}`
- `GET /health` — Return 200
- Both use standard library only, single `main.go` each
- Dockerfiles: multi-stage build from `golang:1.24-alpine`

**Definition of Done:**

- [ ] ActivityPub mock: `POST /inbox` accepts activities and returns 202
- [ ] ActivityPub mock: `GET /users/testuser` returns valid Actor JSON-LD with publicKey
- [ ] ActivityPub mock: `GET /.well-known/webfinger` returns proper JRD
- [ ] ActivityPub mock: `/test/inbox` includes `has_signature` field
- [ ] IOTA mock: JSON-RPC `iota_getChainIdentifier` returns valid response
- [ ] IOTA mock: JSON-RPC `iotax_getBalance` returns valid balance response
- [ ] Smoke tests pass for both mocks
- [ ] Docker images build successfully

**Verify:**

- `cd tests/mocks/activitypub && go test -v && go build -o mock-activitypub .`
- `cd tests/mocks/iota-rpc && go test -v && go build -o mock-iota-rpc .`
- `docker build -t vidra-mock-activitypub tests/mocks/activitypub/`
- `docker build -t vidra-mock-iota-rpc tests/mocks/iota-rpc/`

### Task 5: Add all mock services to Docker Compose (test-integration profile)

**Objective:** Add all mock servers and infrastructure as Docker Compose services under the `test-integration` profile for the integration test stack.

**Dependencies:** Task 2, Task 3, Task 4

**Files:**

- Modify: `docker-compose.yml` (add mock-atproto-pds, mock-activitypub services, and test-integration copies of postgres/redis/ipfs)

**Key Decisions / Notes:**

- Add services under `test-integration` profile:
  - `mock-atproto-pds`: Build from `tests/mocks/atproto-pds/`, port `19200:8080`
  - `mock-activitypub`: Build from `tests/mocks/activitypub/`, port `19300:8080`
  - `mailpit-integration`: Same as existing `mailpit` but on ports `19400:8025` (web UI), `19401:1025` (SMTP) — required for email connection testing in Task 6
  - `mock-iota-rpc`: A lightweight Go mock (add to `tests/mocks/iota-rpc/main.go`) that responds to `iota_getChainIdentifier` and `iota_getLatestCheckpointSequenceNumber` JSON-RPC calls — avoids the 120s+ startup of the real IOTA node. Port `19500:8080`.
  - `postgres-integration`: Same as `postgres-test` but on port `15432:5432` (avoid conflict with test profile)
  - `redis-integration`: Same as `redis-test` but on port `16379:6379`
  - `ipfs-integration`: Same as `ipfs-test` but on port `15001:5001` (same as test, use if not conflicting)
- All on `test-integration-network`
- Use tmpfs for ephemeral data where possible
- Healthchecks on all services
- Add `minio-init` service that creates the `vidra-test` bucket using `minio/mc` (depends on MinIO health)

**Definition of Done:**

- [ ] `docker compose --profile test-integration up -d` starts all mock services and infrastructure
- [ ] All services pass healthchecks within 120 seconds
- [ ] `curl http://localhost:19200/health` returns 200 (mock ATProto PDS)
- [ ] `curl http://localhost:19300/health` returns 200 (mock ActivityPub)
- [ ] `curl http://localhost:19100/minio/health/live` returns 200 (MinIO)
- [ ] `curl http://localhost:19400/` returns 200 (Mailpit web UI)
- [ ] `curl -X POST -H "Content-Type: application/json" -d '{"jsonrpc":"2.0","id":1,"method":"iota_getChainIdentifier","params":[]}' http://localhost:19500` returns valid JSON-RPC response (mock IOTA)
- [ ] PostgreSQL, Redis, IPFS are accessible on their integration ports

**Verify:**

- `docker compose --profile test-integration up -d && docker compose --profile test-integration ps`
- `docker compose --profile test-integration down -v`

### Task 6: Setup wizard E2E integration tests

**Objective:** Create integration tests that drive the setup wizard through its full flow (welcome → database → services → email → networking → storage → security → review → complete) using the in-process HTTP handler, verifying each page renders and form submissions succeed.

**Dependencies:** Task 1, Task 5

**Files:**

- Create: `tests/integration/setup_wizard_e2e_test.go`

**Key Decisions / Notes:**

- Use `httptest.NewServer` with the setup wizard's `Handler()` — no Docker needed for the app itself
- Test the full wizard flow:
  1. GET /setup/welcome — verify 200, contains "Welcome"
  2. GET /setup/database — verify form renders
  3. POST /setup/database — submit Docker mode config, verify redirect to /setup/services
  4. GET /setup/services — verify form renders with IPFS, IOTA, ClamAV, Whisper options
  5. POST /setup/services — enable IPFS + IOTA, verify redirect to /setup/email
  6. GET /setup/email — verify form renders
  7. POST /setup/email — submit Docker email config, verify redirect
  8. Continue through networking, storage, security
  9. POST /setup/review — verify `.env` and `docker-compose.override.yml` are generated in temp dir
  10. GET /setup/complete — verify completion page renders
- **Output path injection:** Use `wizard.OutputDir = t.TempDir()` (from Task 1 refactoring). This directs `.env`, `docker-compose.override.yml`, and `nginx/conf` writes to the temp directory. No `os.Chdir` needed.
- **Wizard is stateful in-memory:** All E2E flow tests MUST use a single `httptest.NewServer` with the same `Handler()` instance. The wizard stores state in the `Wizard` struct fields (no cookies/sessions). Creating separate servers per step will lose state. Use `http.Client{CheckRedirect: func(...) error { return http.ErrUseLastResponse }}` to prevent auto-redirect-following so you can inspect redirect locations.
- **Review POST path:** The `processReviewForm` calls `GenerateNginxConfig`, `WriteComposeOverride`, and `WriteEnvFile`. With Task 1's refactoring, these write to `wizard.OutputDir`. Ensure the nginx conf subdirectory exists: `mkdir -p <tempdir>/nginx/conf` before the review POST.
- **processReviewForm also calls no-op admin creation in setup mode** — the DB isn't connected in httptest, so any DB operations will error. The E2E test should either: (a) test up to the review GET (not POST) for the full-flow test, and test review POST separately with mocked DB, or (b) expect and handle the admin creation error gracefully.
- Verify `.env` contents: `COMPOSE_PROFILES`, `DATABASE_URL`, `REDIS_URL`, `ENABLE_IPFS`, etc.
- Verify `docker-compose.override.yml` contents based on configuration choices
- **Quick Install flow test:** The Quick Install path (`HandleQuickInstall` + `processQuickInstallForm`) is a single-form submission that auto-configures all services in Docker mode (Postgres=docker, Redis=docker, Email=docker via Mailpit). It generates `.env` with Docker-mode defaults and `docker-compose.override.yml` with no overrides needed. Test should verify: form submission redirects to `/setup/complete`, generated `.env` contains `COMPOSE_PROFILES=mail`, `DATABASE_URL` points to docker postgres, and `SETUP_COMPLETED=true`.
- Use `testing.Short()` guard — these tests don't need Docker but are slower than unit tests
- Follow table-driven test pattern for form submissions

**Definition of Done:**

- [ ] Full wizard flow test passes: welcome → database → services → email → networking → storage → security → review → complete
- [ ] Quick Install flow test passes
- [ ] Generated `.env` contains correct values for the submitted configuration
- [ ] Generated `docker-compose.override.yml` correctly disables services based on config
- [ ] Error cases tested: invalid form submissions return errors (not redirects)
- [ ] All tests pass: `go test ./tests/integration/... -run TestSetupWizard -v`

**Verify:**

- `go test ./tests/integration/... -run TestSetupWizard -v -count=1`

### Task 7: External service connection integration tests

**Objective:** Create integration tests that verify each external service test connection handler works against real Docker services (PostgreSQL, Redis, IPFS, mock IOTA, Mailpit).

**Dependencies:** Task 1, Task 5

**Files:**

- Create: `tests/integration/service_connections_test.go`

**Key Decisions / Notes:**

- These tests require Docker services running (`docker compose --profile test-integration up -d`)
- Guard with `testing.Short()` and an env var check (`TEST_INTEGRATION=true`)
- For each service, test both success and failure cases:
  - **PostgreSQL**: Connect to `postgres-integration:5432` (via localhost:15432). Test with valid creds (success) and invalid creds (failure). Note: SSRF validator may block localhost — test via Docker hostname or disable SSRF in test.
  - **Redis**: Connect to `redis-integration:6379` (via localhost:16379). Test PING/PONG.
  - **IPFS**: Connect to `ipfs-integration:5001` (via localhost:15001). Test `/api/v0/version`.
  - **IOTA**: Connect to `iota-node-test:9000` (via localhost:19000 if available). Test `iota_getChainIdentifier` RPC. If IOTA node is too slow to start, skip with `t.Skip`.
  - **Email (Mailpit)**: Test SMTP send to Mailpit and verify via Mailpit API (`GET /api/v1/messages`).
- Use `httptest.NewServer` with the wizard handler, POST to `/setup/test-{service}` endpoints
- **SSRF bypass (from Task 1):** Create the Wizard with `wizard.URLValidator = security.NewURLValidatorAllowPrivate()` so test connections to Docker containers on private IPs (172.x.x.x) are allowed. This is safe because integration tests run in controlled environments.
- **Rate limiting constraint:** All test connection handlers share the SAME rate limit map (`w.testEmailLimit`) protected by `w.mu`. After 3 requests within 5 minutes across ALL endpoints, further requests are rejected. Mitigate by creating a NEW Wizard instance per test function to get a fresh rate limiter. Alternatively, use different `RemoteAddr` values per test case.
- **Redis password bug to document:** The `HandleTestRedis` handler parses a `password` field from the JSON request but NEVER sends an `AUTH` command before `PING`. Test with a password-protected Redis instance to confirm this bug. Document in the findings (Task 11).
- **Email test multi-step:** To test `HandleTestEmail`, first POST to `/setup/email` to set SMTP config (host=localhost, port=<mailpit_smtp_port>), then POST to `/setup/test-email`. Verify delivery via Mailpit API: `GET http://localhost:19400/api/v1/messages`.

**Definition of Done:**

- [ ] PostgreSQL test connection succeeds against live Docker Postgres
- [ ] Redis test connection succeeds against live Docker Redis
- [ ] IPFS test connection succeeds against live Docker IPFS (or skips if slow to start)
- [ ] IOTA test connection succeeds against live Docker IOTA node (or skips if unavailable)
- [ ] Email test sends to Mailpit and message appears in Mailpit API
- [ ] Failure cases tested: wrong credentials, wrong ports, unreachable hosts
- [ ] Tests skip gracefully when Docker services aren't running

**Verify:**

- `TEST_INTEGRATION=true go test ./tests/integration/... -run TestServiceConnections -v -count=1 -timeout 120s`

### Task 8: S3 backend integration test against MinIO

**Objective:** Create an integration test that verifies the S3 storage backend (`internal/storage/s3_backend.go`) works against a real S3-compatible service (MinIO).

**Dependencies:** Task 5

**Files:**

- Create: `tests/integration/s3_storage_test.go`

**Key Decisions / Notes:**

- Test the full S3 lifecycle: create backend → Upload file → Download file → Delete file → verify deletion with Exists()
- **MinIO config:** `S3Config{Endpoint: "http://localhost:19100", Bucket: "vidra-test", AccessKey: "minioadmin", SecretKey: "minioadmin", Region: "us-east-1", PathStyle: true}`. **PathStyle MUST be true** for MinIO (virtual-hosted-style addressing doesn't work with MinIO).
- Test methods: `Upload()`, `UploadPrivate()`, `Download()`, `Delete()`, `DeleteMultiple()`, `Exists()`, `GetMetadata()`, `Copy()`, `GetSignedURL()`
- Test with different file types (video, image, text) and sizes
- Test S3 categories (if `s3_categories.go` provides upload path logic)
- Test error cases: invalid bucket, invalid credentials
- Guard with `TEST_INTEGRATION=true` env var
- **MinIO ACL limitation:** MinIO has deprecated bucket/object ACLs. Tests involving `uploadACLPublic`/`uploadACLPrivate` may succeed silently without enforcing ACLs. Document this as a known limitation.
- Verify public URL generation if `PublicURL` is configured

**Definition of Done:**

- [ ] Upload a test file to MinIO via `S3Backend.Upload()` and retrieve it via `S3Backend.Download()`
- [ ] File content matches after round-trip
- [ ] `Exists()` returns true for uploaded file, false for non-existent
- [ ] `Delete()` removes the object, `Exists()` returns false afterward
- [ ] `GetMetadata()` returns correct content type and size
- [ ] Error case: invalid credentials returns error
- [ ] Error case: non-existent bucket returns error
- [ ] Test passes: `TEST_INTEGRATION=true go test ./tests/integration/... -run TestS3Storage -v`

**Verify:**

- `TEST_INTEGRATION=true go test ./tests/integration/... -run TestS3Storage -v -count=1 -timeout 60s`

### Task 9: ATProto service integration test against mock PDS

**Objective:** Create an integration test that verifies the ATProto publisher service works against the mock PDS, including session creation, video publishing, and blob upload.

**Dependencies:** Task 3, Task 5

**Files:**

- Create: `tests/integration/atproto_service_test.go`

**Key Decisions / Notes:**

- Start mock ATProto PDS (either via Docker on localhost:19200 or in-process using httptest)
- **Required interfaces to mock:**
  1. `InstanceConfigReader` — Create a simple struct implementing `GetInstanceConfig(ctx, key)`. Return the mock PDS URL when key is `"atproto_pds_url"`.
  2. `AtprotoSessionStore` — Create a simple in-memory store implementing `SaveSession()` and `LoadSessionStrings()`.
  3. `encKey` — Any 32-byte key (e.g., `bytes.Repeat([]byte{0x42}, 32)`)
- **Required config.Config fields:**

  ```go
  cfg := &config.Config{
    EnableATProto:         true,
    ATProtoPDSURL:         "http://localhost:19200",
    ATProtoHandle:         "test.handle",
    ATProtoAppPassword:    "test-app-password",
    ATProtoUseImageEmbed:  false,
    PublicBaseURL:          "https://example.com",
  }
  ```

  Note: `config.ATProtoHTTPTimeout` is a constant (5s), not a config field.
- Test flow:
  1. Create session with mock credentials → verify tokens received
  2. Publish a video (create a `domain.Video{ID: uuid, Title: "Test Video", Description: "desc", Privacy: domain.PrivacyPublic, Status: domain.StatusCompleted}`)
  3. Verify the record was created via `GET /test/records` on mock PDS
  4. **Blob upload test:** Create a temp image file on disk (using `testutil.CreateTestPNG()`), set `video.ThumbnailPath` to the file path, publish again, verify blob was uploaded via `GET /test/blobs`
  5. Test session refresh
  6. Test disabled mode (`EnableATProto=false` → `PublishVideo` is no-op)
- Guard with `TEST_INTEGRATION=true`

**Definition of Done:**

- [ ] Session creation against mock PDS succeeds
- [ ] PublishVideo creates a `app.bsky.feed.post` record on mock PDS with correct text and embed
- [ ] Blob upload sends file to mock PDS and returns blob reference
- [ ] Session refresh works
- [ ] Disabled mode (EnableATProto=false) doesn't call PDS
- [ ] Test passes: `TEST_INTEGRATION=true go test ./tests/integration/... -run TestAtprotoService -v`

**Verify:**

- `TEST_INTEGRATION=true go test ./tests/integration/... -run TestAtprotoService -v -count=1 -timeout 60s`

### Task 10: Integration test orchestration (Makefile + script)

**Objective:** Create a `make test-integration` target and supporting script that starts the Docker test stack, waits for health, runs all integration tests, and tears down the stack.

**Dependencies:** Task 6, Task 7, Task 8, Task 9

**Files:**

- Modify: `Makefile` (add `test-integration` target)
- Create: `scripts/test-integration.sh`

**Key Decisions / Notes:**

- Script flow:
  1. `docker compose --profile test-integration up -d --build`
  2. Wait for all healthchecks (with timeout)
  3. `TEST_INTEGRATION=true go test ./tests/integration/... -v -count=1 -timeout 300s`
  4. Capture exit code
  5. `docker compose --profile test-integration down -v` (always, even on failure)
  6. Exit with captured code
- Makefile target: `test-integration: ## Run integration tests with Docker services`
- Add `test-integration-up` and `test-integration-down` for manual control
- Script should print clear status messages for each phase
- Handle the case where Docker isn't running (exit with helpful message)
- Support `NO_TEARDOWN=true` env var to keep services running for debugging

**Definition of Done:**

- [ ] `make test-integration` starts Docker services, runs tests, tears down
- [ ] `make test-integration-up` starts only the Docker services
- [ ] `make test-integration-down` tears down the Docker services
- [ ] Script exits with non-zero code if any tests fail
- [ ] Script cleans up Docker services even on test failure
- [ ] `NO_TEARDOWN=true make test-integration` leaves services running after tests

**Verify:**

- `make test-integration`

### Task 11: Gap analysis and findings document

**Objective:** Run all integration tests, document what works and what doesn't, and create a findings document that catalogs gaps for future plans (including IOTA stub implementation).

**Dependencies:** Task 6, Task 7, Task 8, Task 9, Task 10

**Files:**

- Create: `docs/audit/2026-02-21-integration-audit-findings.md`

**Key Decisions / Notes:**

- Run the full integration test suite and capture results
- For each integration area, document:
  - **Status**: Working / Partially Working / Broken / Not Implemented
  - **Evidence**: Test names that pass/fail
  - **Gaps**: What's missing or broken
  - **Priority**: P0 (blocking) / P1 (important) / P2 (nice-to-have)
- Areas to assess:
  1. Setup Wizard flow (welcome → complete)
  2. PostgreSQL connection (Docker + External modes)
  3. Redis connection (Docker + External modes)
  4. IPFS connection and storage
  5. IOTA node connection and payment stubs
  6. Email (SMTP via Mailpit)
  7. S3 storage backend
  8. ATProto publishing
  9. ActivityPub federation (inbox, actor, WebFinger)
  10. Nginx configuration generation
- IOTA gaps to document: BuildTransaction, SignTransaction, SubmitTransaction all return `ErrNotImplemented`. Custom platform token support doesn't exist. These are Phase 2 items.
- ATProto gaps: No real PDS testing, no firehose/labeler integration. Document.
- ActivityPub gaps: HTTP signature signing exists but no E2E delivery test. Document.
- **Coverage analysis**: Run `go test -coverprofile=coverage.out ./internal/setup/... && go tool cover -func=coverage.out` to identify untested code paths in the setup package. Include coverage data (% per function) in the findings document. This directly supports the user's goal of knowing what is tested vs not.
- **Phase 2 skeleton**: Create `docs/plans/2026-02-21-codebase-audit-phase2-skeleton.md` with placeholder tasks for deferred items (IOTA stubs, custom token, full ActivityPub E2E, ATProto firehose). This ensures continuity between audit phases.

**Definition of Done:**

- [ ] Findings document exists at `docs/audit/2026-02-21-integration-audit-findings.md`
- [ ] Each of the 10 integration areas has a status, evidence, gaps, and priority
- [ ] IOTA payment stubs documented as Phase 2 items with specific requirements
- [ ] Custom platform token gap documented
- [ ] ATProto and ActivityPub gaps documented with specific missing functionality
- [ ] Document includes a "Recommended Next Plans" section for Phase 2+
- [ ] Setup package coverage analysis included (% per function)
- [ ] Phase 2 skeleton plan exists at `docs/plans/2026-02-21-codebase-audit-phase2-skeleton.md`

**Verify:**

- `cat docs/audit/2026-02-21-integration-audit-findings.md | head -20`

## Testing Strategy

- **Unit tests**: Mock servers (Tasks 2-3) have their own build verification
- **Integration tests**: All tests in `tests/integration/` run against live Docker services
- **E2E flow**: Setup wizard driven from welcome to complete via HTTP requests
- **Service connections**: Each external service tested for success and failure
- **Orchestration**: `make test-integration` runs everything automatically

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| SSRF validator blocks test connections to Docker containers on private IPs | High | High | Task 1 refactors the Wizard to accept an injectable URLValidator. Integration tests use `security.NewURLValidatorAllowPrivate()` to bypass private IP restrictions. Production code uses the default secure validator. |
| IOTA node takes too long to start in Docker (120s+ start period) | Med | Med | Use `t.Skip("IOTA node not ready")` with a health check pre-test. Document as known slow dependency. |
| MinIO port conflicts with existing services | Low | Low | Use high port numbers (19100-19300) to avoid all conflicts. |
| Mock ATProto/ActivityPub servers don't match real protocol behavior | Med | Med | Focus on the specific endpoints `atproto_service.go` and `activitypub/` actually call. Add TODO comments for additional endpoints needed later. |
| Docker Compose profile conflicts between test and test-integration | Low | Med | Use separate network (`test-integration-network`) and unique port ranges (19xxx). |
| Integration tests are flaky due to service startup timing | Med | Med | Use health check polling with retries in the orchestration script. Add test helpers that wait for service readiness. |

## Open Questions

- None — requirements are clear from user clarification.

### Deferred Ideas

- **IOTA payment stub implementation** (BuildTransaction, SignTransaction, SubmitTransaction) — Phase 2 plan
- **Custom platform token support** (website-specific token alongside IOTA) — Phase 2 plan
- **Full ActivityPub federation E2E** with real Mastodon instance — Phase 3
- **ATProto firehose and labeler integration testing** — Phase 3
- **Load testing with Docker stack** — Future plan
- **CI/CD pipeline integration** for `make test-integration` — Future plan
