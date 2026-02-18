# Fix Broken User Stories Implementation Plan

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

**Goal:** Fix 16 broken user stories across the codebase where handlers, routes, services, or app lifecycle wiring are disconnected — code exists but doesn't function end-to-end.

**Architecture:** Minimal wiring fixes — the handlers, services, and repositories already exist. The work is connecting them through routes.go, app.go (dependency wiring), and HandlerDependencies. No new business logic needed except for the channel video listing placeholder.

**Tech Stack:** Go, Chi router, existing handler/service/repository code

## Scope

### In Scope

- [P0] Fix livestream handler param mismatches (CreateStream, RotateStreamKey)
- [P0] Implement channel video listing service method (currently returns placeholder)
- [P1] Wire payment worker into app lifecycle
- [P1] Mount chat routes in routes.go
- [P1] Mount waiting room routes and start stream scheduler in app lifecycle
- [P1] Mount plugin admin routes and wire dependencies
- [P1] Mount social (ATProto follow/like/feed) routes and wire dependencies
- [P1] Mount caption generation routes and wire captiongen service into encoding
- [P1] Mount redundancy admin routes and wire dependencies
- [P1] Mount stream analytics routes and wire dependencies
- [P1] Mount video category routes and wire dependencies
- [P2] Fix readiness check to actually ping database
- [P2] Align test bootstrap schema with E2EE migration columns

### Out of Scope

- IPFS backend stub implementations (Upload, Download, Delete, Exists) — these require IPFS protocol design decisions
- OpenAPI spec / README documentation updates — separate documentation task
- New feature development — only wiring existing code
- Secure messaging items (handled in prior pass)

## Prerequisites

- Existing Go build passes (`make build`)
- Existing tests pass (`make test`)

## Context for Implementer

> This section is critical for cross-session continuity.

- **Patterns to follow:** Route mounting follows two patterns: (1) inline in `RegisterRoutesWithDependencies` in `routes.go` (most routes), (2) handler's own `RegisterRoutes(r)` method called from routes.go (chat, social, redundancy, analytics, categories)
- **Conventions:** Dependencies flow: `app.go:initializeDependencies()` creates services → `app.go:registerRoutes()` passes them to `shared.HandlerDependencies` → `routes.go:RegisterRoutesWithDependencies()` creates handlers from deps
- **Key files:**
  - `internal/httpapi/routes.go` — All route mounting
  - `internal/app/app.go` — Dependency wiring and app lifecycle
  - `internal/httpapi/shared/dependencies.go` — `HandlerDependencies` struct
  - `internal/httpapi/handlers/` — Handler implementations
- **Gotchas:**
  - `CreateStream` handler reads `chi.URLParam(r, "channelId")` but route provides no such param — handler must be changed to accept channelID in request body only (not URL)
  - `RotateStreamKey` handler reads `chi.URLParam(r, "channelId")` but route provides `{id}` — handler must read stream ID from `{id}` and look up channelID from the stream
  - Several handlers have their own `RegisterRoutes` method that mounts routes with full paths (e.g., `/api/v1/streams/{streamId}/chat`). These should be called at the top-level router, not nested under `/api/v1`
  - Chat handlers need `ChatServer`, `ChatRepository`, and `SubscriptionRepository` — not currently in deps
  - Social handlers need `SocialService` and `SocialRepository` — not currently in deps

## Progress Tracking

**MANDATORY: Update this checklist as tasks complete. Change `[ ]` to `[x]`.**

- [x] Task 1: Fix livestream handler param mismatches (P0)
- [x] Task 2: Implement channel video listing (P0)
- [x] Task 3: Wire chat routes and dependencies (P1)
- [x] Task 4: Wire waiting room routes and stream scheduler (P1)
- [x] Task 5: Wire payment worker into app lifecycle (P1)
- [x] Task 6: Wire social routes and dependencies (P1)
- [x] Task 7: Wire caption generation routes and encoding integration (P1)
- [x] Task 8: Wire plugin admin routes and dependencies (P1)
- [x] Task 9: Wire redundancy admin routes and dependencies (P1)
- [x] Task 10: Wire stream analytics and video category routes (P1)
- [x] Task 11: Fix readiness check database probe (P2)
- [x] Task 12: Align test schema with E2EE migrations (P2)

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Fix livestream handler param mismatches (P0)

