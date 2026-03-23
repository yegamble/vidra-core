# PeerTube Full Feature Parity Audit — Implementation Plan

Created: 2026-03-20
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Comprehensive audit of Athena vs PeerTube (v8.1.0) covering every API endpoint, user story, and E2E capability. Fix all failing tests, implement critical missing features, and produce a gap report for deferred items.

**Architecture:** Athena already implements ~85% of PeerTube's core features across 20 sprints. This plan addresses: (1) 10 failing tests + 2 build failures, (2) critical missing PeerTube endpoints (RSS feeds, password reset, search for channels/playlists, video blacklisting, user account deletion, video chapters), (3) documented gap report for remaining items.

**Tech Stack:** Go 1.24+, Chi router, PostgreSQL/SQLX, Redis, IPFS, FFmpeg

## Scope

### In Scope

**Phase A — Fix failing tests & build errors (prerequisite):**
- Fix `internal/generated/types_test.go` build failure (Email type mismatch)
- Fix `internal/usecase/atproto_features_test.go` build failure (removed methods)
- Fix `TestImportService_ImportVideo_InvalidURL` failure
- Fix 7 `TestSetupWizard*` failures (HTTP status code mismatches)

**Phase B — Critical missing PeerTube API parity:**
- RSS/Atom feeds (videos, video-comments, subscriptions, podcast)
- Password reset flow (request + confirm endpoints)
- Search for channels and playlists (currently only video search exists)
- Video blacklisting (admin NSFW/takedown system)
- User account self-deletion (`DELETE /api/v1/users/me`)
- Video chapters (markers within videos)
- Blocklist status endpoint (`GET /api/v1/blocklist/status`)
- Video description endpoint (`GET /api/v1/videos/{id}/description`)

**Phase C — Comprehensive gap report:**
- Full mapping document: every PeerTube endpoint → Athena status
- User story coverage analysis
- E2E test recommendations

### Out of Scope

- Video studio editing (cut/intro/outro) — complex frontend-driven feature
- Video ownership transfer — niche admin feature
- User data export/import (resumable) — complex, low priority
- Video password protection — requires frontend support
- Video storyboard generation — requires image sprite generation pipeline
- Custom homepage — requires template system
- Player settings API — frontend-specific
- Video embed privacy — requires iframe integration
- Instance logo/avatar/banner management — cosmetic admin feature
- Video source replacement (resumable) — complex upload variant
- oAuth client flow — Athena uses JWT, has OAuth endpoints already
- Video channel syncs — requires external platform polling

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - HTTP handlers: `internal/httpapi/handlers/<domain>/<handler>.go` with constructor DI
  - Routes: registered in `internal/httpapi/routes.go:141` under `/api/v1` or via `RegisterRoutes()` methods
  - Domain models: `internal/domain/<entity>.go` — no infra deps
  - Repository: `internal/repository/<entity>_repository.go` — SQLX-based
  - Service: `internal/usecase/<domain>/service.go`
  - Response envelope: `shared.WriteJSON()`, `shared.WriteError()` from `internal/httpapi/shared/response.go`
  - Error mapping: `shared.MapDomainErrorToHTTP()` from `internal/httpapi/shared/response.go`

- **Conventions:**
  - Error wrapping: `fmt.Errorf("context: %w", err)` with `domain.ErrXxx` sentinels
  - Table-driven tests with testify `assert`/`require`
  - Constructor DI, no globals
  - UUID for IDs (`github.com/google/uuid`)
  - Pagination via `shared.GetPagination(r)` returning `(page, pageSize, limit, offset)`

- **Key files:**
  - `internal/httpapi/routes.go` — main route registration (~660 lines)
  - `internal/httpapi/shared/response.go` — response envelope helpers
  - `internal/domain/errors.go` — sentinel errors
  - `internal/domain/video.go` — Video model
  - `internal/domain/channel.go` — Channel model
  - `internal/app/app.go` — dependency wiring

- **Gotchas:**
  - Generated types in `internal/generated/types.go` use `openapi_types.Email` (pointer-based) — tests must match
  - `atproto_features_test.go` has build errors — run `go build ./internal/usecase/...` to get exact compiler error lines before fixing
  - Setup wizard tests expect specific HTTP status codes that may have changed
  - Routes are registered conditionally based on `cfg.Enable*` flags and non-nil deps

