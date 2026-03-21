# PeerTube Parity Gaps, OpenAPI & Documentation Update Plan

Created: 2026-03-21
Status: COMPLETE
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Close high and medium priority gaps from the PeerTube parity gap report, ensure comprehensive OpenAPI spec and Postman E2E test coverage for all endpoints, and update all stale documentation with accurate current metrics.

**Architecture:** Three workstreams: (1) Implement ~18 missing endpoints across user blocklist, instance config, video quota, channel avatar/banner, abuse threads, live sessions, and subscription feeds; (2) Audit and update all OpenAPI specs to cover every route in routes.go plus new endpoints; (3) Create comprehensive Postman E2E test collections for all areas and update README/Sprint/CLAUDE.md documentation.

**Tech Stack:** Go 1.24, Chi router, PostgreSQL/SQLX, OpenAPI 3.0 YAML, Postman/Newman JSON collections

## Scope

### In Scope

**Workstream A — High Priority Gap Endpoints:**
1. Public instance config: `GET /api/v1/config` (4h)
2. User video quota: `GET /api/v1/users/me/video-quota-used` (2h)
3. Per-user blocklist: 7 endpoints under `/api/v1/blocklist/` (2 days)
4. Delete user avatar: `DELETE /api/v1/users/me/avatar` (1h)

**Workstream B — Medium Priority Gap Endpoints:**
5. Channel avatar/banner: `POST/DELETE /api/v1/channels/{id}/avatar`, `POST/DELETE /api/v1/channels/{id}/banner` (1 day)
6. Abuse report discussion threads: `GET/POST /api/v1/abuses/{id}/messages`, `DELETE /api/v1/abuses/{id}/messages/{msgId}` (1 day)
7. Live session history: `GET /api/v1/streams/{id}/sessions` (4h)
8. Subscription feed: `GET /feeds/subscriptions.{format}` (4h)
9. Batch notification mark-read: `POST /api/v1/notifications/read` (2h)

**Workstream C — OpenAPI Spec Audit:**
10. Audit all routes.go endpoints against OpenAPI specs — add missing paths
11. Add specs for all Workstream A+B new endpoints
12. Ensure schemas match actual request/response shapes

**Workstream D — Postman E2E Test Collections:**
13. Create missing collections: channels, playlists, feeds, chapters, blacklist, livestreaming, federation, moderation, notifications, blocklist, instance config
14. Add E2E tests for all new endpoints from Workstreams A+B
15. Verify all existing collections are current

**Workstream E — Documentation Accuracy Pass:**
16. Update README.md metrics (test files: 393→actual, test functions: 4383→actual, Go files: 778→actual)
17. Update Sprint README and CLAUDE.md — Quality Programme is 100% complete, not "Sprint 16/20 active"
18. Update gap report with new endpoint statuses
19. Update PROJECT_COMPLETE.md metrics

### Out of Scope

- External runners (entire subsystem, deferred by design)
- Video studio editing (complex frontend feature)
- Storyboards, video password protection, video source replacement
- Server following API (ActivityPub handles federation)
- Jobs admin API (low operational need)
- Plugin marketplace API (config-based)
- User data export, channel syncs
- Account handle-based routes (`/accounts/{name}/*`) — different URL scheme, medium effort, can be a follow-up
- Podcast RSS feed format

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:**
  - HTTP handlers: `internal/httpapi/handlers/<domain>/<handler>.go` with constructor DI
  - Routes: registered in `internal/httpapi/routes.go` under `/api/v1` or via `RegisterRoutes()` methods
  - Domain models: `internal/domain/<entity>.go` — no infra deps
  - Repository: `internal/repository/<entity>_repository.go` — SQLX-based
  - Response envelope: `shared.WriteJSON()`, `shared.WriteError()` from `internal/httpapi/shared/response.go`
  - Error mapping: `shared.MapDomainErrorToHTTP()` from `internal/httpapi/shared/response.go`
  - Pagination: `shared.GetPagination(r)` returning `(page, pageSize, limit, offset)`
  - Existing blocklist pattern: `internal/httpapi/handlers/moderation/blocklist_status.go` (admin blocklist in `moderation_handlers.go`)

