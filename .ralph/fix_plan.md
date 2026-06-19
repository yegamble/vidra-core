# Vidra Core Ralph Fix Plan

> Repo target: `vidra-core` only.
> Ralph must not modify `vidra-user` from this repo. Frontend tasks belong in the frontend repository and are tracked here only when a backend contract is needed.

## Operating Rules

- [ ] Before every loop, read `.ralph/PROMPT.md`, this `fix_plan.md`, `.ralph/AGENT.md`, and all files in `.ralph/specs/`.
- [ ] Work on one coherent vertical slice per loop.
- [ ] Search the codebase before adding new packages, types, tables, endpoints, workers, or config.
- [ ] Keep PeerTube parity evidence current: feature ledger, UI/control inventory, endpoint inventory, acceptance notes, and test evidence.
- [ ] Never mark a feature `VERIFIED` without evidence: tests, screenshots/logs, endpoint contract, migration, or manual QA notes.
- [ ] Never set `EXIT_SIGNAL: true` until every in-scope parity item and Vidra extension is `VERIFIED`, `INTENTIONAL_DIFFERENCE`, or explicitly deferred by the user.
- [ ] Keep commits small and descriptive.
- [ ] Do not store secrets, production credentials, stream keys, JWT signing keys, OAuth secrets, or wallet private keys in the repo.
- [ ] Do not copy PeerTube source code, assets, branding, screenshots, or exact styling. Use PeerTube only as behavioral reference.

## Definition of Done for Any Feature

- [ ] Requirement is listed in the correct ledger.
- [ ] Data model or contract is documented.
- [ ] Implementation is complete with no placeholder behavior.
- [ ] Authz/authn behavior is explicit.
- [ ] Error responses are typed and documented.
- [ ] Unit tests cover core logic.
- [ ] Integration tests cover database/cache/external boundary where applicable.
- [ ] Smoke/API tests cover the happy path.
- [ ] Security impact is considered.
- [ ] Observability/logging is adequate.
- [ ] `.ralph/fix_plan.md`, relevant ledger rows, and `.ralph/AGENT.md` are updated.
- [ ] Focused checks pass locally or the failure is documented as a blocker.

---

# P0 — Ralph Control Plane and Parity Tracking

## P0.1 Required Ralph Files

- [ ] Verify `.ralph/PROMPT.md` exists and includes Vidra-specific rules.
- [ ] Verify `.ralph/AGENT.md` exists and has accurate backend commands.
- [ ] Verify `.ralph/specs/` exists.
- [ ] Verify `.ralph/specs/peertube-reference.md` exists.
- [ ] Verify `.ralph/specs/peertube-feature-ledger.md` exists.
- [ ] Verify `.ralph/specs/peertube-ui-inventory.md` exists.
- [ ] Verify `.ralph/specs/vidra-extensions-ledger.md` exists.
- [ ] Verify `.ralph/specs/parity-acceptance.md` exists.
- [ ] Add or update ledger status vocabulary: `TODO`, `IN_PROGRESS`, `IMPLEMENTED`, `TESTED`, `VERIFIED`, `INTENTIONAL_DIFFERENCE`, `DEFERRED`.
- [ ] Add evidence fields to ledgers: owner repo, files, tests, API endpoints, UI controls, notes, verification date.

## P0.2 PeerTube Reference Inventory

- [ ] Pin PeerTube reference version/date used for parity analysis.
- [ ] Record official documentation URLs used for watch, account, library, publish/live, studio, stats, channel sync, search, mute, report, accessibility, admin, REST API, ActivityPub, embed player, plugins/themes, and storage behavior.
- [ ] Download or inspect PeerTube OpenAPI reference and generate an initial endpoint inventory.
- [ ] Create an endpoint mapping table: PeerTube endpoint → Vidra endpoint → status → tests → intentional difference.
- [ ] Create a backend model mapping table: PeerTube concept → Vidra table/type → status → notes.
- [ ] Create a background job mapping table: PeerTube job/task → Vidra worker/job → status → tests.
- [ ] Create a config mapping table: PeerTube setting → Vidra config key/env var/admin setting → status.
- [ ] Create a moderation mapping table: PeerTube moderation behavior → Vidra behavior → status.
- [ ] Create federation protocol mapping: ActivityPub behavior → Vidra implementation → status.
- [ ] Add ATProto/Bluesky as a Vidra extension, not PeerTube parity.

## P0.3 Route and Button-Level Parity Discipline

- [ ] For each feature family, require a route/control inventory before coding broad UI/API changes.
- [ ] For each user-visible control, capture: label/icon, route, role visibility, enabled/disabled states, backend endpoint, errors, tests, and status.
- [ ] For each backend-only feature, capture: endpoint, method, request/response schema, auth rule, rate limit, validation, and tests.
- [ ] Add a rule that broad items like “upload complete” are not complete until all buttons, tabs, dropdowns, modals, errors, and background states are inventoried and verified.

---

# P1 — Backend Project Foundation

## P1.1 Go Project Scaffold