**Objective:** Fix CreateStream and RotateStreamKey handlers so their parameter expectations match the route definitions.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/livestream/livestream_handlers.go`
- Modify: `internal/httpapi/handlers/livestream/livestream_handlers_test.go`

**Key Decisions / Notes:**

- `CreateStream` (line 100): Currently reads `chi.URLParam(r, "channelId")`. Route is `POST /api/v1/streams/` (no channelId in path). Fix: Remove URL param read, rely solely on `req.ChannelID` from request body. Remove the channelID mismatch check (lines 123-126) since there's no URL param to compare against.
- `RotateStreamKey` (line 573): Currently reads `chi.URLParam(r, "channelId")`. Route is `POST /api/v1/streams/{id}/rotate-key`. Fix: Read stream `{id}` from URL, look up the stream to get its channelID, then verify ownership.
- Follow existing handler patterns in the same file (e.g., `GetStream`, `UpdateStream` which correctly use `chi.URLParam(r, "id")`)

**Definition of Done:**

- [ ] CreateStream reads channelID only from request body, not URL params
- [ ] RotateStreamKey reads stream ID from `{id}` URL param and looks up channel from stream record
- [ ] All existing livestream handler tests pass
- [ ] New/updated tests cover the fixed parameter reading

**Verify:**

- `go test ./internal/httpapi/handlers/livestream/... -v -count=1`

### Task 2: Implement channel video listing (P0)

**Objective:** Replace the placeholder `GetChannelVideos` implementation with actual database queries.

**Dependencies:** None

**Files:**

- Modify: `internal/usecase/channel/service.go`
- Modify: `internal/usecase/channel/service_test.go`
- Modify: `internal/port/channel.go` (if `GetVideosByChannelID` not in interface)
- Modify: `internal/repository/channel_repository.go` (if method missing)

**Key Decisions / Notes:**

- Current implementation at `service.go:125` returns hardcoded empty results
- The `VideoRepository` already has query capabilities — check if `GetVideosByChannelID` exists, otherwise check if we can use `ListByChannel` or similar
- Follow the pagination pattern from the existing `domain.VideoListResponse` struct
- The video repository is not currently injected into ChannelService — may need to add it

**Definition of Done:**

- [ ] `GetChannelVideos` queries the database and returns real video data
- [ ] Pagination (page, pageSize) is correctly applied
- [ ] Returns empty list (not error) when channel has no videos
- [ ] Tests verify actual query behavior with mocked repository

**Verify:**

- `go test ./internal/usecase/channel/... -v -count=1`

### Task 3: Wire chat routes and dependencies (P1)

**Objective:** Mount live chat routes so viewers and moderators can join/moderate stream chat.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `ChatHandlers` needs: `ChatServer`, `ChatRepository`, `LiveStreamRepository`, `UserRepository`, `SubscriptionRepository`
- `ChatHandlers.RegisterRoutes(r)` mounts routes at `/streams/{streamId}/chat/...` — call within the `/api/v1` route group
- Need to create `chat.ChatServer` in app.go and add `ChatRepo` (from `repository.NewChatRepository(db)`) to deps
- Check if `ChatRepository` exists in repository package — the handler imports `repository.ChatRepository`
- The `RequireAuth` middleware used in chat handlers needs to match `middleware.Auth(cfg.JWTSecret)` pattern

**Definition of Done:**

- [ ] Chat routes are mounted at `/api/v1/streams/{streamId}/chat/...`
- [ ] ChatServer, ChatRepository added to HandlerDependencies
- [ ] App.go creates ChatServer and ChatRepository
- [ ] Build succeeds with no compilation errors

**Verify:**

- `go build ./cmd/server/...`
- `go test ./internal/httpapi/... -short -count=1`

### Task 4: Wire waiting room routes and stream scheduler (P1)

**Objective:** Mount waiting room routes and start the stream scheduler in app lifecycle.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `WaitingRoomHandler.RegisterWaitingRoomRoutes(r)` mounts at `/api/v1/streams/{streamId}/waiting-room/...` and `/api/v1/streams/{streamId}/schedule/...` — call at top-level router (routes already include `/api/v1` prefix)
- `WaitingRoomHandler` needs `StreamRepository` and `UserRepository` — both available in deps
- `StreamScheduler` from `internal/livestream/scheduler.go` needs `*sqlx.DB` and `NotificationSender` — create in app.go and start in `Start()` method
- Guard with `cfg.EnableLiveStreaming` flag
- The `middleware.RequireAuth` used in waiting room handlers needs to match auth middleware pattern

**Definition of Done:**

- [ ] Waiting room and schedule routes are mounted
- [ ] StreamScheduler is created in app.go and started in `Start()`
- [ ] StreamScheduler is stopped in `Shutdown()`
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 5: Wire payment worker into app lifecycle (P1)

**Objective:** Start the IOTA payment worker when IOTA payments are enabled.

**Dependencies:** None

**Files:**

- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `IOTAPaymentWorker` from `internal/worker/` needs `IOTARepository` and `IOTAClient` — both created in `initializeDependencies()` when `EnableIOTA` is true
- Create worker in `initializeDependencies()`, start in `Start()`, stop in `Shutdown()`
- Use a configurable interval (default 30s) — check if config has a `IOTAPaymentCheckInterval` field, otherwise add to config
- Store worker reference on `Application` struct for shutdown
- Guard with `cfg.EnableIOTA`

**Definition of Done:**

- [ ] IOTAPaymentWorker is created when IOTA is enabled
- [ ] Worker is started in `Start()` with context
- [ ] Worker is stopped in `Shutdown()`
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`
- `go test ./internal/worker/... -v -count=1`

