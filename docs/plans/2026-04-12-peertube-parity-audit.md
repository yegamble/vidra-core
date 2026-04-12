# PeerTube v7.0–v8.1.5 Parity Audit Implementation Plan

Created: 2026-04-12
Author: yegamble@gmail.com
Status: PENDING
Approved: Yes
Iterations: 1
Worktree: No
Type: Feature

## Summary

**Goal:** Close all backend parity gaps between Vidra Core and PeerTube v8.1.5 (latest). Audit identified 13 implementable backend gaps across REST API query parameters, response shapes, infrastructure features, and federation compatibility.

**Architecture:** Additive changes only — no breaking changes to existing APIs. New fields/params are added alongside existing ones. Deprecated PeerTube fields are kept for backward compat with new `fileUrl`/`thumbnails` alternatives.

**Tech Stack:** Go (Chi), PostgreSQL (SQLX), Redis, ActivityPub

## Scope

### In Scope

- REST API query parameter additions (host filter, typeOneOf alias, channelUpdatedAt sort, playlistUrl)
- Thumbnails array response shape (PeerTube v8.1 deprecation compat)
- Channel Activities endpoint (new in PeerTube v8.0)
- Case-insensitive email matching for login/verification/password reset
- Redis TLS connection support
- FEP-1b12 ActivityPub compatibility (Lemmy, PieFed, Mbin)
- WebP/PNG thumbnail acceptance
- Mastodon verification link support
- Video public link in ActivityPub representation
- fileUrl fields on VideoCaption, Storyboard, ActorImage (deprecation compat)

### Out of Scope (Separate Plans Needed)

These are substantial features that each warrant their own `/spec` plan:

- **Sensitive Content Flags System** (v7.2) — New domain model with predefined flags, per-flag user overrides, Blur/Warn policies. Estimated 8-12 files.
- **Player Theme System** (v8.0) — Admin/channel/video level theme selection (Lucide/Galaxy). Needs schema changes + config + API.
- **Viewer Protocol V2** (v7.0) — Concurrent viewer scalability optimization. Architectural change to viewer counting.
- **Captions Object Storage** (v7.1) — Separate S3 bucket for captions. Storage layer refactoring.
- **SVG Logo Support** (v8.1) — Admin config for SVG logos. Minor but frontend-heavy.

## Approach

**Chosen:** Incremental additive changes — each task independently testable, backward-compatible.
**Why:** Minimizes risk. No migration needed for most changes. Each task ships independently.
**Alternatives considered:**
- Batch all into a single large refactor — rejected, too risky and hard to test
- Response shape migration (remove old fields) — rejected, breaks existing clients

## Context for Implementer

> All changes are additive. Existing API responses keep their current fields. New fields/params are added alongside.

- **Patterns to follow:** `internal/httpapi/handlers/video/video_handlers.go` for query param parsing, `internal/httpapi/shared/response.go` for response helpers
- **Conventions:** snake_case JSON fields, `shared.WriteJSON`/`WriteJSONWithMeta` for responses, `shared.ParsePagination` for pagination
- **Key files:** 
  - `internal/domain/video.go` — Video domain model
  - `internal/httpapi/routes.go` — Route registration
  - `internal/httpapi/handlers/messaging/notifications.go` — Notification handler
  - `internal/httpapi/handlers/channel/subscriptions*.go` — Subscription handlers
  - `internal/httpapi/handlers/channel/collaborators.go` — Collaborator handlers
  - `internal/activitypub/` — Federation code
  - `internal/config/config.go` — Configuration
- **Gotchas:** PeerTube uses `start`/`count` for pagination; Vidra maps to `offset`/`limit`. `typeOneOf` in PeerTube = `types` in Vidra (alias needed).
- **Domain context:** PeerTube deprecated several response fields in v8.0-v8.1. We need to add the new fields (`thumbnails` array, `fileUrl`) while keeping the old ones for backward compat.
- **Pre-implementation:** Current highest migration is `087_add_migration_id_mapping.sql`. New migrations start at 088. Redis client is initialized in `internal/app/app.go:249` via `redis.ParseURL` + `redis.NewClient` (NOT in `internal/database/`).

## Assumptions

