# PeerTube Full Parity Audit Implementation Plan

Created: 2026-03-21
Status: VERIFIED
Approved: Yes
Iterations: 0
Worktree: No
Type: Feature

## Summary

**Goal:** Close ALL remaining PeerTube API gaps: handle-based account/channel routes, channel avatar/banner upload, subscription batch check, video source download, podcast feed, admin user delete/block, config reset, custom homepage, instance media upload, user registrations workflow, jobs API, server following API, plugin install API, and playlist privacies.

**Architecture:** Each gap is a new handler + route registration in `internal/httpapi/routes.go`, backed by existing repository methods where possible. New repos needed for: custom pages, server following, user registrations.

**Tech Stack:** Go (Chi router), PostgreSQL (SQLX), existing `GetByUsername`/`GetByHandle` repo methods for handle resolution.

## Scope

### In Scope

1. Handle-based account routes (`/accounts/{name}/*`) — 5 endpoints
2. Handle-based channel routes (channel handle support) — 2 endpoints
3. Channel avatar/banner upload POST — 2 endpoints
4. Subscription batch check — 1 endpoint
5. Video source file download — 1 endpoint
6. Podcast RSS feed — 1 endpoint
7. Playlist privacy levels — 1 endpoint
8. Channel playlists — 1 endpoint
9. Admin user hard-delete — 1 endpoint
10. Admin user block/unblock — 2 endpoints
11. Config custom DELETE (reset) — 1 endpoint
12. Custom homepage pages — 2 endpoints
13. Instance avatar/banner upload/delete — 4 endpoints
14. User registrations admin workflow — 3 endpoints
15. User token sessions — 2 endpoints
16. Jobs API — 3 endpoints
17. Server following API — 7 endpoints
18. Plugin install/available API — 3 endpoints

### Out of Scope

- External runners (entire subsystem — Athena uses in-process FFmpeg)
- Video studio editing (complex FFmpeg pipeline)
- Storyboard generation
- Video source replacement (PUT)
- Video password protection
- User data export/import (GDPR)
- Channel syncs (YouTube auto-import)
- tus protocol (Athena uses custom chunked upload)

## Context for Implementer

> Write for an implementer who has never seen the codebase.

- **Patterns to follow:** See `internal/httpapi/handlers/auth/users.go` for handler pattern, `internal/httpapi/routes.go:159-658` for route registration
- **Conventions:** Handlers in `internal/httpapi/handlers/<domain>/`, repos in `internal/repository/`, domain models in `internal/domain/`
- **Key files:**
  - `internal/httpapi/routes.go` — all route registration
  - `internal/httpapi/shared/dependencies.go` — DI container
  - `internal/port/user.go:13` — `GetByUsername` already exists
  - `internal/port/channel.go:15` — `GetByHandle` already exists
  - `internal/repository/user_repository.go:163` — username lookup implementation
  - `internal/repository/channel_repository.go:84` — handle lookup implementation
- **Gotchas:**
  - PeerTube uses `@username@domain` handles; Athena uses UUIDs internally — handle routes resolve to UUID then delegate
  - Channel media DELETE exists (`channel_media.go`) but upload POST doesn't
  - `shared.WriteJSON` wraps in `{success, data, error, meta}` envelope
- **Domain context:** PeerTube clients expect handle-based URLs for federation interop

## Runtime Environment

- **Start command:** `make run` / Port: 8080
- **Health check:** `GET /health`

## Assumptions

- `GetByUsername` (user repo) and `GetByHandle` (channel repo) are performant for route resolution — supported by existing ActivityPub usage at `internal/usecase/activitypub/service.go:60`
- PostgreSQL schema already has `username` column indexed — supported by `GetByUsername` being used in login flow
- Channel `handle` column is indexed — supported by `GetByHandle` being used in subscriptions
- Playlist privacy is a static enum, not DB-driven — supported by `domain.PrivacyPublic` etc already existing
- Job queue state is queryable from existing worker infrastructure — Tasks 14 depend on this

## Risks and Mitigations

| Risk | Likelihood | Impact | Mitigation |
|------|------------|--------|------------|
| Handle resolution adds latency to every handle-based request | Medium | Low | Single indexed DB lookup, same as login flow |
| Server following API requires new DB tables | High | Medium | Create migration, table design matches PeerTube's `server` table |
| Jobs API needs access to worker queue internals | Medium | Medium | Expose read-only views through existing scheduler package |
| Plugin install from URL is a security concern | Medium | High | Validate URL, checksum verification, sandboxed execution |

