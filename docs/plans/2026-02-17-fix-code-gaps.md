# Fix Code Gaps Implementation Plan

Created: 2026-02-17
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

**Goal:** Fix security bugs, auth wiring issues, and code quality gaps across the Vidra Core codebase — covering P0 auth wiring, P1 security/ownership gaps, P2 stubbed features, and numerous code-quality improvements.

**Architecture:** Surgical fixes across existing files. No new packages or architectural changes. Groups related fixes by subsystem.

**Tech Stack:** Go (Chi router), PostgreSQL, Redis, IPFS, ActivityPub, ATProto

## Scope

### In Scope

- P0: Auth middleware wiring on feature routes (waiting room, chat, categories, analytics)
- P0: Banned user authentication bypass (Login + RefreshToken)
- P1: ATProto social route auth binding (follower_did/actor_did from JWT, not client)
- P1: IPFS backend stubs (Download, Delete, Exists)
- P2: Readiness endpoint reflecting actual component health
- P2: ActivityPub inbox GET returning proper empty collection
- P2: Redis backup restore stub
- Security: SHA-512 digest verification bug
- Security: DoS via 512MB multipart allocation
- Concurrency: Goroutine leak in RateLimit helper, scheduler blocking, single-threaded view worker
- Code quality: Dead code removal, DRY refactors, hardcoded backup path
- Tests: Torrent prioritization, torrent tracker, torrent manager, imageutil, stream manager, Redis session repo
- Performance: N+1 chat moderator check, view counter contention, subscriber notification scalability

### Out of Scope

- IOTA transaction build/sign/submit (Phase 2 — requires IOTA Rebased SDK maturity)
- ATProto token lifecycle (requires ATProto session/refresh token protocol implementation — separate spec)
- Config struct refactoring (200+ fields, touches all call sites — separate spec)
- FFmpeg var_stream_map fragility (needs FFmpeg behavior investigation for single-stream inputs)

## Prerequisites

- Go 1.24 toolchain
- golangci-lint installed
- `make validate-all` passing before starting

## Context for Implementer

- **Patterns to follow:** Handler registration in `internal/httpapi/routes.go:400-540` shows correct JWT middleware wrapping. Use `middleware.Auth(cfg.JWTSecret)` on router groups, then `middleware.RequireAuth` inside.
- **Conventions:** Error wrapping with `fmt.Errorf("context: %w", err)`. Domain errors from `internal/domain/errors.go`. Response envelope via `shared.WriteJSON`/`shared.WriteError`.
- **Key files:** `internal/httpapi/routes.go` (route wiring), `internal/middleware/auth.go` (JWT/RequireAuth), `internal/httpapi/handlers.go` (Login/RefreshToken/Readiness), `internal/domain/user.go` (User.IsActive field).
- **Gotchas:** `registerExternalFeatureRoutes` at `routes.go:567` mounts routes on the raw router — no JWT middleware wraps them. `middleware.RequireAuth` only reads `UserIDKey` from context but never parses JWT.
- **Domain context:** ATProto social routes accept `follower_did`/`actor_did` from the client payload, which lets any authenticated user act as any DID. These should derive the DID from the JWT-authenticated user.

## Runtime Environment

- **Start infrastructure:** `docker compose up postgres redis -d`
- **Start server:** `make run` (listens on `:8080` by default)
- **Health check:** `curl http://localhost:8080/health` (liveness), `curl http://localhost:8080/ready` (readiness — Task 5 fix)
- **Auth test:** `POST http://localhost:8080/api/v1/users/token` with email/password
- **Integration tests:** `make test` (requires running infra)

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix auth middleware wiring on feature routes
- [x] Task 2: Block banned/deactivated users from authentication
- [x] Task 3: Fix security bugs (SHA-512, DoS upload, error leaks)
- [x] Task 4: ATProto social route auth binding
- [x] Task 5: Fix readiness endpoint to reflect actual health
- [x] Task 6: Implement IPFS backend stubs and ActivityPub inbox GET
- [x] Task 7: Fix concurrency issues (goroutine leak, scheduler, view workers)
- [x] Task 8: Code quality fixes (dead code, DRY, backup path)
- [x] Task 9: Redis backup restore and backup manager DRY refactor
- [x] Task 10: Performance fixes (chat N+1, view counter, subscriber notifications)
- [x] Task 11: Tests — torrent subsystem (prioritization, tracker, manager)
- [x] Task 12: Tests — stream manager, imageutil, Redis session repo

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix Auth Middleware Wiring on Feature Routes