- The `thumbnails` array can be computed from existing `thumbnail_path`/`preview_path` fields without schema changes — supported by the existing Video domain model having both fields. Tasks 1-2 depend on this.
- Redis TLS can be enabled via URL scheme (`rediss://`) or config flag — supported by Go redis libraries. Task 8 depends on this.
- FEP-1b12 requires adding `sensitive` and `content` fields to ActivityPub objects — supported by PeerTube's implementation. Task 9 depends on this.
- Case-insensitive email can use `LOWER()` in SQL with a unique functional index — supported by PostgreSQL. Task 6 depends on this. Assumes no existing case-variant duplicate emails in the `users` table (migration will fail if duplicates exist — add a pre-check).

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|-----------|--------|------------|
| Thumbnails array breaks PeerTube client expectations | Low | High | Add as NEW field, keep old fields unchanged |
| Case-insensitive email causes collisions | Low | Medium | Add unique constraint on LOWER(email) in migration |
| Redis TLS breaks existing non-TLS connections | Low | Medium | TLS is opt-in via config, default off |
| FEP-1b12 changes break existing AP federation | Low | High | Additive only — new fields don't affect existing parsing |

## Goal Verification

### Truths

1. Video API responses include both `thumbnail_path` (existing) AND `thumbnails` array (new)
2. `GET /api/v1/videos?host=example.com` filters videos by instance domain
3. `GET /api/v1/users/me/notifications?typeOneOf=1,2` works as alias for existing `types` param
4. `GET /api/v1/users/me/subscriptions?sort=channelUpdatedAt` sorts by channel update time
5. `GET /api/v1/video-channels/{handle}/activities` returns channel activity log
6. Login with `USER@EXAMPLE.COM` matches user registered as `user@example.com`
7. Redis connects via TLS when `REDIS_TLS=true` or `rediss://` URL scheme
8. ActivityPub video objects include FEP-1b12 compatible fields
9. WebP and PNG files are accepted for video thumbnails
10. VideoCaption, Storyboard, ActorImage responses include `fileUrl` field

### Artifacts

- Modified: `internal/domain/video.go`, `internal/httpapi/handlers/video/*.go`, `internal/httpapi/handlers/messaging/notifications.go`, `internal/httpapi/handlers/channel/*.go`, `internal/config/config.go`, `internal/activitypub/*.go`, `internal/repository/user_repository.go`
- Created: `internal/httpapi/handlers/channel/activities.go`, `internal/domain/channel_activity.go`, `internal/repository/channel_activity_repository.go`, `migrations/088_*.sql`, `migrations/089_*.sql`

## Progress Tracking

- [x] Task 1: Thumbnails array in Video API response
- [x] Task 2: fileUrl deprecation compat fields
- [x] Task 3: Host filter on video list endpoints
- [x] Task 4: typeOneOf alias on notifications
- [x] Task 5: channelUpdatedAt sort on subscriptions
- [x] Task 6: Case-insensitive email matching
- [x] Task 7: Channel Activities endpoint
- [x] Task 8: Redis TLS support
- [ ] Task 9: FEP-1b12 ActivityPub compatibility
- [ ] Task 10: WebP/PNG thumbnail acceptance
- [ ] Task 11: Mastodon verification + Video public link in ActivityPub
- [ ] Task 12: playlistUrl in HLS video file response
- [ ] Task 13: Wire activity writes into video/collaborator handlers
- [x] Task 0: Add deferred features to parity registry
      **Total Tasks:** 14 | **Completed:** 0 | **Remaining:** 14

## Implementation Tasks

### Task 0: Add Deferred Features to Parity Registry

**Objective:** Register all 5 deferred features in `.claude/rules/feature-parity-registry.md` with status `Deferred` before implementation begins. Satisfies autonomous-mode traceability requirement.
**Dependencies:** None

**Files:**

- Modify: `.claude/rules/feature-parity-registry.md` — Add 5 entries to Deferred table

**Key Decisions / Notes:**

- Sensitive Content Flags System, Player Theme System, Viewer Protocol V2, Captions Object Storage, SVG Logo Support
- Each entry references this plan as the source of identification
- Status: `Deferred` with note about PeerTube version where feature appeared

**Definition of Done:**

- [ ] All 5 deferred features listed in feature parity registry
- [ ] Each entry has PeerTube version reference

**Verify:**

- `grep -c "Deferred" .claude/rules/feature-parity-registry.md` shows increased count

---

### Task 1: Thumbnails Array in Video API Response

**Objective:** Add `thumbnails` array to Video and VideoPlaylist JSON responses, matching PeerTube v8.1's new response shape. Keep existing `thumbnail_path` and `preview_path` fields for backward compatibility.
**Dependencies:** None
**Mapped Scenarios:** None (API-only, no UI)