### Task 6: Wire social routes and dependencies (P1)

**Objective:** Mount ATProto social interaction routes (follow, like, feed, moderation).

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `SocialHandler` needs `*usecase.SocialService`
- `SocialService` (alias for `ucsocial.Service`) needs `SocialRepository`, `AtprotoPublisher`, `*config.Config`, and security SSRF guard
- `SocialRepository` exists at `internal/repository/social_repository.go`
- Create `SocialRepository` and `SocialService` in `initializeDependencies()`, guarded by `cfg.EnableATProto`
- `SocialHandler.RegisterRoutes(r)` mounts at `/social/...` — call within `/api/v1` route group
- Add `SocialService` to `HandlerDependencies`

**Definition of Done:**

- [ ] Social routes mounted at `/api/v1/social/...` when ATProto is enabled
- [ ] SocialService and SocialRepository created in app.go
- [ ] SocialService added to HandlerDependencies
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 7: Wire caption generation routes and encoding integration (P1)

**Objective:** Mount caption generation API routes and wire the captiongen service into the encoding service.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `CaptionGenerationHandlers` needs `captiongen.Service` and `VideoRepository`
- `captiongen.NewService()` needs `CaptionGenerationRepository`, `CaptionRepository`, `VideoRepository`, `whisper.Client`, and uploads dir
- Mount routes at `/api/v1/videos/{id}/captions/generate` (POST) and `/api/v1/videos/{id}/captions/jobs` (GET) — add within the existing `/videos/{id}` route group
- Call `encodingService.WithCaptionGenerator(captionGenService)` in app.go to wire auto-generation on encoding completion
- Guard with `cfg.EnableCaptionGeneration` (check if this config field exists)
- Add `CaptionGenService` to `HandlerDependencies`

**Definition of Done:**

- [ ] Caption generation routes are mounted
- [ ] CaptionGenService created in app.go when caption generation is enabled
- [ ] Encoding service has caption generator wired via `WithCaptionGenerator`
- [ ] CaptionGenService added to HandlerDependencies
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 8: Wire plugin admin routes and dependencies (P1)

**Objective:** Mount plugin management admin routes.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `PluginHandler` needs `*repository.PluginRepository`, `*plugin.Manager`, `*plugin.SignatureVerifier`, `requireSignatures bool`
- Mount under `/api/v1/admin/plugins/...` with admin auth middleware
- Check if `PluginRepository` exists in repository package
- Guard with a config flag (check for `EnablePlugins` or similar)
- The simpler `PluginHandlers` (from `handlers.go`) just needs `*config.Config` — check which one to use
- Add relevant deps to `HandlerDependencies`

**Definition of Done:**

- [ ] Plugin admin routes are mounted under `/api/v1/admin/plugins/...`
- [ ] Plugin dependencies wired in app.go
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 9: Wire redundancy admin routes and dependencies (P1)

**Objective:** Mount redundancy management admin routes.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- `RedundancyHandler` needs `RedundancyServiceInterface` and `InstanceDiscoveryInterface`
- `redundancy.NewService()` needs `RedundancyRepository`, `RedundancyVideoRepository`, and `HTTPDoer`
- `redundancy.NewInstanceDiscovery()` likely needs HTTP client
- `RedundancyHandler.RegisterRoutes(r)` mounts at `/api/v1/admin/redundancy/...` and `/api/v1/redundancy/...` — call at top-level (routes include full paths)
- Check if `RedundancyRepository` exists in repository package
- Add `RedundancyService` and `InstanceDiscovery` to `HandlerDependencies`

**Definition of Done:**

- [ ] Redundancy admin routes mounted at `/api/v1/admin/redundancy/...`
- [ ] Redundancy public routes mounted at `/api/v1/redundancy/...`
- [ ] RedundancyService created in app.go
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 10: Wire stream analytics and video category routes (P1)