- **Conventions:**
  - Error wrapping: `fmt.Errorf("context: %w", err)` with `domain.ErrXxx` sentinels
  - Table-driven tests with testify `assert`/`require`
  - Constructor DI, no globals; UUID for IDs (`github.com/google/uuid`)
  - OpenAPI specs: one YAML per domain in `api/openapi_*.yaml`
  - Postman collections: `postman/athena-<domain>.postman_collection.json`

- **Key files:**
  - `internal/httpapi/routes.go` — main route registration (~711 lines)
  - `internal/httpapi/shared/response.go` — response envelope helpers
  - `internal/httpapi/shared/dependencies.go` — HandlerDependencies struct
  - `internal/domain/errors.go` — sentinel errors
  - `internal/app/app.go` — dependency wiring
  - `api/openapi.yaml` — main OpenAPI spec (89 paths)
  - `api/openapi_moderation.yaml` — moderation/blocklist/abuse spec (9 paths)

- **Gotchas:**
  - Routes are registered conditionally based on `cfg.Enable*` flags and non-nil deps
  - The `registerExternalFeatureRoutes()` function in routes.go registers additional routes (waiting room, redundancy, categories, analytics, social)
  - Some routes use `RegisterRoutes()` methods on handler structs (chat, social, analytics, categories, redundancy, waiting room)
  - Existing admin blocklist is at `/api/v1/admin/blocklist` (instance-level). Per-user blocklist is at `/api/v1/blocklist/` (different scope)
  - Postman environment uses `{{baseUrl}}` variable pointing to `http://localhost:8080`

- **Domain context:**
  - PeerTube's per-user blocklist lets individual users block accounts and servers. Athena's admin blocklist blocks at the instance level. These are complementary, not conflicting.
  - Instance config endpoint returns public-facing server capabilities (signup policy, max file size, instance name, features enabled)
  - Channel avatar/banner uses the same IPFS-pinning flow as user avatars (see `authHandlers.UploadAvatar` in routes.go)

## Runtime Environment

- **Start command:** `make run` or `go run cmd/server/main.go`
- **Port:** 8080 (default)
- **Health check:** `GET /health`
- **Restart:** Kill and re-run (no hot reload)

## Assumptions

- Instance config values exist in DB via `instance_configs` table — supported by `admin.NewInstanceHandlers` and existing `GET /admin/instance/config` route — Task 1 depends on this
- Video quota will be computed via `SUM(file_size)` on videos table (no `used_quota` column exists) — Task 2 uses this approach
- IPFS avatar upload pattern can be reused for channel avatar/banner — supported by `authHandlers.UploadAvatar` existing — Task 5 depends on this
- Email service and notification service are wired for subscription feeds — Task 8 depends on this
- PostgreSQL `user_blocks` table will be created via migration 071 — Task 3 depends on this
- No `live_stream_sessions` table exists — will be created via migration 073 — Task 7 depends on this
- **Migration numbering:** 071 = user_blocks (Task 3), 072 = abuse_report_messages (Task 6), 073 = live_stream_sessions (Task 7)

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| User quota computation may be slow for users with many videos | Medium | Medium | Use a denormalized `used_quota` column updated on upload/delete, not a real-time SUM query |
| Per-user blocklist may not integrate with existing content queries | Medium | High | Add blocklist filtering at the repository layer using JOIN/NOT EXISTS patterns |
| Channel avatar upload may need storage backend abstraction | Low | Medium | Reuse existing IPFS/S3 upload helpers from user avatar flow |
| OpenAPI spec changes may break generated types | Medium | Medium | Run `make verify-openapi` after each spec change; only add to specs, don't modify existing schemas |

## Goal Verification

### Truths

1. All high-priority gap endpoints respond correctly: `GET /api/v1/config` returns instance info, `GET /api/v1/users/me/video-quota-used` returns quota, per-user blocklist CRUD works, `DELETE /api/v1/users/me/avatar` resets avatar
2. All medium-priority gap endpoints respond correctly: channel avatar/banner upload, abuse threads, live sessions, subscription feeds, batch notification read
3. Every route in `routes.go` has a corresponding OpenAPI spec entry
4. Postman collections exist for all major API domains with passing tests
5. README.md metrics match actual codebase counts
6. Sprint documentation accurately reflects project completion status
7. Gap report is updated with new "Implemented" statuses

### Artifacts