**Files:**

- Modify: `internal/domain/video.go` — Add `Thumbnails` computed field or response transformer
- Modify: `internal/httpapi/handlers/video/video_handlers.go` — Transform response to include thumbnails array
- Modify: `internal/httpapi/shared/response.go` — Add thumbnail response type if needed
- Test: `internal/httpapi/handlers/video/video_handlers_test.go`

**Key Decisions / Notes:**

- PeerTube `thumbnails` array shape: `[{ "type": "thumbnail"|"preview", "path": "/lazy-static/thumbnails/xxx.jpg", "width": 280, "height": 157 }]`
- Compute from existing `thumbnail_path` and `preview_path` — no schema change needed
- Both Video and VideoPlaylist responses need this array
- `thumbnail_path` and `preview_path` remain in response (deprecated but present)

**Definition of Done:**

- [ ] All tests pass
- [ ] No diagnostics errors
- [ ] `GET /api/v1/videos/{id}` response includes `thumbnails` array with correct shape
- [ ] `GET /api/v1/videos` list response includes `thumbnails` array on each video
- [ ] Existing `thumbnail_path` and `preview_path` still present in response

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1 -run TestVideo`

---

### Task 2: fileUrl Deprecation Compat Fields

**Objective:** Add `file_url` field to VideoCaption, Storyboard, and ActorImage (avatar/banner) responses, alongside deprecated `caption_path`/`storyboard_path`/`path` fields. Matches PeerTube v8.0-v8.1 deprecation pattern.
**Dependencies:** None

**Files:**

- Modify: `internal/domain/video_storyboard.go` — Add FileURL field
- Modify: `internal/domain/channel.go` — Check avatar/banner response shape
- Modify: `internal/httpapi/handlers/video/storyboard_handlers.go` — Include file_url in response
- Modify: `internal/httpapi/handlers/social/captions*.go` — Include file_url in caption response
- Test: `internal/httpapi/handlers/video/storyboard_handlers_test.go`
- Test: `internal/httpapi/handlers/social/captions_test.go`

**Key Decisions / Notes:**

- `file_url` is the full URL to the resource (e.g., `https://instance.com/lazy-static/thumbnails/xxx.jpg`)
- Construct from config base URL + existing path
- Pattern: field value = `{baseURL}{existing_path}` 

**Definition of Done:**

- [ ] All tests pass
- [ ] VideoCaption response includes `file_url` alongside `caption_path`
- [ ] Storyboard response includes `file_url` alongside `storyboard_path`
- [ ] Actor avatar/banner responses include `file_url` alongside `path`

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1`
- `go test -short ./internal/httpapi/handlers/social/... -count=1`

---

### Task 3: Host Filter on Video List Endpoints

**Objective:** Add `host` query parameter to video list and search endpoints, filtering videos by the instance domain they originate from. Matches PeerTube v7.0 REST API addition.
**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/video_handlers.go` — Parse `host` query param
- Modify: `internal/repository/video_repository.go` — Add WHERE clause for `remote_instance_domain`
- Modify: `internal/domain/video.go` — Add Host to video filter struct if one exists
- Test: `internal/httpapi/handlers/video/video_handlers_test.go`
- Test: `internal/repository/video_repository_test.go` or appropriate unit test

**Key Decisions / Notes:**

- Filter on `remote_instance_domain` column (already exists in Video model)
- For local videos, host matches the instance's own domain
- Empty `host` param = no filter (default behavior unchanged)

**Definition of Done:**

- [ ] All tests pass
- [ ] `GET /api/v1/videos?host=example.com` returns only videos from that host
- [ ] `GET /api/v1/search/videos?host=example.com` also supports the filter
- [ ] No filter applied when `host` param is absent

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1`

---

### Task 4: typeOneOf Alias on Notifications Endpoint

**Objective:** Accept `typeOneOf` query parameter as an alias for the existing `types` parameter on the notifications list endpoint. Matches PeerTube v7.0 API naming.
**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/messaging/notifications.go` — Check for `typeOneOf` param as fallback
- Test: `internal/httpapi/handlers/messaging/notifications_handlers_test.go`

**Key Decisions / Notes:**

- Our `parseNotificationTypes(r)` function at `notifications.go:56` already handles `types` param
- Just add: if `types` is empty, try `typeOneOf` — same parsing logic
- Both params accepted; `types` takes precedence if both supplied