## Goal Verification

### Truths

1. `GET /api/v1/accounts/alice` resolves user by username and returns profile
2. `POST /api/v1/channels/{id}/avatar` accepts image upload and stores it
3. `GET /api/v1/users/me/subscriptions/exist?uris=...` returns subscription status map
4. `GET /api/v1/videos/{id}/source` returns original video file download
5. `GET /feeds/podcast/videos.xml` returns podcast-compatible RSS
6. `DELETE /api/v1/config/custom` resets all config to defaults
7. `GET /api/v1/server/following` returns list of followed instances

### Artifacts

1. `internal/httpapi/handlers/account/` — new account handlers package
2. `internal/httpapi/handlers/channel/channel_media.go` — updated with upload
3. `internal/httpapi/handlers/admin/` — updated with user delete, block, registrations
4. `internal/repository/server_following_repository.go` — new
5. `migrations/NNNN_add_server_following.sql` — new migration
6. `internal/httpapi/handlers/video/feed_handlers.go` — updated with podcast feed

## Progress Tracking

- [x] Task 1: Handle-based account routes
- [x] Task 2: Handle-based channel routes
- [x] Task 3: Channel avatar/banner upload
- [x] Task 4: Subscription batch check
- [x] Task 5: Video source download
- [x] Task 6: Podcast RSS feed
- [x] Task 7: Playlist privacies + channel playlists
- [x] Task 8: Admin user delete + block/unblock
- [x] Task 9: Config reset + custom homepage
- [x] Task 10: Instance avatar/banner upload
- [x] Task 11: User registrations workflow
- [x] Task 12: User token sessions
- [x] Task 13: Jobs API
- [x] Task 14: Server following API
- [x] Task 15: Plugin install/available API

**Total Tasks:** 15 | **Completed:** 15 | **Remaining:** 0

## Implementation Tasks

### Task 1: Handle-based account routes

**Objective:** Add PeerTube-compatible `/accounts/{name}` routes that resolve username to user profile.

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/account/handlers.go`
- Create: `internal/httpapi/handlers/account/handlers_test.go`
- Modify: `internal/httpapi/routes.go` (add `/accounts` route group)

**Key Decisions / Notes:**

- Use existing `userRepo.GetByUsername()` for handle resolution (`internal/port/user.go:13`)
- Handle format: strip `@` prefix and `@domain` suffix if present (e.g., `@alice@example.com` → `alice`)
- 5 endpoints: GET account, GET account videos, GET account video-channels, GET account ratings, GET account followers
- Follow same response envelope pattern as `internal/httpapi/shared/response.go`

**Definition of Done:**

- [ ] `GET /api/v1/accounts/{name}` returns user profile by username
- [ ] `GET /api/v1/accounts/{name}/videos` returns user's videos
- [ ] `GET /api/v1/accounts/{name}/video-channels` returns user's channels
- [ ] `GET /api/v1/accounts/{name}/ratings` returns user's ratings
- [ ] `GET /api/v1/accounts/{name}/followers` returns user's followers
- [ ] All tests pass
- [ ] No diagnostics errors

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/account/... -short -count=1`

---

### Task 2: Handle-based channel routes

**Objective:** Add support for channel handle lookup alongside existing numeric ID routes.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/channel/channels.go` (add handle resolution logic)
- Create: `internal/httpapi/handlers/channel/channels_handle_test.go`
- Modify: `internal/httpapi/routes.go` (add `/video-channels/{channelHandle}` route group)

**Key Decisions / Notes:**

- Use existing `channelRepo.GetByHandle()` (`internal/port/channel.go:15`)
- Add new route group `/video-channels` that mirrors PeerTube paths
- Keep existing `/channels/{id}` routes as-is — these new routes are additive
- 2 new endpoints: GET channel by handle, GET channel's video-playlists (Task 7 overlap)

**Definition of Done:**

- [ ] `GET /api/v1/video-channels/{channelHandle}` returns channel by handle
- [ ] `GET /api/v1/video-channels/{channelHandle}/videos` returns channel videos
- [ ] Existing `/channels/{id}` routes still work
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/channel/... -short -count=1`

---

### Task 3: Channel avatar/banner upload

