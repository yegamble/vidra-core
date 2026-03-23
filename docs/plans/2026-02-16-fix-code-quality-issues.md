# Fix Code Quality Issues Implementation Plan

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

**Goal:** Fix 27 code quality issues spanning security vulnerabilities, performance bottlenecks, cleanup debt, and missing test coverage. Issues sourced from static analysis / code review findings.

**Architecture:** All changes are surgical fixes within existing architecture. No new packages or structural changes. Uses existing `security.SanitizeStrictText()` for XSS, `obs` package (`log/slog`) for structured logging, and existing port interfaces.

**Tech Stack:** Go 1.24, bluemonday (already a dependency), `log/slog` via `internal/obs`

## Scope

### In Scope

- Security: XSS sanitization for video metadata/uploads, registration validation, JWT role verification, SSH host key verification
- Performance: N+1 query fix, redundant ffprobe elimination, encoding progress race condition, video repository schema fallback
- Cleanup: Delete .bak files, replace fmt.Printf/Fprintf with slog, fix context misuse, fix Redis backup placeholder, deduplicate timestamp formatting, replace manual string containment, goroutine lifecycle management
- Testing: Tests for `parseClientAuth`, `validateJWTSecret`, email verification handler, TwoFA handler, OAuth token handler

### Out of Scope

- Replacing ALL `fmt.Printf` instances across the entire codebase (only the 3 specifically identified: backup manager, plugin manager, redundancy service — plus the additional instances found are addressed in the broader logging task)
- Refactoring the encoding pipeline architecture
- Full email validation library integration (will use `net/mail.ParseAddress` from stdlib)
- Production-grade Redis backup via RDB file copy from Redis server (will implement proper `redis-cli --rdb` approach)

## Prerequisites

- None — all changes use existing dependencies and patterns

## Context for Implementer

> This section is critical for cross-session continuity.

- **Patterns to follow:**
  - XSS sanitization: Follow `internal/usecase/comment/service.go:67` which uses `security.SanitizeStrictText()` and `security.SanitizeCommentHTML()`
  - Structured logging: Use `log/slog` directly or `obs.GetGlobalLogger()`. See `internal/obs/logger.go` for the app's slog setup
  - Handler testing: Follow `internal/httpapi/handlers/auth/oauth_unit_test.go` for mock patterns with `httptest`
  - Table-driven tests: Standard across the codebase, use `testify/assert` and `testify/require`
- **Conventions:** Error wrapping with `fmt.Errorf("context: %w", err)`, context.Context as first param
- **Key files:**
  - `internal/security/html_sanitizer.go` — existing bluemonday sanitization functions
  - `internal/obs/logger.go` — slog logger factory and global logger
  - `internal/port/video.go` — VideoRepository interface
  - `internal/port/comment.go` — CommentRepository interface with `ListReplies`
  - `internal/backup/sftp.go` — SFTP backend with `HostKey` field and `buildHostKeyCallback()`
- **Gotchas:**
  - The `videoRepository.GetByID` has a fallback query for schema compatibility — the `hasChannelID` flag is already checked via `ensureSchemaChecked()` in `Create` but not in `GetByID`
  - The SFTP backend already has a `HostKey` field and `buildHostKeyCallback()` that supports it — the issue is that `app.go` doesn't pass the config value
  - Issues #16, #23 are the same issue (AggregateAllVideosForDate placeholder)
  - Issues #17, #21 are the same issue (SFTP host key verification)
  - TwoFA handlers already have tests in `twofa_handlers_unit_test.go` and `twofa_integration_test.go`
  - Email verification handlers already have tests in `email_verification_handlers_unit_test.go`
  - OAuth token handler already has extensive tests in `oauth_unit_test.go` and `auth_handlers_unit_test.go`

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Security — XSS sanitization for video metadata and upload filenames
- [x] Task 2: Security — Registration input validation (email format, password strength)
- [x] Task 3: Security — JWT role verification against database
- [x] Task 4: Security — SFTP host key verification wiring
- [x] Task 5: Performance — N+1 query fix in comment service
- [x] Task 6: Performance — Redundant ffprobe elimination and progress race condition
- [x] Task 7: Performance — Video repository schema compatibility fix
- [x] Task 8: Cleanup — Delete .bak files, fix stdlib reimplementation, deduplicate timestamps
- [x] Task 9: Cleanup — Replace unstructured logging with slog
- [x] Task 10: Cleanup — Fix context misuse, goroutine lifecycle, Redis backup
- [x] Task 11: Feature — Implement AggregateAllVideosForDate
- [x] Task 12: Testing — Add tests for parseClientAuth and validateJWTSecret

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Security — XSS Sanitization for Video Metadata and Upload Filenames