- [ ] Initialize or verify Go module.
- [ ] Choose stable package layout: `cmd/`, `internal/`, `pkg/` only where justified.
- [ ] Add `cmd/api` entrypoint.
- [ ] Add `cmd/worker` entrypoint.
- [ ] Add `cmd/migrate` or document migration command.
- [ ] Add `internal/config`.
- [ ] Add `internal/http`.
- [ ] Add `internal/db`.
- [ ] Add `internal/cache`.
- [ ] Add `internal/auth`.
- [ ] Add `internal/media`.
- [ ] Add `internal/storage`.
- [ ] Add `internal/federation`.
- [ ] Add `internal/messaging`.
- [ ] Add `internal/moderation`.
- [ ] Add `internal/observability`.
- [ ] Add `internal/testutil`.
- [ ] Ensure `go test ./...` runs, even if most packages are empty foundations.

## P1.2 Configuration

- [ ] Add typed configuration struct.
- [ ] Support `.env`, environment variables, and Docker Compose defaults.
- [ ] Add `.env.example`.
- [ ] Validate required config on startup.
- [ ] Add safe defaults for local development.
- [ ] Add config for HTTP server address/port.
- [ ] Add config for PostgreSQL DSN/pool.
- [ ] Add config for Redis URL/pool.
- [x] Add config for JWT keys/issuer/audience/expiry. (`JWT_SECRET`/`JWT_ISSUER`/`JWT_AUDIENCE`/`JWT_ACCESS_TTL`; prod rejects the dev default and short secrets)
- [ ] Add config for OAuth2 providers, disabled by default.
- [ ] Add config for TOTP issuer.
- [ ] Add config for CORS allowlist.
- [x] Add config for rate limiting. (`RATE_LIMIT_ENABLED`/`RATE_LIMIT_REQUESTS`/`RATE_LIMIT_WINDOW`, validated when enabled)
- [ ] Add config for SSRF allow/deny behavior.
- [ ] Add config for storage backend: local, S3-compatible, IPFS.
- [ ] Add config for FFmpeg paths and transcoding options.
- [ ] Add config for ClamAV and fallback mode.
- [ ] Add config for RTMP/HLS.
- [ ] Add config for Whisper captions, disabled by default.
- [ ] Add config for ActivityPub, disabled/enabled per instance.
- [ ] Add config for ATProto/Bluesky, disabled by default.
- [ ] Add config tests for defaults, env override, validation failure, and secret redaction.

## P1.3 Docker-First Development

- [ ] Add `Dockerfile` for API.
- [ ] Add `Dockerfile.worker` or multi-target Dockerfile.
- [ ] Add `docker-compose.yml` for API, worker, PostgreSQL, Redis.
- [ ] Add optional Compose profile for ClamAV.
- [ ] Add optional Compose profile for MinIO/S3-compatible storage.
- [ ] Add optional Compose profile for IPFS/Kubo.
- [ ] Add optional Compose profile for RTMP/HLS.
- [ ] Add optional Compose profile for Whisper.
- [ ] Add named volumes for PostgreSQL, Redis, media, and object-storage emulator.
- [ ] Add health checks for all first-party containers.
- [ ] Add Makefile or task runner commands: `dev`, `up`, `down`, `logs`, `test`, `lint`, `migrate`, `seed`.
- [ ] Document how to run only API, only worker, only dependencies, and all services.

## P1.4 CI Skeleton

> NOTE (monorepo): GitHub Actions workflows live at the repository root in
> `../.github/workflows/` (GitHub does not read workflows from subdirectories).
> Backend workflows must use `vidra-core/**` path filters and a `vidra-core`
> working directory. This is the one allowed cross-boundary edit from this repo.

- [ ] Add GitHub Actions workflow for Go tests.
- [ ] Add GitHub Actions workflow for lint/static analysis.
- [ ] Add GitHub Actions workflow for Docker build.
- [ ] Add shared/reusable workflow or composite action for dependency setup.
- [ ] Add Go module cache.
- [ ] Add Docker layer cache.
- [ ] Add PostgreSQL and Redis service containers for integration tests.
- [ ] Add artifact upload for test reports/logs.
- [ ] Keep CI under reasonable runtime by splitting smoke, unit, integration, fuzz, and benchmark jobs.

---

# P2 — Database, Migrations, and sqlc

## P2.1 Database Foundation

- [ ] Choose migration tool and document why.
- [ ] Add initial migration for required PostgreSQL extensions: `pg_trgm`, `uuid-ossp`.
- [ ] Add migration for schema version tracking if not provided by tool.
- [ ] Add connection pooling with sane limits and timeouts.
- [ ] Add database readiness check.
- [ ] Add transactional test helper.
- [ ] Add migration up/down smoke test against live PostgreSQL.
- [ ] Add rollback test for initial migrations where feasible.

## P2.2 Core Tables

