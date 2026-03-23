# Codebase Audit Remediation Plan

Created: 2026-02-15
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

**Goal:** Remediate findings from the four-part codebase audit: fix test quality issues (flakiness, placeholders), split oversized production files, update stale documentation, and create missing Postman E2E collections for critical endpoints.

**Audit Source:** This plan remediates findings from four audit reports provided by the user at `docs/audit/` (codebase-consistency-audit.md, documentation-audit.md, postman-e2e-audit.md, test-quality-audit.md). The audit discovery phase is complete; this plan focuses on implementation of fixes. Best practices research (Go testing strategies, why 100% coverage is harmful, flaky test avoidance) is documented in `docs/audit/test-quality-audit.md`.

**Architecture:** Mechanical refactoring (file splits) with no behavioral changes. Test fixes replace `time.Sleep` with `require.Eventually`. Documentation updates correct outdated numbers. Postman collections add new test coverage.

**Tech Stack:** Go 1.24, testify (require.Eventually), Postman/Newman

## Scope

### In Scope

- Delete placeholder E2E tests and skipped tests for unimplemented features
- Replace `time.Sleep` with `require.Eventually` in top 5 flakiest test files
- Fix access control TODO in chat_handlers.go (security gap)
- Split top 3 oversized production files (activitypub/service.go, videos.go, video_repository.go)
- Update PROJECT_COMPLETE.md with current numbers
- Fix httpapi/CLAUDE.md route discrepancy
- Clean up stale sprint progress files
- Create Videos CRUD and Comments/Channels Postman collections

### Out of Scope

- Splitting the remaining 26 oversized files (track as future work)
- Implementing IPFS download/delete/exists (stub pattern is acceptable for now)
- Creating sprint completion docs for sprints 15-20 (covered by CHANGELOG.md)
- Implementing the 14 placeholder E2E test scenarios (separate effort)
- Creating Postman collections for federation, admin, payments, live streams (lower priority)
- Adding missing OpenAPI specs for ~11 endpoint groups (separate effort)
- Consolidating duplicate 2FA OpenAPI specs

## Prerequisites

- All 3,752 tests currently passing (confirmed)
- Go 1.24 toolchain available
- `testify` already a dependency (for `require.Eventually`)

## Context for Implementer

- **Patterns to follow:** File splits must preserve all existing function signatures. No behavioral changes.
- **Conventions:** All Go files use `gofmt` formatting. Tests use table-driven pattern with `testify`. Postman collections follow the structure in existing `postman/vidra-auth.postman_collection.json`.
- **Key files:**
  - `internal/httpapi/routes.go` — all route definitions, needed for Postman collections
  - `internal/usecase/activitypub/service.go:40` — `NewService` constructor, anchor for split
  - `internal/httpapi/handlers/video/videos.go:31` — first handler, anchor for split
  - `internal/repository/video_repository.go:27` — constructor, anchor for split
- **Gotchas:**
  - The `Service` struct in activitypub/service.go has many dependencies injected via constructor. All split files share the same `Service` receiver — they stay in the same package, just different files.
  - `videos.go` uses standalone handler functions (not methods on a struct), so split is simpler.
  - `video_repository.go` uses unexported `videoRepository` struct with methods — split files stay in same package.
- **Domain context:** File splits in Go just mean moving functions to new files within the same package. No import changes needed for callers since Go resolves at package level, not file level.

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Delete placeholder and skipped tests
- [x] Task 2: Fix time.Sleep flakiness in top 5 test files
- [x] Task 3: Fix access control TODO in chat_handlers.go
- [x] Task 4: Split activitypub/service.go into focused modules
- [x] Task 5: Split video/videos.go into focused handler files
- [x] Task 6: Split video_repository.go into focused query files
- [x] Task 7: Update PROJECT_COMPLETE.md with current numbers
- [x] Task 8: Fix documentation discrepancies and clean stale files
- [x] Task 9: Create Videos CRUD Postman collection
- [x] Task 10: Create Comments & Channels Postman collection

**Total Tasks:** 10 | **Completed:** 10 | **Remaining:** 0

## Implementation Tasks

### Task 1: Delete placeholder and skipped tests

**Objective:** Remove tests that provide no value — placeholder E2E tests that all `t.Skip()` and skipped tests for features that don't exist yet.

**Dependencies:** None

**Files:**