**Objective:** Add POST endpoints for uploading channel avatar and banner images.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/channel/channel_media.go` (add UploadAvatar, UploadBanner)
- Modify: `internal/httpapi/handlers/channel/channel_media_test.go`
- Modify: `internal/httpapi/routes.go` (register upload POST routes)

**Key Decisions / Notes:**

- `ChannelMediaRepository` interface (`channel_media.go:16`) already has `SetAvatar` and `SetBanner` repo methods. No HTTP upload handlers exist yet — write `UploadAvatar` and `UploadBanner` handler functions following the pattern in `internal/httpapi/handlers/auth/avatar.go` UploadAvatar handler
- `routes.go` lines 338-344 currently only register DELETE routes; add POST routes in the same `r.Route` block
- Use multipart form upload, validate image type (PNG, JPEG, WebP, GIF)
- Store via IPFS if available, local storage otherwise
- Max size: 8MB (matching PeerTube's `avatar.file.size.max`)

**Definition of Done:**

- [ ] `POST /api/v1/channels/{id}/avatar` accepts image upload and stores it
- [ ] `POST /api/v1/channels/{id}/banner` accepts image upload and stores it
- [ ] Only channel owner can upload
- [ ] Invalid file types rejected with 400
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/channel/... -short -count=1`

---

### Task 4: Subscription batch check

**Objective:** Add endpoint to check if user is subscribed to multiple channels in one call.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/channel/subscriptions.go` (add batch check handler)
- Create: `internal/httpapi/handlers/channel/subscriptions_exist_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `GET /users/me/subscriptions/exist?uris=channel1,channel2`
- Returns `{ "channel1": true, "channel2": false }`
- Use existing `SubRepo` to check each subscription
- Limit to 50 URIs per request

**Definition of Done:**

- [ ] `GET /api/v1/users/me/subscriptions/exist?uris=...` returns subscription map
- [ ] Handles comma-separated channel IDs/handles
- [ ] Rate limited to prevent abuse
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/channel/... -short -count=1`

---

### Task 5: Video source download

**Objective:** Allow downloading the original source video file.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/handlers.go` (add GetVideoSource handler)
- Create: `internal/httpapi/handlers/video/source_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `GET /videos/{id}/source` returns the original uploaded file
- Check video exists, check auth (owner or admin), stream file from storage
- Use `video.S3URLs["source"]` if S3, or local file path from encoding job `SourceFilePath`
- Set `Content-Disposition: attachment` header

**Definition of Done:**

- [ ] `GET /api/v1/videos/{id}/source` returns original video file
- [ ] Requires authentication (video owner or admin)
- [ ] Returns 404 if source file no longer available
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/video/... -short -count=1`

---

### Task 6: Podcast RSS feed

**Objective:** Add podcast-compatible RSS feed for video content.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/video/feed_handlers.go` (add PodcastFeed handler)
- Create: `internal/httpapi/handlers/video/feed_podcast_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `GET /feeds/podcast/videos.xml`
- Generate RSS 2.0 with iTunes podcast namespace extensions
- Include enclosure tags for video files, duration, episode metadata
- Filter to public videos only

**Definition of Done:**

- [ ] `GET /feeds/podcast/videos.xml` returns valid podcast RSS
- [ ] Includes `<itunes:*>` namespace tags
- [ ] Each video is an `<item>` with `<enclosure>` for media
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/video/... -short -count=1`

---

### Task 7: Playlist privacies + channel playlists

**Objective:** Add playlist privacy levels endpoint and channel playlists lookup.

**Dependencies:** Task 2 (channel handle routes)

**Files:**

- Modify: `internal/httpapi/handlers/social/playlists.go` (add GetPrivacies, GetChannelPlaylists)
- Create: `internal/httpapi/handlers/social/playlists_privacies_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `GET /video-playlists/privacies` returns `{1: "Public", 2: "Unlisted", 3: "Private"}`
- Static response from domain privacy constants
- `GET /video-channels/{channelHandle}/video-playlists` lists playlists belonging to a channel

**Definition of Done:**

- [ ] `GET /api/v1/video-playlists/privacies` returns privacy level map
- [ ] `GET /api/v1/video-channels/{channelHandle}/video-playlists` returns channel playlists
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/social/... -short -count=1`

---

### Task 8: Admin user delete + block/unblock

**Objective:** Add admin hard-delete user and user block/unblock endpoints.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/admin/user_handlers.go` (add DeleteUser, BlockUser, UnblockUser)
- Create: `internal/httpapi/handlers/admin/user_handlers_delete_test.go`
- Modify: `internal/httpapi/routes.go`
- (No repo changes needed — existing `Delete()` is already a hard delete)

**Key Decisions / Notes:**