- **Domain context:**
  - PeerTube's "accounts" map to Athena's "users" — each user has an account actor
  - PeerTube's "video-channels" map to Athena's "channels" — videos belong to channels
  - PeerTube uses "blacklist" for admin video takedowns; Athena has no equivalent
  - PeerTube's feed system provides RSS/Atom/JSON Feed for videos, comments, subscriptions

## Assumptions

- Email service is wired and available for password reset flow — supported by `internal/email/service.go` having `SendPasswordResetEmail` — Task 5 depends on this
- Database migration tooling (Goose) is functional — supported by `migrations/` directory with 50+ migrations — Tasks 6-9 depend on this
- The existing `SearchVideosHandler` pattern can be extended for channels/playlists — supported by `internal/httpapi/handlers/video/videos.go` — Task 6 depends on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Setup wizard test fixes require understanding deprecated wizard flow | Medium | Low | Read wizard handler code before fixing; may need to update expected status codes |
| RSS feed generation adds a new dependency (gorilla/feeds or similar) | Low | Low | Use stdlib `encoding/xml` with manual templates to avoid new deps |
| Video blacklisting conflicts with existing moderation system | Low | Medium | Integrate with existing `blocklist` admin routes; blacklist is video-specific, blocklist is user/instance-specific |
| Password reset token storage needs new DB table or Redis key | Low | Low | Use existing email verification token pattern from `internal/email/` |

## Goal Verification

### Truths

1. `go test -short ./...` reports 0 failures and 0 build errors
2. RSS feed at `/feeds/videos.{format}` returns valid Atom/RSS XML with real video data
3. `POST /api/v1/users/ask-reset-password` sends a password reset email
4. `GET /api/v1/search/video-channels` returns channels matching a query
5. `POST /api/v1/videos/{id}/blacklist` (admin) hides a video from public listing
6. `DELETE /api/v1/users/me` soft-deletes the authenticated user
7. Gap report document covers all PeerTube v8.1.0 OpenAPI endpoints with Athena status (verify count against spec before finalizing)

### Artifacts

1. Test fix commits — all 10 failures + 2 build errors resolved
2. `internal/httpapi/handlers/video/feed_handlers.go` — RSS/Atom feed generation
3. `internal/httpapi/handlers/auth/password_reset.go` — password reset flow
4. `internal/httpapi/handlers/video/search_handlers.go` — extended search
5. `internal/httpapi/handlers/moderation/blacklist_handlers.go` — video blacklisting
6. `docs/reports/peertube-parity-gap-report.md` — comprehensive gap report

## Progress Tracking

- [x] Task 1: Fix generated types test build failure
- [x] Task 2: Fix atproto features test build failure
- [x] Task 3: Fix TestImportService_ImportVideo_InvalidURL
- [x] Task 4: Fix setup wizard test failures
- [x] Task 5: Implement password reset flow
- [x] Task 6: Implement search for channels and playlists
- [x] Task 7: Implement RSS/Atom feed endpoints
- [x] Task 8: Implement video blacklisting
- [x] Task 9: Implement user account self-deletion
- [x] Task 10: Implement video chapters
- [x] Task 11: Add video description + blocklist status endpoints
- [x] Task 12: Produce comprehensive PeerTube parity gap report

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix generated types test build failure

**Objective:** Fix the `internal/generated/types_test.go` build error where string literal `"test@example.com"` is used where `*openapi_types.Email` (or `openapi_types.Email`) is expected.

**Dependencies:** None

**Files:**

- Modify: `internal/generated/types_test.go`

**Key Decisions / Notes:**

- The `LoginRequest.Email` field is typed as `*openapi_types.Email` (from oapi-codegen), not `string`
- Fix by creating a properly typed value: `email := openapi_types.Email("test@example.com")` then using `&email` or `email` depending on pointer-ness
- Check all test functions in the file — `TestLoginRequest`, `TestAuthResponse`, and any others using Email fields
- Do NOT modify the generated `types.go` — only fix the test file

**Definition of Done:**