1. `internal/httpapi/handlers/` — new handler files for gap endpoints
2. `internal/repository/` — new repository files for blocklist, quota
3. `migrations/` — new SQL migrations for user blocklist, abuse messages
4. `api/openapi*.yaml` — updated/new OpenAPI specs
5. `postman/` — new and updated Postman collection JSON files
6. `README.md`, `CLAUDE.md`, `docs/sprints/README.md`, `docs/sprints/PROJECT_COMPLETE.md` — updated docs
7. `docs/reports/peertube-parity-gap-report.md` — updated gap statuses

## Progress Tracking

- [x] Task 1: Public instance config endpoint
- [x] Task 2: User video quota endpoint
- [x] Task 3: Per-user blocklist (7 endpoints + migration)
- [x] Task 4: Delete user avatar endpoint
- [x] Task 5: Channel avatar/banner endpoints
- [x] Task 6: Abuse report discussion threads
- [x] Task 7: Live session history endpoint
- [x] Task 8: Subscription RSS/Atom feed
- [x] Task 9: Batch notification mark-read
- [x] Task 10: OpenAPI spec audit and updates
- [x] Task 11: Postman E2E test collections
- [x] Task 12: Documentation accuracy pass

**Total Tasks:** 12 | **Completed:** 12 | **Remaining:** 0

## Implementation Tasks

### Task 1: Public Instance Config Endpoint

**Objective:** Add `GET /api/v1/config` returning public instance configuration (name, description, signup policy, max file size, enabled features).

**Dependencies:** None

**Files:**
- Create: `internal/httpapi/handlers/admin/config_handler.go`
- Create: `internal/httpapi/handlers/admin/config_handler_test.go`
- Modify: `internal/httpapi/routes.go` (add public config route outside admin group)
- Modify: `api/openapi.yaml` (add `/api/v1/config` path)

**Key Decisions / Notes:**
- Reuse existing `instanceHandlers` from `admin.NewInstanceHandlers()` — it already reads from instance_configs table
- Route goes OUTSIDE the `/admin` group (no auth required) — PeerTube's `GET /config` is public
- **Three endpoints in this task:** `GET /api/v1/config` (public instance config), `GET /api/v1/config/about` (instance about page info), and `GET /api/v1/instance/stats` (public stats: user count, video count, instance version)
- `GET /api/v1/config` response shape: `{ instance: { name, description, shortDescription }, signup: { allowed, requiresEmailVerification }, video: { maxFileSize }, features: { activityPub, ipfs, iota, livestreaming } }`
- `GET /api/v1/config/about` response: `{ instance: { name, description, terms, administrator: { email } } }`
- `GET /api/v1/instance/stats` response: `{ totalUsers, totalVideos, totalLocalVideos, totalInstanceFollowers, totalInstanceFollowing }`
- Pattern: see existing `r.Get("/instance/about", instanceHandlers.GetInstanceAbout)` at routes.go:603

**Definition of Done:**
- [ ] `GET /api/v1/config` returns 200 with instance config JSON
- [ ] `GET /api/v1/config/about` returns 200 with about page info
- [ ] `GET /api/v1/instance/stats` returns 200 with public stats
- [ ] No auth required for any of the three endpoints
- [ ] All tests pass
- [ ] OpenAPI spec entries added for all three

**Verify:**
- `go test ./internal/httpapi/handlers/admin/... -run TestConfig -v`

---

### Task 2: User Video Quota Endpoint

**Objective:** Add `GET /api/v1/users/me/video-quota-used` returning the authenticated user's storage usage.

**Dependencies:** None

**Files:**
- Modify: `internal/httpapi/handlers/auth/users.go` (add GetVideoQuotaUsed handler)
- Create: `internal/httpapi/handlers/auth/quota_handler_test.go`
- Modify: `internal/httpapi/routes.go` (add route under `/users/me`)
- Modify: `internal/port/user.go` (add GetVideoQuotaUsed to interface if needed)
- Modify: `internal/repository/user_repository.go` (add quota query)
- Modify: `api/openapi.yaml` (add path)

**Key Decisions / Notes:**
- **Chosen approach: SUM query** — compute from `SUM(file_size)` on videos table filtered by user_id. No new column or migration needed. Acceptable performance for current scale (most users have <1000 videos).
- Response: `{ videoQuotaUsed: 12345678, videoQuotaUsedDaily: 5000000 }`
- PeerTube returns bytes used — match that format
- Pattern: similar to `auth.GetCurrentUserHandler` at routes.go:257

