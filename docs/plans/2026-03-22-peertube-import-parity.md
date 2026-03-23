# PeerTube Import Parity Implementation Plan

Created: 2026-03-22
Updated: 2026-03-23 (comprehensive audit pass)
Status: VERIFIED WITH FOLLOW-UP
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Close the API/runtime parity gaps and produce an accurate gap report that distinguishes validated PeerTube-shaped runtime behavior from the still-missing PeerTube ETL/importer.

## 2026-03-23 Comprehensive Audit Results

A full audit was performed comparing Athena against PeerTube v8.1.0's REST API surface. See `docs/reports/peertube-parity-gap-report.md` for the complete findings. Key metrics:

- **API parity**: ~85% of ~270+ PeerTube endpoints (33 categories COMPLETE, 5 PARTIAL, 15 MISSING — most missing are low-impact admin/moderation)
- **Tests**: 4,687 functions, 77/77 packages pass, build clean
- **Newman**: 19/29 collections in CI, all pass (341/468 requests automated)
- **OpenAPI**: 20 specs (22,582 lines), but ~80-90 endpoints undocumented (~30-35%)
- **Extra features**: 8 Athena-only features fully implemented (~1,000 tests)

### Remaining Work (prioritized)

**P0 — Client Compatibility**: Static file serving aliases (`/static/web-videos/`, `/static/streaming-playlists/hls/`), PeerTube URL aliases for playlists, uploads, captions, notifications, blocklists.

**P1 — Newman CI**: Add 7 existing collections to CI runner, create 4 new collections (captions, 2FA, chat, redundancy), add E2E workflow chains.

**P2 — OpenAPI**: Create 4 new spec files (notifications, messaging, runners, backup), extend existing specs for ~80 undocumented endpoints, fix quality issues.

**P3 — Missing Features**: Watched words + automatic tags (12 endpoints), user data import/export (7), video passwords (4), source replacement (3), file management (5), comment approval, storyboards, embed privacy, channel sync, player settings, server debug/logs, bulk ops, client config, overviews (~48 endpoints total).

**P4 — Migration Tooling**: PeerTube dump import pipeline, fixture-based migration E2E, upstream compatibility harness.

**P5 — Code Quality**: Remove unused `extractPlugin`, fix silent IPFS pin failures, complete plugin `InstallFromURL`, fix `PublishVideoBatch` empty refs.
**Architecture:** Add real-artifact unit/integration tests for plugin and runner lifecycles, a federation import E2E scenario, wire the ATProto PDS mock into the Docker test profile, and promote the selected Newman collections to live validation.
**Tech Stack:** Go 1.24 (testify, httptest, archive/zip), Docker Compose, Postman/Newman, OpenAPI YAML

## Scope

### In Scope

1. Plugin archive install pipeline unit tests with real in-memory ZIP archives
2. Full runner lifecycle handler integration test (single coherent register→job→upload→success chain)
3. Federation import E2E test proving discovery, follow, and timeline flows
4. ATProto PDS mock wired into Docker `test` profile for live Newman assertions
5. Promote the selected Newman collections from "API contract" to "live validated"
6. Gap report rewrite: reflect the green runtime baseline while explicitly calling out any remaining real migration gaps
7. Documentation accuracy pass (test counts, Newman status)

### Out of Scope

- Real yt-dlp video download E2E (requires external video hosting infrastructure)
- Real plugin binary execution (requires sandboxing infrastructure)
- Real FFmpeg encoding via remote runner agent (requires runner agent binary)
- ATProto PDS mock extensions beyond existing XRPC endpoints
- Real PeerTube dump-plus-media ETL/import tooling

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Runner handler tests: `internal/httpapi/handlers/runner/handlers_test.go` — uses `stubRunnerRepo` and `stubEncodingRepo` for in-process mocking
  - Plugin handler tests: `internal/httpapi/handlers/plugin/plugin_lifecycle_test.go` — uses `newSQLMockPluginHandler` with sqlmock
  - E2E tests: `tests/e2e/scenarios/import_lifecycle_test.go` — uses `httptest.Server`, `e2e.TestClient`, and `e2e.WaitForService`
  - Response envelope: `shared.WriteJSON` wraps all responses in `{success, data, error, meta}`

- **Conventions:**
  - Test names: `TestSubject_Scenario` (e.g., `TestPluginArchive_ValidZIP`)
  - Domain errors: sentinel errors from `internal/domain/errors.go`
  - Handler auth: `middleware.GetUserIDFromContext()` for user auth, `X-Runner-Token` header for runners
  - Docker profiles: `test` for Newman/E2E, `test-integration` for Go integration tests