- Delete: `tests/e2e/workflows_test.go` (456 lines, all placeholders)
- Modify: `internal/worker/iota_payment_worker_test.go` — remove skipped tests at lines ~389 and ~521
- Modify: `internal/usecase/activitypub/comment_publisher_test.go` — remove skipped test at line ~482
- Modify: `internal/usecase/payments/payment_service_test.go` — remove skipped test at line ~350

**Key Decisions / Notes:**

- Delete the entire `workflows_test.go` file since ALL 14+ test functions are `t.Skip` placeholders
- For the individual skipped tests, remove just the skipped test function (not the entire file)
- These tests create false impressions of coverage and noise in test output

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] No `t.Skip` calls remain for "not implemented" reasons in modified files
- [ ] `workflows_test.go` no longer exists
- [ ] Test count decreases (confirming removal, not breakage)

**Verify:**

- Before deletion: `go test ./... -short 2>&1 | grep -cE '^ok'` — record baseline package count
- After deletion: `go test ./... -short -count=1 2>&1 | tail -3` — all pass
- `test ! -f tests/e2e/workflows_test.go` — file deleted

---

### Task 2: Fix time.Sleep flakiness in top 5 test files

**Objective:** Replace `time.Sleep` with `require.Eventually` or proper synchronization in the 5 highest-risk test files.

**Dependencies:** None

**Files:**

- Modify: `internal/database/pool_test.go` (14 sleeps, max 2s)
- Modify: `internal/livestream/rtmp_integration_test.go` (12 sleeps, max 2s)
- Modify: `internal/middleware/ratelimit_leak_test.go` (8 sleeps, max 500ms)
- Modify: `internal/chat/chat_integration_test.go` (6 sleeps, max 500ms)
- Modify: `internal/scheduler/federation_scheduler_test.go` (5 sleeps, max 200ms)

**Key Decisions / Notes:**

- Best practices research (from `docs/audit/test-quality-audit.md`): 100% coverage creates maintenance burden and false confidence; `require.Eventually` pattern preferred over `time.Sleep`; focus on behavior testing over line coverage
- Replace `time.Sleep(duration)` + assertion with `require.Eventually(t, func() bool { return condition }, timeout, pollInterval)`
- Use 2x the original sleep duration as the Eventually timeout, with 10ms poll interval
- For sleeps that are genuinely needed (e.g., waiting for goroutines to start), use channels or `sync.WaitGroup` instead
- Keep any `time.Sleep` that's inside the code-under-test (not the test itself), or that tests timeout behavior itself
- Import `require.Eventually` from `github.com/stretchr/testify/require`

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] Zero `time.Sleep` calls remain in test assertion patterns in these 5 files (exception: sleeps in setup/teardown followed by actual synchronization, or sleeps testing timeout behavior itself)
- [ ] No new flakiness introduced (run tests 3x with -race to verify)

**Verify:**

- `go test ./internal/database/ ./internal/livestream/ ./internal/middleware/ ./internal/chat/ ./internal/scheduler/ -short -race -count=3` — all pass 3 times with race detector
- `grep -c 'time.Sleep' internal/database/pool_test.go` — significantly reduced

---

### Task 3: Fix access control TODO in chat_handlers.go