**Definition of Done:**

- [ ] All tests pass
- [ ] `GET /api/v1/notifications?typeOneOf=1,2` works identically to `?types=1,2`
- [ ] Existing `types` param still works unchanged

**Verify:**

- `go test -short ./internal/httpapi/handlers/messaging/... -count=1`

---

### Task 5: channelUpdatedAt Sort on Subscriptions

**Objective:** Add `channelUpdatedAt` as a sort option for the list subscriptions endpoint. Matches PeerTube v7.0 REST API.
**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/channel/subscriptions*.go` — Parse sort param, add channelUpdatedAt option
- Modify: `internal/repository/subscription_repository.go` — Add ORDER BY clause for channel updated_at
- Test: `internal/httpapi/handlers/channel/subscriptions_test.go`

**Key Decisions / Notes:**

- Maps to `channels.updated_at` column via JOIN
- Existing sort options should continue working
- Default sort unchanged

**Definition of Done:**

- [ ] All tests pass
- [ ] `GET /api/v1/users/me/subscriptions?sort=channelUpdatedAt` returns subscriptions sorted by channel update time
- [ ] Default sort unchanged when no sort param

**Verify:**

- `go test -short ./internal/httpapi/handlers/channel/... -count=1`

---

### Task 6: Case-insensitive Email Matching

**Objective:** Make login, email verification, and password reset use case-insensitive email comparison. Matches PeerTube v7.0 behavior.
**Dependencies:** None

**Files:**

- Modify: `internal/repository/user_repository.go` — Change `GetByEmail` to use `LOWER(u.email) = LOWER($1)`
- Create: `migrations/088_add_lower_email_unique_index.sql` — Add functional index for performance
- Test: `internal/repository/user_repository_test.go` or appropriate test

**Key Decisions / Notes:**

- `GetByEmail` at user_repository.go:159 currently uses exact match: `u.email = $1`
- Change to `LOWER(u.email) = LOWER($1)` for case-insensitive matching
- Add migration with UNIQUE functional index: `CREATE UNIQUE INDEX idx_users_lower_email ON users (LOWER(email))`
- Include pre-migration dedup check: `SELECT LOWER(email), COUNT(*) FROM users GROUP BY LOWER(email) HAVING COUNT(*) > 1` — if any rows, warn and fail gracefully
- Migration must have `-- +goose Down` section

**Definition of Done:**

- [ ] All tests pass
- [ ] Login with `USER@EXAMPLE.COM` matches `user@example.com`
- [ ] Email verification with mixed case works
- [ ] UNIQUE functional index prevents future case-variant duplicate registrations
- [ ] Migration includes dedup safety check

**Verify:**

- `go test -short ./internal/repository/... -count=1 -run TestUser`

---

### Task 7: Channel Activities Endpoint

**Objective:** Add `GET /api/v1/video-channels/{handle}/activities` endpoint returning a log of actions performed within a channel (read path only). Matches PeerTube v8.0 feature. Write-path wiring is in Task 13.
**Dependencies:** Task 6 (migration numbering — use 089 since Task 6 uses 088)

**Files:**

- Create: `internal/domain/channel_activity.go` — ChannelActivity model
- Create: `internal/port/channel_activity.go` — Repository interface
- Create: `internal/repository/channel_activity_repository.go` — SQLX implementation
- Create: `internal/httpapi/handlers/channel/activities.go` — Handler
- Create: `migrations/089_create_channel_activities.sql` — Schema
- Modify: `internal/httpapi/routes.go` — Register route
- Test: `internal/httpapi/handlers/channel/activities_test.go`
- Test: `internal/repository/channel_activity_repository_test.go`

**Key Decisions / Notes:**

- PeerTube tracks: video publish, video update, video delete, playlist changes, collaborator changes
- Model: `{ id, channel_id, user_id, action_type, target_type, target_id, metadata, created_at }`
- Paginated with `start`/`count` (PeerTube convention)
- Only channel owner and editors can view activities
- This task creates the read path (schema, repo, handler). Write-path wiring is Task 13.

**Definition of Done:**

- [ ] All tests pass
- [ ] `GET /api/v1/video-channels/{handle}/activities` returns paginated activity list (empty until Task 13 wires writes)
- [ ] Only authenticated channel owner/editors can access
- [ ] Migration is reversible (has Down section)
- [ ] Repository has sqlmock-based tests
- [ ] Repository includes `CreateActivity` method (for Task 13 to call)

**Verify:**

- `go test -short ./internal/httpapi/handlers/channel/... -count=1 -run TestActivit`
- `go test -short ./internal/repository/... -count=1 -run TestChannelActivity`

---

### Task 8: Redis TLS Support

**Objective:** Allow Redis connections over TLS via config flag or `rediss://` URL scheme. Matches PeerTube v8.1 feature.
**Dependencies:** None