**Objective:** Mount stream analytics and video category management routes.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`
- Modify: `internal/app/app.go`

**Key Decisions / Notes:**

- **AnalyticsHandler** needs `StreamRepositoryForAnalytics`, `AnalyticsRepository`, `AnalyticsCollectorInterface`
  - `AnalyticsRepository` at `internal/repository/analytics_repository.go`
  - `AnalyticsCollector` at `internal/livestream/analytics_collector.go`
  - `AnalyticsHandler.RegisterRoutes(r)` mounts at `/api/v1/streams/{streamId}/analytics/...` and `/api/v1/analytics/...`
  - Guard with `cfg.EnableLiveStreaming`
- **VideoCategoryHandler** needs `VideoCategoryUseCase`
  - `VideoCategoryUseCase` at `internal/usecase/video_category_usecase.go`
  - `VideoCategoryRepository` at `internal/repository/video_category_repository.go`
  - `RegisterRoutes(r)` mounts at `/api/v1/categories/...` and `/api/v1/admin/categories/...`
  - Always active (categories are a core feature)

**Definition of Done:**

- [ ] Analytics routes mounted when live streaming enabled
- [ ] Video category routes mounted (public + admin)
- [ ] Dependencies wired in app.go
- [ ] Build succeeds

**Verify:**

- `go build ./cmd/server/...`

### Task 11: Fix readiness check database probe (P2)

**Objective:** Make the readiness endpoint actually ping the database instead of always returning healthy.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers.go`

**Key Decisions / Notes:**

- The `Server` struct already has access to a database connection via `s.db` (from `NewServerWithOAuth`)
- Current `ReadinessCheck` at line 387 sets `dbStatus := generated.ReadinessResponseChecksDatabaseHealthy` without checking
- Fix: Add a `db.PingContext(ctx)` with a 2-second timeout, similar to how Redis ping works at line 391-401
- The `Server` struct needs a DB reference — check if it already has one, if not add `*sqlx.DB` to `NewServerWithOAuth`
- Follow the same pattern as the Redis check (timeout, status enum)

**Definition of Done:**

- [ ] ReadinessCheck pings the database with a timeout
- [ ] Returns unhealthy status when database is unreachable
- [ ] Existing health endpoint tests still pass

**Verify:**

- `go test ./internal/httpapi/... -run TestReadiness -v -count=1`

### Task 12: Align test schema with E2EE migrations (P2)

**Objective:** Add E2EE columns to the test bootstrap schema so integration tests can exercise E2EE features.

**Dependencies:** None

**Files:**

- Modify: `internal/testutil/database.go`

**Key Decisions / Notes:**

- Test schema at `database.go:477` creates basic `messages` and `conversations` tables
- Migration `016_add_e2ee_messaging.sql` adds: `encrypted_content`, `content_nonce`, `pgp_signature`, `is_encrypted`, `encryption_version` to messages; `is_encrypted`, `key_exchange_complete`, `encryption_version`, `last_key_rotation` to conversations
- Migration `065_fix_e2ee_schema_contradictions.sql` may have further changes — read it
- Also need to add the `user_master_keys` table and any other tables from migration 016
- Follow the same `CREATE TABLE IF NOT EXISTS` / `ALTER TABLE` pattern used in testutil

**Definition of Done:**

- [ ] Test schema includes all E2EE columns from migrations 016 and 065
- [ ] E2EE-related tables (user_master_keys, etc.) are created in test bootstrap
- [ ] Existing tests still pass with the expanded schema

**Verify:**

- `go test ./internal/testutil/... -v -count=1`
- `go test ./internal/repository/... -short -count=1`

## Testing Strategy

- **Unit tests:** Each task updates or adds tests for the specific handler/service being fixed
- **Integration tests:** Tasks 1-2 have direct test verification. Tasks 3-10 verify via build success (wiring tasks don't need new logic tests — the handlers already have tests)
- **Build verification:** Every task must pass `go build ./cmd/server/...`
- **Final validation:** `make validate-all` after all tasks complete

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Missing dependency interfaces — handler needs a type not in deps struct | Medium | Medium | Each task checks if dependency types exist before modifying deps; add interface to deps struct if missing |
| Circular imports from new wiring | Low | High | Follow existing import patterns; use interface types from port/ package |
| Chat middleware.RequireAuth vs middleware.Auth mismatch | Medium | Low | Verify RequireAuth is an alias or compatible; update chat handler if needed |
| Config flags don't exist for some features | Medium | Low | Check config struct for each flag; add simple bool config fields if missing |
| Test schema changes break existing tests | Low | Medium | Use `IF NOT EXISTS` and `ADD COLUMN IF NOT EXISTS` patterns; run full test suite after |

## Open Questions

- None — all issues are verified against source code

### Deferred Ideas

- IPFS backend full implementation (Upload, Download, Delete, Exists)
- OpenAPI spec synchronization with actual routes
- README parity audit