**Objective:** Wrap feature routes (waiting room, chat, categories, analytics) with JWT-parsing middleware so `RequireAuth` can read the user ID from context.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go` (registerExternalFeatureRoutes function, line 567)
- Modify: `internal/httpapi/handlers/messaging/chat_handlers.go` (RegisterRoutes, line 47)

**Key Decisions / Notes:**

- `registerExternalFeatureRoutes` at `routes.go:567` mounts routes directly on the raw router with no JWT middleware. `RequireAuth` reads `UserIDKey` from context, but `Auth(cfg.JWTSecret)` is never applied at this level.
- Fix: Wrap `registerExternalFeatureRoutes` call in an `r.Group` that applies `middleware.Auth(cfg.JWTSecret)` — matching the pattern at `routes.go:400-540` where the `/api/v1` group applies auth.
- Chat routes (`chat_handlers.go:47`) register at `/streams/{streamId}/chat` on the raw router. The same JWT middleware wrapping applies.
- Public GET endpoints (ListCategories, GetCategory, GetChatMessages, viewer analytics tracking) should remain outside the auth group.
- The `registerExternalFeatureRoutes` function needs access to `cfg.JWTSecret` — currently it only receives `deps`. Fix: pass `cfg *config.Config` as a second parameter to `registerExternalFeatureRoutes` and update the call site in `RegisterRoutesWithDependencies`.

**Definition of Done:**

- [ ] All routes using `RequireAuth` are wrapped in `Auth(cfg.JWTSecret)` middleware
- [ ] Public read-only endpoints remain accessible without auth
- [ ] `go build ./...` succeeds
- [ ] Existing route tests still pass

**Verify:**

- `go build ./cmd/server/...`
- `go test ./internal/httpapi/... -count=1 -short`

---

### Task 2: Block Banned/Deactivated Users from Authentication

**Objective:** Add `IsActive` check in Login and RefreshToken handlers to prevent banned/deactivated users from obtaining tokens.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers.go` (Login at line 75, RefreshToken at line 307)

**Key Decisions / Notes:**

- Login handler (`handlers.go:75`): After password verification (line 101) and before generating JWT (line 123), check `dUser.IsActive`. If false, return 403 with "account deactivated".
- RefreshToken handler (`handlers.go:307`): After retrieving existing token (line 324), look up the user by `existing.UserID` and check `IsActive`. If false, revoke the refresh token and return 403.
- User model has `IsActive bool` at `internal/domain/user.go:19`.
- The `s.userRepo` is already available in the Server struct.

**Definition of Done:**

- [ ] Login returns 403 when `IsActive` is false
- [ ] RefreshToken returns 403 when `IsActive` is false and revokes the token
- [ ] Tests cover both active and deactivated user scenarios
- [ ] Existing auth tests still pass

**Verify:**

- `go test ./internal/httpapi/... -run "Login|RefreshToken" -count=1 -v`

---

### Task 3: Fix Security Bugs (SHA-512, DoS Upload, Error Leaks)

**Objective:** Fix SHA-512 hash mismatch, reduce multipart memory allocation, and prevent internal error leaking.

**Dependencies:** None

**Files:**

- Modify: `internal/activitypub/httpsig.go` (verifyDigest at line 161)
- Modify: `internal/httpapi/handlers/video/upload_handlers.go` (UploadVideoFileHandler at line 241)
- Modify: `internal/httpapi/handlers/social/social.go` (Follow/Unfollow error responses)

**Key Decisions / Notes:**

- **SHA-512 bug:** `httpsig.go:166-167` handles "SHA-512" case but calls `sha256.Sum256` instead of `sha512.Sum512`. Fix: import `crypto/sha512` and use `sha512.Sum512(body)`.
- **DoS upload:** `upload_handlers.go:241` uses `r.ParseMultipartForm(512 << 20)` = 512MB per request. Reduce to `32 << 20` (32MB) — aligns with the chunked upload design where chunks are 32MB max.
- **Error leaks in social.go:** `Follow` (line 102) and `Unfollow` (line 120) pass raw `err` to `WriteError`, leaking internal details. Replace with generic messages.

**Definition of Done:**

- [ ] SHA-512 case uses `sha512.Sum512` from `crypto/sha512`
- [ ] Upload multipart allocation reduced to 32MB
- [ ] Social handler errors use generic messages, not raw errors
- [ ] Tests for SHA-512 verification cover both SHA-256 and SHA-512