- [ ] Add accounts/users table.
- [ ] Add roles/permissions table or enum strategy.
- [x] Add sessions/refresh tokens table if not Redis-only. (`sessions` table in 0002; sqlc queries in `internal/store/queries/sessions.sql` — Create/Get-by-hash/Revoke/RevokeAll/DeleteExpired)
- [ ] Add OAuth identities table.
- [ ] Add TOTP/MFA settings table.
- [x] Add channels table. (migration `0003_channels`; owner FK → users, unique `lower(handle)`, trigram index; integration test asserts the table exists)
- [x] Add videos table. (migration `0006_videos`: channel FK, title/description, privacy + state CHECK enums, channel + partial public-published indexes; integration test asserts table)
- [ ] Add video files/renditions table.
- [ ] Add streaming playlists/HLS assets table.
- [ ] Add thumbnails/previews/storyboards table.
- [ ] Add captions/subtitles table.
- [ ] Add video imports table.
- [ ] Add live streams table.
- [ ] Add playlists table.
- [ ] Add playlist items table.
- [ ] Add comments table.
- [ ] Add likes/dislikes or reactions table according to spec.
- [ ] Add watch history table.
- [ ] Add watch later/private library tables.
- [x] Add follows/subscriptions table. (migration `0005_channel_follows`: `channel_follows` (follower_id, channel_id) composite PK + channel_id index; sqlc Follow/Unfollow/CountFollowers/IsFollowing)
- [ ] Add notifications table.
- [ ] Add abuse reports table.
- [ ] Add video blocks/quarantine table.
- [ ] Add watched words lists and matches tables.
- [ ] Add muted accounts/instances table.
- [ ] Add admin audit log table.
- [ ] Add federation actors table.
- [ ] Add federation activities/inbox/outbox table.
- [ ] Add ATProto identities/events tables.
- [ ] Add direct messages conversations table.
- [ ] Add direct messages table.
- [ ] Add encrypted message device/prekey/session tables if E2EE is enabled.
- [ ] Add attachments table.
- [ ] Add link previews table.
- [ ] Add crypto donation addresses table.
- [ ] Add verification challenges for donation addresses.

## P2.3 sqlc

- [ ] Add `sqlc.yaml`.
- [ ] Generate typed queries for health/readiness.
- [ ] Generate typed queries for users/accounts.
- [x] Generate typed queries for channels. (`internal/store/queries/channels.sql` — Create / GetByID / GetByHandle / ListByOwner / CountByOwner)
- [x] Generate typed queries for videos. (`internal/store/queries/videos.sql` — CreateVideo / GetVideoByID (joined owner_id) / ListVideosByChannel / ListPublicVideosByChannel / UpdateVideo / DeleteVideo)
- [ ] Generate typed queries for playlists.
- [ ] Generate typed queries for messaging.
- [ ] Generate typed queries for moderation.
- [ ] Add sqlc generation command to Makefile/task runner.
- [ ] Add CI check that generated sqlc output is current.
- [ ] Add tests for critical query behavior.

---

# P3 — HTTP API and Contracts

## P3.1 API Foundation

- [ ] Add Echo server setup.
- [ ] Add request ID middleware.
- [x] Add structured logging middleware. (slog request logger, `server.go requestLogger`; level escalates by status class)
- [ ] Add panic recovery middleware.
- [ ] Add CORS middleware with config allowlist.
- [x] Add body size limits. (`middleware.BodyLimit(cfg.HTTPBodyLimit)`, default 8M, configurable via `HTTP_BODY_LIMIT`; oversized → 413 `request_entity_too_large` envelope; tested)
- [x] Add timeout middleware. (`requestDeadline` propagates a per-request context deadline, `HTTP_REQUEST_TIMEOUT` default 30s; ctx-deadline → 503 `request_timeout` envelope; server WriteTimeout is the hard backstop; tested)
- [x] Add rate limit middleware using Redis. (`internal/ratelimit` fixed-window via Redis INCR+ExpireNX+PTTL behind a `Counter` interface; `httpapi` middleware on `/api` per client IP, `X-RateLimit-*` headers, `429 rate_limited` envelope + `Retry-After`, fails open if Redis down, system probes exempt; unit-tested with a fake counter + Redis-gated integration test)
- [x] Add JWT auth middleware. (`auth_middleware.go requireAuth` — Bearer → `auth.Service.Parse` → principal (user ID + role) in context; any failure → 401 without revealing which check failed; `bearerToken` parser unit-tested; powers `GET /api/v1/auth/me`)
- [x] Add role/permission middleware. (`auth_middleware.go requireRole(...roles)` — chains after `requireAuth`; principal lacking an allowed role → 403, no principal → 401; tested. Ready for P9 admin routes to mount.)
- [x] Add consistent JSON error envelope. (`errors.go` — `ErrorResponse {error:{code,message,request_id}}` via custom `echo.HTTPErrorHandler`; 5xx detail hidden; documented as `ErrorResponse` in `api/openapi.yaml`; tested)
- [x] Add validation layer. (`validation.go` — `bindAndValidate` + `Validatable` interface; malformed body → 400 `bad_request`, failed validation → 422 `unprocessable_entity` with a `fields` array; dependency-free, documented in `api/openapi.yaml ErrorResponse`; tested)
- [x] Maintain an OpenAPI contract at `api/openapi.yaml` as the source of truth for the HTTP API (seeded for the system endpoints).
- [x] Add a route↔spec drift stop guard (`TestOpenAPIContract` in `internal/httpapi`) that fails the build when routes and `api/openapi.yaml` diverge.
- [x] Add the `openapi.yml` GitHub Actions workflow (Redocly lint + `make openapi-verify`) and `make openapi-lint` / `openapi-verify` / `docs-check` targets.
- [ ] Extend `api/openapi.yaml` (and its schemas) as each new endpoint family lands, keeping the drift guard green every slice.
- [ ] Generate or validate TypeScript client/types for `vidra-user` from `api/openapi.yaml`.
- [ ] Add Postman collection scaffold.
- [ ] Add API smoke tests against live Docker database.