**Definition of Done:**
- [ ] `GET /api/v1/users/me/video-quota-used` returns 200 with quota JSON
- [ ] Requires auth
- [ ] All tests pass
- [ ] OpenAPI spec entry added

**Verify:**
- `go test ./internal/httpapi/handlers/auth/... -run TestQuota -v`

---

### Task 3: Per-User Blocklist (7 Endpoints)

**Objective:** Implement per-user account and server blocking. Users can block individual accounts and entire server domains.

**Dependencies:** None

**Files:**
- Create: `migrations/071_create_user_blocks_table.sql` (assigned: 071)
- Create: `internal/domain/user_block.go`
- Create: `internal/repository/user_block_repository.go`
- Create: `internal/httpapi/handlers/moderation/user_blocklist_handlers.go`
- Create: `internal/httpapi/handlers/moderation/user_blocklist_handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add `/blocklist/accounts` and `/blocklist/servers` routes)
- Modify: `internal/httpapi/shared/dependencies.go` (add UserBlockRepo)
- Modify: `internal/app/app.go` (wire UserBlockRepo)
- Modify: `api/openapi.yaml` (add 7 paths)

**Key Decisions / Notes:**
- Migration creates `user_blocks` table: `id, user_id, block_type (account|server), target_account_id, target_server_host, created_at`
- 7 endpoints: `GET /blocklist/accounts`, `POST /blocklist/accounts`, `DELETE /blocklist/accounts/{accountName}`, `GET /blocklist/servers`, `POST /blocklist/servers`, `DELETE /blocklist/servers/{host}`, existing `GET /blocklist/status` already done
- All require auth. No admin role needed — these are per-user blocks.
- Pattern: follow existing `moderation.NewModerationHandlers` pattern at routes.go:458

**Definition of Done:**
- [ ] All 7 blocklist endpoints return correct status codes (CRUD for account blocks, CRUD for server blocks)
- [ ] Migration creates user_blocks table
- [ ] Blocklist CRUD works (create, list, delete blocks)
- [ ] All tests pass
- [ ] OpenAPI spec entries added

**Note:** Feed filtering (hiding blocked user content from feeds/search) is a cross-cutting change affecting video_repository.go, subscription queries, and search handlers. This is deferred to a follow-up task to keep this task scoped to the blocklist CRUD API.

**Verify:**
- `go test ./internal/httpapi/handlers/moderation/... -run TestUserBlock -v`
- `go test ./internal/repository/... -run TestUserBlock -v`

---

### Task 4: Delete User Avatar Endpoint

**Objective:** Add `DELETE /api/v1/users/me/avatar` to reset a user's avatar to default.

**Dependencies:** None

**Files:**
- Modify: `internal/httpapi/handlers/auth/auth_handlers.go` (add DeleteAvatar handler)
- Create: `internal/httpapi/handlers/auth/avatar_delete_test.go`
- Modify: `internal/httpapi/routes.go` (add DELETE route)
- Modify: `api/openapi.yaml` (add path)

**Key Decisions / Notes:**
- Set user's `avatar_url`/`avatar_cid` to empty string in DB
- Don't unpin from IPFS (other users may reference it)
- Pattern: adjacent to existing `r.Post("/me/avatar", authHandlers.UploadAvatar)` at routes.go:260

**Definition of Done:**
- [ ] `DELETE /api/v1/users/me/avatar` returns 204 (or 200)
- [ ] User's avatar fields cleared in DB
- [ ] All tests pass
- [ ] OpenAPI spec entry added

**Verify:**
- `go test ./internal/httpapi/handlers/auth/... -run TestDeleteAvatar -v`

---

### Task 5: Channel Avatar/Banner Endpoints

**Objective:** Add avatar and banner upload/delete for video channels (4 endpoints).

**Dependencies:** Task 4 (similar pattern)

**Files:**
- Modify: `internal/httpapi/handlers/channel/channels.go` (or create `channel_media.go`)
- Create: `internal/httpapi/handlers/channel/channel_media_test.go`
- Modify: `internal/httpapi/routes.go` (add 4 routes under `/channels/{id}`)
- Modify: `api/openapi_channels.yaml` (add 4 paths)

**Key Decisions / Notes:**
- 4 endpoints: `POST /channels/{id}/avatar`, `DELETE /channels/{id}/avatar`, `POST /channels/{id}/banner`, `DELETE /channels/{id}/banner`
- Reuse IPFS upload logic from user avatar handler
- Channel model needs `avatar_url`, `banner_url` fields (check if they exist in domain/channel.go)
- Only channel owner or admin can modify

**Definition of Done:**
- [ ] All 4 channel media endpoints work
- [ ] Images are stored via same storage backend as user avatars
- [ ] Only channel owner or admin can modify
- [ ] All tests pass
- [ ] OpenAPI spec entries added

**Verify:**
- `go test ./internal/httpapi/handlers/channel/... -run TestChannelMedia -v`

---

### Task 6: Abuse Report Discussion Threads

**Objective:** Add messaging within abuse reports — reporters and moderators can discuss.

**Dependencies:** None

**Files:**
- Create: `migrations/072_create_abuse_report_messages_table.sql` (assigned: 072)
- Create: `internal/domain/abuse_message.go`
- Modify: `internal/repository/moderation_repository.go` (or create `abuse_message_repository.go`)
- Create: `internal/httpapi/handlers/moderation/abuse_message_handlers.go`
- Create: `internal/httpapi/handlers/moderation/abuse_message_handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add routes under `/abuse-reports/{id}/messages` or `/admin/abuse-reports/{id}/messages`)
- Modify: `api/openapi_moderation.yaml` (add 3 paths)