- [ ] `go test -short ./internal/generated/...` builds and passes
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/generated/... -v`

---

### Task 2: Fix atproto features test build failure

**Objective:** Fix `internal/usecase/atproto_features_test.go` build errors. The test file references methods on the `atprotoService` struct that may have been removed or renamed.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/atproto_features_test.go`
- Read: `internal/usecase/atproto_service.go` (to understand current methods)

**Key Decisions / Notes:**

- **FIRST STEP:** Run `go build ./internal/usecase/...` and capture the exact compiler error messages and line numbers. The plan's initial analysis found references at lines ~473 and ~498, but the exact nature of the error (undefined method, type mismatch, unused import) must be confirmed from the compiler output before making changes.
- Lines ~98 and ~107 contain HTTP mock server path matchers (`r.URL.Path == "/xrpc/app.bsky.feed.getPostThread"`) — these are NOT the build errors themselves, they are test infrastructure that may or may not need cleanup depending on the actual fix.
- If the methods were intentionally removed from production code, delete the test functions that call them AND clean up any associated mock server handlers that are now unreachable.
- If methods were renamed or moved, redirect the tests to call the new method names.

**Definition of Done:**

- [ ] `go test -short ./internal/usecase/...` builds and passes
- [ ] No dead test code left referencing removed methods
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/usecase/... -v -run TestAtproto`

---

### Task 3: Fix TestImportService_ImportVideo_InvalidURL

**Objective:** Fix the failing test in `internal/usecase/import/service_test.go` that expects an error when importing from an invalid URL but gets a different behavior.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/import/service_test.go` (likely)
- Possibly modify: `internal/usecase/import/service.go` (if the bug is in production code)

**Key Decisions / Notes:**

- Test mocks `ytdlp.ValidateURL` to return an error, but `ImportVideo` may have changed its validation order
- Check if `ImportVideo` now checks quota/rate limits before URL validation, causing the mock expectations to not match
- The mock setup expects `CountByUserIDToday` and `CountByUserIDAndStatus` to be called — verify the call order in the service
- Fix the test to match current service behavior, OR fix the service if the behavior change was unintentional

**Definition of Done:**

- [ ] `go test -short ./internal/usecase/import/... -run TestImportService_ImportVideo_InvalidURL` passes
- [ ] All other import service tests still pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/usecase/import/... -v`

---

### Task 4: Fix setup wizard test failures

**Objective:** Fix 7 failing tests in `tests/integration/setup_wizard_e2e_test.go` that expect HTTP 303/400 status codes but get different responses.

**Dependencies:** None

**Files:**

- Modify: `tests/integration/setup_wizard_e2e_test.go`
- Possibly modify: `internal/setup/` handlers (if behavior changed)

**Key Decisions / Notes:**

- `TestSetupWizardFullFlow` and `TestSetupWizardQuickInstallFlow` expect 303 (See Other redirect) but get a different status
- `TestSetupWizardInvalidFormSubmissions` subtests expect 400 but get different status
- Read the setup wizard handler to understand current response codes
- **Decision:** The setup wizard is an HTML-form-based flow; 303 See Other is the correct HTTP response for successful POST submissions per HTTP semantics. If the handler now returns 200 JSON instead of 303, this is a regression and the **handler should be restored** to return 303. Only update the tests to expect a different status if the wizard was intentionally converted to a JSON API (document this decision explicitly in a code comment).
- For the validation tests (expect 400): if the handler now returns a different error status, check whether the validation logic is still executing. 400 Bad Request is correct for form validation errors.

**Definition of Done:**

- [ ] All 7 `TestSetupWizard*` tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./tests/integration/... -v -run TestSetupWizard`

---

### Task 5: Implement password reset flow

**Objective:** Add PeerTube-compatible password reset endpoints: `POST /api/v1/users/ask-reset-password` and `POST /api/v1/users/{id}/reset-password`.

**Dependencies:** Task 1-4 (tests should be green first)

**Files:**

- Create: `internal/httpapi/handlers/auth/password_reset.go`
- Create: `internal/httpapi/handlers/auth/password_reset_test.go`
- Create: `migrations/0XX_create_password_reset_tokens_table.sql`
- Modify: `internal/httpapi/routes.go` (add routes)
- Modify: `internal/repository/user_repository.go` (add token CRUD methods)
- Modify: `internal/app/app.go` (wire password reset handler with email service and user repo)