**Files:**

- Modify: `internal/config/config.go` — Add `RedisTLS` and `RedisTLSInsecure` config fields
- Modify: `internal/app/app.go` — Update `initializeRedis()` at line 249 to apply TLS config to `redis.Options` after `ParseURL`
- Test: `internal/config/config_test.go`
- Test: `internal/app/app_test.go` (if exists) or appropriate integration test

**Key Decisions / Notes:**

- Standard approach: `rediss://` URL scheme auto-enables TLS (Go redis libraries support this)
- Also add explicit `REDIS_TLS=true` env var as override
- Default: TLS disabled (backward compat)
- Use `crypto/tls` with `InsecureSkipVerify` option configurable via `REDIS_TLS_INSECURE`

**Definition of Done:**

- [ ] All tests pass
- [ ] `rediss://` URL scheme enables TLS connection
- [ ] `REDIS_TLS=true` enables TLS for `redis://` scheme
- [ ] Default behavior unchanged (no TLS)

**Verify:**

- `go test -short ./internal/config/... -count=1`
- `go test -short ./internal/database/... -count=1`

---

### Task 9: FEP-1b12 ActivityPub Compatibility

**Objective:** Add FEP-1b12 compatible fields to ActivityPub objects for interoperability with Lemmy, PieFed, Mbin, and other fediverse platforms. Matches PeerTube v8.1 feature.
**Dependencies:** None

**Files:**

- Modify: `internal/activitypub/types.go` or equivalent — Add `sensitive` boolean field, ensure `content` field present
- Modify: `internal/activitypub/federation*.go` — Include FEP-1b12 fields in outgoing objects
- Test: `internal/activitypub/*_test.go`

**Key Decisions / Notes:**

- FEP-1b12 specifies that `sensitive` boolean + `content` (HTML summary) should be included in Note/Video objects
- Lemmy, PieFed, Mbin use these fields to display content warnings
- `sensitive` maps to our video NSFW/privacy fields
- `content` should contain the video description as HTML
- Reference: PeerTube PR for this feature

**Definition of Done:**

- [ ] All tests pass
- [ ] ActivityPub Video objects include `sensitive` boolean
- [ ] ActivityPub Video objects include `content` HTML field
- [ ] Existing federation tests still pass
- [ ] NEW test asserts outgoing AP Video object contains `sensitive` (bool) and `content` (string) fields

**Verify:**

- `go test -short ./internal/activitypub/... -count=1`

---

### Task 10: WebP/PNG Thumbnail Acceptance

**Objective:** Accept WebP and PNG image formats for video thumbnails, not just JPEG. Matches PeerTube v8.1 image management overhaul.
**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/thumbnail*.go` — Accept image/webp and image/png MIME types
- Modify: `internal/storage/` or thumbnail processing code — Handle WebP/PNG storage
- Test: `internal/httpapi/handlers/video/thumbnail_test.go`

**Key Decisions / Notes:**

- Currently likely only accepts JPEG for thumbnails
- Add `image/webp` and `image/png` to accepted MIME types
- Store as-is (no conversion needed) — modern browsers support all three
- Validate magic bytes for each format (security)
- Use `testutil.CreateTestWebP()` and `testutil.CreateTestPNG()` for test data

**Definition of Done:**

- [ ] All tests pass
- [ ] WebP file accepted as video thumbnail
- [ ] PNG file accepted as video thumbnail
- [ ] JPEG still works (no regression)
- [ ] Magic byte validation for all three formats

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1 -run TestThumb`

---

### Task 11: Mastodon Verification + Video Public Link in ActivityPub

**Objective:** (A) Add `rel="me"` link support for Mastodon verification of PeerTube links. (B) Include video public link (`url` field) in ActivityPub video representation for federation discoverability with Akkoma, Sharkey, etc.
**Dependencies:** None

**Files:**

- Modify: `internal/activitypub/` — Add `url` Link object to Video AP representation
- Modify: `internal/httpapi/handlers/` or HTML template — Add `rel="me"` meta link for actor pages
- Test: `internal/activitypub/*_test.go`