**Objective:** Sanitize video title and description in the video creation handler and the upload filename-derived title to prevent stored XSS. (Issues #10, #11)

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/videos.go`
- Modify: `internal/usecase/upload/service.go`
- Test: `internal/httpapi/handlers/video/videos_test.go` (or existing test file)
- Test: `internal/usecase/upload/service_test.go` (or existing test file)

**Key Decisions / Notes:**

- Use `security.SanitizeStrictText()` for video titles (plain text, no HTML)
- Use `security.SanitizeCommentHTML()` for video descriptions (allows basic formatting like comments do)
- Follow the pattern at `internal/usecase/comment/service.go:67-70` — sanitize then check if content was stripped
- For upload filename title (`upload/service.go:68`), sanitize the `req.FileName` before using in `fmt.Sprintf`
- Import `athena/internal/security` in both files

**Definition of Done:**

- [ ] `videos.go`: `req.Title` sanitized with `SanitizeStrictText` before assignment to `video.Title`
- [ ] `videos.go`: `req.Description` sanitized with `SanitizeCommentHTML` before assignment to `video.Description`
- [ ] `upload/service.go`: `req.FileName` sanitized before use in title construction
- [ ] Tests verify XSS payloads like `<script>alert('xss')</script>` are stripped from title/description
- [ ] All existing tests still pass

**Verify:**

- `go test ./internal/httpapi/handlers/video/... -run XSS -v`
- `go test ./internal/usecase/upload/... -v`

---

### Task 2: Security — Registration Input Validation

**Objective:** Add email format validation and password complexity requirements to the user registration handler. (Issue #12)

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers.go`
- Test: `internal/httpapi/handlers_test.go` (or existing test file)

**Key Decisions / Notes:**

- Use `net/mail.ParseAddress()` from stdlib for email validation (no new dependencies)
- Password requirements: minimum 8 characters, at least one uppercase, one lowercase, one digit
- Add validation after the existing empty-field checks at line 195
- Return 400 with descriptive error messages for each validation failure

**Definition of Done:**

- [ ] Email validated with `net/mail.ParseAddress()` — rejects malformed emails
- [ ] Password minimum 8 chars enforced
- [ ] Password requires mix of uppercase, lowercase, and digit
- [ ] Error messages are descriptive (e.g., "Password must be at least 8 characters")
- [ ] Tests cover: valid input, invalid email format, short password, password without uppercase/lowercase/digit

**Verify:**

- `go test ./internal/httpapi/... -run Register -v`

---

### Task 3: Security — JWT Role Verification Against Database

**Objective:** Add a database check in the auth middleware to verify the JWT role claim matches the user's current role, enabling immediate privilege revocation. (Issue #18)

**Dependencies:** None

**Files:**

- Modify: `internal/middleware/auth.go`
- Test: `internal/middleware/auth_test.go`

**Key Decisions / Notes:**

- The middleware currently extracts role from JWT and sets it in context without verification
- Add an optional `UserRepository` (or a minimal interface with `GetByID`) to the middleware
- If the repo is provided, look up the user and compare roles; if roles differ, use the DB role
- If user is banned/deleted in DB, return 401
- Make the repo optional to avoid breaking existing middleware usage — when nil, fall through to current behavior (backward compatible)
- Use a cache-friendly approach: check DB only for protected routes, not every request

**Definition of Done:**

- [ ] Auth middleware accepts an optional user lookup function/interface
- [ ] When provided, verifies user exists and is not banned
- [ ] When provided, uses DB role over JWT role if they differ
- [ ] When not provided, falls through to existing JWT-only behavior
- [ ] Tests cover: role mismatch detected, banned user rejected, nil repo falls back to JWT

**Verify:**

- `go test ./internal/middleware/... -run Auth -v`

---

### Task 4: Security — SFTP Host Key Verification Wiring

**Objective:** Pass the configured SFTP host key from config to the SFTP backend, and add a `SetHostKey` method. (Issues #17, #21)

**Dependencies:** None

**Files:**

- Modify: `internal/app/app.go` (line ~472)
- Modify: `internal/backup/sftp.go` (add SetHostKey or extend constructor)
- Test: `internal/backup/sftp_test.go`

**Key Decisions / Notes:**

- The `SFTPBackend` already has a `HostKey` field and `buildHostKeyCallback()` handles it properly
- The fix is simply setting `sftpBackend.HostKey = app.Config.BackupSFTPHostKey` in `app.go` after constructing the backend
- Config field `BackupSFTPHostKey` already exists at `internal/config/config.go:195`
- The `buildHostKeyCallback()` at `sftp.go:103-125` already uses `s.HostKey` — it just was never set from config
- Remove the `// TODO: Set known host key if provided` comment

**Definition of Done:**

- [ ] `app.go`: After `NewSFTPBackend(...)`, set `sftpBackend.HostKey = app.Config.BackupSFTPHostKey`
- [ ] TODO comment at `app.go:472` removed
- [ ] Test verifies that when HostKey is set, `buildHostKeyCallback` returns `ssh.FixedHostKey`
- [ ] Test verifies that when HostKey is empty, TOFU callback is used

**Verify:**

- `go test ./internal/backup/... -run HostKey -v`
- `go test ./internal/app/... -v`

---

### Task 5: Performance — N+1 Query Fix in Comment Service

**Objective:** Replace per-comment `ListReplies` calls with a single batch query. (Issue #7)

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/comment/service.go` (ListComments method, line ~216)
- Modify: `internal/port/comment.go` (add batch method to interface)
- Modify: `internal/repository/comment_repository.go` (implement batch method)
- Test: `internal/usecase/comment/service_test.go`

**Key Decisions / Notes:**

- Add `ListRepliesBatch(ctx context.Context, parentIDs []uuid.UUID, limit int) (map[uuid.UUID][]*domain.CommentWithUser, error)` to the comment repo interface
- Implementation: Single query with `WHERE parent_id IN ($1, $2, ...)` using `ANY($1)` with a UUID array
- In `ListComments`, collect all comment IDs, fetch all replies in one call, then distribute them
- Keep the existing `ListReplies` for backward compatibility (other callers may use it)
- Limit replies per comment to 3 (matching existing behavior at line 218)

**Definition of Done:**

- [ ] `ListRepliesBatch` added to `port/comment.go` interface
- [ ] Batch implementation in `repository/comment_repository.go` uses single query with `WHERE parent_id = ANY($1)`
- [ ] `ListComments` calls `ListRepliesBatch` once instead of N `ListReplies` calls
- [ ] Tests verify batch behavior returns correct replies per comment
- [ ] Existing tests still pass

**Verify:**

- `go test ./internal/usecase/comment/... -v`
- `go test ./internal/repository/... -run Comment -v`

---

### Task 6: Performance — Redundant ffprobe and Progress Race Condition

**Objective:** Fetch video duration once per encoding job and aggregate progress updates to fix race condition. (Issues #6, #15)

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/encoding/service.go`
- Test: `internal/usecase/encoding/service_test.go`

**Key Decisions / Notes:**

- **Issue #6 (ffprobe):** Add a `duration` parameter to `execFFmpegWithProgress` and `transcodeHLS`. Compute duration once in the caller (the `processJob` method around line 210) and pass it down. Remove the `getVideoDuration` call inside `execFFmpegWithProgress`.
- **Issue #15 (race condition):** The progress goroutines per-resolution each call `s.repo.UpdateJobProgress`. Fix by:
  1. Creating a `sync.Mutex`-protected progress aggregator struct that tracks per-resolution progress
  2. Computing weighted average across resolutions
  3. Updating DB from a single goroutine or with mutex protection, throttled to every 5%
- The aggregator should be created per-job and passed to each resolution's goroutine

**Definition of Done:**

- [ ] `getVideoDuration` called once per job, result passed to `transcodeHLS`/`execFFmpegWithProgress`
- [ ] `execFFmpegWithProgress` accepts duration as parameter instead of computing it
- [ ] Progress updates aggregated across resolutions with mutex protection
- [ ] DB updates throttled (max every 5% change in aggregate progress)
- [ ] Tests verify duration is passed through, not re-computed
- [ ] No race detector warnings: `go test -race ./internal/usecase/encoding/...`

**Verify:**

- `go test ./internal/usecase/encoding/... -v`
- `go test -race ./internal/usecase/encoding/...`

---

### Task 7: Performance — Video Repository Schema Compatibility Fix

**Objective:** Use cached `hasChannelID` flag in `GetByID` instead of error-based query fallback. (Issue #14)

**Dependencies:** None

**Files:**

- Modify: `internal/repository/video_repository.go` (GetByID method, line ~195)
- Test: `internal/repository/video_repository_test.go`

**Key Decisions / Notes:**

- `ensureSchemaChecked()` is already called in `Create` and sets `hasChannelID`
- In `GetByID`, call `r.ensureSchemaChecked(ctx)` at the start
- Use `r.hasChannelID` to choose the correct query upfront (with or without s3_urls columns)
- Remove the error-based fallback at lines 240-269 that catches "column does not exist" errors
- Keep the `sql.ErrNoRows` and UUID validation error handling

**Definition of Done:**

- [ ] `GetByID` calls `r.ensureSchemaChecked(ctx)` at start
- [ ] Query selection based on `r.hasChannelID` flag (like `Create` does)
- [ ] Error-based fallback for "column does not exist" removed
- [ ] Standard error handling preserved (ErrNoRows, invalid UUID)
- [ ] Tests verify correct query is used based on schema state

**Verify:**

- `go test ./internal/repository/... -run Video -v`

---

### Task 8: Cleanup — Delete .bak Files, Fix stdlib Reimplementation, Deduplicate Timestamps

**Objective:** Delete all .bak files, replace custom `contains` with `strings.Contains`, and deduplicate VTT/SRT timestamp formatting. (Issues #1, #2, #13)

**Dependencies:** None

**Files:**

- Delete: All 9 `.bak*` files found in codebase
- Modify: `internal/repository/transaction_manager.go` (replace custom `contains`, delete `contains` and `containsHelper` functions)
- Modify: `internal/whisper/local_client.go` (deduplicate `formatVTTTimestamp`/`formatSRTTimestamp`)
- Test: `internal/whisper/local_client_test.go`

**Key Decisions / Notes:**

- Replace `contains(errStr, "deadlock detected")` with `strings.Contains(errStr, "deadlock detected")` at lines 171-173
- Delete `contains` function (line 192) and `containsHelper` function (line 196)
- Add `"strings"` import if not present
- For timestamp dedup: Create `formatTimestamp(seconds float64, millisSep string) string` and call with `"."` for VTT and `","` for SRT
- .bak files to delete: `internal/backup/sftp.go.bak`, `.bak2`, `.bak3`, `.bak4`, `internal/usecase/backup/service.go.bak`, `internal/httpapi/handlers/backup/backup_handlers_test.go.bak`, `.bak2`, `internal/httpapi/handlers/messaging/messaging_handlers_unit_test.go.bak`, `postman/test-files/avatars/avatar.png.bak`

**Definition of Done:**

- [ ] All 9 `.bak*` files deleted
- [ ] Custom `contains`/`containsHelper` functions removed from `transaction_manager.go`
- [ ] `strings.Contains` used instead at lines 171-173
- [ ] Shared `formatTimestamp` function replaces duplicate VTT/SRT formatters
- [ ] `formatVTTTimestamp` and `formatSRTTimestamp` are thin wrappers calling `formatTimestamp`
- [ ] Tests for timestamp formatting cover both VTT and SRT separators

**Verify:**

- `go build ./...`
- `go test ./internal/repository/... -run Transaction -v`
- `go test ./internal/whisper/... -v`

---

### Task 9: Cleanup — Replace Unstructured Logging with slog

**Objective:** Replace `fmt.Printf` and `fmt.Fprintf(os.Stderr, ...)` calls with `log/slog` structured logging. (Issues #3, #19, #22, #27)

**Dependencies:** None

**Files:**

- Modify: `internal/backup/manager.go` (lines 101, 110)
- Modify: `internal/plugin/manager.go` (line 375)
- Modify: `internal/usecase/redundancy/service.go` (lines 228, 251, 331, 354, 378, 433, 441, 520)
- Modify: `internal/usecase/encoding/service.go` (line 295)
- Modify: `internal/usecase/captiongen/service.go` (line 365)
- Modify: `internal/usecase/comment/service.go` (line 289)
- Modify: `internal/usecase/activitypub/comment_federation.go` (lines 180, 284, 378)
- Modify: `internal/usecase/activitypub/delivery.go` (line 97)
- Modify: `internal/repository/rating_repository.go` (lines 111, 201, 219)
- Modify: `internal/httpapi/handlers/social/captions.go` (line 212)
- Modify: `internal/httpapi/handlers/video/stream_handlers.go` (line 114)
- Modify: `internal/importer/ytdlp.go` (lines 45, 183)

**Key Decisions / Notes:**

- Replace `fmt.Printf("Warning: ...")` with `slog.Warn("message", "key", value)`
- Replace `fmt.Fprintf(os.Stderr, "ERROR: ...")` with `slog.Error("message", "error", err)`
- Use `log/slog` directly (stdlib) — consistent with the project's slog usage
- For files that already import `"log"`, switch to `"log/slog"` instead
- Pattern: `slog.Warn("failed to load plugin", "path", manifestPath, "error", err)`
- Remove `"os"` import from files that only used it for `os.Stderr` in fmt.Fprintf

**Definition of Done:**

- [ ] All identified `fmt.Printf` and `fmt.Fprintf(os.Stderr, ...)` calls replaced with slog equivalents
- [ ] Imports updated (add `"log/slog"`, remove unused `"fmt"` or `"os"` where applicable)
- [ ] Log messages use structured key-value pairs, not string formatting
- [ ] `go build ./...` succeeds with no errors

**Verify:**

- `go build ./...`
- `go vet ./...`

---

### Task 10: Cleanup — Fix Context Misuse, Goroutine Lifecycle, Redis Backup

**Objective:** Fix context.Background() misuse in plugin handler, add goroutine lifecycle management to import service, and fix broken Redis backup. (Issues #4, #5, #20)

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/plugin/plugin_handlers.go` (line 496)
- Modify: `internal/usecase/import/service.go` (line 143)
- Modify: `internal/backup/manager.go` (dumpRedis method, line 179)
- Test: `internal/backup/manager_test.go`

**Key Decisions / Notes:**

- **Issue #4 (context misuse):** Replace `context.Background()` with `r.Context()` at `plugin_handlers.go:496`. The `TriggerEvent` call should use the request context for cancellation propagation.
- **Issue #20 (goroutine lifecycle):** The import service already has `activeImports` map with cancel functions (lines 152-154) and cleanup in defer. The issue is that `context.Background()` is used (line 143) so shutdown can't cancel running imports. Fix: Accept a base context from the service constructor (the app's lifecycle context) and derive import contexts from it. Add a `Shutdown()` method that cancels all active imports.
- **Issue #5 (Redis backup):** Replace the `time.Sleep` + empty file approach with a proper `redis-cli --rdb <output>` command that actually dumps the RDB file. This is the standard approach for Redis backup without direct server access.

**Definition of Done:**

- [ ] `plugin_handlers.go:496`: `context.Background()` → `r.Context()`
- [ ] Import service accepts a base context and uses it for import goroutines
- [ ] Import service has `Shutdown(ctx context.Context)` that cancels all active imports
- [ ] Redis backup uses `redis-cli --rdb <outputPath>` instead of `time.Sleep` + empty file
- [ ] Redis backup has proper error handling and validates output file is non-empty
- [ ] Tests verify Redis backup produces non-empty output (can mock exec)

**Verify:**

- `go build ./...`
- `go test ./internal/backup/... -v`
- `go test ./internal/usecase/import/... -v`

---

### Task 11: Feature — Implement AggregateAllVideosForDate

**Objective:** Implement the placeholder `AggregateAllVideosForDate` method with actual video list retrieval and batch processing. (Issues #16, #23)

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/analytics/service.go` (AggregateAllVideosForDate at line 167)
- Modify: `internal/usecase/analytics/service.go` (Service struct — add videoRepo dependency)
- Modify: `internal/port/video.go` (may need to add a `ListAllIDs` method if not feasible with existing `List`)
- Test: `internal/usecase/analytics/service_test.go`

**Key Decisions / Notes:**

- Add `videoRepo port.VideoRepository` to the analytics `Service` struct
- Update `NewService` constructor to accept the video repo
- Use `videoRepo.List` with pagination to iterate through all videos in batches of 100
- For each batch, call `s.AggregateVideoAnalytics(ctx, videoID, date)` per video
- Use a simple sequential approach (batch by batch) — parallelism is premature here
- Handle errors per-video: log and continue, don't abort the entire job
- Update all callers of `NewService` to pass the video repo
- Remove the TODO comment

**Definition of Done:**

- [ ] `Service` struct has `videoRepo` field
- [ ] `NewService` accepts `port.VideoRepository` parameter
- [ ] `AggregateAllVideosForDate` fetches all video IDs in batches
- [ ] Each video's analytics aggregated via existing `AggregateVideoAnalytics`
- [ ] Per-video errors logged but don't abort the batch
- [ ] TODO comment removed
- [ ] All callers of `NewService` updated
- [ ] Tests verify batch processing with mock repo

**Verify:**

- `go build ./...`
- `go test ./internal/usecase/analytics/... -v`

---

### Task 12: Testing — Add Tests for parseClientAuth and validateJWTSecret

**Objective:** Add unit tests for the `parseClientAuth` function and `validateJWTSecret` function. (Issues #8, #9)

**Dependencies:** None

**Files:**

- Create: `internal/config/config_helpers_test.go`
- Modify: `internal/httpapi/handlers/auth/oauth_unit_test.go` (add parseClientAuth tests, or create new file)

**Key Decisions / Notes:**

- `parseClientAuth` at `oauth.go:212` — test with: Basic auth header (valid base64), malformed base64, form-based client_id/client_secret, no auth provided, Basic prefix case-insensitivity
- `validateJWTSecret` at `config_helpers.go:35` — test with: short secret (<32 chars), known insecure defaults ("changeme", "secret", etc.), valid long secret, non-production environment bypass
- Use table-driven tests with `testify/assert`
- `parseClientAuth` is unexported — tests must be in the same package (`package auth`)
- `validateJWTSecret` is unexported — tests must be in `package config`

**Definition of Done:**

- [ ] `parseClientAuth` tests cover: valid Basic auth, invalid base64, form values, empty auth, case-insensitive "basic" prefix
- [ ] `validateJWTSecret` tests cover: short secret rejected (production), insecure defaults rejected, valid secret accepted, non-production skips validation
- [ ] All new tests pass
- [ ] No regressions in existing tests

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -run parseClientAuth -v`
- `go test ./internal/config/... -run validateJWTSecret -v`

---

## Testing Strategy

- **Unit tests:** Each task includes specific test requirements. Use table-driven tests with testify.
- **Integration tests:** Not required for these fixes — they are surgical changes within existing patterns.
- **Manual verification:** `make validate-all` after all tasks complete.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| JWT role DB check adds latency to every request | Med | Med | Make repo optional; when provided, result can be cached per-request (already in context). Consider checking only on write operations. |
| N+1 fix changes reply ordering or count | Low | Med | Keep same LIMIT 3 per parent, verify ordering matches existing behavior |
| Encoding progress aggregation changes UX | Low | Low | Weighted average preserves meaningful progress display; test with multiple resolutions |
| Analytics NewService signature change breaks callers | Med | Low | Search all callers with LSP/grep, update each one |
| Redis backup with --rdb flag may not be available in all redis-cli versions | Low | Med | Fall back to BGSAVE + file copy approach if --rdb not supported; document requirement |

## Open Questions

- None — all issues are well-specified with clear locations and fixes.

### Deferred Ideas

- Replace ALL `fmt.Printf` across the entire codebase (not just the 3+N identified) — would be a separate cleanup task
- Add request-scoped slog logger (using obs.LoggerFromContext) to all services — larger refactor
- Add rate limiting to Redis role lookups in JWT middleware