- PeerTube: `DELETE /users/{id}`, `POST /users/{id}/block`, `POST /users/{id}/unblock`
- Reuse existing `userRepo.Delete()` — it already performs a hard `DELETE FROM users` (`user_repository.go:211`). Only new work is the admin handler and route registration
- Block sets `blocked=true` on user, prevents login
- Admin-only (`RequireRole("admin")`)

**Definition of Done:**

- [ ] `DELETE /api/v1/admin/users/{id}` hard-deletes user
- [ ] `POST /api/v1/users/{id}/block` blocks user (admin only)
- [ ] `POST /api/v1/users/{id}/unblock` unblocks user (admin only)
- [ ] Blocked users cannot login
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/admin/... -short -count=1`

---

### Task 9: Config reset + custom homepage

**Objective:** Add config reset-to-defaults and custom homepage page management.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/admin/config_handler.go` (add DeleteCustomConfig)
- Modify: `internal/httpapi/handlers/admin/instance.go` (add GetCustomHomepage, UpdateCustomHomepage)
- Create: `internal/httpapi/handlers/admin/config_reset_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `DELETE /config/custom` resets all custom config to defaults
- `GET /custom-pages/homepage/instance` and `PUT /custom-pages/homepage/instance`
- Homepage content stored as instance config key `homepage_content`
- Admin-only for PUT, public for GET

**Definition of Done:**

- [ ] `DELETE /api/v1/config/custom` resets config to defaults (admin only)
- [ ] `GET /api/v1/custom-pages/homepage/instance` returns homepage content
- [ ] `PUT /api/v1/custom-pages/homepage/instance` updates homepage (admin only)
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/admin/... -short -count=1`

---

### Task 10: Instance avatar/banner upload

**Objective:** Add instance-level avatar and banner image upload/delete.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/admin/instance.go` (add media upload handlers)
- Create: `internal/httpapi/handlers/admin/instance_media_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `POST/DELETE /config/instance-avatar/pick`, `POST/DELETE /config/instance-banner/pick`
- Store as instance config keys `instance_avatar_path` and `instance_banner_path`
- Admin-only, same multipart upload pattern as channel/user avatar

**Definition of Done:**

- [ ] `POST /api/v1/config/instance-avatar/pick` uploads instance avatar
- [ ] `DELETE /api/v1/config/instance-avatar/pick` removes instance avatar
- [ ] `POST /api/v1/config/instance-banner/pick` uploads instance banner
- [ ] `DELETE /api/v1/config/instance-banner/pick` removes instance banner
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/admin/... -short -count=1`

---

### Task 11: User registrations admin workflow

**Objective:** Add admin endpoints to view/approve/reject pending user registrations.

**Dependencies:** None

**Files:**

- Create: `internal/repository/registration_repository.go`
- Create: `internal/repository/registration_repository_test.go`
- Create: `internal/httpapi/handlers/admin/registrations.go`
- Create: `internal/httpapi/handlers/admin/registrations_test.go`
- Create: `migrations/NNNN_add_user_registrations.sql`
- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`

**Key Decisions / Notes:**

- PeerTube: `GET /users/registrations`, `POST /users/registrations/{id}/accept`, `POST /users/registrations/{id}/reject`
- New `user_registrations` table: id, username, email, reason, status (pending/accepted/rejected), moderator_response, created_at
- Registration flow: register → pending → admin approves/rejects
- Gate the approval-required path behind a config flag (`instance.registrationRequiresApproval` bool from instance config). When flag is false (default), register flow is unchanged. When true, create registration record instead of user. Update existing register handler tests to cover both code paths — zero regression risk on the default path

**Definition of Done:**

- [ ] `GET /api/v1/admin/registrations` lists pending registrations
- [ ] `POST /api/v1/admin/registrations/{id}/accept` approves and creates user
- [ ] `POST /api/v1/admin/registrations/{id}/reject` rejects with reason
- [ ] Migration creates `user_registrations` table
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/admin/... ./internal/repository/... -short -count=1`

---

### Task 12: User token sessions

**Objective:** Add endpoints to list and revoke user token sessions.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/auth/handlers.go` or create `internal/httpapi/handlers/auth/sessions.go`
- Create: `internal/httpapi/handlers/auth/sessions_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `GET /users/{id}/token-sessions`, `POST /users/{id}/token-sessions/{tokenSessionId}/revoke`
- Use existing session/auth repository to list active sessions
- Admin or self only

**Definition of Done:**

- [ ] `GET /api/v1/users/{id}/token-sessions` lists active sessions
- [ ] `POST /api/v1/users/{id}/token-sessions/{tokenSessionId}/revoke` revokes a session
- [ ] Only accessible by user themselves or admin
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/auth/... -short -count=1`