**Objective:** Implement the subscriber/access check at `chat_handlers.go:172` where a TODO marks a security gap — non-owner users are always denied access to private streams even if they should have access.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/messaging/chat_handlers.go` — implement access check at line 172
- Test: `internal/httpapi/handlers/messaging/chat_handlers_test.go` (or create if not exists)

**Key Decisions / Notes:**

- The current code at line 172 returns 403 for any non-owner. It should check if the user is a subscriber of the channel that owns the stream.
- **Verified:** `SubscriptionRepository.IsSubscribed(ctx, subscriberID, channelID uuid.UUID) (bool, error)` exists at `internal/repository/subscription_repository.go:88`. Interface defined at `internal/port/subscription.go:15`.
- The chat handler needs access to the subscription repository. Check how it's injected (via handler struct or passed through dependencies)
- Use the existing `IsSubscribed` method — no new infrastructure needed
- Need to resolve the stream's channel ID from the stream object to check subscription

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] Non-owner subscribers can access private stream chat messages
- [ ] Non-subscribers still get 403 for private streams
- [ ] TODO comment removed from line 172

**Verify:**

- `go test ./internal/httpapi/handlers/messaging/ -short -v -run TestChat` — tests pass
- `grep -n 'TODO.*subscriber' internal/httpapi/handlers/messaging/chat_handlers.go` — no TODO remains

---

### Task 4: Split activitypub/service.go into focused modules

**Objective:** Split the 2,063-line `internal/usecase/activitypub/service.go` into focused files within the same package. No behavioral changes.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/activitypub/service.go` — keep only `Service` struct, constructor, and actor-related methods (~200 lines)
- Create: `internal/usecase/activitypub/inbox_handlers.go` — `HandleInboxActivity`, `handleFollow`, `handleUndo`, `handleAccept`, `handleReject`, `handleLike`, `handleAnnounce`, `handleCreate`, `handleUpdate`, `handleDelete` (~350 lines)
- Create: `internal/usecase/activitypub/delivery.go` — `DeliverActivity`, `getOrCreateActorKeys` (~80 lines)
- Create: `internal/usecase/activitypub/comment_federation.go` — `BuildNoteObject`, `CreateCommentActivity`, `PublishComment`, `UpdateComment`, `DeleteComment` (~450 lines)
- Create: `internal/usecase/activitypub/video_federation.go` — `BuildVideoObject`, `CreateVideoActivity`, `PublishVideo`, `UpdateVideo`, `DeleteVideo`, `ingestRemoteVideo`, `updateRemoteVideo` (~600 lines)
- Create: `internal/usecase/activitypub/collections.go` — `GetOutbox`, `GetFollowers`, `GetFollowing`, `GetOutboxCount`, `GetFollowersCount`, `GetFollowingCount`, `buildFollowCollectionPage` (~200 lines)
- Create: `internal/usecase/activitypub/helpers.go` — `buildActorID`, `extractUsernameFromURI`, `extractVideoIDFromURI`, `parseDuration`, `extractVideoURL`, `extractThumbnailURL` (~120 lines)

**Key Decisions / Notes:**

- All new files use `package activitypub` — same package, so no import changes needed anywhere
- The `Service` struct definition and `NewService` constructor stay in `service.go`
- All methods use `(s *Service)` receiver — they work from any file in the package
- Helper functions (`parseDuration`, `extractVideoURL`, `extractThumbnailURL`) are package-level, not methods
- No file should exceed 300 lines after split (target <250; 300 acceptable; >300 requires further split)
- **Before splitting:** grep for unexported package-level vars, consts, and init() functions. These must stay in `service.go` or be explicitly moved together with their dependent functions
- **Split procedure:** Create all new files with package declaration first, then move functions one group at a time, running `go build ./internal/usecase/activitypub/...` after each group. If build fails, only the last group needs reverting
- **Corresponding test files:** Test file splitting is deferred (out of scope). `service_test.go` stays monolithic for now

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] `service.go` is under 300 lines
- [ ] No new file exceeds 300 lines
- [ ] No behavioral changes (same public API)
- [ ] `golangci-lint run ./internal/usecase/activitypub/...` clean
- [ ] `go vet ./internal/usecase/activitypub/...` clean

**Verify:**

- `go test ./internal/usecase/activitypub/... -short -count=1` — all pass
- `wc -l internal/usecase/activitypub/*.go | sort -rn | head` — all under 300 lines
- `go vet ./internal/usecase/activitypub/...` — clean

---

### Task 5: Split video/videos.go into focused handler files

