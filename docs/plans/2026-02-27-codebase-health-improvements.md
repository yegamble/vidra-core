# Codebase Health Improvements Implementation Plan

Created: 2026-02-27
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

> **Status Lifecycle:** PENDING → COMPLETE → VERIFIED
> **Iterations:** Tracks implement→verify cycles (incremented by verify phase)

## Summary

**Goal:** Address ~24 code health items across the Athena codebase: N+1 query optimizations, bug fixes, security hardening, code quality refactoring, and test improvements.

**Architecture:** Surgical, per-file changes. No architectural shifts. Each task is independently testable.

**Tech Stack:** Go (existing), PostgreSQL UNNEST/CTE for batch queries, `strings.Builder` for Go idioms, `gorilla/csrf` or custom CSRF tokens for setup wizard.

## Already Fixed (Verified During Exploration)

These items from the original list are already implemented — no work needed:

| # | Item | Evidence |
|---|------|----------|
| 8 | Stream manager heartbeat flush | `processHeartbeats` already calls `flushRemainingHeartbeats(batch)` on `ctx.Done()` and `shutdownChan` (stream_manager.go:255-260) |
| 9 | Redis AUTH command | `HandleTestRedis` sends AUTH before PING when password is provided (wizard_test_connections.go:177-193). Test expects failure on wrong password (service_connections_test.go:151) |
| 24 | BuildVideoObject test assertions | Comprehensive assertions in `video_publisher_test.go:19-145` (TestBuildVideoObject_Basic) |
| 25 | BuildNoteObject test assertions | Comprehensive assertions in `comment_publisher_test.go:18-120` (TestBuildNoteObject_Basic) |
| 26 | CreateVideoActivity test assertions | Comprehensive assertions in `video_publisher_test.go:548-640` (TestCreateVideoActivity) |
| 27 | PublishComment test | Full implementation in `comment_publisher_test.go:380-520` (TestPublishComment) |

## Scope

### In Scope

- N+1 query fixes (batch DB operations)
- Bug fixes (contains helper, context misuse)
- Code quality (email dedup, config structs, OAuth flatten, avatar cleanup, analytics refactor)
- Security (CSRF, SSRF hardening, chunk assembly DoS, DNS rebinding, SFTP TOFU)
- Test improvements (ingestRemoteVideo edge cases, skeleton cleanup)
- IOTA transaction-based detection (Phase 2 TODO)

### Out of Scope

- Duplicated scanning logic in video_queries.go (item 17) — very high blast radius, 500+ lines of scan logic across 10+ methods. Needs its own dedicated spec.
- Full io.Pipe streaming for IPFS upload (item 18) — avatar files are small (<10MB). The memory cost is negligible for the use case. Would only matter for video uploads, which use chunked upload already.

## Prerequisites

- None — all changes are to existing code with existing tests

## Context for Implementer

- **Patterns to follow:** Table-driven tests (see `internal/usecase/activitypub/video_publisher_test.go`), error wrapping with `fmt.Errorf("context: %w", err)`, config structs (see `internal/email/service.go:10` Config struct)
- **Conventions:** `testify/assert` + `testify/require` for tests, `domain.ErrXxx` sentinel errors, `context.Context` first param
- **Key files:** `internal/domain/errors.go` (sentinel errors), `internal/port/` (repository interfaces)
- **Gotchas:** Repository interface changes require updating both the port interface and all mock implementations in test files. Use `Grep` to find all callers before modifying any function signature.

## Progress Tracking

- [x] Task 1: Batch DB Operations (N+1 fixes)
- [x] Task 2: Torrent Generator Optimizations
- [x] Task 3: Livestream Analytics Fixes (contains bug + collectAllStreams refactor)
- [x] Task 4: Context Misuse in Video Import
- [x] Task 5: Email Service Refactor (dedup + config struct)
- [x] Task 6: Backup Config Structs + SFTP TOFU Persistence
- [x] Task 7: OAuth Scopes Middleware Flattening
- [x] Task 8: Avatar Handler Debug Logging Cleanup
- [x] Task 9: Security - CSRF Protection in Setup Wizard
- [x] Task 10: Security - SSRF Hardening (ytdlp + DNS rebinding)
- [x] Task 11: Security - Chunk Assembly DoS Protection
- [x] Task 12: Test Improvements (ingestRemoteVideo + skeleton cleanup)

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Batch DB Operations (N+1 Fixes)

