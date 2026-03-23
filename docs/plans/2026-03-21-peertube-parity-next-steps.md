# PeerTube Parity Next Steps Implementation Plan

Created: 2026-03-21
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Close remaining PeerTube parity gaps: runner/plugin/import success-path lifecycle coverage, Vidra Core-only extension E2E validation (payments, IPFS, ATProto), a simulated import lifecycle E2E scenario, and keep OpenAPI specs + Postman E2E tests in lockstep with all Go changes.

**Architecture:** Additive test and specification layer on existing handlers/services. No new endpoints or schema changes needed — all handler code already exists. New work is: Go unit tests, OpenAPI spec entries, dedicated Postman collections, and documentation updates.

**Tech Stack:** Go 1.24, Chi router, testify, OpenAPI 3.0 YAML, Postman/Newman JSON collections

## Scope

### In Scope

**Workstream A — Runner Success-Path Lifecycle:**
1. Go unit tests for full runner job lifecycle (register → request job → accept → update → success/error → upload files)
2. Dedicated `vidra-runners.postman_collection.json` with success-path E2E
3. Verify runner OpenAPI spec completeness

**Workstream B — Plugin Success-Path Lifecycle:**
4. Go unit tests for plugin install → get → settings → update → uninstall roundtrip
5. Dedicated `vidra-plugins.postman_collection.json` with success-path E2E

**Workstream C — Import Positive-Path Lifecycle:**
6. Audit existing import handler tests (24 test functions exist — identify gaps only) and fill any missing positive-path coverage
7. Create `vidra-import-lifecycle.postman_collection.json` with success-path requests

**Workstream D — Vidra Core-Only Extension Validation:**
8. `vidra-payments.postman_collection.json` for crypto payments E2E
9. IPFS flow Postman requests + Go test coverage
10. ATProto interop Postman requests + Go test verification

**Workstream E — Import Lifecycle E2E Scenario:**
11. Simulated import lifecycle E2E (Go integration test + Postman collection)

**Workstream F — OpenAPI + Documentation Lockstep:**
12. OpenAPI audit for any remaining gaps + documentation accuracy pass

### Out of Scope

- New endpoint implementations (all handlers already exist)
- Database schema or migration changes
- Real PeerTube instance federation tests (mock only)
- Frontend changes
- External runner infrastructure (Vidra Core uses in-process FFmpeg)
- Full imported-instance behavior verification (deferred — requires real federation)

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - Handler tests: table-driven with `httptest.NewRecorder()` + chi.RouteContext for URL params. See `internal/httpapi/handlers/payments/payment_handlers_test.go` for well-structured example (16 test functions)
  - Runner auth: `runnerTokenFromRequest(r)` reads from `X-Runner-Token` header or JSON body `runnerToken`/`token` field
  - Plugin handlers: `internal/httpapi/handlers/plugin/plugin_handlers.go` — `NewPluginHandler` constructor takes `pluginRepo`, `pluginManager`, and optional `storageBackend`
  - Import service: `internal/usecase/import/service.go` — `Service` interface with `ImportVideo`, `CancelImport`, `RetryImport`, `ListUserImports`
  - Response envelope: `shared.WriteJSON(w, status, data)` wraps in `{success, data, error, meta}`
  - Postman collections: use `{{baseUrl}}` and `{{authToken}}` env vars. Follow `postman/vidra-auth.postman_collection.json` structure

- **Conventions:**
  - Error wrapping: `fmt.Errorf("context: %w", err)` with `domain.ErrXxx` sentinels
  - Mock interfaces inline in `_test.go` files using testify/mock
  - Postman test scripts: `pm.test("Status code is 200", () => { pm.response.to.have.status(200); });`
  - OpenAPI specs: one YAML per domain in `api/openapi_*.yaml`