## P3.2 System Endpoints

- [x] `GET /healthz`. (`internal/httpapi/health.go`, tested)
- [x] `GET /readyz`. (postgres + redis readiness, 503 when degraded, tested)
- [x] `GET /version`. (`version.go` + `internal/version` package, ldflags-injected via `make build`; documented + tested)
- [ ] `GET /nodeinfo/2.0.json` or documented intentional difference. (minimal
      `GET /api/v1/nodeinfo` exists; canonical NodeInfo path still TODO)
- [ ] `GET /.well-known/nodeinfo` or documented intentional difference.
- [ ] `GET /.well-known/webfinger` for federation identity lookup when ActivityPub is enabled.
- [x] Add tests for currently-registered system endpoints. (`internal/httpapi/health_test.go`)

---

# P4 — Auth, Accounts, and Identity

- [x] Implement registration enable/disable setting. (`REGISTRATION_ENABLED` config, default true; `POST /api/v1/auth/register` → 403 when disabled; surfaced in `GET /api/v1/instance`; tested)
- [x] Implement account signup. (`POST /api/v1/auth/register`, `internal/auth.Service.Register`; first account → admin; unique violation → 409; tested)
- [ ] Implement email verification token flow placeholder or adapter boundary.
- [x] Implement login. (`POST /api/v1/auth/login`, `internal/auth.Service.Login`; enumeration-safe 401; disabled → 403; tested)
- [x] Implement refresh token/session rotation. (`POST /api/v1/auth/refresh`; register/login persist a hashed refresh token in `sessions`, refresh rotates (revoke old + issue new); rotated-token reuse → revoke all sessions; opaque 256-bit token, SHA-256 stored; `JWT_REFRESH_TTL` default 720h; tested)
- [x] Implement logout current session. (`POST /api/v1/auth/logout` revokes the presented refresh token; idempotent 204; tested)
- [x] Implement logout all sessions. (`POST /api/v1/auth/logout-all` behind `requireAuth` → `Service.LogoutAll` revokes every active session for the principal; 204; tested)
- [ ] Implement password reset request/complete flow.
- [x] Implement password hashing with modern algorithm. (bcrypt cost 12, `internal/auth/password.go`; salted, tested)
- [x] Implement JWT claims and validation. (`internal/auth/jwt.go` HS256 via golang-jwt/v5; sub+role+iss+aud+exp, alg pinned; issue/parse tested incl. tamper/expiry/audience)
- [ ] Implement OAuth2 provider abstraction.
- [ ] Implement TOTP enrollment.
- [ ] Implement TOTP verification.
- [ ] Implement recovery codes.
- [ ] Implement account export request/status/download foundation.
- [ ] Implement account import foundation.
- [ ] Implement account deletion/deactivation.
- [ ] Add auth rate limits.
- [ ] Add auth audit logs.
- [ ] Add unit/integration tests for signup/login/session/MFA.
- [ ] Add Postman tests for auth happy/error paths.

---

# P5 — Channels, Profiles, and Instance Metadata