**Key Decisions / Notes:**
- 3 endpoints: `GET /admin/abuse-reports/{id}/messages`, `POST /admin/abuse-reports/{id}/messages`, `DELETE /admin/abuse-reports/{id}/messages/{messageId}`
- Migration: `abuse_report_messages` table with `id, abuse_report_id, sender_id, message, created_at`
- Reporter can also post messages (check abuse report ownership), moderators/admins always can
- Pattern: similar to comment handlers

**Definition of Done:**
- [ ] All 3 abuse message endpoints work
- [ ] Migration creates the table
- [ ] Both reporter and admin/mod can post messages
- [ ] All tests pass
- [ ] OpenAPI spec entries added

**Verify:**
- `go test ./internal/httpapi/handlers/moderation/... -run TestAbuseMessage -v`

---

### Task 7: Live Session History Endpoint

**Objective:** Add `GET /api/v1/streams/{id}/sessions` to list past live session records.

**Dependencies:** None

**Files:**
- Create: `migrations/073_create_live_stream_sessions_table.sql` (assigned: 073)
- Create: `internal/repository/live_stream_session_repository.go`
- Modify: `internal/httpapi/handlers/livestream/livestream_handlers.go` (add GetSessionHistory handler)
- Modify: `internal/httpapi/handlers/livestream/livestream_handlers_test.go` (add tests)
- Modify: `internal/httpapi/routes.go` (add route under `/streams/{id}`)
- Modify: `internal/httpapi/shared/dependencies.go` (add LiveStreamSessionRepo)
- Modify: `internal/app/app.go` (wire LiveStreamSessionRepo)
- Modify: `api/openapi_livestreaming.yaml` (add path)

**Key Decisions / Notes:**
- **No session history table exists** — migration 073 creates `live_stream_sessions` table with `id, stream_id, started_at, ended_at, peak_viewers, total_duration, avg_viewers`
- Returns list of past sessions with start/end times, peak viewers, duration
- Auth required (stream owner or admin)
- Pattern: adjacent to existing `r.Get("/stats", liveStreamHandlers.GetStreamStats)` at routes.go:369

**Definition of Done:**
- [ ] `GET /api/v1/streams/{id}/sessions` returns session history
- [ ] Paginated response
- [ ] All tests pass
- [ ] OpenAPI spec entry added

**Verify:**
- `go test ./internal/httpapi/handlers/livestream/... -run TestSessionHistory -v`

---

### Task 8: Subscription RSS/Atom Feed

**Objective:** Add `GET /feeds/subscriptions.atom` and `GET /feeds/subscriptions.rss` for authenticated user's subscription activity.

**Dependencies:** None