- **Key files:**
  - `internal/httpapi/handlers/plugin/archive_install.go` — `extractPluginManifest()`, `extractPluginArchive()`, `installPluginArchive()` functions
  - `internal/httpapi/handlers/runner/handlers.go` — Full runner lifecycle handlers including `UploadJobFile`
  - `internal/httpapi/handlers/federation/server_following.go` — Server follow/unfollow handlers
  - `internal/httpapi/routes.go:157-165` — WebFinger, NodeInfo, HostMeta federation discovery routes
  - `tests/mocks/atproto-pds/main.go` — Mock ATProto PDS server (exists but only in test-integration profile)
  - `docker-compose.yml:388-433` — `app-test` service configuration
  - `docs/reports/peertube-parity-gap-report.md` — The gap report to update

- **Gotchas:**
  - `installPluginArchive()` takes concrete `*repository.PluginRepository` and `*coreplugin.Manager`, not interfaces — testing it requires either sqlmock or testing the helper functions directly
  - Plugin URL install handler enforces HTTPS prefix (SSRF protection) — httptest servers are HTTP only, so test via multipart upload path or test helpers directly
  - Docker `test` profile uses `test-network`, `test-integration` uses `test-integration-network` — new test-profile services must join `test-network`
  - Runner auth via `X-Runner-Token` header or `runnerToken` JSON body field
  - `stubRunnerRepo.GetRunnerByToken` returns a copy to prevent `authenticateRunner`'s `runner.Token = ""` from breaking subsequent calls

- **Domain context:**
  - "Import PeerTube instance" means Athena can discover and follow remote ActivityPub instances, resolve their actors, and surface their content in a federation timeline
  - Plugin install pipeline: download/receive ZIP → extract `plugin.json` manifest → validate permissions → extract files to plugin directory → create DB record
  - Runner lifecycle: admin creates registration token → runner registers with token → runner requests job → accepts → updates progress → uploads result file → marks success

## Assumptions

- ATProto PDS mock at `tests/mocks/atproto-pds/` is complete enough for the ATProto Newman collection — supported by existing `test-integration` profile usage — Tasks 4, 5 depend on this
- `extractPluginManifest()` is a pure function (takes `[]byte`, returns struct). `extractPluginArchive()` does real filesystem I/O (writes to `destDir`) — tests must use `t.TempDir()` — supported by reading `archive_install.go:197-236` — Task 1 depends on this
- The existing `stubRunnerRepo` and `stubEncodingRepo` in runner handler tests are sufficient for a full lifecycle test — supported by existing test patterns — Task 2 depends on this
- Federation handlers (WebFinger, NodeInfo, server following) work correctly in the test profile since `ENABLE_ACTIVITYPUB=true` and `ACTIVITYPUB_DOMAIN=app-test` are set — supported by `docker-compose.yml:408-409` — Task 3 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
| --- | --- | --- | --- |
| Plugin install test needs concrete PluginRepository | Medium | Medium | Test `extractPluginManifest` and `extractPluginArchive` as pure functions; skip `installPluginArchive` integration in this plan since it requires full DB setup |
| ATProto mock may not respond on correct ports in test profile | Low | Medium | Use same build context and health check pattern as test-integration profile; verify with docker-compose config |
| Federation E2E test may fail if ActivityPub service doesn't fully initialize in test mode | Medium | High | Use `WaitForService` pattern and `t.Skip` if ActivityPub is not available; validate WebFinger endpoint first before testing follow flows |
| Newman collection promotion may reveal latent failures | Medium | Medium | Fix any failures discovered during promotion rather than ignoring them |

## Goal Verification

### Truths

1. All `extractPluginManifest` and `extractPluginArchive` unit tests pass with real in-memory ZIP archives (valid, invalid, path traversal)
2. A single Go test proves the complete runner lifecycle: register → request → accept → update → upload → success → completed state
3. A Go E2E test proves federation discovery (WebFinger + NodeInfo) and server follow/unfollow lifecycle
4. Docker test profile includes ATProto PDS mock and `app-test` connects to it
5. All selected Newman collections pass as "validated against live server" (currently 19 in `postman/run-all-tests.sh`)
6. Gap report accurately states that API/runtime parity confidence is high, while full PeerTube ETL/import confidence remains lower because the importer itself is not shipped
7. Gap report recommended next steps are limited to the remaining migration-specific gaps

### Artifacts

1. `internal/httpapi/handlers/plugin/archive_install_test.go` — new file with archive pipeline tests
2. `internal/httpapi/handlers/runner/handlers_test.go` — extended with full lifecycle test
3. `tests/e2e/scenarios/federation_import_test.go` — new file with federation E2E test
4. `docker-compose.yml` — updated with ATProto PDS in test profile
5. `postman/athena-*.postman_collection.json` — updated collections for live validation
6. `docs/reports/peertube-parity-gap-report.md` — rewritten confidence statement and next steps