- [x] Implement account profile read/update. (migration `0004_user_profile` adds `display_name`+`bio`; read via `GET /api/v1/auth/me`, update via `PATCH /api/v1/auth/me` (partial, behind `requireAuth`); identity fields username/email deferred to a dedicated re-verification flow; `userView` exposes the new fields; tested)
- [ ] Implement avatar upload/storage.
- [ ] Implement banner upload/storage.
- [x] Implement channel create/read/update/delete. (`POST /api/v1/channels`, `GET /api/v1/me/channels`, `GET /api/v1/channels/:handle`, `PATCH`/`DELETE /api/v1/channels/:handle` (owner-only, partial PATCH via COALESCE); `internal/channel`; tested)
- [ ] Implement channel avatar/banner.
- [x] Implement channel ownership and permissions. (channels created under the authed principal's `owner_id`; create/list/update/delete behind `requireAuth`; update/delete enforce owner == principal → 403 otherwise; handle uniqueness → 409; tested)
- [x] Implement public channel page data endpoint. (`GET /api/v1/channels/:handle`, case-insensitive, no auth; 404 envelope when absent; tested)
- [x] Implement account/channel follow model. (`POST`/`DELETE /api/v1/channels/:handle/follow` behind `requireAuth`, idempotent 204; `follower_count` on the channel view; `internal/channel` Follow/Unfollow/FollowerCount; tested)
- [ ] Implement channel sync placeholder/foundation for remote channels.
- [x] Implement instance about/config endpoint for frontend. (`GET /api/v1/instance` (public) → name, software{name,version}, registration_enabled; `internal/httpapi/instance.go`; documented + tested)
- [x] Implement terms/privacy/about/contact instance metadata. (`GET /api/v1/instance` now returns description, terms_url, privacy_url, contact_email from `INSTANCE_DESCRIPTION`/`INSTANCE_TERMS_URL`/`INSTANCE_PRIVACY_URL`/`INSTANCE_CONTACT_EMAIL`; documented + tested)
- [~] Add tests for channel/profile permissions. (channel: create-requires-auth, validation, duplicate-409, create→list→public-get, get-404, owner/non-owner update-403, delete-403/204, plus service unit tests; profile tests pending the profile slice)

---

# P6 — Video Publishing and Media Pipeline

## P6.1 Upload and Import

- [x] Implement create video draft/upload session. (`POST /api/v1/channels/:handle/videos` (requireAuth, owner-only) creates a draft; `GET /api/v1/videos/:id` (optionalAuth) public/unlisted to anyone, private owner-only (else 404); `PATCH`/`DELETE /api/v1/videos/:id` owner-only (non-owner/unknown → 404); `GET /api/v1/channels/:handle/videos` (optionalAuth) lists all for the owner, public-only otherwise; `internal/video`; tested. File upload itself is a later slice.)
- [ ] Implement local file upload.
- [ ] Implement resumable upload strategy or documented initial limitation.
- [ ] Implement upload progress/status in Redis and database.
- [ ] Implement video metadata validation: title, description, tags, category, language, license, privacy, channel.
- [~] Implement privacy levels. (videos: public/unlisted/private enforced on read — private hidden as 404 to non-owners; account/channel-level privacy still TODO)
- [ ] Implement publish date/scheduled publish.
- [ ] Implement file validation.
- [ ] Implement ClamAV scan integration.
- [ ] Implement ClamAV fallback modes: fail-closed, fail-open, quarantine.
- [ ] Implement URL import with SSRF protection.
- [ ] Implement torrent/magnet import placeholder or adapter boundary.
- [ ] Implement upload cancellation.
- [ ] Implement failed upload cleanup.
- [ ] Add API tests for file upload, URL import, validation errors, and scan failure.

## P6.2 Storage

- [ ] Implement storage interface.
- [ ] Implement local storage backend.
- [ ] Implement S3-compatible backend.
- [ ] Implement Backblaze B2-compatible configuration.
- [ ] Implement DigitalOcean Spaces-compatible configuration.
- [ ] Implement IPFS backend adapter or deferred spec.
- [ ] Implement object key naming strategy.
- [ ] Implement private/public object handling.
- [ ] Implement signed URL or proxy strategy.
- [ ] Implement media deletion/garbage collection.
- [ ] Add integration tests using local filesystem and MinIO.

## P6.3 Transcoding

- [ ] Implement FFmpeg probe.
- [ ] Implement media metadata extraction.
- [ ] Implement H.264 profile.
- [ ] Implement VP9 profile.
- [ ] Implement AV1 profile.
- [ ] Implement HLS output.
- [ ] Implement thumbnail generation.
- [ ] Implement preview generation.
- [ ] Implement storyboard generation or documented defer.
- [ ] Implement worker queue for transcode jobs.
- [ ] Implement retry/backoff/dead-letter behavior.
- [ ] Implement status updates in Redis and PostgreSQL.
- [ ] Add unit tests for job planning.
- [ ] Add smoke test with tiny fixture video.

---

# P7 — Playback, Discovery, and Public Video API

- [x] Implement public video list endpoint. (`GET /api/v1/videos` (public, paginated limit≤100/offset) → cross-channel public videos newest-first; sqlc `ListPublicVideos`; `internal/video.ListPublic`; tested. State filtering (published-only) joins once the publish pipeline exists.)
- [ ] Implement local videos endpoint.
- [~] Implement trending/recent/popular sort modes or documented staged rollout. (recent (newest-first) shipped via `GET /api/v1/videos`; trending/popular need view counts — staged after the views slice)
- [ ] Implement video detail endpoint.
- [ ] Implement video playback manifest endpoint.
- [ ] Implement captions endpoint.
- [ ] Implement download metadata endpoint.
- [ ] Implement share/embed metadata endpoint.
- [ ] Implement oEmbed or documented difference.
- [ ] Implement OpenGraph metadata.
- [x] Implement search endpoint with PostgreSQL trigram search. (`GET /api/v1/videos/search?q=` (public, paginated) over public video titles; ILIKE filter ranked by `similarity()`; migration `0007` adds a `gin_trgm_ops` index on `videos.title`; sqlc `SearchPublicVideos`; `internal/video.SearchPublic`; tested. Channel/account search still TODO.)
- [ ] Implement tags/categories/languages/licenses config endpoints.
- [ ] Implement view count recording with abuse/rate-limit protection.
- [ ] Implement watch progress endpoint.
- [ ] Add tests for public visibility/privacy rules.

---

# P8 — Library, Playlists, Comments, and Notifications

- [ ] Implement watch history.
- [ ] Implement resume progress.
- [ ] Implement watch later playlist.
- [ ] Implement playlist create/read/update/delete.
- [ ] Implement playlist visibility rules.
- [ ] Implement playlist item add/remove/reorder.
- [ ] Implement quick-add to playlist API.
- [ ] Implement comments create/read/update/delete.
- [ ] Implement comment threading if in-scope.
- [ ] Implement comment moderation hooks.
- [ ] Implement video like/dislike or reaction behavior according to spec.
- [ ] Implement subscriptions/follows.
- [ ] Implement notification creation/read/mark-read.
- [ ] Add tests for playlist permissions, history privacy, and comment moderation.

---

# P9 — Moderation, Admin, and Safety

- [ ] Implement roles: user, moderator, admin, owner.
- [ ] Implement admin users list/search/filter.
- [ ] Implement user edit: role, quota, enabled/disabled, bypass quarantine, email verified.
- [ ] Implement registration approval queue.
- [ ] Implement abuse reports for videos/comments/accounts.
- [ ] Implement report accept/reject/delete/internal note.
- [ ] Implement notifications to reporter where applicable.
- [ ] Implement video block manual flow.
- [ ] Implement video unblock flow.
- [ ] Implement auto-block/quarantine setting.
- [ ] Implement video quarantine approve/reject.
- [ ] Implement muted accounts.
- [ ] Implement muted instances.
- [ ] Implement watched words lists.
- [ ] Implement watched words tagging for videos/comments.
- [ ] Implement admin comments overview.
- [ ] Implement admin videos overview.
- [ ] Implement admin audit log.
- [ ] Implement rate-limit management endpoints or config-only decision.
- [ ] Add moderation integration tests.
- [ ] Add Postman admin collection tests.

---

# P10 — Federation

## P10.1 ActivityPub

- [ ] Implement local actor model for accounts.
- [ ] Implement local actor model for channels.
- [ ] Implement WebFinger.
- [ ] Implement ActivityPub actor endpoints.
- [ ] Implement inbox endpoint.
- [ ] Implement outbox endpoint.
- [ ] Implement HTTP signatures.
- [ ] Implement JSON-LD signature strategy or documented compatibility plan.
- [ ] Implement follow remote instance/channel/account.
- [ ] Implement receive remote video activity.
- [ ] Implement announce video from channel.
- [ ] Implement federated comments if in-scope.
- [ ] Implement federated deletes/updates.
- [ ] Implement federation queue/retry/dead-letter.
- [ ] Implement remote media cache strategy.
- [ ] Add federation contract tests using fixtures.

## P10.2 ATProto / Bluesky Extension

- [ ] Add ATProto settings table/config.
- [ ] Document ActivityPub and ATProto can be enabled independently.
- [ ] Implement identity linking placeholder or first slice.
- [ ] Implement posting/syndication strategy spec before code.
- [ ] Implement tests only after protocol behavior is specified.

---

# P11 — Messaging

## P11.1 Normal Secure Messaging

- [ ] Implement conversations.
- [ ] Implement conversation participants.
- [ ] Implement message send/list/read.
- [ ] Implement message attachments.
- [ ] Implement attachment virus scanning.
- [ ] Implement link preview extraction with SSRF protection.
- [ ] Implement read receipts.
- [ ] Implement typing presence or explicitly defer.
- [ ] Implement blocking/reporting integration.
- [ ] Add messaging API tests.

## P11.2 Encrypted Messaging

- [ ] Write E2EE threat model before implementation.
- [ ] Choose audited protocol/library; do not invent crypto.
- [ ] Implement device registration model.
- [ ] Implement public identity/prekey endpoints.
- [ ] Store ciphertext only for encrypted messages.
- [ ] Implement disappearing message expiry metadata.
- [ ] Implement deletion/expiry worker.
- [ ] Ensure backend cannot decrypt encrypted messages.
- [ ] Add tests for storage invariants and expiry behavior.
- [ ] Block completion if no acceptable audited crypto approach is selected.

---

# P12 — Live Streaming

- [ ] Implement live stream create endpoint.
- [ ] Implement normal live vs permanent/recurring live model.
- [ ] Generate private stream key.
- [ ] Store stream key hashed or encrypted.
- [ ] Implement RTMP ingestion integration boundary.
- [ ] Implement HLS output path.
- [ ] Implement live status updates.
- [ ] Implement live replay conversion.
- [ ] Implement live stream delete/archive.
- [ ] Add smoke test for live metadata and HLS path without requiring full RTMP in CI.
- [ ] Add optional integration test profile for RTMP.

---

# P13 — Captions and Whisper

- [ ] Implement caption upload.
- [ ] Implement caption list/download/delete.
- [ ] Implement VTT validation.
- [ ] Implement optional Whisper job adapter.
- [ ] Implement auto-caption request/status.
- [ ] Implement language metadata.
- [ ] Add tests for manual captions.
- [ ] Add Whisper mocked integration tests.

---

# P14 — Simple Crypto Donations

- [ ] Add user/channel donation address fields.
- [ ] Support address type/network metadata.
- [ ] Add signed challenge flow to verify address ownership where feasible.
- [ ] Display verified/unverified status via API.
- [ ] Do not custody funds.
- [ ] Do not implement premium subscriptions, payouts, balances, escrow, or payment processing.
- [ ] Add tests for wallet validation and verification state.

---

# P15 — Security Hardening

- [ ] Add SSRF protection package/policy for URL imports and link previews.
- [ ] Add upload file type allowlist.
- [ ] Add malware scan hooks.
- [ ] Add path traversal protections for local storage.
- [ ] Add CORS tests.
- [ ] Add rate-limit tests.
- [ ] Add JWT key rotation plan or documented defer.
- [ ] Add OAuth redirect validation.
- [ ] Add secure headers.
- [ ] Add audit logging for sensitive actions (typed audit events, no secrets; see P17.2 and `.ralph/specs/observability.md`).
- [ ] Enforce no-secrets-in-logs via the secrets-in-logs guard test (P17.2).
- [ ] Add fuzz tests for URL parsing.
- [ ] Add fuzz tests for metadata parsing.
- [ ] Add fuzz tests for ActivityPub parsing when implemented.

---

# P16 — Testing Strategy

- [ ] Add unit test pattern and examples.
- [ ] Add integration test pattern with PostgreSQL and Redis.
- [ ] Add smoke test for API startup.
- [ ] Add Postman collection and environment for live DB tests.
- [ ] Add fuzz test target list.
- [ ] Add benchmark target list.
- [ ] Add tiny media fixtures.
- [ ] Add testcontainers or Compose-based test runner.
- [ ] Add CI jobs for unit tests.
- [ ] Add CI jobs for integration tests.
- [ ] Add CI jobs for smoke tests.
- [ ] Add scheduled or manual fuzz/benchmark workflows.
- [ ] Document when Ralph should run focused vs full test suites.

---

# P17 — Observability and Operations

> Follow `.ralph/specs/observability.md`. Logging/tracing ship with the code they
> describe, not in a later phase.

## P17.1 Developer-friendly logging

- [x] Add structured logs (slog JSON to stdout).
- [x] Add request IDs (Echo RequestID + per-request slog line).
- [ ] Centralize logger construction in `internal/observability` and inject it.
- [ ] Add `LOG_LEVEL` and `LOG_FORMAT` (json/text) config + `.env.example` + tests.
- [ ] Propagate the request-scoped logger (request_id/trace_id) through service and store layers via `context.Context`.

## P17.2 Security-friendly logging

- [ ] Add a redaction helper + denylist of sensitive field names in `internal/observability`; route struct/config logging through it (never log `cfg` whole).
- [ ] Add the banned-logging guard test (`TestNoForbiddenLogging`): no `fmt.Print*`/`log.Print*`/`println` diagnostics outside `main`/tests.
- [ ] Add the secrets-in-logs guard test: fail when a denylisted key is used as an slog/span/metric key.
- [ ] Implement typed audit events for auth/admin/moderation actions (durable, no secrets); add per-action audit tests asserting no denylisted field. (See P15.)

## P17.3 OpenTelemetry (traces + metrics)

- [ ] Add OTel Go SDK setup + graceful shutdown in `internal/observability`, wired in `cmd/api` (and worker), no-op when disabled.
- [ ] Add config `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_SERVICE_NAME`, `METRICS_ENABLED` + `.env.example` + validation.
- [ ] Instrument HTTP (otelecho), datastore (pgx/Redis spans), and outbound HTTP calls.
- [ ] Accept inbound W3C `traceparent` from `vidra-user`; inject context on outbound calls.
- [ ] Export RED metrics with bounded label cardinality (no IDs/tokens/raw URLs as labels); gate the metrics surface behind `METRICS_ENABLED` and document any route in `api/openapi.yaml`.
- [ ] Stamp `trace_id`/`span_id` into slog output when OTel is enabled.
- [ ] Add optional Docker Compose profile for a local OTel Collector / Jaeger.

## P17.4 Operations

- [ ] Add health/readiness for dependencies. (done for postgres/redis)
- [ ] Add worker status reporting.
- [ ] Add job retry/dead-letter visibility.
- [ ] Add admin-facing system status endpoint.
- [ ] Add backup/restore docs for PostgreSQL, media storage, and Redis assumptions.
- [ ] Add production deployment notes.

---

# P18 — PeerTube Import and Migration

> Import an existing PeerTube instance (its PostgreSQL DB + media storage) into
> Vidra. Follow `.ralph/specs/peertube-import.md`. Read-only on the source;
> idempotent, resumable, dry-runnable, admin-only, audited. Depends on the data
> models from P4–P10 existing; build incrementally as those land.

## P18.1 Preflight and source connection

- [ ] Add read-only source-DB config (DSN) and source-storage config (local/S3) to `internal/config` + `.env.example` (off by default; source creds are secrets, never committed/logged).
- [ ] Detect PeerTube schema/version on preflight; pin supported version range in `.ralph/specs/peertube-reference.md`; refuse unverified versions without `--force`.
- [ ] Verify source DB reachability, storage reachability, and free disk space before any write.

## P18.2 Mapping ledger and dry-run

- [ ] Fill in the entity mapping ledger (PeerTube entity → Vidra model → status → notes) per the spec.
- [ ] Implement a durable import ledger mapping source UUID/id → Vidra id with per-row status (enables idempotency + resume).
- [ ] Implement `--dry-run`: report counts, mapping plan, conflicts, and unsupported/partial entities; write nothing.

## P18.3 Entity import (incremental, idempotent)

- [ ] Import users/accounts/actors, including identity; bcrypt password-hash strategy (keep if compatible, else disable + force reset). Never log hashes.
- [ ] Import channels (+ ActivityPub actor handles/keypairs for federation continuity; see P10).
- [ ] Import videos + `videoFile`/`videoStreamingPlaylist` (HLS) + thumbnails + captions, with media copy/re-probe (streaming, checksummed, resumable).
- [ ] Import comments (threaded), playlists + elements, tags/categories/metadata.
- [ ] Import follows/subscriptions; moderation data (blacklists/blocklists/abuse) where in scope, else mark `deferred`.
- [ ] Apply the configured conflict policy (skip|rename|merge|fail) for username/handle/email/slug collisions.

## P18.4 Surface, safety, tests, docs

- [ ] Add the `cmd/peertube-import` CLI (source DSN, storage, conflict policy, `--dry-run`, `--resume`).
- [ ] Optional admin API endpoint to launch/monitor an import — if added, document it in `api/openapi.yaml` (drift guard) as the contract for the `vidra-user` admin import UI.
- [ ] Emit audit events for import start/finish/summary (no secrets); apply SSRF + path-traversal + file-type/size protections on source storage reads.
- [ ] Add import tests: seed a known-version PeerTube schema + fixtures, assert mapping/idempotency (re-run is a no-op)/dry-run/conflict handling and that no secret is logged.
- [ ] Write an operator migration guide (prereqs, read-only source setup, dry-run, run/resume, what is imported vs deferred, post-import verification).

---

# P19 — Release Gates

- [ ] All P0 tracking files exist and are current.
- [ ] All backend required sections above are either complete or explicitly deferred by user.
- [ ] PeerTube endpoint inventory has no unclassified endpoints.
- [ ] PeerTube feature ledger has no unclassified in-scope backend items.
- [ ] Vidra extensions ledger has no unclassified in-scope backend items.
- [ ] OpenAPI contract (`api/openapi.yaml`) is current: lints clean (`make openapi-lint`) and the route↔spec drift guard passes (`make openapi-verify` / `TestOpenAPIContract`).
- [ ] `README.md`, `.env.example`, and `.ralph/AGENT.md` reflect the current endpoints, env vars, and commands (no documentation drift).
- [ ] Logging is structured and configurable (`LOG_LEVEL`/`LOG_FORMAT`); the banned-logging and secrets-in-logs guard tests pass; no denylisted data in logs/spans/metric labels.
- [ ] Audit events exist and are tested for in-scope sensitive actions.
- [ ] OpenTelemetry traces/metrics follow `.ralph/specs/observability.md` (behind config flags; logs carry `trace_id` when enabled).
- [ ] Migrations apply cleanly to empty database.
- [ ] Migrations apply cleanly to existing database fixture.
- [ ] Docker Compose can start required local services.
- [ ] Unit tests pass.
- [ ] Integration tests pass or documented external dependency is unavailable.
- [ ] Smoke tests pass.
- [ ] Lint/static analysis passes.
- [ ] `make ci` passes locally and CI is green running the same `make ci` gate (local↔CI parity); `ci-guard.yml` passes (no hidden failures, workflows invoke the canonical gate).
- [ ] `.ralph/AGENT.md` is accurate.
- [ ] No secrets are committed.

---

# Optional / Deferred / Non-Blocking

These items do not block Ralph exit if configured as optional in `.ralphrc` and explicitly kept in this section.

- [ ] Premium subscriptions.
- [ ] Creator payouts.
- [ ] Custodial crypto payments.
- [ ] Mobile native apps.
- [ ] Full plugin/theme API parity.
- [ ] Advanced recommendation engine.
- [ ] Full multi-region deployment automation.
- [ ] Enterprise SSO.
- [ ] Advanced analytics warehouse.
- [ ] AI moderation beyond basic hooks.
- [ ] WebTorrent/P2P playback if intentionally replaced by IPFS/S3/HLS architecture.

---

# Completed

- [x] Project initialization.
- [x] Repo split: backend lives in `vidra-core/` (monorepo subdir) with its own Ralph control plane.

---

# Notes for Ralph

- Prefer backend contracts before frontend assumptions.
- Build boring foundations before flashy features.
- Keep parity ledgers brutally honest.
- If a feature cannot be implemented safely, mark it `BLOCKED` with reason and continue to the next safe foundational task.
- If the same failure repeats for multiple loops, stop and report `BLOCKED`.