- **Key files:**
  - `internal/httpapi/handlers/runner/handlers.go` — 17 runner endpoints (547 lines)
  - `internal/httpapi/handlers/runner/handlers_test.go` — only 2 tests (SEVERELY under-tested)
  - `internal/httpapi/handlers/plugin/plugin_handlers.go` — plugin CRUD + settings + install
  - `internal/httpapi/handlers/video/import_handlers.go` — import lifecycle handlers
  - `internal/usecase/import/service.go` — import business logic
  - `internal/httpapi/handlers/payments/payment_handlers.go` — IOTA payment handlers
  - `internal/usecase/atproto_service.go` — ATProto publish logic
  - `internal/usecase/atproto_features.go` — PublishComment, PublishVideoBatch
  - `internal/httpapi/routes.go` — route registration (lines 420-474 for runners)
  - `api/openapi.yaml` — main spec (runners at lines ~4405-5103)
  - `api/openapi_plugins.yaml` — plugin spec
  - `api/openapi_payments.yaml` — payments spec
  - `api/openapi_imports.yaml` — imports spec

- **Gotchas:**
  - Runner endpoints require `X-Runner-Token` header or JSON body `runnerToken` field — NOT standard JWT auth
  - Runner routes are conditionally registered based on `deps.RunnerRepo != nil` (routes.go:421)
  - Plugin install downloads from URL — must mock HTTP client in tests
  - Import service runs processing in goroutine (`go s.processImport`) — tests must handle async
  - ATProto is behind `cfg.EnableATProto` flag — needs explicit enabling in tests
  - Some runner handlers use `loadRunnerAssignment` which authenticates AND loads job assignment in one call

- **Domain context:**
  - Runner lifecycle: registration token → register runner → request job → accept → update progress → upload files → success/error
  - Plugin lifecycle: install from URL/archive → get details → configure settings → update → uninstall
  - Import lifecycle: create import → pending → processing → completed/failed → cancel (if pending) / retry (if failed)
  - IOTA payments: create wallet → create payment intent → check balance → confirm transaction
  - ATProto: authenticate session → create post record → verify post via getRecord

## Runtime Environment

- **Start command:** `make run` or `go run cmd/server/main.go`
- **Port:** 8080 (default)
- **Health check:** `GET /health`
- **Restart:** Kill and re-run (no hot reload)

## Assumptions

- All handler code is complete and functional — this plan only adds test coverage and documentation, not new runtime code — supported by gap report stating "routes exist and work" — All tasks depend on this
- Runner repository mock interface matches the `runnerRepository` interface in `handlers.go:21-39` — supported by existing test file structure — Tasks 1-2 depend on this
- Plugin manager interface supports `Install()`, `Uninstall()`, `GetPlugin()` methods — supported by `plugin/manager.go` — Tasks 3-4 depend on this
- Import service mock `MockImportService` already exists in `test_helpers.go:57` — supported by file read — Tasks 5-6 depend on this
- Payment handler mocks use `port.PaymentService` interface — supported by `payment_handlers_test.go` — Task 7 depends on this
- ATProto service testability requires mocking HTTP client for PDS API calls — supported by `atproto_service.go:92` using `http.Client` — Task 8 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Runner handler tests may reveal hidden bugs in handler logic | Medium | Low | Fix any discovered bugs inline; they're pre-existing, not introduced |
| Postman success-path tests require running Docker stack for validation | Medium | Medium | Design Postman tests to be runnable standalone; validate via `newman run` against live server only in CI |
| ATProto tests may require PDS mock service | Low | Medium | Mock at HTTP client level in Go tests; Postman tests require ATProto PDS mock in docker-compose |
| OpenAPI spec changes may invalidate generated types | Medium | Low | Run `make verify-openapi` after each spec change; only add paths, don't modify existing schemas |

## Goal Verification

### Truths

1. Runner handler package has ≥15 test functions covering the full job lifecycle (register → request → accept → update → success → upload)
2. Plugin handler package has success-path test coverage for install → get → settings → update → uninstall
3. Import handler package has positive-path test coverage for create → cancel and create → retry with actual data flow
4. Dedicated Postman collections exist for runners, plugins, and payments with success-path requests
5. ATProto and IPFS flows have Go test coverage and Postman E2E requests
6. A simulated import lifecycle E2E test verifies the complete create → process → complete flow
7. All new Postman collections pass in the Docker Newman suite
8. OpenAPI specs cover all runner/plugin/payment/ATProto endpoints
9. Gap report and documentation updated to reflect new coverage