**Verify:**

- `go test ./internal/activitypub/... -run "Digest|Sig" -count=1 -v`
- `go build ./cmd/server/...`

---

### Task 4: ATProto Social Route Auth Binding

**Objective:** Require authentication on mutating social routes and derive user identity from JWT instead of accepting it from client payload.

**Dependencies:** Task 1 (auth middleware wiring)

**Files:**

- Modify: `internal/httpapi/handlers/social/social.go` (RegisterRoutes at line 29, Follow at line 95, Unfollow at line 111)

**Key Decisions / Notes:**

- Currently all `/social` routes are public — no auth middleware. Mutating operations (Follow, Unfollow, Like, Unlike, Comment, DeleteComment, ApplyLabel, RemoveLabel, IngestFeed) need `RequireAuth`.
- Read-only endpoints (GetActor, GetActorStats, GetFollowers, GetFollowing, GetLikes, GetComments, GetCommentThread, GetLabels) can remain public.
- `Follow` at line 90 accepts `follower_did` from request body. Note: `domain.User` has no ATProto DID field — DID lives only on `domain.Channel`. Since there's no user→DID mapping, the fix for now: read the authenticated user's UUID from `middleware.GetUserIDFromContext` and use it as the identity string for social operations (replacing `follower_did`). The social service internally resolves the user's channel DID if needed. Do NOT attempt to derive a DID from JWT — just use the UUID.
- `Unfollow` at line 113 accepts `follower_did` as query param — same fix: use authenticated user's UUID, ignore query param.
- Pattern: Split routes into public group and authed group within the `/social` route.

**Definition of Done:**

- [ ] Mutating social endpoints require authentication
- [ ] Follow/Unfollow derive user identity from JWT, not client payload
- [ ] Read-only endpoints remain public
- [ ] Tests verify auth requirement on mutating endpoints

**Verify:**

- `go test ./internal/httpapi/handlers/social/... -count=1 -v`
- `go build ./cmd/server/...`

---

### Task 5: Fix Readiness Endpoint to Reflect Actual Health