**Objective:** Split the 1,290-line `internal/httpapi/handlers/video/videos.go` into focused handler files. No behavioral changes.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/videos.go` — keep only CRUD handlers: `ListVideosHandler`, `SearchVideosHandler`, `GetVideoHandler`, `CreateVideoHandler`, `UpdateVideoHandler`, `DeleteVideoHandler`, `GetUserVideosHandler` (~350 lines)
- Create: `internal/httpapi/handlers/video/upload_handlers.go` — `InitiateUploadHandler`, `UploadChunkHandler`, `CompleteUploadHandler`, `GetUploadStatusHandler`, `ResumeUploadHandler`, `UploadVideoFileHandler`, `VideoUploadChunkHandler`, `VideoCompleteUploadHandler` (~450 lines)
- Create: `internal/httpapi/handlers/video/stream_handlers.go` — `StreamVideo`, `StreamVideoHandler`, `GetSupportedQualities`, `validateStreamRequest`, `fetchVideo`, `tryServeFromOutputPaths`, `tryServeFromLocalDirectory`, `serveMockPlaylist`, `isRemoteURL`, `hlsRelPath` (~300 lines)
- Create: `internal/httpapi/handlers/video/validation.go` — `isAllowedVideo`, `isAllowedVideoExt`, `isAllowedVideoMime`, `hasKnownVideoSignature`, `extFromContentType` (~80 lines)

**Key Decisions / Notes:**

- All files use `package video` — same package, no import changes for callers
- `routes.go` references these functions by name (e.g., `video.UploadChunkHandler`) — these work regardless of which file defines them
- Keep related functions together (all upload handlers in one file, all stream handlers in another)
- **Before splitting:** grep for unexported package-level vars, consts. Move together with dependent functions
- **Split procedure:** Create all new files with package declaration first, move functions one group at a time, `go build` after each group
- **Corresponding test files:** Test file splitting deferred (out of scope)

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] `videos.go` is under 300 lines
- [ ] No new file exceeds 300 lines (stream_handlers.go at ~300 is acceptable)
- [ ] No behavioral changes
- [ ] `golangci-lint run ./internal/httpapi/handlers/video/...` clean
- [ ] `go vet ./internal/httpapi/handlers/video/...` clean

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -short -count=1` — all pass
- `wc -l internal/httpapi/handlers/video/*.go | sort -rn | head` — all under 300 lines
- `go build ./...` — builds successfully

---

### Task 6: Split video_repository.go into focused query files

**Objective:** Split the 1,004-line `internal/repository/video_repository.go` into focused files. No behavioral changes.

**Dependencies:** None

**Files:**

- Modify: `internal/repository/video_repository.go` — keep struct, constructor, `ensureSchemaChecked`, `scanVideoRow`, and basic CRUD: `Create`, `GetByID`, `Update`, `Delete` (~300 lines)
- Create: `internal/repository/video_queries.go` — `GetByIDs`, `GetByUserID`, `List`, `Search`, `GetVideosForMigration`, `GetByRemoteURI` (~350 lines)
- Create: `internal/repository/video_mutations.go` — `UpdateProcessingInfo`, `UpdateProcessingInfoWithCIDs`, `CreateRemoteVideo` and any remaining write operations (~250 lines)

**Key Decisions / Notes:**

- All files use `package repository` — same package
- The unexported `videoRepository` struct and its methods work across files in the same package
- The `scanVideoRow` helper stays in the main file since it's used by multiple query methods
- **Before splitting:** grep for unexported package-level vars, consts. Move together with dependent functions
- **Split procedure:** Create all new files with package declaration first, move functions one group at a time, `go build` after each group
- **Corresponding test files:** Test file splitting deferred (out of scope)

**Definition of Done:**

- [ ] All tests pass (`go test ./... -short`)
- [ ] `video_repository.go` is under 300 lines
- [ ] No new file exceeds 300 lines (video_queries.go may be ~350, acceptable)
- [ ] No behavioral changes
- [ ] `golangci-lint run ./internal/repository/...` clean
- [ ] `go vet ./internal/repository/...` clean

**Verify:**

- `go test ./internal/repository/... -short -count=1` — all pass
- `wc -l internal/repository/video_repository*.go internal/repository/video_queries.go internal/repository/video_mutations.go | sort -rn | head` — all near 300 lines
- `go build ./...` — builds successfully

---

### Task 7: Update PROJECT_COMPLETE.md with current numbers

**Objective:** Update `docs/sprints/PROJECT_COMPLETE.md` with accurate Sprint 20 final numbers instead of stale October 2025 data.

**Dependencies:** None

**Files:**

- Modify: `docs/sprints/PROJECT_COMPLETE.md`

**Key Decisions / Notes:**

- Update all outdated claims per the documentation audit:
  - "719+ automated tests" → "3,752 automated tests"
  - "42,886 lines of production code" → "~78,329 lines production + ~167,213 lines test"
  - "14 sprints across 7 months" → "20 sprints (14 feature + 6 quality programme)"
  - "Go 1.21+" → "Go 1.24"
  - "Go-Atlas" → "Goose"
  - "52 database migrations" → "61 migration files"
  - Coverage claim: ">85% for core components" → "62.3% average (90%+ for core packages)"