### Artifacts

- `internal/httpapi/handlers/runner/handlers_test.go` — expanded from 2 to 15+ test functions
- `internal/httpapi/handlers/plugin/plugin_lifecycle_test.go` — new success-path tests
- `internal/httpapi/handlers/video/import_handlers_test.go` — extended with any missing gap-fill tests
- `postman/vidra-runners.postman_collection.json` — new
- `postman/vidra-plugins.postman_collection.json` — new
- `postman/vidra-payments.postman_collection.json` — new
- `postman/vidra-import-lifecycle.postman_collection.json` — new
- `tests/e2e/scenarios/import_lifecycle_test.go` — new simulated E2E
- `api/openapi.yaml` or domain specs — updated with any missing paths
- `docs/reports/peertube-parity-gap-report.md` — updated

## Progress Tracking

- [x] Task 1: Runner success-path Go tests
- [x] Task 2: Runner Postman E2E collection + OpenAPI verification
- [x] Task 3: Plugin success-path Go tests
- [x] Task 4: Plugin Postman E2E collection
- [x] Task 5: Import positive-path Go tests
- [x] Task 6: Import lifecycle Postman E2E collection
- [x] Task 7: Payments Postman E2E collection
- [x] Task 8: IPFS + ATProto Go tests and Postman E2E
- [x] Task 9: Simulated import lifecycle E2E scenario
- [x] Task 10: OpenAPI audit + lockstep verification
- [x] Task 11: Documentation accuracy pass

**Total Tasks:** 11 | **Completed:** 11 | **Remaining:** 0

## Implementation Tasks

### Task 1: Runner Success-Path Go Tests

**Objective:** Expand runner handler tests from 2 to 15+ test functions covering the complete job lifecycle.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/runner/handlers_test.go`

**Key Decisions / Notes:**

- Current state: only `TestHandlers_RegisterRunner` and `TestHandlers_RequestJob` exist
- Need table-driven tests for each handler: `ListRunners`, `ListRegistrationTokens`, `CreateRegistrationToken`, `DeleteRegistrationToken`, `RegisterRunner`, `UnregisterRunner`, `DeleteRunner`, `ListJobs`, `CancelJob`, `DeleteJob`, `RequestJob`, `AcceptJob`, `AbortJob`, `UpdateJob`, `ErrorJob`, `SuccessJob`, `UploadJobFile`
- Mock the `runnerRepository` interface (defined at `handlers.go:21-39`) and `port.EncodingRepository`
- Test auth flow: runner token extraction from header and body (`runnerTokenFromRequest` at `handlers.go:519`)
- Test lifecycle state transitions: pending → accepted → running → completed/failed
- Follow mock pattern from `internal/httpapi/handlers/payments/payment_handlers_test.go`

**Definition of Done:**

- [ ] ≥15 test functions in handlers_test.go
- [ ] Full lifecycle covered: register → request job → accept → update → success path
- [ ] Error/abort paths covered: error report, abort, cancel
- [ ] File upload path covered
- [ ] Auth failure paths tested (missing/invalid token)
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/runner/... -short -count=1 -v`

---

### Task 2: Runner Postman E2E Collection + OpenAPI Verification

**Objective:** Create dedicated `vidra-runners.postman_collection.json` with success-path lifecycle tests and verify runner OpenAPI spec completeness.

**Dependencies:** Task 1

**Files:**

- Create: `postman/vidra-runners.postman_collection.json`
- Possibly modify: `api/openapi.yaml` (if any runner paths are missing)
- Note: `postman/run-all-tests.sh` registration deferred to Task 11

**Key Decisions / Notes:**

- Collection structure: Setup (auth) → Registration Token → Runner Registration → Job Lifecycle → Cleanup
- Job lifecycle: request job → accept → update progress → upload file → success
- Error lifecycle: request job → accept → error report
- Use environment variables for `runnerToken`, `jobUUID`, etc.
- Runner OpenAPI paths are at `openapi.yaml:4405-5103` — verify all 17 endpoints are represented
- Tests assert on response structure, status codes, and state transitions