**Key Decisions / Notes:**

- PeerTube's flow: user requests reset → email sent with token → user submits token + new password
- Athena already has `email.SendPasswordResetEmail()` in `internal/email/service.go:135` — reuse this
- **Token storage: Use a DB table** (`password_reset_tokens` with columns: `id`, `user_id`, `token_hash`, `expires_at`, `created_at`, `used_at`). Follow the email verification token pattern. DB is preferred over Redis because: tokens should survive Redis restarts, and the email verification system already uses DB storage — consistency is important.
- Handler pattern: follow existing auth handlers in `internal/httpapi/handlers/auth/`
- Rate limit the request endpoint to prevent abuse (use existing `strictAuthLimiter`)
- Token should be hashed before storage (SHA-256); 1-hour expiry; single-use (mark `used_at` on consumption)

**Definition of Done:**

- [ ] `POST /api/v1/users/ask-reset-password` accepts email, sends reset email, returns 204
- [ ] `POST /api/v1/users/{id}/reset-password` accepts token + new password, updates password, returns 204
- [ ] Invalid/expired tokens return 403
- [ ] Rate limiting applied
- [ ] All tests pass

**Verify:**

- `go test -short ./internal/httpapi/handlers/auth/... -run TestPasswordReset`

---

### Task 6: Implement search for channels and playlists

**Objective:** Add PeerTube-compatible search endpoints: `GET /api/v1/search/video-channels` and `GET /api/v1/search/video-playlists`.

**Dependencies:** Task 1-4

**Files:**

- Create: `internal/httpapi/handlers/video/search_handlers.go`
- Create: `internal/httpapi/handlers/video/search_handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add search routes)
- Modify: `internal/repository/channel_repository.go` (add `Search` method)
- Modify: `internal/repository/playlist_repository.go` (add `Search` method)
- Modify: `internal/port/channel_repository.go` (add `Search` to interface)
- Modify: `internal/port/playlist_repository.go` (add `Search` to interface — create if absent)

**Key Decisions / Notes:**

- PeerTube supports `search` query param with optional filters (sort, host, etc.)
- Athena already has `SearchVideosHandler` at `/api/v1/videos/search` — follow the same pattern
- Channel search: query against `name`, `display_name`, `description` fields
- Playlist search: query against `title`, `description` fields
- Mount at `/api/v1/search/video-channels` and `/api/v1/search/video-playlists` per PeerTube spec
- **Route coexistence:** The existing `/api/v1/videos/search` route must be KEPT for backward compatibility. The new `/api/v1/search/videos` should be added as the PeerTube-canonical path, reusing the same `SearchVideosHandler`. Both routes must coexist.
- **Interface requirement:** Any new `Search` method on the concrete repository must also be added to the corresponding interface in `internal/port/` — otherwise the handler cannot accept the repo by interface and the DI pattern breaks.

**Definition of Done:**

- [ ] `GET /api/v1/search/video-channels?search=term` returns matching channels with pagination
- [ ] `GET /api/v1/search/video-playlists?search=term` returns matching playlists with pagination
- [ ] `GET /api/v1/search/videos?search=term` works (alias)
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestSearch`

---

### Task 7: Implement RSS/Atom feed endpoints

**Objective:** Add PeerTube-compatible feed endpoints for videos, comments, and subscriptions.

**Dependencies:** Task 1-4

**Files:**