**Objective:** Return HTTP 503 with `status: "not_ready"` when any component check fails, instead of always returning 200 with `status: "ready"`.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers.go` (ReadinessCheck at line 392)

**Key Decisions / Notes:**

- Currently `ReadinessCheck` at line 433 always sets `Status: generated.Ready` and returns HTTP 200 regardless of component health.
- Fix: After checking DB, Redis, IPFS, if any **non-nil** component's status is "unhealthy", set overall status to `generated.NotReady` and return HTTP 503. Important: nil component = not configured = skip check (preserve existing behavior). Only flip to unhealthy when component is non-nil AND its health check fails.
- Kubernetes/orchestrators use 503 to mark pods as not ready for traffic.

**Definition of Done:**

- [ ] Returns 503 when any component is unhealthy
- [ ] Returns 200 only when all checked components are healthy
- [ ] Test covers healthy and unhealthy scenarios

**Verify:**

- `go test ./internal/httpapi/... -run "Readiness" -count=1 -v`

---

### Task 6: Implement IPFS Backend Stubs and ActivityPub Inbox GET

**Objective:** Replace IPFS backend error stubs with real implementations using the IPFS HTTP API. Return proper empty OrderedCollection for ActivityPub inbox GET.

**Dependencies:** None

**Files:**

- Modify: `internal/storage/ipfs_backend.go` (Download at line 60, Delete at line 80, Exists at line 87, Upload at line 40)
- Modify: `internal/ipfs/client.go` (add Cat, Unpin, IsPinned methods wrapping `/api/v0/cat`, `/api/v0/pin/rm`, `/api/v0/pin/ls`)
- Modify: `internal/httpapi/handlers/federation/activitypub.go` (GetInbox at line 234)

**Key Decisions / Notes:**

- **IPFS Download:** Use `client.CatFile(ctx, cid)` or equivalent IPFS HTTP API cat endpoint (`/api/v0/cat?arg=<cid>`) to return an `io.ReadCloser`.
- **IPFS Delete:** Use `client.Unpin(ctx, cid)` — note: unpinning doesn't guarantee deletion in IPFS's distributed nature, but it removes local pinning.
- **IPFS Exists:** Use `client.IsPinned(ctx, cid)` to check if CID is pinned locally.
- **IPFS Upload (io.Reader):** Use `client.Add(ctx, reader)` via IPFS HTTP API `/api/v0/add`.
- Check what methods the `i.client` interface provides — it's from `internal/ipfs/` package.
- **ActivityPub Inbox GET:** Return proper empty `OrderedCollection` JSON instead of plain text 501. This is valid ActivityPub behavior — inbox content is typically not exposed via GET for privacy.

**Definition of Done:**

- [ ] IPFS Download returns content via gateway or cat API
- [ ] IPFS Delete unpins the CID
- [ ] IPFS Exists checks pin status
- [ ] IPFS Upload accepts io.Reader
- [ ] ActivityPub inbox GET returns proper empty OrderedCollection with 200
- [ ] Tests cover IPFS operations (mocked client)

**Verify:**

- `go test ./internal/storage/... -count=1 -v`
- `go test ./internal/httpapi/handlers/federation/... -run "Inbox" -count=1 -v`
- `go build ./cmd/server/...`

---

### Task 7: Fix Concurrency Issues (Goroutine Leak, Scheduler, View Workers)

**Objective:** Fix goroutine leak in RateLimit helper, make backup scheduler non-blocking, and add multiple view tracking workers.

**Dependencies:** None

**Files:**

- Modify: `internal/middleware/ratelimit.go` (RateLimit function at line 220)
- Modify: `internal/middleware/ratelimit_test.go` (update callers of RateLimit)
- Modify: `internal/backup/scheduler.go` (Start method at line 56)
- Modify: `internal/usecase/views/service.go` (NewService at line 27, worker at line 46)

**Key Decisions / Notes:**

- **Goroutine leak:** `RateLimit()` at line 220 creates a `NewRateLimiter` (which spawns a cleanup goroutine at line 51) but the returned `*RateLimiter` is never stored — only the middleware closure is returned. The goroutine runs forever since `done` channel is never closed. Fix: Change `RateLimit()` to return `*RateLimiter` directly (callers use `rl.Limit` as middleware). Grep all callers and update them to store the `*RateLimiter` and call `rl.Shutdown()` on teardown. Add `internal/app/app.go` to store rate limiters during shutdown.
- **Scheduler blocking:** `scheduler.go:56` runs `s.runBackup(ctx)` synchronously in the ticker case. If backup takes longer than the tick interval, ticks are missed and shutdown is delayed. Fix: wrap in `go s.runBackup(ctx)` with a guard to prevent concurrent backups (e.g., `sync.Mutex` trylock or `atomic.Bool` flag).
- **Single-threaded view worker:** `service.go:27` spawns only 1 worker goroutine. Under load, the channel (buffer 1000) fills up. Fix: Spawn a configurable number of workers (default 4). Adjust `workerWG.Add(numWorkers)`.

**Definition of Done:**

- [ ] `RateLimit()` returns `*RateLimiter` so callers can call `Shutdown()`
- [ ] Backup scheduler runs backups in a goroutine with concurrent-run guard
- [ ] View service spawns multiple workers (configurable, default 4)
- [ ] Tests verify concurrent backup guard prevents double-run
- [ ] Tests verify multiple view workers process tasks

**Verify:**

- `go test ./internal/middleware/... -run "RateLimit" -count=1 -v`
- `go test ./internal/backup/... -run "Scheduler" -count=1 -v`
- `go test ./internal/usecase/views/... -count=1 -v`

---

### Task 8: Code Quality Fixes (Dead Code, DRY, Backup Path)

**Objective:** Remove dead code, extract hardcoded backup path constant, and clean up minor issues.

**Dependencies:** None

**Files:**

- Modify: `internal/livestream/hls_transcoder.go` (remove CleanupOldSegments at line 381)
- Modify: `cmd/cli/main.go` (extract backup path constant, line 132)
- Modify: `internal/config/object_storage_test.go` (improve clearObjectStorageEnvVars at line 170)

**Key Decisions / Notes:**

- **CleanupOldSegments:** No-op function at `hls_transcoder.go:381-392`. Only called from within the same file — check for external references first. If no callers, remove entirely.
- **Backup path:** `./backups` is hardcoded at `cli/main.go:134`. Check for other occurrences. Extract to `const defaultBackupPath = "./backups"`.
- **clearObjectStorageEnvVars:** Manual list at `object_storage_test.go:170`. Use `os.Environ()` filtering with `S3_` prefix to clear all S3-related env vars dynamically, avoiding drift when struct fields change.

**Definition of Done:**

- [ ] CleanupOldSegments removed (or kept if interface requires it)
- [ ] Backup path uses a named constant
- [ ] Object storage env var cleanup uses `S3_` prefix filtering instead of manual list
- [ ] All tests pass

**Verify:**

- `go build ./cmd/...`
- `go test ./internal/livestream/... -count=1 -short`
- `go test ./internal/config/... -count=1 -v`
- `go test ./cmd/cli/... -count=1 -short`

---

### Task 9: Redis Backup Restore and Backup Manager DRY Refactor

**Objective:** Implement Redis restore and deduplicate dumpDatabase/dumpRedis logic.

**Dependencies:** None

**Files:**

- Modify: `internal/backup/restore.go` (restoreRedis at line 295)
- Modify: `internal/backup/manager.go` (dumpDatabase at line 159, dumpRedis at line 180)

**Key Decisions / Notes:**

- **Redis restore:** `restoreRedis` at `restore.go:295-298` is a stub returning `nil`. Implement by: (1) determine Redis data dir via `redis-cli -u <url> CONFIG GET dir`, (2) copy the RDB file to that directory as `dump.rdb`, (3) return an error instructing the operator to restart Redis to load the file. Do NOT use `DEBUG LOADRDB` (restricted in Redis 7.x). If `redis-cli` is not found in PATH, return `domain.ErrServiceUnavailable`.
- **DRY refactor:** `dumpDatabase` (line 159) and `dumpRedis` (line 180) share identical structure: create context with timeout → exec command → check error → stat output file → check size. Extract a `runDumpCommand(ctx, cmdName, args, outputPath, timeout)` helper.

**Definition of Done:**

- [ ] `restoreRedis` copies the RDB file to the Redis data directory (via `CONFIG GET dir`), returns `domain.ErrServiceUnavailable` if `redis-cli` is not found in PATH, and logs a warning instructing operator to restart Redis
- [ ] `dumpDatabase` and `dumpRedis` use shared helper function
- [ ] Tests verify dump helper works with mock commands
- [ ] Existing backup tests still pass

**Verify:**

- `go test ./internal/backup/... -count=1 -v`
- `go build ./cmd/...`

---

### Task 10: Performance Fixes (Chat N+1, View Counter, Subscriber Notifications)

**Objective:** Cache moderator status to fix chat N+1 query, batch view counter updates, and paginate subscriber notification generation.

**Dependencies:** None

**Files:**

- Modify: `internal/chat/websocket_server.go` (checkRateLimit at line 414)
- Modify: `internal/usecase/views/service.go` (processViewTask/IncrementVideoViews at line 149)
- Modify: `internal/usecase/notification/service.go` (CreateVideoNotificationForSubscribers at line 35)

**Key Decisions / Notes:**

- **Chat N+1:** `checkRateLimit` at `websocket_server.go:415` calls `s.chatRepo.IsModerator(ctx, streamID, userID)` on every message. Fix: cache moderator status in a `sync.Map` with 60-second TTL, keyed by `streamID:userID`. TTL-based expiry only (no cross-component invalidation — AddModerator/RemoveModerator live in HTTP handlers with no signal path to WebSocket server). 60-second staleness is acceptable per risk assessment.
- **View counter contention:** `IncrementVideoViews` at `service.go:149` runs inside the async worker but causes DB row-level lock contention on popular videos. Fix: add a `viewCounts map[string]int64` buffer with `sync.Mutex` and a separate flush goroutine (every 10 seconds). The flush goroutine batches all accumulated counts into a single UPDATE per video. `Close()` must flush the buffer before returning. This requires ~30 lines of new code for buffer + flush + shutdown.
- **Subscriber notifications:** `GetSubscribers` at `notification/service.go:43` fetches all subscribers at once. Fix: add pagination — fetch in batches of 500, create notifications in batches, use a cursor-based approach.

**Definition of Done:**

- [ ] Moderator status cached with 60-second TTL (no cross-component invalidation needed)
- [ ] View counter uses buffered batch updates
- [ ] Subscriber notification uses paginated fetch (batches of 500)
- [ ] Tests verify caching behavior and batch flush

**Verify:**

- `go test ./internal/chat/... -count=1 -v`
- `go test ./internal/usecase/views/... -count=1 -v`
- `go test ./internal/usecase/notification/... -count=1 -v`

---

### Task 11: Tests — Torrent Subsystem (Prioritization, Tracker, Manager)

**Objective:** Add unit tests for torrent prioritization strategies, WebTorrent tracker, and torrent manager.

**Dependencies:** None

**Files:**

- Create: `internal/torrent/seeder_test.go` (prioritization tests)
- Create: `internal/torrent/tracker_test.go` (tracker tests)
- Create: `internal/torrent/manager_test.go` (manager tests)

**Key Decisions / Notes:**

- **Prioritization tests:** `PopularityPrioritizer` and `FIFOPrioritizer` at `seeder.go:555-595` are pure functions. Test with various input slices: empty, single torrent, multiple with different seeders/leechers/age.
- **Tracker tests:** WebTorrent tracker at `tracker.go:20` handles WebSocket signaling. Mock WebSocket connections using `gorilla/websocket` test utilities. Test announce, scrape, and swarm management.
- **Manager tests:** `manager.go:20` coordinates seeder, client, tracker. Heavy mocking needed for file system, database, sub-components. Focus on coordination logic: generate → seed → track lifecycle.

**Definition of Done:**

- [ ] Prioritization tests cover PopularityPrioritizer and FIFOPrioritizer
- [ ] Tracker tests cover announce and scrape protocol
- [ ] Manager tests cover generate/seed/track lifecycle
- [ ] All tests pass with `go test ./internal/torrent/... -count=1`

**Verify:**

- `go test ./internal/torrent/... -count=1 -v`

---

### Task 12: Tests — Stream Manager, Imageutil, Redis Session Repo

**Objective:** Add unit tests for stream manager, imageutil package, and Redis session repository.

**Dependencies:** None

**Files:**

- Create: `internal/livestream/stream_manager_test.go`
- Modify: `pkg/imageutil/imageutil_test.go` (replace placeholder)
- Create: `internal/repository/auth_session_redis_test.go`

**Key Decisions / Notes:**

- **Stream manager:** `stream_manager.go:18` manages live stream state, viewer counting, heartbeats. Mock repositories (interfaces) and Redis client. Test start/stop stream, viewer join/leave, heartbeat timeout.
- **Imageutil:** `imageutil_test.go` is a placeholder. Test image processing functions using `testutil.CreateTestPNG/JPEG/WebP()` helpers. Focus on wrapper logic around imaging operations.
- **Redis session repo:** `auth_session_redis.go:13` implements session storage. Use `go-redis/redismock` or `miniredis` for testing. Test CRUD operations: create session, get session, delete session, token expiry.

**Definition of Done:**

- [ ] Stream manager tests cover start/stop, viewer management, heartbeat
- [ ] Imageutil tests cover actual image processing functions
- [ ] Redis session tests cover CRUD and expiry behavior
- [ ] All tests pass with `-short` flag (skip integration if no infra)

**Verify:**

- `go test ./internal/livestream/... -run "StreamManager" -count=1 -v`
- `go test ./pkg/imageutil/... -count=1 -v`
- `go test ./internal/repository/... -run "AuthSession" -count=1 -v`

---

## Testing Strategy

- **Unit tests:** Each task includes specific test requirements. Table-driven tests per Go convention.
- **Integration tests:** Guarded by `testing.Short()`. DB/Redis-dependent tests skip in CI when infra unavailable.
- **Manual verification:** `make validate-all` after all tasks complete.

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Auth middleware wiring breaks existing public routes | Med | High | Split routes into public and authed groups explicitly; test public endpoints remain accessible |
| RateLimit API change breaks callers | Low | Med | Check all callers of `RateLimit()` function; update call sites to store returned `*RateLimiter` |
| View counter batch flush loses counts on crash | Med | Low | Flush on graceful shutdown via `Close()` method; accept best-effort for crash scenarios |
| IPFS client interface may not expose needed methods | Med | Med | Read `internal/ipfs/` client interface before implementing; fall back to HTTP API calls if methods missing |
| Moderator cache serves stale data | Low | Low | 60-second TTL limits staleness; force-invalidate on moderator add/remove operations |

## Open Questions

- None — all issues verified by reading source code.

### Deferred Ideas

- IOTA transaction build/sign/submit (Phase 2 of IOTA Rebased integration)
- ATProto proper session/token lifecycle (separate spec needed)
- Config struct decomposition (200+ fields "God Class" — major refactor)
- FFmpeg var_stream_map single-stream handling investigation
- Manual environment variable list reflection-based approach (explored in Task 8 as quick fix)