**Definition of Done:**

- [ ] `vidra-runners.postman_collection.json` exists with ≥10 requests
- [ ] Success-path lifecycle: register → request → accept → update → success
- [ ] Error path: register → request → accept → error
- [ ] Admin operations: list runners, list jobs, cancel job, delete job
- [ ] All runner endpoints present in OpenAPI spec
- [ ] All tests pass
- [ ] Note: `run-all-tests.sh` registration handled in Task 11

**Verify:**

- `newman run postman/vidra-runners.postman_collection.json -e postman/test-env.json --bail` (against running server)
- `make verify-openapi`

---

### Task 3: Plugin Success-Path Go Tests

**Objective:** Add success-path test coverage for the plugin install → get → settings → update → uninstall roundtrip.

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/plugin/plugin_lifecycle_test.go`

**Key Decisions / Notes:**

- Existing test files cover individual operations but not the success-path lifecycle
- Test the `InstallPluginFromURL` handler with a mock HTTP server returning a valid plugin archive
- Test `UpdatePluginFromURL` with an existing plugin record
- Test `GetPluginSettings` and `UpdatePluginSettings` roundtrip (write settings, read back, verify)
- Mock `PluginManager` interface and `PluginRepository` interface
- Plugin handler constructor: `NewPluginHandler(pluginRepo, pluginManager, storageBackend, isAdmin)`

**Definition of Done:**

- [ ] Success-path install test: URL → download → register → returns 200 with plugin details
- [ ] Success-path settings roundtrip: install → get settings → update settings → verify updated
- [ ] Success-path update: install → update from URL → verify version changed
- [ ] Success-path uninstall: install → uninstall → verify 204
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/plugin/... -short -count=1 -v`

---

### Task 4: Plugin Postman E2E Collection

**Objective:** Create dedicated `vidra-plugins.postman_collection.json` with success-path lifecycle E2E tests.

**Dependencies:** Task 3

**Files:**

- Create: `postman/vidra-plugins.postman_collection.json`
- Note: `postman/run-all-tests.sh` registration deferred to Task 11

**Key Decisions / Notes:**

- Collection structure: Setup (admin auth) → List Plugins → Install Plugin (contract test) → Get Plugin → Get Settings → Update Settings → Uninstall → List Available
- Use `{{baseUrl}}` and admin auth token
- **Plugin install fixture strategy:** Newman cannot spin up `httptest.NewServer`, so the install request will test the API contract shape only — send a request with a non-downloadable URL and verify the handler returns the correct error structure (400/422). The Go unit tests (Task 3) cover the actual success-path install with mocked downloads. The Postman collection proves the API contract, not the download.
- The `GET /api/v1/admin/plugins` list and `GET /api/v1/admin/plugins/available` success paths can be tested without a plugin archive
- If a pre-seeded plugin exists in the Docker test profile, the get/settings/update endpoints can also be success-path tested

**Definition of Done:**

- [ ] `vidra-plugins.postman_collection.json` exists with ≥8 requests
- [ ] List plugins and available plugins success-path tested
- [ ] Install API contract shape verified (error response for invalid URL)
- [ ] Get/settings/update tested against pre-seeded or listed plugin if available
- [ ] Error paths: install invalid URL, get missing plugin
- [ ] All tests pass
- [ ] Note: `run-all-tests.sh` registration handled in Task 11

**Verify:**

- `newman run postman/vidra-plugins.postman_collection.json -e postman/test-env.json --bail`

---

### Task 5: Import Positive-Path Go Test Gap-Fill

**Objective:** Audit existing import handler tests and fill any remaining positive-path coverage gaps. Existing file has 24 test functions including success paths — this task identifies and fills what's missing, not duplicates what exists.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/import_handlers_test.go` (extend, do not create new file)

**Key Decisions / Notes:**