**Key Decisions / Notes:**

- Mastodon verification: when a user's profile page includes `<a rel="me" href="https://mastodon.social/@user">`, Mastodon can verify the link. PeerTube adds this to actor pages.
- Video public link: PeerTube adds a `url` Link object pointing to the watch page URL, fixing discoverability for platforms that don't follow `id` for remote content.
- Both are additive AP changes.

**Definition of Done:**

- [ ] All tests pass
- [ ] ActivityPub Video objects include public watch page `url` Link
- [ ] Actor pages include `rel="me"` capability for external verification
- [ ] Existing federation tests pass
- [ ] NEW test asserts outgoing AP Video object contains a `url` Link object with `href` pointing to the watch page

**Verify:**

- `go test -short ./internal/activitypub/... -count=1`

---

### Task 12: playlistUrl in HLS Video File Response

**Objective:** Add `playlist_url` field to HLS video file JSON representation. Matches PeerTube v7.0 REST API addition.
**Dependencies:** None

**Files:**

- Modify: `internal/domain/video.go` or video file response type — Add PlaylistURL field
- Modify: Handler that serializes video files — Compute and include `playlist_url`
- Test: Appropriate video handler test

**Key Decisions / Notes:**

- `playlist_url` points to the HLS `.m3u8` playlist file for a specific resolution
- Compute from video ID + resolution: `/static/streaming-playlists/hls/{videoUUID}/{resolution}.m3u8`
- Only included for HLS video files (not webtorrent files)

**Definition of Done:**

- [ ] All tests pass
- [ ] HLS video file objects in API response include `playlist_url`
- [ ] Non-HLS files do not include `playlist_url`

**Verify:**

- `go test -short ./internal/httpapi/handlers/video/... -count=1`

---

### Task 13: Wire Activity Writes into Video/Collaborator Handlers

**Objective:** Integrate channel activity recording into existing video publish, video update, video delete, playlist change, and collaborator change handlers so the activities endpoint (Task 7) returns real data.
**Dependencies:** Task 7

**Files:**

- Modify: `internal/httpapi/handlers/video/video_handlers.go` — Call `activityRepo.CreateActivity()` on video publish/update/delete
- Modify: `internal/httpapi/handlers/channel/collaborators.go` — Call `activityRepo.CreateActivity()` on invite/accept/reject/delete
- Modify: `internal/httpapi/handlers/social/playlists*.go` — Call on playlist add/remove
- Modify: `internal/httpapi/routes.go` — Wire activity repo dependency into affected handlers
- Test: `internal/httpapi/handlers/channel/activities_test.go` — Integration test that creates a video then checks activities

**Key Decisions / Notes:**

- Activity writes are fire-and-forget — log errors but don't fail the primary operation
- Use the `CreateActivity` method created in Task 7's repository
- Action types: `video:publish`, `video:update`, `video:delete`, `playlist:add`, `playlist:remove`, `collaborator:invite`, `collaborator:accept`, `collaborator:reject`, `collaborator:remove`

**Definition of Done:**

- [ ] All tests pass
- [ ] Video publish creates a `video:publish` activity record
- [ ] Collaborator invite creates a `collaborator:invite` activity record
- [ ] `GET /api/v1/video-channels/{handle}/activities` returns non-empty results after channel actions
- [ ] Activity write failures are logged but do not fail the primary operation

**Verify:**

- `go test -short ./internal/httpapi/handlers/channel/... -count=1 -run TestActivit`
- `go test -short ./internal/httpapi/handlers/video/... -count=1`

---

## Deferred Ideas

These are tracked in the feature parity registry as separate work items:

1. **Sensitive Content Flags System** — PeerTube v7.2 major redesign. Needs its own `/spec` plan (~8-12 files, new domain model, migration, API endpoints).
2. **Player Theme System** — PeerTube v8.0 Lucide/Galaxy themes. Backend needs schema for theme preference at admin/channel/video level.
3. **Viewer Protocol V2** — PeerTube v7.0 concurrent viewer scalability. Architectural change to how live viewers are counted.
4. **Captions Object Storage** — PeerTube v7.1 separate S3 bucket for captions. Storage layer refactoring.
5. **SVG Logo Support** — PeerTube v8.1 admin SVG logos. Minor backend + frontend.

## Open Questions

None — all gaps are clearly defined with PeerTube source as reference.