## Progress Tracking

- [x] Task 1: Plugin archive pipeline unit tests
- [x] Task 2: Runner full lifecycle integration test
- [x] Task 3: Federation import E2E test
- [x] Task 4: ATProto PDS mock in test profile
- [x] Task 5: Promote Newman collections to live validation
- [x] Task 6: Gap report & documentation update

**Total Tasks:** 6 | **Completed:** 6 | **Remaining:** 0

## Implementation Tasks

### Task 1: Plugin archive pipeline unit tests

**Objective:** Test `extractPluginManifest()` and `extractPluginArchive()` with real in-memory ZIP archives to prove the plugin install pipeline works with actual artifact data.
**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/plugin/archive_install_test.go`

**Key Decisions / Notes:**

- Build real ZIP archives in-memory using `archive/zip` + `bytes.Buffer`
- Test cases: valid ZIP with `plugin.json`, missing manifest, invalid JSON, missing required fields (name/version/author), path traversal attack
- `extractPluginManifest` is a pure function (takes `[]byte`, returns struct — no mocking needed). `extractPluginArchive` writes to the real filesystem — use `t.TempDir()` as `destDir` in tests (auto-cleaned up)
- For `extractPluginArchive`, verify files are extracted to correct locations and path traversal is blocked

**Definition of Done:**

- [ ] All tests pass
- [ ] Tests cover: valid ZIP, missing plugin.json, invalid JSON, missing required fields, path traversal protection
- [ ] `go test -short ./internal/httpapi/handlers/plugin/... -run TestArchive` passes with 0 failures

**Verify:**

- `go test -short ./internal/httpapi/handlers/plugin/... -run TestArchive -v`

---

### Task 2: Runner full lifecycle integration test

**Objective:** Add a single coherent test that proves the complete runner lifecycle: create registration token → register runner → request job → accept job → update progress → upload file → mark success → verify completed state.
**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/runner/handlers_test.go`

**Key Decisions / Notes:**

- Use existing `stubRunnerRepo` and `stubEncodingRepo` infrastructure
- Single test function `TestHandlers_FullLifecycle_RegisterToCompletion` that chains all steps
- Each step calls the handler directly via httptest and verifies the response
- Upload step sends real binary data (not just a string) to prove the upload path handles actual content
- Final verification: GET the job assignment and confirm state is `completed`, progress is 100

**Definition of Done:**

- [ ] All tests pass
- [ ] Single test proves the full register → request → accept → update → upload → success chain
- [ ] `go test -short ./internal/httpapi/handlers/runner/... -run TestHandlers_FullLifecycle` passes

**Verify:**

- `go test -short ./internal/httpapi/handlers/runner/... -run TestHandlers_FullLifecycle -v`

---

### Task 3: Federation import E2E test

**Objective:** Create a Go E2E test that proves federation discovery and server follow/unfollow lifecycle — the "import PeerTube instance" user story.
**Dependencies:** None

**Files:**

- Create: `tests/e2e/scenarios/federation_import_test.go`

**Key Decisions / Notes:**

- Skip if API service is not available (`e2e.WaitForService` pattern)
- Test steps:
  1. Verify WebFinger endpoint responds correctly for local actors
  2. Verify NodeInfo 2.0 endpoint responds with valid metadata
  3. Register a user, then use server follow endpoints to follow/list/unfollow
  4. Verify federation timeline returns content
- Uses `e2e.TestClient` for HTTP calls (same pattern as `import_lifecycle_test.go`)
- Mock remote instance is NOT needed for this test — we're testing Athena's own federation endpoints

**Definition of Done:**

- [ ] All tests pass
- [ ] WebFinger, NodeInfo 2.0, server follow/unfollow, and federation timeline are verified
- [ ] `go test ./tests/e2e/scenarios/... -run TestFederation` passes (requires Docker test profile running) or skips gracefully when no server is available

**Verify:**

- `go test ./tests/e2e/scenarios/... -run TestFederation -v` (full validation requires Docker test profile: `docker compose --profile test up -d` first; test skips gracefully when server is not available)

---

### Task 4: ATProto PDS mock in Docker test profile

**Objective:** Wire the existing ATProto PDS mock server into the Docker `test` profile so the `athena-atproto` Newman collection can make live assertions instead of contract-shape checks.
**Dependencies:** None

**Files:**

- Modify: `docker-compose.yml` — add `mock-atproto-pds-test` service under `test` profile, add `ATPROTO_PDS_URL` env var to `app-test`

**Key Decisions / Notes:**