- Add note about Quality Programme (Sprints 15-20) at the top
- Add missing sprint entries (3-4, 11) or note their documentation location
- If additional stale content is discovered during updates: trivial (1-line) fixes — include them; substantial (paragraph rewrites) — document as deferred work

**Definition of Done:**

- [ ] All numeric claims match CHANGELOG.md (source of truth)
- [ ] Go version matches go.mod (1.24)
- [ ] Migration tool correctly listed as Goose
- [ ] Migration count matches actual file count

**Verify:**

- `grep '3,752' docs/sprints/PROJECT_COMPLETE.md` — updated test count
- `grep 'Go 1.24' docs/sprints/PROJECT_COMPLETE.md` — updated Go version
- `grep -i 'goose' docs/sprints/PROJECT_COMPLETE.md` — updated migration tool

---

### Task 8: Fix documentation discrepancies and clean stale files

**Objective:** Fix the route discrepancy in httpapi/CLAUDE.md and clean up 6 stale sprint progress files.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/CLAUDE.md` — fix upload route from `PUT /api/v1/uploads/{sessionId}/chunks/{index}` to `POST /uploads/{sessionId}/chunks`
- Add historical note to or delete: `docs/sprints/SPRINT1_PROGRESS.md`, `SPRINT5_PROGRESS.md`, `SPRINT6_PROGRESS.md`, `SPRINT7_PROGRESS.md`, `SPRINT8_PROGRESS.md`, `SPRINT13_PROGRESS.md`

**Key Decisions / Notes:**

- The actual route is `POST /uploads/{sessionId}/chunks` (POST method, no index parameter)
- For stale progress files: add a 2-line header noting they are historical, with a pointer to the corresponding SPRINT*_COMPLETE.md or CHANGELOG.md
- Don't delete them since they contain historical context that may be useful

**Definition of Done:**

- [ ] httpapi/CLAUDE.md upload route matches actual route in `routes.go:236`
- [ ] All 6 stale progress files have historical notice header

**Verify:**

- `grep 'POST.*chunks' internal/httpapi/CLAUDE.md` — correct route
- `head -2 docs/sprints/SPRINT1_PROGRESS.md` — shows historical notice

---

### Task 9: Create Videos CRUD Postman collection

**Objective:** Create a Postman collection covering the Video CRUD endpoints — the core feature with zero Postman coverage.

**Dependencies:** None

**Files:**

- Create: `postman/vidra-videos.postman_collection.json`

**Key Decisions / Notes:**

- Follow structure from existing `postman/vidra-auth.postman_collection.json`
- Cover these endpoints:
  - `GET /api/v1/videos` — list (with pagination params)
  - `GET /api/v1/videos/search` — search (with query params)
  - `POST /api/v1/videos` — create
  - `GET /api/v1/videos/{id}` — get by ID
  - `PUT /api/v1/videos/{id}` — update
  - `DELETE /api/v1/videos/{id}` — delete
  - `GET /api/v1/users/me/videos` — user's videos
- Edge cases to include:
  - Create with missing required fields (400)
  - Get with non-existent UUID (404)
  - Get with invalid ID format (400)
  - Update video owned by different user (403)
  - Delete video owned by different user (403)
  - List with negative page number
  - List with page size > 1000
  - Search with empty query
- Use environment variables for `{{baseUrl}}` and `{{authToken}}`
- Include pre-request scripts to set auth token where needed
- Before adding edge case requests, grep existing Postman collections for similar tests to avoid duplication with `vidra-edge-cases-security.postman_collection.json`
- Cross-reference ALL endpoint URLs and auth patterns against `routes.go` and existing `vidra-auth.postman_collection.json`

**Definition of Done:**

- [ ] Collection file is valid JSON
- [ ] All 7 endpoints have at least happy-path and 1 error-case test
- [ ] All edge cases from `postman-e2e-audit.md` Priority 2 "Video CRUD" section implemented: missing fields (400), non-existent UUID (404), invalid ID format (400), wrong owner update/delete (403), negative page number, page size >1000, empty search query
- [ ] Uses environment variables consistently

**Verify:**

- `python3 -m json.tool postman/vidra-videos.postman_collection.json > /dev/null` — valid JSON
- `grep -c '"request"' postman/vidra-videos.postman_collection.json` — 15+ requests

---

### Task 10: Create Comments & Channels Postman collection

**Objective:** Create a Postman collection covering Comments and Channels endpoints — the second and third most important missing collections.

**Dependencies:** None

**Files:**

- Create: `postman/vidra-social.postman_collection.json`

**Key Decisions / Notes:**

- Combine comments and channels into one "social" collection (they're related features)
- Comments endpoints:
  - `GET /api/v1/videos/{videoId}/comments` — list
  - `POST /api/v1/videos/{videoId}/comments` — create
  - `GET /api/v1/comments/{commentId}` — get
  - `PUT /api/v1/comments/{commentId}` — update
  - `DELETE /api/v1/comments/{commentId}` — delete
  - `POST /api/v1/comments/{commentId}/flag` — flag
  - `POST /api/v1/comments/{commentId}/moderate` — moderate
- Channels endpoints:
  - `GET /api/v1/channels` — list
  - `GET /api/v1/channels/{id}` — get
  - `POST /api/v1/channels` — create
  - `PUT /api/v1/channels/{id}` — update
  - `DELETE /api/v1/channels/{id}` — delete
  - `POST /api/v1/channels/{id}/subscribe` — subscribe
  - `DELETE /api/v1/channels/{id}/subscribe` — unsubscribe
- Edge cases:
  - Comment on non-existent video (404)
  - Update comment owned by different user (403)
  - Flag own comment
  - Create channel with duplicate name (409)
  - Delete channel with active videos
  - Subscribe to own channel
  - Subscribe twice (idempotency)

**Definition of Done:**

- [ ] Collection file is valid JSON
- [ ] All 14 endpoints have happy-path tests
- [ ] All edge cases from `postman-e2e-audit.md` Priority 2 "Comments" and "Channels" sections implemented: comment on non-existent video (404), wrong owner update/delete (403), flag own comment, duplicate channel name (409), delete channel with videos, subscribe to own channel, subscribe twice (idempotency)
- [ ] Uses environment variables consistently

**Verify:**

- `python3 -m json.tool postman/vidra-social.postman_collection.json > /dev/null` — valid JSON
- `grep -c '"request"' postman/vidra-social.postman_collection.json` — 20+ requests

## Runtime Environment

- **Start command:** `make run` (requires PostgreSQL and Redis running)
- **Infrastructure:** `docker compose up postgres redis` to start dependencies
- **Port:** 8080 (default)
- **Health check:** `curl http://localhost:8080/health`
- **Running Postman collections:** `newman run postman/<collection>.json --environment postman/test-local.postman_environment.json` (requires server running)
- **Note:** Some endpoints (IPFS metrics, federation) require additional infrastructure not covered in basic validation