**Files:**
- Modify: `internal/httpapi/handlers/video/feed_handlers.go` (add SubscriptionFeed handler)
- Modify: `internal/httpapi/handlers/video/feed_handlers_test.go` (add tests)
- Modify: `internal/httpapi/routes.go` (add 2 feed routes)
- Modify: `api/openapi.yaml` (add 2 paths)

**Key Decisions / Notes:**
- Requires auth (token-based — PeerTube uses `?token=xxx` query param for feed auth)
- Fetches recent videos from subscribed channels
- Reuse existing `feedHandlers` from routes.go:128 and subscription repo
- Pattern: existing `r.Get("/feeds/videos.atom", feedHandlers.VideosFeed)` at routes.go:129

**Definition of Done:**
- [ ] `GET /feeds/subscriptions.atom` returns Atom feed of subscription videos
- [ ] `GET /feeds/subscriptions.rss` returns RSS feed equivalent
- [ ] Auth via Bearer token or `?token=` query param
- [ ] All tests pass
- [ ] OpenAPI spec entries added

**Verify:**
- `go test ./internal/httpapi/handlers/video/... -run TestSubscriptionFeed -v`

---

### Task 9: Batch Notification Mark-Read

**Objective:** Add `POST /api/v1/notifications/read` to mark multiple notification IDs as read in one request.

**Dependencies:** None

**Files:**
- Modify: `internal/httpapi/handlers/messaging/notification_handlers.go` (add BatchMarkAsRead handler)
- Modify: `internal/httpapi/handlers/messaging/notification_handlers_test.go` (add tests)
- Modify: `internal/httpapi/routes.go` (add POST route)
- Modify: `api/openapi.yaml` (add path)

**Key Decisions / Notes:**
- Request body: `{ "ids": ["uuid1", "uuid2", ...] }`
- Batch update in single DB query
- **POST /notifications/read (new, batch by IDs) is distinct from the existing PUT /notifications/read-all (mark ALL read).** POST is correct per PeerTube spec. Do not change the existing PUT endpoint.
- Pattern: adjacent to existing notification routes at routes.go:406-415

**Definition of Done:**
- [ ] `POST /api/v1/notifications/read` marks specified IDs as read
- [ ] Returns count of updated notifications
- [ ] All tests pass
- [ ] OpenAPI spec entry added

**Verify:**
- `go test ./internal/httpapi/handlers/messaging/... -run TestBatchMarkRead -v`

---

### Task 10: OpenAPI Spec Audit and Updates

**Objective:** Audit every route in routes.go and external RegisterRoutes() methods against all OpenAPI specs. Add missing paths.

**Dependencies:** Tasks 1-9 (new endpoints need specs too)

**Files:**
- Modify: `api/openapi.yaml` (add missing paths)
- Modify: `api/openapi_channels.yaml` (channel avatar/banner)
- Modify: `api/openapi_moderation.yaml` (abuse messages, user blocklist)
- Modify: `api/openapi_livestreaming.yaml` (session history, waiting room)
- Modify: `api/openapi_analytics.yaml` (stream analytics, viewer tracking)
- Create: `api/openapi_social.yaml` (social routes: follow/unfollow, likes, comments via ATProto)
- Create: `api/openapi_waiting_room.yaml` or add to livestreaming spec

**Key Decisions / Notes:**
- Known missing from OpenAPI (existing routes not in any spec):
  - Social routes: `/social/actors/{handle}`, `/social/follow`, etc.
  - Stream analytics: `/streams/{streamId}/analytics/*`
  - Analytics tracking: `/analytics/viewer/join`, `/viewer/leave`, `/engagement`
  - Categories: `/categories`, `/admin/categories` (in routes.go via RegisterRoutes)
  - Waiting room: `/api/v1/streams/{streamId}/waiting-room`
  - Redundancy routes (already have spec but verify completeness)
  - `GET /encoding/my-jobs`, `GET /users/me/ratings`, `GET /users/me/watch-later`, `GET /users/me/channels`
  - `DELETE /users/me` (account deletion)
  - `GET /avatars/{cid}` (avatar proxy)
  - `.well-known/atproto-did`
- Run `make verify-openapi` after changes