- **Existing coverage (verified):** `import_handlers_test.go` already has 24 test functions including:
  - `TestImportHandlers_CreateImport_Success` (line 23)
  - `TestImportHandlers_GetImport_Success` (line 217)
  - `TestImportHandlers_ListImports_Success` (line 301), `_WithPagination` (line 350), `_WithStatusFilter` (line 388)
  - `TestImportHandlers_CancelImport_Success` (line 451)
  - `TestImportHandlers_CancelImportCanonical_Success` (line 557)
  - `TestImportHandlers_RetryImport_Success` (line 601)
- **Gap-fill focus:** Audit for missing scenarios:
  - Retry after cancel (cancel → retry → should fail since state is cancelled, not failed)
  - List imports empty result (zero imports)
  - Create import with torrent/magnet URL if handler supports it
  - Response shape assertions against OpenAPI spec (verify JSON structure matches `openapi_imports.yaml`)
- If all gaps are already covered, document the audit result and mark task complete

**Definition of Done:**

- [ ] Audit of existing import tests completed and documented
- [ ] Any missing positive-path scenarios added
- [ ] Response shapes verified against OpenAPI import spec
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -run TestImport -short -count=1 -v`

---

### Task 6: Import Lifecycle Postman E2E Collection

**Objective:** Create `vidra-import-lifecycle.postman_collection.json` with full positive-path import lifecycle tests.

**Dependencies:** Task 5

**Files:**

- Create: `postman/vidra-import-lifecycle.postman_collection.json`
- Note: `postman/run-all-tests.sh` registration deferred to Task 11

**Key Decisions / Notes:**

- Collection structure: Setup (auth) → Create Import → Get Import Status → List User Imports → Cancel Import → Retry Import → Cleanup
- Tests assert on: response envelope structure, import status transitions, pagination metadata
- Use environment variables for `importId`, `authToken`
- Test both URL import and torrent import if endpoints exist
- Important: actual video download won't complete in test environment — test the API contract, not the download

**Definition of Done:**

- [ ] `vidra-import-lifecycle.postman_collection.json` exists with ≥6 requests
- [ ] Create → Get → List → Cancel lifecycle
- [ ] Create → Get → Retry lifecycle (for failed imports)
- [ ] All tests pass
- [ ] Note: `run-all-tests.sh` registration handled in Task 11

**Verify:**

- `newman run postman/vidra-import-lifecycle.postman_collection.json -e postman/test-env.json --bail`

---

### Task 7: Payments Postman E2E Collection

**Objective:** Create `vidra-payments.postman_collection.json` for crypto payments E2E validation.

**Dependencies:** None

**Files:**

- Create: `postman/vidra-payments.postman_collection.json`
- Note: `postman/run-all-tests.sh` registration deferred to Task 11

**Key Decisions / Notes:**

- Payment routes: `POST /api/v1/payments/wallet` (create), `GET /api/v1/payments/wallet` (get), `POST /api/v1/payments/intent` (create intent), `GET /api/v1/payments/intent/{id}` (get intent), `GET /api/v1/payments/transactions` (history)
- Collection structure: Setup (auth) → Create Wallet → Get Wallet → Create Payment Intent → Get Payment Intent → Get Transaction History
- Error paths: duplicate wallet (409), wallet not found (404), invalid amount
- Note: IOTA node may not be available in test env — payment intent creation should succeed but balance check won't complete. Test the API contract.
- See `internal/httpapi/routes.go` for payment route registration
- Payment OpenAPI spec exists at `api/openapi_payments.yaml` — verify it's complete

**Definition of Done:**

- [ ] `vidra-payments.postman_collection.json` exists with ≥8 requests
- [ ] Wallet lifecycle: create → get → duplicate error
- [ ] Payment intent: create → get → status check
- [ ] Transaction history endpoint tested
- [ ] Error paths: unauthorized, invalid input, not found
- [ ] All tests pass
- [ ] Note: `run-all-tests.sh` registration handled in Task 11

**Verify:**

- `newman run postman/vidra-payments.postman_collection.json -e postman/test-env.json --bail`

---

### Task 8: IPFS + ATProto Go Tests and Postman E2E

**Objective:** Add Go test coverage and Postman E2E requests for IPFS-backed flows and ATProto interoperability.

**Dependencies:** None

**Files:**

- Use existing: `internal/usecase/atproto_features_test.go` (22 functions) and `internal/usecase/atproto_service_test.go` (6 functions) — pre-existing coverage for PublishVideo, PublishComment, PublishVideoBatch confirmed passing; no new file needed
- Create: `postman/vidra-atproto.postman_collection.json`
- Note: `postman/run-all-tests.sh` registration deferred to Task 11

**Key Decisions / Notes:**

- **IPFS:** Avatar upload with IPFS is already tested in auth handler tests. What's missing is Postman E2E proof. Add IPFS-backed avatar upload request to existing `vidra-auth` or `vidra-social` collection (or note in docs that IPFS-specific Postman tests require IPFS node in docker-compose)
- **ATProto:** `PublishVideo`, `PublishComment`, `PublishVideoBatch` in `internal/usecase/atproto_features.go` need test coverage. Mock the HTTP client for PDS API calls. Verify post creation, comment threading, and batch error handling.
- **ATProto social routes verified:** `/api/v1/social/*` is registered via `SocialHandler.RegisterRoutes()` at `social.go:26-50`. 17 endpoints exist: `GET /social/actors/{handle}`, `GET /social/actors/{handle}/stats`, `GET /social/followers/{handle}`, `GET /social/following/{handle}`, `GET /social/likes/{uri}`, `GET /social/comments/{uri}`, `GET /social/comments/{uri}/thread`, `GET /social/moderation/labels/{did}`, `POST /social/follow`, `DELETE /social/follow/{handle}`, `POST /social/like`, `DELETE /social/like`, `POST /social/comment`, `DELETE /social/comment/{uri}`, `POST /social/moderation/label`, `DELETE /social/moderation/label/{id}`, `POST /social/ingest/{handle}`. Postman collection can test all public GET endpoints and authenticated POST/DELETE endpoints.
- Check existing ATProto test coverage first — tests may already exist in `internal/usecase/`

**Definition of Done:**

- [ ] ATProto PublishVideo Go test: mock PDS, verify post created with correct record
- [ ] ATProto PublishComment Go test: mock PDS, verify threaded reply structure
- [ ] ATProto PublishVideoBatch Go test: verify partial failure handling
- [ ] `vidra-atproto.postman_collection.json` with social/ATProto API requests (≥10 requests covering verified social endpoints)
- [ ] IPFS flow documented: which Postman tests exercise IPFS and what config is needed
- [ ] All tests pass
- [ ] Note: `run-all-tests.sh` registration handled in Task 11

**Verify:**

- `go test ./internal/usecase/... -run TestAtproto -short -count=1 -v`
- `newman run postman/vidra-atproto.postman_collection.json -e postman/test-env.json --bail`

---

### Task 9: Simulated Import Lifecycle E2E Scenario

**Objective:** Create a comprehensive simulated import E2E test that verifies the complete import lifecycle: create → pending → processing → complete, with cancel and retry paths.

**Dependencies:** Tasks 5-6

**Files:**

- Create: `tests/e2e/scenarios/import_lifecycle_test.go`

**Key Decisions / Notes:**

- Integration test (not unit) — uses the real import service with a mock HTTP server serving a small test video
- Lifecycle: create import from URL → verify pending status → trigger processing → verify video created → verify video appears in user's videos list
- Cancel path: create import → cancel before processing → verify cancelled status
- Retry path: create import → simulate failure → retry → verify re-queued
- Guard with `if testing.Short() { t.Skip() }` since it requires DB
- Use `testutil.SetupTestDB(t)` for database
- Mock the HTTP download server with `httptest.NewServer` returning a small test file
- **Async synchronization strategy:** Import processing runs in a goroutine (`go s.processImport`). To synchronize: call `service.ProcessPendingImports(ctx)` directly after create to force synchronous completion, then assert status. If `ProcessPendingImports` is not available, use a polling loop: `for i := 0; i < 50; i++ { imp, _ := service.GetImport(ctx, id, userID); if imp.Status == "completed" { break }; time.Sleep(100*time.Millisecond) }` with a 5s timeout and clear error message on timeout.

**Definition of Done:**

- [ ] E2E test: import from URL → pending → complete → video accessible
- [ ] E2E test: import → cancel → verify cancelled
- [ ] E2E test: import → fail → retry → pending
- [ ] Tests use mock HTTP server, no external dependencies
- [ ] Guarded with `testing.Short()` skip
- [ ] All tests pass

**Verify:**

- `go test ./tests/e2e/scenarios/... -run TestImportLifecycle -count=1 -v` (requires DB)

---

### Task 10: OpenAPI Audit + Lockstep Verification

**Objective:** Audit all OpenAPI specs against routes.go to ensure every runner, plugin, payment, ATProto, and import endpoint has a spec entry. Fill any gaps.

**Dependencies:** Tasks 1-9

**Files:**

- Modify: `api/openapi.yaml` (if runner paths are incomplete)
- Modify: `api/openapi_plugins.yaml` (if plugin paths are incomplete)
- Modify: `api/openapi_payments.yaml` (if payment paths are incomplete)
- Modify: `api/openapi_imports.yaml` (if import paths are incomplete)
- Possibly create: `api/openapi_social.yaml` (for ATProto social routes if not covered)

**Key Decisions / Notes:**

- Methodical audit: extract all route registrations from `routes.go`, compare against all `api/openapi*.yaml` paths
- Known gaps from prior audit (parity-gaps-docs-openapi plan Task 10): social routes, some analytics routes, `.well-known/atproto-did`
- After any changes, run `make verify-openapi` to ensure generated code matches spec
- Do NOT modify existing schemas — only add missing path entries

**Definition of Done:**

- [ ] Every runner endpoint in routes.go has OpenAPI spec entry
- [ ] Every plugin endpoint has spec entry
- [ ] Every payment endpoint has spec entry
- [ ] Every import endpoint has spec entry
- [ ] ATProto/social routes have spec entries
- [ ] `make verify-openapi` passes
- [ ] All tests pass

**Verify:**

- `make verify-openapi`
- Compare route count in routes.go vs path count in all OpenAPI specs

---

### Task 11: Documentation Accuracy Pass

**Objective:** Update gap report and all stale documentation to reflect the new test and E2E coverage achieved in this plan.

**Dependencies:** Tasks 1-10

**Files:**

- Modify: `docs/reports/peertube-parity-gap-report.md` — update "What is not true yet" and "Recommended Next Steps" sections
- Modify: `.claude/rules/project.md` — update test counts and Newman collection count
- Modify: `CLAUDE.md` — update test counts
- Modify: `postman/README.md` — add new collections to documentation
- Modify: `postman/run-all-tests.sh` — register all new Postman collections (runners, plugins, payments, import-lifecycle, atproto) in a single coordinated edit

**Key Decisions / Notes:**

- Gap report changes: move "plugin/runner success-path lifecycle" from "not yet proven" to "proven by stateful Newman"
- Update Newman collection count (currently 13 passing) to reflect new collections
- Update test function counts across documentation
- Verify no stale references to "still not proven" for areas now covered
- **Consolidated `run-all-tests.sh` registration:** All 5 new Postman collections are registered here in one edit to avoid merge conflicts from parallel task edits. Collections to add: `vidra-runners`, `vidra-plugins`, `vidra-payments`, `vidra-import-lifecycle`, `vidra-atproto`

**Definition of Done:**

- [ ] Gap report "What is not true yet" section updated
- [ ] Gap report "Recommended Next Steps" section reflects completed items
- [ ] Newman collection count updated in project docs
- [ ] Test function counts accurate in CLAUDE.md and project.md
- [ ] `postman/README.md` documents all new collections
- [ ] All 5 new collections registered in `postman/run-all-tests.sh`
- [ ] No stale coverage claims remain

**Verify:**

- `grep -r "not yet proven\|still not.*proven" docs/reports/peertube-parity-gap-report.md` returns only genuinely remaining gaps
- All documented counts match actual file counts

## Open Questions

None — all clarified via user Q&A.

### Deferred Ideas

- Full imported-instance behavior verification (requires real federation mock)
- Mock PeerTube instance in docker-compose for true federation E2E
- tus protocol support for resumable uploads
- GDPR user data export/import
