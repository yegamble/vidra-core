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
- [x] Add config for storage backend: local, S3-compatible, IPFS. (`STORAGE_BACKEND` (local; s3/ipfs rejected until implemented) + `STORAGE_LOCAL_ROOT`, validated)
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
- [x] Add playlists table. (migration 0019 `playlists` (id, owner FK `ON DELETE CASCADE`, title, description, `visibility` CHECK public/unlisted/private default private, created/updated; `(owner_id, created_at DESC)` index).)
- [x] Add playlist items table. (migration 0019 `playlist_items` (id, playlist FK, video FK both `ON DELETE CASCADE`, `position`, `added_at`, `UNIQUE(playlist_id, video_id)`; `(playlist_id, position)` index).)
- [ ] Add comments table.
- [x] Add likes/dislikes or reactions table according to spec. (migration 0015 `video_ratings` (PK `(user_id, video_id)`, `rating` CHECK like/dislike, `ON DELETE CASCADE` from videos+users, `video_id` index). A user has at most one rating per video, settable/changeable/clearable. Endpoints (on **public, published** videos via the shared `publicVideoID` guard, else 404): `GET /api/v1/videos/:id/rating` (optionalAuth → `{like_count, dislike_count, my_rating}`; `my_rating` null for anon/unrated), `PUT /api/v1/videos/:id/rating` (auth, body `{rating: like|dislike}`, upsert, 422 on bad value), `DELETE /api/v1/videos/:id/rating` (auth, idempotent clear). `internal/rating` service (Set/Clear/Get + Summary) + `internal/httpapi/ratings.go`; openapi documents all three + `VideoRating` schema (drift guard extended). sqlc `UpsertVideoRating`/`DeleteVideoRating`/`GetVideoRating`/`CountVideoRatings` (FILTER counts). Tested: 3 service + 3 handler (set→change→clear, anon hides my_rating, invalid 422, auth 401, non-public 404).)
- [x] Add watch history table. (migration 0017 `watch_history` (PK `(user_id, video_id)`, `position_seconds INTEGER NOT NULL DEFAULT 0 CHECK (>= 0)`, `created_at`, `updated_at`, `ON DELETE CASCADE` from users+videos, `(user_id, updated_at DESC)` index). One row per (user, video): the viewer's last watch + resume position; `updated_at` bumped on every progress report so history lists most-recently-watched first.)
- [x] Add watch later/private library tables. (migration 0016 `saved_videos` (PK `(user_id, video_id)`, `created_at`, `ON DELETE CASCADE` from users+videos, `(user_id, created_at DESC)` index). A "watch later"/library: save a video once, list newest-saved first. Endpoints (all requireAuth): `POST /api/v1/videos/:id/save` (idempotent; only **public, published** videos via `publicVideoID`, else 404), `DELETE /api/v1/videos/:id/save` (idempotent; no public check so a user can always clean up), `GET /api/v1/me/saved` (paginated discovery cards, reuses `videoFeedResponse`, filters to public+published). Mirrors the subscriptions feed: `Save`/`Unsave`/`ListSaved` on the **video** service (sqlc `SaveVideo`/`UnsaveVideo`/`ListSavedVideos`) reusing `newFeedItem`/`feedItemView`. openapi documents all three (list → `VideoFeedResponse`); drift guard covers them (routes are under the existing video block). Tested: video-service round-trip + 3 handler (save→list newest-first→idempotent→unsave, non-public 404, auth 401). DEFERRED: named playlists + ordering (separate `playlists`/`playlist_items` slice).)
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
- [x] Generate typed queries for watch history. (`internal/store/queries/watch_history.sql` — UpsertWatchProgress / GetWatchProgress / ListWatchHistory (discovery-card join + position + watched_at) / DeleteWatchHistoryEntry / ClearWatchHistory)
- [x] Generate typed queries for playlists. (`internal/store/queries/playlists.sql` — CreatePlaylist / GetPlaylistByID (+ public video_count) / ListPlaylistsByOwner / UpdatePlaylist (COALESCE partial) / DeletePlaylist / AddPlaylistItem (append at MAX(position)+1, idempotent ON CONFLICT) / RemovePlaylistItem / ListPlaylistItems (discovery-card join, public+published only, ordered by position).)
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
- [x] Implement email verification token flow placeholder or adapter boundary. (`POST /api/v1/auth/verify-email` (behind `requireAuth`, always 202, no-op if already verified) issues a single-use, 24h-expiring token — only its SHA-256 hash is stored in `email_verification_tokens` (migration 0013); the raw token is delivered by the shared `Mailer` adapter (`SendEmailVerification`; default no-op until a provider is wired). `POST /api/v1/auth/verify-email/confirm` (public) consumes a valid token → flips `users.email_verified` TRUE and marks the token used; unknown/used/expired → 400. `internal/auth/verification.go`; sqlc `email_verifications.sql`; reuses the password-reset token pattern + unified `Mailer` interface. Tested: 6 service + 4 handler (full request→confirm flow asserting `/me.email_verified` flips, already-verified no-op, unknown/expired, auth-required, 400/422). Real-DB execution covered by sqlc validation + CI migrate-apply.)
- [x] Implement login. (`POST /api/v1/auth/login`, `internal/auth.Service.Login`; enumeration-safe 401; disabled → 403; tested)
- [x] Implement refresh token/session rotation. (`POST /api/v1/auth/refresh`; register/login persist a hashed refresh token in `sessions`, refresh rotates (revoke old + issue new); rotated-token reuse → revoke all sessions; opaque 256-bit token, SHA-256 stored; `JWT_REFRESH_TTL` default 720h; tested)
- [x] Implement logout current session. (`POST /api/v1/auth/logout` revokes the presented refresh token; idempotent 204; tested)
- [x] Implement logout all sessions. (`POST /api/v1/auth/logout-all` behind `requireAuth` → `Service.LogoutAll` revokes every active session for the principal; 204; tested)
- [x] Implement password reset request/complete flow. (`POST /api/v1/auth/password-reset` (always 202, enumeration-safe) issues a single-use, 1h-expiring token — only its SHA-256 hash is stored in `password_reset_tokens` (migration 0012); the raw token is delivered by an injectable `PasswordResetMailer` adapter boundary (default no-op until a provider is wired — `WithMailer`). `POST /api/v1/auth/password-reset/confirm` consumes a valid token → sets the new bcrypt password, marks the token used, and revokes ALL the user's sessions; unknown/used/expired → 400. `internal/auth/reset.go` + `email.go`; sqlc `password_resets.sql`. Tested: 6 service tests (delivery, enumeration-safety, change+single-use, session revocation, unknown/expired) + 5 handler tests (full flow incl. new-password-logs-in/old-rejected, 202/422/400). Real-DB query execution covered by sqlc compile-time validation + CI migrate-apply; tagged integration coverage is a follow-up.)
- [x] Implement password hashing with modern algorithm. (bcrypt cost 12, `internal/auth/password.go`; salted, tested)
- [x] Implement JWT claims and validation. (`internal/auth/jwt.go` HS256 via golang-jwt/v5; sub+role+iss+aud+exp, alg pinned; issue/parse tested incl. tamper/expiry/audience)
- [ ] Implement OAuth2 provider abstraction.
- [ ] Implement TOTP enrollment.
- [ ] Implement TOTP verification.
- [ ] Implement recovery codes.
- [ ] Implement account export request/status/download foundation.
- [ ] Implement account import foundation.
- [~] Implement account deletion/deactivation. (Deactivation done: `POST /api/v1/auth/me/deactivate` (behind `requireAuth`, body `{password}`) re-confirms the current password, sets `users.is_active=FALSE` (sqlc `DeactivateUser`), and revokes all sessions — the account can no longer log in (→403) and its tokens stop resolving (→401). Reversible by an admin. `internal/auth/account.go` `Service.DeactivateAccount` + `ErrInvalidPassword`; handler + openapi + `auth.account.deactivate` audit event (success/failure). Tested: 3 service (disable+revoke, wrong-password leaves active, unknown user) + 3 handler (403/204/login-403/me-401 flow, requires-auth 401, validation 422). DEFERRED: hard deletion (removing/anonymising the account and its videos/channels/comments) needs a data-retention/anonymisation policy decision — see safety rails.)
- [x] Add auth rate limits. (A stricter, dedicated fixed-window limiter (`AUTH_RATE_LIMIT_REQUESTS`, default 10/`RATE_LIMIT_WINDOW`, per client IP, keyed `auth:<ip>`) layered over the general 120/min API limiter on the credential-stuffing / token-guessing endpoints: login, register, password-reset, password-reset/confirm, verify-email/confirm. `httpapi.authRateLimit` middleware + `WithAuthRateLimiter`; wired in `cmd/api` sharing the Redis counter; gated by `RATE_LIMIT_ENABLED`. Fails open if Redis is down (degrade protection, not availability) and emits an `auth.rate_limited` audit event on denial (never the credentials). Tested: throttle-after-N + audit + password-not-logged, fail-open on store error, and not-applied-to-non-sensitive-routes (logout), plus the config default. `.env.example` updated.)
- [x] Add auth audit logs. (New `internal/observability` package: typed `AuditEvent` (action/result/actor_id/request_id/reason + slog timestamp as occurred_at), `Audit()` emitter, and the canonical `IsSensitiveKey` denylist from the observability spec. Wired into the auth handlers via `Server.audit` (`internal/httpapi/auth.go`): register, login success/failure (failure carries no actor_id/email — enumeration-safe), logout, logout-all, password-reset request + complete (success/failure), email-verify request + confirm (success/failure). Events are marked `audit=true`, distinct from request logs; never carry secrets/PII. `WithLogger` server option added as a capture seam. Tested: 4 observability unit (required fields, omit-empty, no-denylisted-key, IsSensitiveKey) + 2 httpapi handler (login emits success+failure with correct actor_id presence and reason; logout/reset events; asserts no denylisted key and the password never appears in logs). Partially advances P17.1/P17.2 observability.)
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
- [x] Implement local file upload. (`POST /api/v1/videos/:id/file` (requireAuth, owner-only, multipart `file`) streams the original through `internal/storage` to key `web-videos/<id>.<ext>` (PeerTube-aligned asset layout — one top-level dir per asset kind; see `.ralph/specs/storage-layout.md`; thumbnails → `thumbnails/<id>.jpg`), records a `video_files` row (kind=original, size, content_type, original_name), and flips the video draft→processing; re-upload replaces the prior original; non-owner/unknown → 404. Backend wired from config in `cmd/api`. `video_files` table = migration 0008. Transcode/probe/scan are later slices.)
- [ ] Implement resumable upload strategy or documented initial limitation. (Note: the original upload is a single multipart request bounded by `HTTP_BODY_LIMIT`; chunked/resumable upload is still TODO.)
- [ ] Implement upload progress/status in Redis and database.
- [ ] Implement video metadata validation: title, description, tags, category, language, license, privacy, channel.
- [~] Implement privacy levels. (videos: public/unlisted/private enforced on read — private hidden as 404 to non-owners; account/channel-level privacy still TODO)
- [ ] Implement publish date/scheduled publish.
- [~] Implement file validation. (upload enforces a size cap — `UPLOAD_MAX_SIZE`, default 2G, via a per-route body limit so the upload route is exempt from the small JSON `HTTP_BODY_LIMIT`; oversize → 413 — and an extension allowlist of video containers; unaccepted → 415, checked after ownership so non-owners still see 404. Authoritative content/codec validation is FFprobe's job in the transcode slice; magic-byte sniffing is unreliable for video containers in Go's detector.)
- [ ] Implement ClamAV scan integration.
- [ ] Implement ClamAV fallback modes: fail-closed, fail-open, quarantine.
- [ ] Implement URL import with SSRF protection.
- [ ] Implement torrent/magnet import placeholder or adapter boundary.
- [ ] Implement upload cancellation.
- [ ] Implement failed upload cleanup.
- [~] Add API tests for file upload, URL import, validation errors, and scan failure. (file upload covered: success→processing, refetch, missing-field 400, non-owner/unknown 404, plus service-level store/replace/key tests; URL import and ClamAV scan-failure still TODO.)

## P6.2 Storage

- [x] Implement storage interface. (`internal/storage.Backend` — Put/Open/Delete/Exists over forward-slash object keys; `ErrInvalidKey`/`ErrNotFound`)
- [x] Implement local storage backend. (`internal/storage.Local` rooted dir, creates parent dirs, idempotent delete; path-traversal-safe key resolution — tested incl. an escape-attempt that cannot write outside root)
- [ ] Implement S3-compatible backend.
- [ ] Implement Backblaze B2-compatible configuration.
- [ ] Implement DigitalOcean Spaces-compatible configuration.
- [ ] Implement IPFS backend adapter or deferred spec.
- [~] Implement object key naming strategy. (originals use `videos/<video_id>/original.<safe-ext>`; rendition/thumbnail key scheme still TODO.)
- [ ] Implement private/public object handling.
- [ ] Implement signed URL or proxy strategy.
- [ ] Implement media deletion/garbage collection.
- [ ] Add integration tests using local filesystem and MinIO.

## P6.3 Transcoding

> Publish-pipeline seam landed: after upload, `video.Service.Process` finalises a
> video `processing → published` (or `failed`) via an injected `Prober` interface
> (`internal/video`). With no prober configured (current default) the original is
> trusted and published directly — the extension allow-list already gated the
> upload. The real FFprobe/transcode implementation slots into this seam via
> `video.WithProber(...)` in `cmd/api` once FFmpeg is in the runtime image. The
> public surfaces already filter `state='published'`.

- [x] Implement FFmpeg probe. (`internal/media.FFProbe` shells out to `ffprobe -print_format json`; pure JSON parser unit-tested with fixtures, exec path in a `//go:build integration` test excluded from `make ci`; wired via `media.DetectFFProbe` in `cmd/api` only when ffprobe is on PATH — graceful publish-unprobed fallback otherwise; ffmpeg added to the runtime image. Reads originals via the new `storage.PathProvider` capability, temp-download fallback for non-path backends.)
- [x] Implement media metadata extraction. (probe extracts duration_seconds/width/height into `video_metadata` (migration 0009, 1:1 side table) during `Process`; unknown measures stored NULL; `GET /api/v1/videos/:id` exposes them, omitted when absent.)
- [ ] Implement H.264 profile.
- [ ] Implement VP9 profile.
- [ ] Implement AV1 profile.
- [ ] Implement HLS output.
- [x] Implement thumbnail generation. (`internal/media.Thumbnailer` shells out to ffmpeg to grab one scaled JPEG poster frame; pure seek/arg builders unit-tested, exec behind `-tags=integration`. `Process` generates it best-effort after a successful probe (never blocks publish), stored as a `kind='thumbnail'` video_file (migration 0010 widens the kind CHECK) at `videos/<id>/thumbnail.jpg`. Served by `GET /api/v1/videos/:id/thumbnail` (same visibility as detail) reusing `serveStoredObject`; detail exposes `has_thumbnail`. Wired via `media.DetectThumbnailer` in `cmd/api` only when ffmpeg is present.)
- [ ] Implement preview generation.
- [ ] Implement storyboard generation or documented defer.
- [ ] Implement worker queue for transcode jobs.
- [ ] Implement retry/backoff/dead-letter behavior.
- [~] Implement status updates in Redis and PostgreSQL. (PostgreSQL `videos.state` transitions draft→processing→published/failed via `SetVideoState`; live Redis progress for in-flight transcode jobs still TODO.)
- [ ] Add unit tests for job planning.
- [ ] Add smoke test with tiny fixture video.

---

# P7 — Playback, Discovery, and Public Video API

- [x] Implement public video list endpoint. (`GET /api/v1/videos` (public, paginated limit≤100/offset) → cross-channel public videos newest-first; sqlc `ListPublicVideos`; `internal/video.ListPublic`; tested. Now filters `state='published'` — the publish pipeline landed, so feed/search/channel-public surfaces exclude draft/processing/failed.)
- [ ] Implement local videos endpoint.
- [x] Implement trending/recent/popular sort modes or documented staged rollout. (`GET /api/v1/videos?sort=recent|popular|trending` (unknown → recent, echoed back in the response). `ListPublicVideosSorted` LEFT JOINs `video_view_counts` and orders by a CASE on the sort param: popular = all-time views, trending = views decayed by age (HN-style `views / (age_hours+2)^1.5`). Feed items now also carry `views` + `has_thumbnail` for cards. `internal/video.FeedItem`; tested incl. popular ordering + sort fallback.)
- [ ] Implement video detail endpoint.
- [~] Implement video playback manifest endpoint. (`GET /api/v1/videos/:id/original` streams the stored original with HTTP Range/206 support via `http.ServeContent` + the `storage.PathProvider` capability; visibility mirrors detail (private→owner-only/404, no-original→404). Progressive playback of the original works now; HLS/DASH manifest + renditions need the transcode pipeline.)
- [ ] Implement captions endpoint.
- [ ] Implement download metadata endpoint.
- [ ] Implement share/embed metadata endpoint.
- [ ] Implement oEmbed or documented difference.
- [ ] Implement OpenGraph metadata.
- [x] Implement search endpoint with PostgreSQL trigram search. (`GET /api/v1/videos/search?q=` (public, paginated) over public video titles; ILIKE filter ranked by `similarity()`; migration `0007` adds a `gin_trgm_ops` index on `videos.title`; sqlc `SearchPublicVideos`; `internal/video.SearchPublic`; tested. Results carry `views`/`has_thumbnail` cards. Channel/account search still TODO.)
- [x] Discovery-card consistency: search results and channel video lists (`GET /api/v1/channels/:handle/videos`, owner + public views) now LEFT JOIN view counts + thumbnail availability like the feed, so every video grid returns `views` + `has_thumbnail`. (`internal/video.FeedItem` reused; enriched `SearchPublicVideos`/`ListVideosByChannel`/`ListPublicVideosByChannel`; tested.) **Cards now also carry `channel_handle` + `channel_display_name`** (all 5 card queries `JOIN channels`; `FeedItem` + the `videoView` card projection expose them; openapi `Video` schema documents them, omitted on the detail view). This lets the frontend link a video card to `/channels/{handle}` and show the channel name — the unblock for the frontend's BLOCKED subscribe flow (a logged-in user can now reach a channel page from a card). Tested: `TestFeedCardsCarryChannelInfo` asserts the feed card returns the channel handle + name.
- [ ] Implement tags/categories/languages/licenses config endpoints.
- [x] Implement view count recording with abuse/rate-limit protection. (`POST /api/v1/videos/:id/view` (optionalAuth) records a view in a `video_view_counts` side table (migration 0011), deduped per viewer per hour via Redis SETNX (`cache.Deduper`, injected `video.ViewDeduper` seam; hashed user-id/IP key — no raw PII). Visibility mirrors detail; only published videos count; always 204. `views` exposed on detail. Surfacing on feed + trending sort still TODO.)
- [x] Implement watch progress endpoint. (`PUT /api/v1/videos/:id/watch-progress` (requireAuth, body `{position_seconds}` clamped ≥0, upsert) records the caller's resume position on a **public, published** video via the shared `publicVideoID` guard (else 404), bumping it to the top of history; `GET /api/v1/videos/:id/watch-progress` (requireAuth) reads it back (`{video_id, position_seconds}`, 0 when none). See the watch-history slice in P8 for the full feature + tests.)
- [ ] Add tests for public visibility/privacy rules.

---

# P8 — Library, Playlists, Comments, and Notifications

- [x] Implement watch history. (Watch history + resume progress as one slice on the `video` service (migration 0017 `watch_history`, sqlc `watch_history.sql`). Endpoints (all requireAuth): `PUT /api/v1/videos/:id/watch-progress` (upsert resume position, `publicVideoID` guard → 404 on non-public/unknown, 422 on negative), `GET /api/v1/videos/:id/watch-progress` (read it back, 0 when none), `GET /api/v1/me/history` (paginated discovery cards extended with `position_seconds` + `watched_at`, most-recently-watched first, filtered to public+published — reuses `feedItemView` via an embedded `videoView`), `DELETE /api/v1/me/history/:id` (remove one entry, idempotent, no public check so a user can always clean up), `DELETE /api/v1/me/history` (clear all, idempotent). `internal/video` `HistoryItem` + `RecordProgress`/`Progress`/`ListHistory`/`RemoveHistoryEntry`/`ClearHistory` reusing `newFeedItem`; `internal/httpapi/history.go`. openapi documents all 5 + `WatchProgress`/`WatchProgressRequest`/`HistoryItem`(allOf Video)/`WatchHistoryResponse` schemas (drift guard extended). Tested: 1 service round-trip (record→list newest-first→re-watch-rebumps→clamp-negative→absent-for-unwatched→remove→clear) + 5 handler (full round-trip incl. card channel link, delete+clear idempotency, non-public 404, negative 422, auth 401 on all 5 routes). DEFERRED: a history-tracking on/off user setting.)
- [x] Implement resume progress. (Covered by the watch-history slice above: `position_seconds` is upserted via `PUT /videos/:id/watch-progress`, read via `GET /videos/:id/watch-progress`, and carried on every `GET /me/history` card so the frontend can show resume bars + resume playback.)
- [x] Implement watch later playlist. (Covered by the saved-videos slice (migration 0016 `saved_videos`, `POST`/`DELETE /api/v1/videos/:id/save` + `GET /api/v1/me/saved`). Named playlists below are the general collection feature; watch-later is the dedicated single library.)
- [x] Implement playlist create/read/update/delete. (Named playlists (migration 0019 `playlists`+`playlist_items`, sqlc `playlists.sql`, `internal/playlist` service). Endpoints: `POST /api/v1/playlists` (requireAuth, title+optional visibility, default private), `GET /api/v1/me/playlists` (requireAuth, own list with public `video_count`), `GET /api/v1/playlists/:id` (optionalAuth, playlist + ordered public+published video cards; private → 404 to non-owner via visibility gate), `PATCH /api/v1/playlists/:id` (owner-only COALESCE partial; re-reads for current count), `DELETE /api/v1/playlists/:id` (owner-only). `internal/httpapi/playlists.go`; non-owner mutate → 404 (existence not leaked, mirrors videos). openapi documents all + `Playlist`/`PlaylistListResponse`/`PlaylistDetailResponse`(allOf Playlist)/`CreatePlaylistRequest`/`UpdatePlaylistRequest`/`AddPlaylistItemRequest` (drift guard extended via `fullRouteOptions`).)
- [x] Implement playlist visibility rules. (public/unlisted/private CHECK on the table; `GET /playlists/:id` gates private → owner-only (else 404); list/detail `video_count` + item list only count/return public+published videos so a leaked private video never surfaces in a public playlist. Tested: anon-sees-public, anon-404-on-private, owner-sees-own-private.)
- [~] Implement playlist item add/remove/reorder. (add + remove DONE: `POST /api/v1/playlists/:id/videos` (owner-only, body `{video_id}`; appends at `MAX(position)+1`, idempotent `ON CONFLICT DO NOTHING`; only **public, published** videos addable else 404), `DELETE /api/v1/playlists/:id/videos/:videoId` (owner-only, idempotent). DEFERRED: reorder (drag position update) — needs a positions-rewrite endpoint.)
- [x] Implement quick-add to playlist API. (The `POST /api/v1/playlists/:id/videos` add-item endpoint is the quick-add API: a single authed call appends a video to a playlist, idempotent. A convenience "create playlist + add in one call" is DEFERRED.)
- [~] Implement comments create/read/update/delete. (Flat comments (no threading yet): `POST /api/v1/videos/:id/comments` (auth) posts a comment on a **public, published** video; `GET /api/v1/videos/:id/comments` (public, paginated `limit`≤100/`offset`) lists them newest-first with the author's username + display name (sqlc `ListCommentsByVideo` JOINs users); `DELETE /api/v1/comments/:id` (auth) removes the caller's OWN comment (403 for another's, 404 unknown). Non-public/unpublished/unknown video → 404 (`commentableVideoID` guard). migration 0014 (`comments` table, `ON DELETE CASCADE` from videos+users); `internal/comment` service + `internal/httpapi/comments.go`; openapi documents all three + `Comment`/`CommentListResponse` schemas (drift guard extended via `fullRouteOptions`). Tested: 2 service + 3 handler (create→list→delete-by-author, non-author 403, non-public 404, blank-body 422). DEFERRED: edit (PATCH), threading (parent_id), moderation hooks (P9).)
- [ ] Implement comment threading if in-scope.
- [ ] Implement comment moderation hooks.
- [ ] Implement video like/dislike or reaction behavior according to spec.
- [x] Implement subscriptions/follows. (The follow model landed in P5 (`POST`/`DELETE /api/v1/channels/:handle/follow`). This adds the **subscriptions feed**: `GET /api/v1/me/subscriptions/videos` (behind `requireAuth`, paginated `limit`≤100/`offset`) returns public, published videos from the channels the user follows, newest first, with the same discovery-card data (`views`, `has_thumbnail`) as the main feed. sqlc `ListSubscriptionVideos` (filters `channel_id IN (SELECT … FROM channel_follows WHERE follower_id = $1)`); `internal/video.ListSubscriptions`; openapi documented; tested — service-level (only-followed-channels) + handler-level using the real follow flow (anon→401, empty-before-follow, video-appears-after-follow).)
- [x] Implement notification creation/read/mark-read. (migration 0018 `notifications` (PK id, recipient `user_id`, `type`, nullable `actor_id`/`channel_id`/`video_id`/`comment_id` all `ON DELETE CASCADE`, `read_at` (NULL=unread), `created_at`; `(user_id, created_at DESC)` index + partial `(user_id) WHERE read_at IS NULL` for cheap unread). `internal/notification` service: `NotifyFollow`/`NotifyComment` (best-effort side effects, skip self-notify), `List` (unread-filterable, joins actor/channel/video for display), `UnreadCount`, `MarkRead` (idempotent, ErrNotFound on unknown/not-yours), `MarkAllRead`. Endpoints (all requireAuth): `GET /api/v1/me/notifications?unread=&limit=&offset=` → `{notifications, unread_count, limit, offset}`, `GET /api/v1/me/notifications/unread-count`, `POST /api/v1/me/notifications/read-all`, `POST /api/v1/me/notifications/:id/read`. Creation hooks: `handleFollowChannel` notifies the channel owner only on a **genuinely new** follow (`FollowChannel` changed to `:execrows` so `channel.Service.Follow` returns `(channel, created, err)`); `handleCreateComment` notifies the video owner. `internal/httpapi/notifications.go`; openapi documents all 4 + `Notification`/`NotificationActor`/`NotificationListResponse`/`UnreadCountResponse` (drift guard extended via `fullRouteOptions`). Tested: 2 service unit (notify+list ordering+self-skip; mark-read idempotent/404/mark-all) + 5 handler (follow→owner notified+mark-read clears unread, self-follow none, comment→owner notified+self-comment none+read-all, auth 401 on all 4, unknown 404). DEFERRED: new-video-from-subscription fan-out, notification preferences, email/push delivery.)
- [~] Add tests for playlist permissions, history privacy, and comment moderation. (Playlist permissions + visibility DONE: 2 service unit (create/get/items ordering+idempotent; owner-only update/add/remove/delete → ErrForbidden/ErrNotFound) + 5 handler (CRUD+items round trip with card + count, private 404 to anon / 200 to owner / public 200, non-owner mutate 404, add-non-public 404, blank-title 422, auth 401 on all 7). History privacy: watch-history is already per-user (requireAuth). Comment moderation: pending P9.)

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
- [x] Add upload file type allowlist. (original-file upload accepts only known video-container extensions — mp4/m4v/mov/webm/mkv/avi/ogv/ogg/mpg/mpeg/ts/flv/wmv/3gp; others → 415. See `internal/video.acceptedVideoExts`.)
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
- [x] OpenAPI contract (`api/openapi.yaml`) is current: lints clean (`make openapi-lint` — Redocly @1, 0 errors) and the route↔spec drift guard passes (`make openapi-verify` / `TestOpenAPIContract`). Fixed two 3.1 contract bugs that had reddened the `openapi` workflow for several commits: `nullable: true` (a 3.0-ism 3.1 removed) → JSON-Schema type-arrays on `Video.{duration_seconds,width,height}`, and undeclared per-op auth → a document-level `security: []` (public by default; protected ops override with `bearerAuth`). Remaining 8 advisory warnings (info-license [repo license TBD], no-server-example.com [localhost dev URL], operation-4xx-response ×6) are non-blocking; documenting 4xx bodies is a later completeness pass.
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