**Definition of Done:**
- [ ] All routes listed in the "Known missing" notes below are added to OpenAPI specs
- [ ] `make verify-openapi` passes (checks generated code matches spec)
- [ ] All tests pass

**Note:** `make verify-openapi` only checks spec-to-generated-code consistency, not that all routes have spec entries. Full route-to-spec coverage is verified via the manual audit of the known missing list above.

**Verify:**
- `make verify-openapi`
- Grep routes.go for route registrations, compare against `grep -h '^\s\s/' api/openapi*.yaml | sort` to verify coverage

---

### Task 11: Postman E2E Test Collections

**Objective:** Create comprehensive Postman collections for all API domains. Ensure all implemented endpoints have at least one happy-path and one error-path test.

**Dependencies:** Tasks 1-9 (need endpoints to test), Task 10 (specs as reference)

**Files:**
- Create: `postman/athena-channels.postman_collection.json`
- Create: `postman/athena-playlists.postman_collection.json`
- Create: `postman/athena-feeds.postman_collection.json`
- Create: `postman/athena-chapters-blacklist.postman_collection.json`
- Create: `postman/athena-livestreaming.postman_collection.json`
- Create: `postman/athena-federation.postman_collection.json`
- Create: `postman/athena-moderation.postman_collection.json`
- Create: `postman/athena-notifications.postman_collection.json`
- Create: `postman/athena-blocklist.postman_collection.json`
- Create: `postman/athena-instance-config.postman_collection.json`
- Modify: existing collections to add tests for new endpoints

**Key Decisions / Notes:**
- Each collection should use `{{baseUrl}}` and `{{authToken}}` variables
- Include pre-request scripts for auth (login, save token)
- Test assertions: status code, response envelope structure, required fields
- Follow existing pattern from `postman/athena-auth.postman_collection.json` (61 requests, good reference)
- Cover: happy path, auth failure (401), not found (404), validation error (400)

**Definition of Done:**
- [ ] Every major API domain has a Postman collection
- [ ] Each collection has happy-path and error-path tests
- [ ] Collections use environment variables consistently
- [ ] All new gap endpoints (Tasks 1-9) are covered

**Verify:**
- `newman run postman/athena-<collection>.postman_collection.json -e postman/test-env.json` (smoke test)

---

### Task 12: Documentation Accuracy Pass

**Objective:** Update all stale documentation with accurate current metrics and status.

**Dependencies:** Tasks 1-11 (need final state)

**Files:**
- Modify: `README.md` — update metrics table, Project Status section, API endpoint count
- Modify: `CLAUDE.md` — update sprint status reference, test counts
- Modify: `docs/sprints/README.md` — update project metrics, mark Quality Programme as complete
- Modify: `docs/sprints/PROJECT_COMPLETE.md` — update test counts and code metrics
- Modify: `docs/reports/peertube-parity-gap-report.md` — update endpoint statuses from Missing→Implemented
- Modify: `.claude/rules/project.md` — update Sprint Status, test counts, endpoint count

**Key Decisions / Notes:**
- Current actual metrics: 778 Go files (385 production + 393 test), 4,383 test functions, ~200+ API endpoints
- README says "Sprint 16/20 active" in CLAUDE.md — should say "Quality Programme Complete"
- Gap report needs ~18 endpoints updated from Missing→Implemented
- Sprint README says "14 planned sprints" but there were 20 (14 feature + 6 quality)
- Endpoint count changed from ~184 to ~200+ with new endpoints

**Definition of Done:**
- [ ] README.md metrics match reality (run automated count)
- [ ] CLAUDE.md Sprint Status is accurate
- [ ] Sprint README reflects all 20 sprints completed
- [ ] PROJECT_COMPLETE.md metrics current
- [ ] Gap report updated with new endpoint statuses
- [ ] No references to "Sprint 16/20 active" remain

**Verify:**
- `grep -r "Sprint 16" docs/ CLAUDE.md README.md` returns no stale references
- `grep -r "3,740\|3740\|312 test" docs/ CLAUDE.md README.md` returns no stale test counts

## Open Questions

None — all clarified via user Q&A.

### Deferred Ideas

- Account handle-based routes (`/accounts/{name}/*`) — different URL pattern, separate spec
- Podcast RSS feed format (`/feeds/podcast/videos.{format}`)
- Playlist privacy levels endpoint
- Video source download endpoint