**Objective:** Replace N individual SQL statements with single batch queries in 4 locations.

**Dependencies:** None

**Files:**

- Modify: `internal/repository/views_repository.go` (BatchIncrementVideoViews)
- Modify: `internal/usecase/redundancy/service.go` (evaluatePolicy)
- Modify: `internal/repository/channel_repository.go` (List)
- Modify: `internal/repository/social_repository.go` (BatchUpsertActors)
- Modify: `internal/port/` (add new interface methods if needed)
- Test: existing test files for each package

**Key Decisions / Notes:**

- **BatchIncrementVideoViews** (views_repository.go:478): Replace loop with single `UPDATE videos SET views = views + c.count, updated_at = NOW() FROM (SELECT unnest($1::uuid[]) AS id, unnest($2::bigint[]) AS count) c WHERE videos.id = c.id`. Build two parallel slices from the map.
- **evaluatePolicy** (redundancy/service.go:384): Add `GetVideoRedundanciesByVideoIDs(ctx, videoIDs []string)` to the redundancy port interface. Implement with `WHERE video_id = ANY($1)`. Group results by video ID in Go.
- **Channel List** (channel_repository.go:202): Replace the N+1 `loadChannelAccount` loop with a JOIN in the main query: `LEFT JOIN accounts a ON c.account_id = a.id`. Scan account fields inline.
- **BatchUpsertActors** (social_repository.go:431): Replace loop with multi-row INSERT using value list builder: `INSERT INTO ... VALUES ($1,$2,...), ($13,$14,...) ON CONFLICT ...`. Build args slice dynamically. Keep within PostgreSQL's 65535 param limit — batch at 5461 actors max per INSERT (12 columns × 5461 = 65,532 < 65,535).

**Definition of Done:**

- [ ] All 4 batch operations use single SQL query instead of N queries
- [ ] Existing tests pass (no behavior change, only performance)
- [ ] New unit tests verify batch operations with 0, 1, and multiple items
- [ ] Mock implementations updated for any new/changed interface methods
- [ ] `go test ./internal/repository/... ./internal/usecase/redundancy/... -short` passes

**Verify:**

- `go test ./internal/repository/... -short -run BatchIncrement`
- `go test ./internal/usecase/redundancy/... -short`
- `go test ./internal/repository/... -short -run Channel`
- `go test ./internal/repository/... -short -run BatchUpsert`

---

### Task 2: Torrent Generator Optimizations

**Objective:** Pre-allocate slice and use strings.Builder for magnet URI generation.

**Dependencies:** None

**Files:**

- Modify: `internal/torrent/generator.go` (generatePieces ~line 229, generateMagnetURI ~line 300)
- Test: `internal/torrent/generator_test.go`

**Key Decisions / Notes:**

- **generatePieces**: After `currentPiece = nil` (line 237), replace with `currentPiece = make([]byte, 0, g.config.PieceLength)`. Also initialize before the loop with `currentPiece := make([]byte, 0, g.config.PieceLength)`.
- **generateMagnetURI**: Replace `magnet += fmt.Sprintf(...)` pattern with `var b strings.Builder` + `b.WriteString(...)` + `fmt.Fprintf(&b, ...)`. Return `b.String()`.

**Definition of Done:**

- [ ] `currentPiece` pre-allocated with `make([]byte, 0, g.config.PieceLength)`
- [ ] `generateMagnetURI` uses `strings.Builder` instead of string concatenation
- [ ] All existing torrent tests pass
- [ ] `go test ./internal/torrent/... -short` passes

**Verify:**

- `go test ./internal/torrent/... -short -v`

---

### Task 3: Livestream Analytics Fixes

**Objective:** Fix the `contains` helper bug and refactor `collectAllStreams` into sub-functions.

**Dependencies:** None

**Files:**

- Modify: `internal/livestream/analytics_collector.go`
- Test: `internal/livestream/analytics_collector_test.go`

**Key Decisions / Notes:**