- Create: `internal/httpapi/handlers/video/feed_handlers.go`
- Create: `internal/httpapi/handlers/video/feed_handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add feed routes)

**Key Decisions / Notes:**

- PeerTube provides: `GET /feeds/videos.{format}` (RSS, Atom, JSON), `GET /feeds/video-comments.{format}`, `GET /feeds/subscriptions.{format}`, `GET /feeds/podcast/videos.xml`
- Use stdlib `encoding/xml` for Atom/RSS generation — avoid new dependencies
- Atom 1.0 is the preferred format (PeerTube defaults to Atom)
- Format determined by extension: `.atom`, `.rss`, `.json`
- Feed entries should include video title, description, author, published date, thumbnail, duration
- Support filtering by `accountId`, `accountName`, `videoChannelId`, `videoChannelName`
- Register routes outside `/api/v1` since feeds are at `/feeds/*`

**Definition of Done:**

- [ ] `GET /feeds/videos.atom` returns valid Atom feed with recent videos
- [ ] `GET /feeds/videos.rss` returns valid RSS 2.0 feed
- [ ] `GET /feeds/video-comments.atom` returns valid comment feed
- [ ] Feeds support `accountId` and `videoChannelId` filters
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestFeed`

---

### Task 8: Implement video blacklisting

**Objective:** Add PeerTube-compatible video blacklisting for admin content moderation.

**Dependencies:** Task 1-4

**Files:**

- Create: `internal/httpapi/handlers/moderation/blacklist_handlers.go`
- Create: `internal/httpapi/handlers/moderation/blacklist_handlers_test.go`
- Create: `migrations/0XX_create_video_blacklist_table.sql`
- Modify: `internal/httpapi/routes.go` (add blacklist routes)
- Modify: `internal/repository/moderation_repository.go` (add blacklist methods)
- Modify: `internal/app/app.go` (wire BlacklistRepository into deps struct and pass to handler constructor)

**Key Decisions / Notes:**

- PeerTube endpoints: `POST /api/v1/videos/{id}/blacklist` (add), `DELETE /api/v1/videos/{id}/blacklist` (remove), `GET /api/v1/videos/blacklist` (list)
- Blacklisted videos are hidden from public listing but not deleted — admin can un-blacklist
- Create a `video_blacklist` table: `id`, `video_id`, `reason`, `unfederated` (bool), `created_at`
- When a video is blacklisted, it should be excluded from `ListVideos` and search results
- Admin/mod-only operations — use existing `middleware.RequireRole("admin", "mod")`
- Different from user blocklist (which blocks users/instances)

**Definition of Done:**

- [ ] `POST /api/v1/videos/{id}/blacklist` adds video to blacklist (admin only)
- [ ] `DELETE /api/v1/videos/{id}/blacklist` removes from blacklist
- [ ] `GET /api/v1/videos/blacklist` lists all blacklisted videos with pagination
- [ ] Blacklisted videos excluded from public video listing
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/moderation/... -run TestBlacklist`
- `go test -short ./internal/repository/... -run TestBlacklist`

---

### Task 9: Implement user account self-deletion

**Objective:** Add `DELETE /api/v1/users/me` endpoint allowing users to delete their own account.

**Dependencies:** Task 1-4

**Files:**

- Create: `internal/httpapi/handlers/auth/account_deletion.go`
- Create: `internal/httpapi/handlers/auth/account_deletion_test.go`
- Modify: `internal/httpapi/routes.go` (add route)
- Modify: `internal/repository/user_repository.go` (add soft-delete or anonymize method)

**Key Decisions / Notes:**

- PeerTube allows users to delete their own account via `DELETE /api/v1/users/me`
- Implement as soft-delete: set `is_active = false`, anonymize PII (email, username), clear avatar
- Keep videos with anonymized author ("Deleted User") — don't cascade delete content
- Require password confirmation in request body for safety
- Invalidate all sessions and tokens for the user
- Admin `DELETE /api/v1/users/{id}` endpoint already exists — reuse deletion logic
- **Storage assets:** Check how admin user deletion handles IPFS pins and S3 objects. Self-deletion must NOT trigger storage cleanup jobs since videos are retained with "Deleted User" attribution. Document this behavior in a handler comment.

**Definition of Done:**

- [ ] `DELETE /api/v1/users/me` with password confirmation soft-deletes the account
- [ ] User's sessions are invalidated
- [ ] User's PII is anonymized (email, display name)
- [ ] User's videos remain with "Deleted User" attribution
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/auth/... -run TestAccountDeletion`

---

### Task 10: Implement video chapters

**Objective:** Add video chapter markers (timestamps with titles within a video), matching PeerTube's chapters API.

**Dependencies:** Task 1-4

**Files:**

- Create: `migrations/0XX_create_video_chapters_table.sql`
- Create: `internal/httpapi/handlers/video/chapter_handlers.go`
- Create: `internal/httpapi/handlers/video/chapter_handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add chapter routes)
- Modify: `internal/domain/video.go` (add Chapter struct)

**Key Decisions / Notes:**

- PeerTube endpoints: `GET /api/v1/videos/{id}/chapters`, `PUT /api/v1/videos/{id}/chapters`
- Chapters are an ordered list of `{timecode: int, title: string}` markers
- `PUT` replaces all chapters at once (not individual CRUD)
- Store in a `video_chapters` table: `id`, `video_id`, `timecode` (seconds), `title`, `position`
- Only video owner can update chapters
- Chapters returned as part of video detail response is optional (can be a separate endpoint)

**Definition of Done:**

- [ ] `GET /api/v1/videos/{id}/chapters` returns ordered chapter list
- [ ] `PUT /api/v1/videos/{id}/chapters` replaces all chapters (owner only)
- [ ] Chapters validate: timecodes must be positive, titles required, ordered by timecode
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestChapter`

---

### Task 11: Add video description + blocklist status endpoints

**Objective:** Add two small PeerTube-compatible endpoints: `GET /api/v1/videos/{id}/description` and `GET /api/v1/blocklist/status`.

**Dependencies:** Task 1-4

**Files:**

- Modify: `internal/httpapi/handlers/video/videos.go` (add description handler)
- Modify: `internal/httpapi/handlers/moderation/moderation_handlers.go` (add blocklist status)
- Modify: `internal/httpapi/routes.go` (add routes)
- Create: test files for new handlers

**Key Decisions / Notes:**

- Video description endpoint: returns the full description text (PeerTube truncates in list view). Simple handler that gets video by ID and returns `{description: "..."}`. Already available via GetVideo — this is a dedicated endpoint for clients that only need description
- Blocklist status: returns `{accounts: [{id, ...}], servers: [{host, ...}]}` for the current user's block list. Used by clients to check block status without paginated list call. Reuse existing blocklist repository

**Definition of Done:**

- [ ] `GET /api/v1/videos/{id}/description` returns video description text
- [ ] `GET /api/v1/blocklist/status` returns current user's block list summary
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -run TestDescription`
- `go test -short ./internal/httpapi/handlers/moderation/... -run TestBlocklistStatus`

---

### Task 12: Produce comprehensive PeerTube parity gap report

**Objective:** Create a detailed mapping document of all 212 PeerTube API endpoints against Athena's implementation status.

**Dependencies:** Tasks 1-11

**Files:**

- Create: `docs/reports/peertube-parity-gap-report.md`

**Key Decisions / Notes:**

- Document format: table with columns: PeerTube Endpoint | HTTP Method | Athena Status | Notes
- Statuses: `Implemented`, `Partial`, `Missing`, `N/A` (Athena has different approach), `Deferred`
- Group by PeerTube API category (accounts, videos, channels, etc.)
- Include user story coverage analysis: what can a PeerTube user do that an Athena user cannot?
- Include E2E test recommendations for critical paths
- After Tasks 5-11, the gap should be primarily cosmetic/admin features

**Definition of Done:**

- [ ] All PeerTube v8.1.0 OpenAPI endpoints mapped (verify count from spec before finalizing)
- [ ] Each endpoint has status and notes
- [ ] User story gaps identified
- [ ] E2E test recommendations included
- [ ] Document is accurate (verified against actual codebase)

**Verify:**

- Manual review — count of endpoints matches PeerTube OpenAPI spec

## Open Questions

None — all critical decisions resolved during exploration.

### Deferred Ideas

1. **Video studio editing** — PeerTube allows cut/intro/outro editing. Requires ffmpeg pipeline extensions.
2. **Video password protection** — Allow password-gated video access. Needs auth middleware changes.
3. **Storyboard generation** — Create sprite sheets of video thumbnails at intervals. Needs ffmpeg + image composition.
4. **Custom instance homepage** — Admin-configurable landing page. Requires template system.
5. **User data export** — Export all user data as ZIP. Requires data aggregation across all tables.
6. **Video channel syncs** — Auto-import from external YouTube/etc channels on schedule.
7. **Video source replacement** — Replace source file while keeping metadata. Needs re-encoding pipeline.
8. **Resumable uploads (tus protocol)** — PeerTube uses tus; Athena has chunked upload which is functionally equivalent.