## Testing Strategy

- **Unit tests:** All existing tests must pass after every task. File splits should not change test behavior.
- **Flakiness validation:** After Task 2, run modified test files 3 times to confirm no flakiness.
- **Integration:** Run `make validate-all` after all tasks complete.
- **Postman:** Validate collection JSON structure. Full E2E execution requires running server (manual step).

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| File split introduces compilation error | Low | High | Run `go build ./...` and `go test ./... -short` after each split |
| time.Sleep replacement changes test semantics | Medium | Medium | Use 2x original timeout for `require.Eventually`; run 3x to check flakiness |
| Chat access control change breaks existing auth flow | Low | High | Review existing middleware auth pattern; test both subscriber and non-subscriber paths |
| PROJECT_COMPLETE.md numbers inaccurate | Low | Low | Cross-reference with CHANGELOG.md which is confirmed accurate |
| Postman collections have invalid JSON | Low | Low | Validate with `python3 -m json.tool` before marking complete |

## Open Questions

- None — audit findings are well-documented and actionable.

### Deferred Ideas

- Split remaining 26 oversized files (future sprint)
- Split corresponding oversized test files (service_test.go at 2,759 lines, etc.) to match new production file structure
- Implement IPFS download/delete/exists operations
- Create Postman collections for: 2FA, notifications, messaging, live streams, captions, payments, federation, admin, health
- Add missing OpenAPI specs for ~11 endpoint groups
- Implement the 14 E2E test scenarios from workflows_test.go (after deletion, track as issues)
- Consolidate duplicate 2FA OpenAPI specs
- Add response schema validation to existing Postman collections
- Establish file size linting rule (golangci-lint funlen or custom script) to prevent future file size growth
- Comprehensive documentation audit beyond the four provided audit files