- **contains bug** (line 429): The function `s[:len(substr)] == substr` is HasPrefix, not Contains. Used for user-agent parsing (lines 367-378) where keywords like "Chrome", "Firefox" appear mid-string. Replace body with `return strings.Contains(s, substr)`. Add `"strings"` import if not already present.
- **collectAllStreams refactor** (line 197): Function is ~150 lines. Extract into: `fetchStreamMetrics(ctx, streamIDs) (viewerCounts, activeViewersMap, redisCmds)`, `buildAnalyticsRecord(stream, metrics, viewerCount, redisCmds) *domain.StreamAnalytics`, and `updateStreamMetrics(metrics, viewerCount, activeViewers)`. Keep the main function as orchestrator.

**Definition of Done:**

- [ ] `contains` function uses `strings.Contains` (or replaced with direct `strings.Contains` calls)
- [ ] New test verifies mid-string matching (e.g., `contains("Mozilla/5.0 Chrome/120", "Chrome")` returns true)
- [ ] `collectAllStreams` split into 3+ sub-functions, each < 50 lines
- [ ] All existing livestream tests pass
- [ ] `go test ./internal/livestream/... -short` passes

**Verify:**

- `go test ./internal/livestream/... -short -v`

---

### Task 4: Context Misuse in Video Import