---

### Task 13: Jobs API

**Objective:** Add admin endpoints for job queue management.

**Dependencies:** None

**Files:**

- Create: `internal/httpapi/handlers/admin/jobs.go`
- Create: `internal/httpapi/handlers/admin/jobs_test.go`
- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go` (add scheduler dependency if needed)

**Key Decisions / Notes:**

- PeerTube: `GET /jobs/{state}`, `POST /jobs/pause`, `POST /jobs/resume`
- Query encoding jobs from `encoding_jobs` table by status
- Pause/resume: set a flag in scheduler that stops picking up new jobs
- Admin-only

**Definition of Done:**

- [ ] `GET /api/v1/admin/jobs/{state}` lists jobs filtered by state (active/waiting/failed/completed)
- [ ] `POST /api/v1/admin/jobs/pause` pauses job processing
- [ ] `POST /api/v1/admin/jobs/resume` resumes job processing
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/admin/... -short -count=1`

---

### Task 14: Server following API

**Objective:** Add instance-to-instance following/followers management for federation.

**Dependencies:** None

**Files:**

- Create: `internal/domain/server_following.go` (ServerFollowing struct + status constants)
- Create: `internal/port/server_following.go` (ServerFollowingRepository interface)
- Create: `internal/repository/server_following_repository.go`
- Create: `internal/repository/server_following_repository_test.go`
- Create: `internal/httpapi/handlers/federation/server_following.go`
- Create: `internal/httpapi/handlers/federation/server_following_test.go`
- Create: `migrations/NNNN_add_server_following.sql`
- Modify: `internal/httpapi/routes.go`
- Modify: `internal/httpapi/shared/dependencies.go`

**Key Decisions / Notes:**

- PeerTube: GET/POST/DELETE for `/server/following` and `/server/followers`
- New `server_following` table: id, host, state (pending/accepted), follower (boolean), created_at
- ActivityPub Follow activity emission is out of scope for this task — emit a TODO comment at the POST handler; log the follow intent but do not send the AP activity until federation wiring is verified end-to-end
- 7 endpoints total: list followers, list following, follow, unfollow, accept/reject follower, delete follower

**Definition of Done:**

- [ ] `GET /api/v1/server/followers` lists instance followers
- [ ] `GET /api/v1/server/following` lists followed instances
- [ ] `POST /api/v1/server/following` follows an instance (admin)
- [ ] `DELETE /api/v1/server/following/{host}` unfollows (admin)
- [ ] `POST /api/v1/server/followers/{host}/accept` accepts follower (admin)
- [ ] `POST /api/v1/server/followers/{host}/reject` rejects follower (admin)
- [ ] `DELETE /api/v1/server/followers/{host}` removes follower (admin)
- [ ] Migration creates `server_following` table
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/federation/... ./internal/repository/... -short -count=1`

---

### Task 15: Plugin install/available API

**Objective:** Add REST API for installing plugins from URL and browsing available plugins.

**Dependencies:** None

**Files:**

- Modify: `internal/httpapi/handlers/plugin/handlers.go` (add InstallPlugin, ListAvailable)
- Create: `internal/httpapi/handlers/plugin/install_test.go`
- Modify: `internal/httpapi/routes.go`

**Key Decisions / Notes:**

- PeerTube: `POST /plugins/install`, `GET /plugins/available`
- Install from URL: download, validate, register with plugin manager
- Available: return list of known plugins (could be hardcoded initially or from a registry URL)
- Admin-only
- Security: validate download URL, check plugin manifest, sandbox execution

**Definition of Done:**

- [ ] `POST /api/v1/admin/plugins/install` installs plugin from URL
- [ ] `GET /api/v1/admin/plugins/available` lists available plugins
- [ ] Plugin uninstall already exists at `routes.go:642` (`DELETE /{name}`) — no work needed
- [ ] URL validation prevents SSRF
- [ ] All tests pass

**Verify:**

- `GOROOT=$(/opt/homebrew/bin/go env GOROOT) PATH="/opt/homebrew/bin:$PATH" go test ./internal/httpapi/handlers/plugin/... -short -count=1`

---

## Open Questions

None — all decisions resolved through user input.

### Deferred Ideas

- tus protocol support for resumable uploads (PeerTube uses tus, Athena has custom chunked)
- Video thumbnails list endpoint (`GET /videos/{id}/thumbnails`)
- Video embed endpoint (currently handled by oEmbed)
- Accounts list endpoint (`GET /api/v1/accounts` — list all accounts)