- Create a new `mock-atproto-pds-test` service with `profiles: ["test"]` that reuses `tests/mocks/atproto-pds` build context
- The ATProto PDS mock listens on port 8080 (see `tests/mocks/atproto-pds/main.go:392`). Copy the healthcheck block from the existing `mock-atproto-pds` service at `docker-compose.yml:563-568` verbatim, changing the network to `test-network`
- Join `test-network` (not `test-integration-network`)
- Add `ATPROTO_PDS_URL: http://mock-atproto-pds-test:8080` to `app-test` environment
- Add `mock-atproto-pds-test` to `app-test` depends_on with `condition: service_healthy`

**Definition of Done:**

- [ ] `docker compose --profile test config` shows the new ATProto PDS mock service
- [ ] `app-test` environment includes `ATPROTO_PDS_URL`
- [ ] No YAML validation errors

**Verify:**

- `docker compose --profile test config --quiet`

---

### Task 5: Promote Newman collections to live validation

**Objective:** Update the 5 "API contract validated" Newman collections (runners, plugins, payments, import-lifecycle, atproto) so they run live assertions against the Docker test server instead of contract-shape-only checks.
**Dependencies:** Task 4 (ATProto PDS mock must be in test profile)

**Files:**

- Modify: `postman/athena-runners.postman_collection.json`
- Modify: `postman/athena-plugins.postman_collection.json`
- Modify: `postman/athena-payments.postman_collection.json`
- Modify: `postman/athena-import-lifecycle.postman_collection.json`
- Modify: `postman/athena-atproto.postman_collection.json`

**Key Decisions / Notes:**

- Each collection needs its test scripts updated to assert specific response values (not just status codes and shape)
- Runners: create registration token → register runner → verify token returned → list runners → verify count
- Plugins: list plugins → verify empty array → check settings → verify 404 for nonexistent
- Payments: wallet operations → verify wallet ID returned → transaction history → verify empty
- Import lifecycle: create import → verify ID in response → get import → list imports → cancel
- ATProto: resolve handle → verify DID returned → create record → verify URI format
- All collections must pass against the live Docker test server (not just contract validation)

**Definition of Done:**

- [ ] All 5 collections updated with live assertions
- [ ] All selected collections pass when run against Docker test profile
- [ ] Gap report can state the current live-validated collection count accurately

**Verify:**

- Run all collections: `for f in postman/athena-*.postman_collection.json; do echo "--- $f ---"; newman run "$f" -e postman/athena.local.postman_environment.json || exit 1; done`
- Alternatively: `./postman/run-all-tests.sh postman/athena.local.postman_environment.json`

---

### Task 6: Gap report & documentation update

**Objective:** Rewrite the gap report to reflect the green runtime baseline, preserve the remaining ETL/import gap, and update documentation with accurate test counts.
**Dependencies:** Tasks 1-5

**Files:**

- Modify: `docs/reports/peertube-parity-gap-report.md`
- Modify: `.claude/rules/project.md`
- Modify: `postman/README.md`

**Key Decisions / Notes:**

- Gap report "What is not true yet" section: keep only the genuine remaining migration/tooling gaps
- Gap report "Recommended Next Steps" section: focus only on the remaining migration-specific work
- Gap report "Confidence Statement" section: distinguish runtime parity from ETL/import readiness
- Gap report Newman coverage table: update the live-validated collection count accurately
- Update `.claude/rules/project.md` test counts to reflect new test files
- Update `postman/README.md` to reflect the current live-validated collection set accurately

**Definition of Done:**

- [ ] Gap report clearly distinguishes runtime parity from missing migration tooling
- [ ] Gap report recommended next steps cover only the remaining migration work
- [ ] Newman coverage shows the correct live-validated collection count
- [ ] Project docs reflect accurate test counts
- [ ] `postman/README.md` reflects accurate collection status

**Verify:**

- `rg -n "ETL|importer|migration" docs/reports/peertube-parity-gap-report.md` shows the remaining migration/tooling gap explicitly

---

## Autonomous Decisions

- **Testing approach for plugin archive:** Test pure functions (`extractPluginManifest`, `extractPluginArchive`) directly rather than through HTTP handler. The handler-level test would require full DB setup via sqlmock which is overly complex for proving "real artifact" coverage. The pure function tests prove the archive pipeline works with real ZIP data.
- **Federation E2E scope:** Test Athena's own federation endpoints (WebFinger, NodeInfo, server follow) rather than simulating a full remote instance import. A full import scenario would require a running remote PeerTube instance, which is out of scope. The federation endpoint coverage proves the discovery and follow mechanisms work.
- **Runner lifecycle as handler test, not E2E:** Use httptest handler-level testing (like existing patterns) rather than requiring the Docker stack. This is more reliable and faster while still proving the full stateful chain.
- **ATProto PDS mock:** Create a separate docker-compose service for the test profile rather than adding the test profile to the existing test-integration service. This avoids cross-profile network conflicts.