**Objective:** Replace `context.Background()` with parent context in downloadVideo callback.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/import/service.go` (line 224)
- Test: `internal/usecase/import/service_test.go`

**Key Decisions / Notes:**

- Line 224: `_ = s.importRepo.UpdateProgress(context.Background(), imp.ID, progress, downloadedBytes)` should use `ctx` from the enclosing `downloadVideo` function. The `ctx` parameter is already available in scope. Change to `_ = s.importRepo.UpdateProgress(ctx, imp.ID, progress, downloadedBytes)`.

**Definition of Done:**

- [ ] Progress callback uses parent `ctx` instead of `context.Background()`
- [ ] Existing tests pass
- [ ] `go test ./internal/usecase/import/... -short` passes

**Verify:**

- `go test ./internal/usecase/import/... -short -v`

---

### Task 5: Email Service Refactor

**Objective:** Extract shared SMTP logic from SendTLS/SendSTARTTLS into a helper.

**Dependencies:** None

**Files:**

- Modify: `internal/email/service.go` (SendTLS line 50, SendSTARTTLS line 96)
- Test: `internal/email/service_test.go`

**Key Decisions / Notes:**

- Both methods share: Auth → Mail(from) → Rcpt(to...) → Data() → Write(msg) → Close → Quit. The only difference is how the connection/client is established.
- Extract `sendViaClient(client *smtp.Client, auth smtp.Auth, from string, to []string, msg []byte) error` as a shared helper.
- SendTLS creates client via `tls.Dial` + `smtp.NewClient`, SendSTARTTLS via `smtp.Dial` + `StartTLS`. Both then call `sendViaClient`.
- The `EmailSender` interface has 6 params on SendTLS/SendSTARTTLS but they're already interface methods — changing their signature would be a breaking interface change. Keep the interface as-is, only refactor the `smtpSender` implementation.

**Definition of Done:**

- [ ] Shared `sendViaClient` helper extracts duplicated SMTP logic
- [ ] SendTLS and SendSTARTTLS each < 20 lines (connection setup + delegate to helper)
- [ ] Existing email tests pass
- [ ] `go test ./internal/email/... -short` passes

**Verify:**

- `go test ./internal/email/... -short -v`

---

### Task 6: Backup Config Structs + SFTP TOFU Persistence

**Objective:** Replace 6-arg constructors with config structs. Persist SFTP known hosts to disk.

**Dependencies:** None

**Files:**

- Modify: `internal/backup/sftp.go` (NewSFTPBackend, buildHostKeyCallback)
- Modify: `internal/backup/s3.go` (NewS3Backend)
- Modify: callers of NewSFTPBackend and NewS3Backend (find with Grep)
- Test: `internal/backup/sftp_test.go`, `internal/backup/s3_test.go`

**Key Decisions / Notes:**

- **SFTPConfig**: `type SFTPConfig struct { Host string; Port int; User string; Password string; KeyPath string; Path string; KnownHostsFile string }`. `NewSFTPBackend(cfg SFTPConfig)`.
- **S3Config**: `type S3Config struct { Bucket string; Region string; Prefix string; Endpoint string; AccessKey string; SecretKey string }`. `NewS3Backend(cfg S3Config)`.
- **TOFU persistence**: Read known hosts from `KnownHostsFile` during `NewSFTPBackend()` construction (before `sync.Once` in `initClient`). Store in `knownHostKey` field. In `buildHostKeyCallback`, when `knownHostKey == ""` and TOFU accepts a key, write it to `KnownHostsFile` (default: `~/.ssh/known_hosts_athena` or configurable). Use `os.OpenFile` with `O_CREATE|O_APPEND`. **Critical:** The known_hosts file must be read at construction time, not inside `initClient`, because `sync.Once` means `initClient` only runs once — if it reads from file inside `sync.Once`, later file writes won't be visible on reconnection without recreating the backend.
- **Callers:** Search with `Grep` for `NewSFTPBackend(` and `NewS3Backend(` to find all call sites. Update them to pass config structs.

**Definition of Done:**

- [ ] Both constructors accept a config struct instead of positional args
- [ ] All callers updated to use config structs
- [ ] SFTP TOFU writes accepted host keys to a file
- [ ] SFTP TOFU reads known hosts from file on startup
- [ ] Existing backup tests pass
- [ ] `go test ./internal/backup/... -short` passes

**Verify:**

- `go test ./internal/backup/... -short -v`
- `grep -rn "NewSFTPBackend\|NewS3Backend" internal/` — all callers use config struct

---

### Task 7: OAuth Scopes Middleware Flattening

**Objective:** Reduce nesting in RequireScopes by extracting helper functions.

**Dependencies:** None

**Files:**

- Modify: `internal/middleware/oauth_scopes.go` (RequireScopes, line 33)
- Test: `internal/middleware/oauth_scopes_test.go`

**Key Decisions / Notes:**

- Extract `extractTokenScopes(token *jwt.Token) ([]string, error)` — handles token claims parsing, scope type switching (string vs []interface{})
- Extract `hasAllScopes(tokenScopes []string, requiredScopes []string) bool` — checks scope presence with admin bypass
- Main middleware becomes: extract scopes → check scopes → store in context. ~15 lines.

**Definition of Done:**

- [ ] RequireScopes main function body < 20 lines
- [ ] Helper functions `extractTokenScopes` and `hasAllScopes` extracted
- [ ] Existing middleware tests pass
- [ ] `go test ./internal/middleware/... -short` passes

**Verify:**

- `go test ./internal/middleware/... -short -v`

---

### Task 8: Avatar Handler Debug Logging Cleanup

**Objective:** Remove or convert debug `log.Printf` calls to structured logging.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/auth/avatar.go` (lines 54, 59-63, 70)

**Key Decisions / Notes:**

- Line 54: `log.Printf("UploadAvatar handler called")` — remove entirely (not useful in production)
- Lines 59-63: Panic recovery logging — keep but convert to `slog.Error(...)` structured logging
- Line 70: `log.Printf("Avatar upload: No user ID in context")` — convert to `slog.Warn("avatar upload: no user ID in context")`
- Check if `"log/slog"` is already imported; if not, add it and remove `"log"` if no other callers.

**Definition of Done:**

- [ ] No `log.Printf` calls remain in avatar.go
- [ ] Panic recovery uses `slog.Error`
- [ ] Missing user ID uses `slog.Warn`
- [ ] Existing tests pass
- [ ] `go test ./internal/httpapi/handlers/auth/... -short` passes

**Verify:**

- `go test ./internal/httpapi/handlers/auth/... -short -v`
- `grep -n 'log.Printf' internal/httpapi/handlers/auth/avatar.go` — should return nothing

---

### Task 9: Security - CSRF Protection in Setup Wizard

**Objective:** Add CSRF token verification to setup wizard POST forms.

**Dependencies:** None

**Files:**

- Modify: `internal/setup/wizard.go` (form handlers)
- Modify: `internal/setup/server.go` (middleware registration)
- Modify: Setup wizard HTML templates (add hidden CSRF token fields)
- Test: `internal/setup/wizard_test.go`

**Key Decisions / Notes:**

- Generate a random CSRF token per wizard session (store in `Wizard` struct, generated at construction time with `crypto/rand`).
- Add `csrfToken` to template data. Templates include `<input type="hidden" name="_csrf_token" value="{{.CSRFToken}}">` in all forms.
- Each POST handler validates `r.FormValue("_csrf_token")` matches stored token. Return 403 on mismatch.
- No external dependency needed — simple token comparison with `crypto/subtle.ConstantTimeCompare`.
- The wizard is single-user (one admin during setup), so a single token per wizard instance is sufficient.

**Definition of Done:**

- [ ] CSRF token generated at wizard creation
- [ ] All POST form handlers validate CSRF token
- [ ] HTML templates include hidden CSRF token field
- [ ] POST without `_csrf_token` returns 403 Forbidden
- [ ] POST with wrong `_csrf_token` returns 403 Forbidden
- [ ] POST with correct `_csrf_token` returns 2xx (normal flow)
- [ ] Token comparison uses `crypto/subtle.ConstantTimeCompare`
- [ ] `go test ./internal/setup/... -short` passes

**Verify:**

- `go test ./internal/setup/... -short -v`

---

### Task 10: Security - SSRF Hardening

**Objective:** Add URL scheme validation to yt-dlp wrapper and fix DNS rebinding in SSRF validator.

**Dependencies:** None

**Files:**

- Modify: `internal/importer/ytdlp.go` (ValidateURL, ExtractMetadata, Download)
- Modify: `internal/security/validation.go` (IsSSRFSafeURL)
- Test: `internal/importer/ytdlp_test.go`, `internal/security/validation_test.go`

**Key Decisions / Notes:**

- **yt-dlp SSRF** (ytdlp.go): Before passing URL to yt-dlp, validate scheme is http/https only (reject `file://`, `ftp://`, etc.). Also call `security.IsSSRFSafeURL()` to check for private IPs. Add to `ValidateURL`, `ExtractMetadata`, and `Download`.
- **DNS rebinding** (validation.go:107-114): Current approach does `time.Sleep(DNSRebindDelay)` then re-resolves. This doesn't prevent the actual HTTP client from resolving to a different IP. Fix: export `NewSSRFSafeHTTPClient()` that returns an `*http.Client` with a custom `Transport` whose `DialContext` resolves the hostname, validates the resolved IPs against the SSRF blocklist, then connects to the validated IP directly. This is the standard Go pattern for IP pinning. Remove the sleep-and-re-resolve approach.

**Definition of Done:**

- [ ] yt-dlp wrapper rejects non-http/https URLs
- [ ] yt-dlp wrapper calls SSRF validation on target URL
- [ ] `NewSSRFSafeHTTPClient()` exported, returns `*http.Client` with IP-pinning DialContext
- [ ] Sleep-and-re-resolve DNS rebinding approach removed
- [ ] Tests verify file://, ftp://, and private IP rejection
- [ ] `go test ./internal/importer/... ./internal/security/... -short` passes

**Verify:**

- `go test ./internal/importer/... -short -v`
- `go test ./internal/security/... -short -v`

---

### Task 11: Security - Chunk Assembly DoS Protection

**Objective:** Add upper bound to ChunkSize and use streaming instead of `os.ReadFile` in AssembleChunks.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/upload/service.go` (InitiateUpload ~line 56, AssembleChunks ~line 259)
- Test: `internal/usecase/upload/service_test.go`

**Key Decisions / Notes:**

- **ChunkSize bound**: In `InitiateUpload` (line 56), add validation: `if req.ChunkSize <= 0 || req.ChunkSize > MaxChunkSize { return error }`. Set `MaxChunkSize = 64 * 1024 * 1024` (64MB) as a sensible upper bound. The `> 0` check prevents a divide-by-zero at line 64 where ChunkSize is used as a divisor (`expectedChunks = totalSize / chunkSize`).
- **Streaming assembly**: In `AssembleChunks` (line 284), replace `os.ReadFile(chunkPath)` + `finalFile.Write(chunkData)` with `chunkFile, _ := os.Open(chunkPath)` + `io.Copy(finalFile, chunkFile)` + `chunkFile.Close()`. This streams chunk data without loading it all into memory.

**Definition of Done:**

- [ ] ChunkSize validated: must be > 0 AND <= 64MB in InitiateUpload
- [ ] AssembleChunks uses `io.Copy` streaming instead of `os.ReadFile`
- [ ] Test verifies rejection of ChunkSize=0 (divide-by-zero prevention)
- [ ] Test verifies rejection of oversized ChunkSize
- [ ] Existing upload tests pass
- [ ] `go test ./internal/usecase/upload/... -short` passes

**Verify:**

- `go test ./internal/usecase/upload/... -short -v`

---

### Task 12: Test Improvements + Skeleton Cleanup

**Objective:** Add edge case tests for ingestRemoteVideo and clean up redundant skeleton tests.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/activitypub/service_test.go` (clean up skeletons at lines 1207, 1294)
- Modify: `internal/usecase/activitypub/service_extended_test.go` or new test file for ingestion edge cases
- Test: existing test infrastructure in the package

**Key Decisions / Notes:**

- **Skeleton cleanup**: `TestServicePublishComment` (line 1207) and `TestServiceCreateVideoActivity` (line 1294) are redundant with comprehensive tests in `comment_publisher_test.go` and `video_publisher_test.go`. Delete these skeleton functions.
- **ingestRemoteVideo edge cases**: Add tests for: missing `id` field → error, missing video URL → error, invalid duration string → defaults to 0, missing `name` → defaults to "Untitled Remote Video", malformed `published` timestamp → uses current time.
- The function accepts `map[string]interface{}` so test data is easy to construct.

**Definition of Done:**

- [ ] Skeleton tests `TestServicePublishComment` and `TestServiceCreateVideoActivity` removed from service_test.go
- [ ] 5+ edge case tests added for ingestRemoteVideo
- [ ] All ActivityPub tests pass
- [ ] `go test ./internal/usecase/activitypub/... -short` passes

**Verify:**

- `go test ./internal/usecase/activitypub/... -short -v`

---

## Deferred Items

| Item | Reason |
|------|--------|
| **Duplicated scanning in video_queries.go** (item 17) | 500+ lines across 10+ methods. Very high blast radius. Needs dedicated spec with careful feature inventory. |
| **IPFS upload streaming** (item 18) | Avatar files are small (<10MB). Memory cost is negligible. Would only matter for large file uploads which already use chunked upload. |
| **IOTA transaction-based detection** (items 29, 30) | Requires deep understanding of IOTA Rebased SDK and `iota_getTransactionBlock` RPC. Should be a separate spec with dedicated research phase. |

## Testing Strategy

- **Unit tests:** Each task has package-level tests. Run with `go test ./internal/<package>/... -short`.
- **Integration tests:** N+1 query fixes (Task 1) ideally verified with integration tests against test DB, but unit tests with sqlmock are sufficient for correctness.
- **Manual verification:** Run `make validate-all` after all tasks complete.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Repository interface changes break mocks | Medium | Medium | Grep all mock implementations before changing interfaces. Update mocks in same commit. |
| Batch SQL queries with empty inputs | Low | High | All batch operations handle empty input gracefully (return nil/empty, no query executed) |
| CSRF token breaks existing wizard tests | Medium | Low | Update test helpers to include CSRF token in POST requests |
| SSRF SafeDialer breaks legitimate requests | Low | High | Only apply to yt-dlp and explicitly SSRF-protected code paths, not globally |
| Backup config struct changes break callers | Medium | Medium | Find ALL callers with Grep before changing. Update in same commit. |

## Goal Verification

### Truths

- N+1 query patterns eliminated from 4 identified locations
- User-agent detection in analytics correctly identifies browsers
  mid-string (not just prefix)
- Setup wizard POST forms are protected against CSRF
- yt-dlp URL import rejects file:// and private IP addresses
- Chunk uploads reject oversized ChunkSize values
- SFTP known hosts survive server restarts

### Artifacts

- `internal/repository/views_repository.go` — single UNNEST/CTE query in BatchIncrementVideoViews
- `internal/security/validation.go` — SafeDialer or SSRFSafeHTTPClient with IP pinning
- `internal/setup/wizard.go` — CSRF token generation and validation
- `internal/backup/sftp.go` — SFTPConfig struct + persistent known_hosts file

### Key Links

- `IsSSRFSafeURL` resolved IPs → `SafeDialer` connection pinning
- Setup wizard CSRF token in struct → template data → hidden form field → POST validation
- `NewSFTPBackend(SFTPConfig)` → `buildHostKeyCallback` reads `KnownHostsFile`

## Open Questions

- None — all items have clear implementations based on codebase exploration.
