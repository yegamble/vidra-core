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
- [x] Add `internal/messaging`. (normal 1:1 DM service; E2EE is P11.2)
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
- [x] Add captions/subtitles table. (migration 0024 `captions` (id, `video_id` FK `ON DELETE CASCADE`, `language`, `label`, `storage_key`, timestamps, `UNIQUE(video_id, language)`); one WebVTT track per language per video, the .vtt bytes in the storage backend at `captions/<video_id>/<language>.vtt`.)
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
- [x] Add abuse reports table. (migration 0020 `reports` (video/comment targets); migration 0027 widens `target_type` to add `account` + adds nullable `reported_user_id` FK → users `ON DELETE CASCADE` + partial unique `(reporter_id, reported_user_id)`. See P9 abuse-reports.)
- [x] Add video blocks/quarantine table. (migration 0021 `video_blocks` (PK `video_id` → videos `ON DELETE CASCADE`, `reason TEXT NOT NULL DEFAULT ''`, `blocked_by` → users `ON DELETE SET NULL`, `created_at`). One row per blocked video; see the P9 block/unblock flow. Auto-quarantine (block-on-upload + approve/reject) is a later slice.)
- [x] Add watched words lists and matches tables. (Words list: migration 0023 `watched_words` (id, `word`, `created_by` FK → users `ON DELETE SET NULL`, `created_at`; unique index on `lower(word)`). Matches: migration 0030 `watched_word_matches` (`watched_word_id`+`comment_id` FKs `ON DELETE CASCADE`, `UNIQUE(watched_word_id, comment_id)`, `created_at` index) — records which comment matched which term when posted. See the watched-words tagging item in P9.)
- [~] Add muted accounts/instances table. (Accounts done: migration 0022 `muted_accounts` (PK `(muter_id, muted_id)`, both FK → users `ON DELETE CASCADE`, `created_at`, CHECK `muter_id <> muted_id`; `(muter_id, created_at DESC)` index). Muted **instances** (federation) is a later slice.)
- [ ] Add admin audit log table.
- [ ] Add federation actors table.
- [ ] Add federation activities/inbox/outbox table.
- [ ] Add ATProto identities/events tables.
- [x] Add direct messages conversations table. (migration 0031: `conversations` + `conversation_participants`)
- [x] Add direct messages table. (migration 0031: `messages`, index on `(conversation_id, created_at DESC)`)
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
- [x] Generate typed queries for messaging. (`internal/store/queries/messaging.sql` — CreateConversation (ON CONFLICT DO NOTHING by dm_key) / GetConversationByDMKey / AddConversationParticipant / IsConversationParticipant / CreateMessage / TouchConversation / ListMessages (sender join, newest-first) / ListConversations (LATERAL last-message + other participant, COALESCE for empty threads).)
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
- [x] Add a dev-only email-token retrieval seam (unblocks the frontend reset-complete + email-verify pages, which need the out-of-band token to backed-verify). `DEV_MAIL_CAPTURE_ENABLED=true` (default false) wires an in-memory `CaptureMailer` (`internal/auth/capture.go`, injected via `WithMailer`) that records the most recent raw reset/verify token per (kind, email) — never logged/persisted to disk/DB — and registers `GET /api/v1/dev/email-token?email=&kind=reset|verification` (`internal/httpapi/dev.go`, via `WithDevMailCapture`). The route exists ONLY when the flag is on (production never carries it) and the api WARNs loudly on boot; intentionally excluded from `api/openapi.yaml` (test seam, not a public contract — `fullRouteOptions` omits it so `TestOpenAPIContract` stays green). Tested: 4 auth (`capture_test.go`: kind-namespacing, latest-wins, missing) + 4 httpapi (`dev_test.go`: returns token for reset/default/verification, 422 bad-kind/missing-email, 404 no-token, and route ABSENT without the option = prod-safe). `.env.example` + `docker-compose.yml` pass the flag through. VERIFIED end-to-end against the live stack (`DEV_MAIL_CAPTURE_ENABLED=true`): full reset flow (request 202 → dev-endpoint token → confirm 204 → old-password login 401, new-password login 200) AND verify flow (request 202 → dev-endpoint token → confirm 204 → `/me.email_verified=true`).
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
- [~] Implement video metadata validation: title, description, tags, category, language, license, privacy, channel. (title/description/privacy/channel long done. **category/language/license DONE**: migration 0025 adds three nullable TEXT columns to `videos`; `POST /channels/:handle/videos` + `PATCH /videos/:id` accept optional `category`/`language`/`license`, validated against the canonical `video.IsCategory`/`IsLanguage`/`IsLicense` maps (unknown/empty → 422 field error); stored on create (empty → NULL) and COALESCE-partial on update; exposed on the `Video` detail/create/update views (omitted when unset). sqlc CreateVideo/GetVideoByID/UpdateVideo/SetVideoState carry the columns; openapi `Video`/`CreateVideoRequest`/`UpdateVideoRequest` document them (drift guard green). Tested: handler `TestVideoTaxonomyMetadata` (store+round-trip on detail, unset→omitted, unknown-on-create 422, partial update preserves siblings, unknown/empty-on-update 422) + `config_test` validators. **VERIFIED** end-to-end against real PG (migration applies; create/detail/update/422 flow + psql-confirmed row). Clearing a set value back to unset is not yet supported (COALESCE can't distinguish keep from clear). Remaining: free-form **tags** (a separate many-to-many/array feature, not a config map).)
- [~] Implement privacy levels. (videos: public/unlisted/private enforced on read — private hidden as 404 to non-owners; account/channel-level privacy still TODO)
- [ ] Implement publish date/scheduled publish.
- [~] Implement file validation. (upload enforces a size cap — `UPLOAD_MAX_SIZE`, default 2G, via a per-route body limit so the upload route is exempt from the small JSON `HTTP_BODY_LIMIT`; oversize → 413 — and an extension allowlist of video containers; unaccepted → 415, checked after ownership so non-owners still see 404. Authoritative content/codec validation is FFprobe's job in the transcode slice; magic-byte sniffing is unreliable for video containers in Go's detector.)
- [ ] Implement ClamAV scan integration.
- [ ] Implement ClamAV fallback modes: fail-closed, fail-open, quarantine.
- [x] Implement URL import with SSRF protection. (`POST /api/v1/videos/:id/import` (requireAuth, owner-only), body `{url}` — the URL-import counterpart of `POST /videos/:id/file`. Flow: `urlsafety.ValidateURL` (http/https + non-public literal-IP reject → 422); **ownership checked BEFORE any fetch** so a non-owner (or unknown video) is 404 and the server never fetches on their behalf; fetch via `urlsafety.NewClient` (SSRF-guarded at dial time — loopback/private/link-local/CGNAT/DNS-rebinding/internal-redirects refused → 422); non-200 → 422; `Content-Length > UPLOAD_MAX_SIZE` → 413 and a streaming `maxBytesReader` hard-caps a lying/absent length; then reuse `video.AttachOriginal` (container type from the URL path extension, or — when the path has none, e.g. `/download?id=…` or the backend's own `/original` — from the response **Content-Type** via `extForContentType`; unknown → 415) + `Process` (publish/fail) → 201 `UploadVideoFileResponse`, identical to upload. The fetch client is injectable in tests (`Server.importClient`) so the happy path is exercised against a loopback httptest origin while production always uses the guard. The guard is config-driven — `urlsafety.Guard{AllowPrivate: cfg.ImportAllowPrivateURLs}` — so the dev/test `HTTP_IMPORT_ALLOW_PRIVATE_URLS` knob (P15, compose default off) lets a real backend import from a loopback/compose-network origin, which is what makes the **frontend URL-import backed e2e provable in CI**. openapi documents the route + `ImportVideoRequest` (drift guard green). Tested: `videos_import_test.go` — happy path (import→published→publicly served), 415 non-video ext, **SSRF guard blocks loopback via the default client (422)**, **allow-private config imports a literal-127.0.0.1 origin (201)**, **Content-Type fallback imports an extension-less URL (201)**, missing/ftp url 422, anon 401, non-owner 404. **Backed-import now fully enabled**: with `HTTP_IMPORT_ALLOW_PRIVATE_URLS=true` a real backend can import its OWN extension-less `/original` via the compose service name (`http://api:8080/api/v1/videos/:src/original`) — verified live (201), so a `vidra-user` backed e2e can prove the studio "Import from URL" flow without any external origin (compose also adds `extra_hosts host.docker.internal:host-gateway` as an alternative host origin). Torrent/magnet import is separate below.)
- [ ] Implement torrent/magnet import placeholder or adapter boundary.
- [ ] Implement upload cancellation.
- [ ] Implement failed upload cleanup.
- [~] Add API tests for file upload, URL import, validation errors, and scan failure. (file upload covered: success→processing, refetch, missing-field 400, non-owner/unknown 404, plus service-level store/replace/key tests. **URL import now covered** (`videos_import_test.go`: happy path, 415, SSRF-guard-blocks-loopback 422, missing/ftp-url 422, anon 401, non-owner 404). ClamAV scan-failure still TODO.)

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
- [x] Implement thumbnail generation. (`internal/media.Thumbnailer` shells out to ffmpeg to grab one scaled JPEG poster frame; pure seek/arg builders unit-tested, exec behind `-tags=integration`. `Process` generates it best-effort after a successful probe (never blocks publish), stored as a `kind='thumbnail'` video_file (migration 0010 widens the kind CHECK) at `videos/<id>/thumbnail.jpg`. Served by `GET /api/v1/videos/:id/thumbnail` (same visibility as detail) reusing `serveStoredObject`; detail exposes `has_thumbnail`. Wired via `media.DetectThumbnailer` in `cmd/api` only when ffmpeg is present. **Custom thumbnail upload now DONE too**: `POST /api/v1/videos/:id/thumbnail` (requireAuth, owner-only, multipart `file`) stores a creator-supplied poster (JPEG/PNG/WebP by extension → 415 otherwise; served Content-Type derived from the extension, not the client-declared type), replacing any generated/previous thumbnail at the same deterministic key (one poster per video); no state change; non-owner/unknown → 404; bounded by the global 8M body limit. `video.SetThumbnail` + `acceptedImageExts`; `internal/httpapi/videos.go` `handleSetVideoThumbnail` → 201 `VideoFile`. openapi documents the POST (drift guard green). Tested: `videos_thumbnail_test.go` — upload→served with derived CT + detail `has_thumbnail`, 415 non-image, 401 anon, 404 non-owner. **This unblocks the `vidra-user` studio thumbnail-editing feature** (was DEFERRED on this contract).)
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
- [x] Implement tags/categories/languages/licenses config endpoints. (`GET /api/v1/videos/config` (public, static, no auth) returns the video-metadata taxonomy the frontend reads to populate its create/edit dropdowns: `{categories, licenses, languages, privacies}`, each an ordered `[{id,label}]` list. Categories + licenses use PeerTube's numeric ids (as strings) for import compatibility; languages are a curated ISO 639-1 set (INTENTIONAL_DIFFERENCE from PeerTube's full list); privacies use Vidra's `privacy` values. Canonical data + `IsCategory`/`IsLicense`/`IsLanguage` validators in `internal/video/config.go` (the source of truth for the later metadata-validation slice, P6.312); handler `internal/httpapi/video_config.go`; route is a static `/videos/config` sitting before `/videos/:id` (Echo matches static first, like `/videos/search`). openapi documents the path + `VideoConfigResponse`/`VideoConfigOption` (drift guard green). Tested: 2 video-package (well-formed/unique ids + validators) + 1 handler (200, all four lists populated, known values). NB: free-form per-video **tags** are a video field, not a config map — deferred with the metadata-storage slice.)
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
- [~] Implement comments create/read/update/delete. (Flat comments (no threading yet): `POST /api/v1/videos/:id/comments` (auth) posts a comment on a **public, published** video; `GET /api/v1/videos/:id/comments` (public, paginated `limit`≤100/`offset`) lists them newest-first with the author's username + display name (sqlc `ListCommentsByVideo` JOINs users); `DELETE /api/v1/comments/:id` (auth) removes the caller's OWN comment (403 for another's, 404 unknown). Non-public/unpublished/unknown video → 404 (`commentableVideoID` guard). migration 0014 (`comments` table, `ON DELETE CASCADE` from videos+users); `internal/comment` service + `internal/httpapi/comments.go`; openapi documents all three + `Comment`/`CommentListResponse` schemas (drift guard extended via `fullRouteOptions`). Tested: 2 service + 3 handler (create→list→delete-by-author, non-author 403, non-public 404, blank-body 422). DEFERRED: edit (PATCH). Threading (parent_id) has now landed — see the next item; moderation hooks are P9.)
- [x] Implement comment threading if in-scope. (Replies via a nullable `comments.parent_id` self-FK (migration 0026, reserved by 0014; `ON DELETE CASCADE` so a thread is removed with its root — soft-delete-that-keeps-the-thread is a later refinement). `POST /api/v1/videos/:id/comments` accepts an optional `parent_id`: the service (`comment.Create` now takes `parentID *uuid.UUID`) validates it references an existing comment on the **same** video, else `ErrParentNotFound` → 422 field error (a reply can't be smuggled onto another video's thread, and arbitrary comment-id existence isn't leaked); a malformed `parent_id` is a 422 too. Every comment view now carries `parent_id` (null for top-level), and `GET /videos/:id/comments` returns replies inline with the link so the client builds the tree. sqlc `CreateComment`/`GetComment`/`ListCommentsByVideo` carry `parent_id`; openapi documents the request field + `Comment.parent_id` (drift guard green). Tested: 1 service unit (`TestCreateReply`: reply ok records parent_id; unknown parent → ErrParentNotFound; cross-video parent → ErrParentNotFound) + 1 handler (`TestCommentReplyThreading`: top-level null parent, reply carries parent_id, both listed, malformed 422, cross-video 422). INTENTIONAL_DIFFERENCE from PeerTube: a flat list + `parent_id` (client builds the tree) rather than dedicated `comment-threads` / thread-detail endpoints — those can be a later refinement.)
- [~] Implement comment moderation hooks. (Moderator delete DONE: `DELETE /api/v1/comments/:id` lets a moderator/admin delete any comment (not just the author's) — see the admin comments overview in P9. Watched-words auto-flagging of comments is a later slice.)
- [x] Implement video like/dislike or reaction behavior according to spec. (Already implemented — reconciling a stale checkbox. `internal/rating` (`Like`/`Dislike`) backs `PUT /api/v1/videos/:id/rating` (requireAuth, body `{rating:"like"|"dislike"}`, upsert that replaces the caller's prior rating), `DELETE /api/v1/videos/:id/rating` (clears it), and `GET /api/v1/videos/:id/rating` (public: `{like_count, dislike_count, my_rating}` where `my_rating` is null for an anonymous/unrated viewer) — all guarded to public+published videos. `internal/httpapi/ratings.go` + `ratings_test.go`; openapi documents both ops + `VideoRating` (drift guard green). Verified present + passing under `make ci` (internal/rating + httpapi tests green).)
- [x] Implement subscriptions/follows. (The follow model landed in P5 (`POST`/`DELETE /api/v1/channels/:handle/follow`). This adds the **subscriptions feed**: `GET /api/v1/me/subscriptions/videos` (behind `requireAuth`, paginated `limit`≤100/`offset`) returns public, published videos from the channels the user follows, newest first, with the same discovery-card data (`views`, `has_thumbnail`) as the main feed. sqlc `ListSubscriptionVideos` (filters `channel_id IN (SELECT … FROM channel_follows WHERE follower_id = $1)`); `internal/video.ListSubscriptions`; openapi documented; tested — service-level (only-followed-channels) + handler-level using the real follow flow (anon→401, empty-before-follow, video-appears-after-follow).)
- [x] Implement notification creation/read/mark-read. (migration 0018 `notifications` (PK id, recipient `user_id`, `type`, nullable `actor_id`/`channel_id`/`video_id`/`comment_id` all `ON DELETE CASCADE`, `read_at` (NULL=unread), `created_at`; `(user_id, created_at DESC)` index + partial `(user_id) WHERE read_at IS NULL` for cheap unread). `internal/notification` service: `NotifyFollow`/`NotifyComment` (best-effort side effects, skip self-notify), `List` (unread-filterable, joins actor/channel/video for display), `UnreadCount`, `MarkRead` (idempotent, ErrNotFound on unknown/not-yours), `MarkAllRead`. Endpoints (all requireAuth): `GET /api/v1/me/notifications?unread=&limit=&offset=` → `{notifications, unread_count, limit, offset}`, `GET /api/v1/me/notifications/unread-count`, `POST /api/v1/me/notifications/read-all`, `POST /api/v1/me/notifications/:id/read`. Creation hooks: `handleFollowChannel` notifies the channel owner only on a **genuinely new** follow (`FollowChannel` changed to `:execrows` so `channel.Service.Follow` returns `(channel, created, err)`); `handleCreateComment` notifies the video owner. `internal/httpapi/notifications.go`; openapi documents all 4 + `Notification`/`NotificationActor`/`NotificationListResponse`/`UnreadCountResponse` (drift guard extended via `fullRouteOptions`). Tested: 2 service unit (notify+list ordering+self-skip; mark-read idempotent/404/mark-all) + 5 handler (follow→owner notified+mark-read clears unread, self-follow none, comment→owner notified+self-comment none+read-all, auth 401 on all 4, unknown 404). **Message notifications now added** (migration 0032 adds a nullable `notifications.conversation_id` FK → conversations `ON DELETE CASCADE`): sending a direct message best-effort notifies the recipient — `notification.NotifyMessage(recipient, sender, conversationID)` (type `message`, actor = sender, links the conversation; NO message body stored → no plaintext leak), emitted from `handleSendMessage` via `messaging.OtherParticipant` (new sqlc `GetOtherParticipant`). `Notification.type` enum + `conversation_id` field documented in openapi. Tested: 1 notification unit (`TestNotifyMessage`: message notif carries conversation id, self-message skipped) + messaging `OtherParticipant` unit + handler assertion in `TestMessagingFlow` (recipient sees a `message` notification linking the conversation from the sender; sender is NOT self-notified). DEFERRED: new-video-from-subscription fan-out, notification preferences, email/push delivery.)
- [~] Add tests for playlist permissions, history privacy, and comment moderation. (Playlist permissions + visibility DONE: 2 service unit (create/get/items ordering+idempotent; owner-only update/add/remove/delete → ErrForbidden/ErrNotFound) + 5 handler (CRUD+items round trip with card + count, private 404 to anon / 200 to owner / public 200, non-owner mutate 404, add-non-public 404, blank-title 422, auth 401 on all 7). History privacy: watch-history is already per-user (requireAuth). Comment moderation: pending P9.)

---

# P9 — Moderation, Admin, and Safety

- [~] Implement roles: user, moderator, admin, owner. (user/moderator/admin done end-to-end: the `users.role` CHECK enum, JWT-carried role, `requireRole(...)` middleware gating admin/moderator routes (reports queue, admin users), and admin role assignment via `PATCH /admin/users/:id` below. An explicit `owner` super-role is DEFERRED — admin is the top role today.)
- [x] Implement admin users list/search/filter. (`GET /api/v1/admin/users?q=&limit=&offset=` (requireRole admin) lists accounts newest-first, filterable by a username/email substring (`q`), paginated. `internal/admin` `Service.ListUsers` (sqlc `ListUsers`); `internal/httpapi/admin_users.go` `adminUserView` deliberately omits the password hash. openapi documents it + `AdminUser`/`AdminUserListResponse` (drift guard extended via `fullRouteOptions`).)
- [~] Implement user edit: role, quota, enabled/disabled, bypass quarantine, email verified. (role + enabled/disabled DONE: `PATCH /api/v1/admin/users/:id` (requireRole admin, body `{role?, is_active?}`, partial) via `admin.Service.UpdateUser` (sqlc `AdminUpdateUser` COALESCE). Safety: an admin cannot demote or deactivate **their own** account (`ErrSelfChange` → 422), preventing last-admin lockout; deactivating a user revokes their sessions (best-effort) so the ban is immediate. Emits an `admin.user.update` audit event (new `observability.ActionAdminUserUpdate`). Tested: 3 service unit (search; promote+deactivate-revokes-sessions; self-guard + not-found) + 2 handler (list + non-admin 403 + promote + deactivate + self-demote/deactivate 422 + bad-role/empty 422 + unknown 404; auth 401). DEFERRED: quota, bypass-quarantine, admin-set email_verified (need those fields/flows).)
- [x] Implement registration approval queue. (`REGISTRATION_REQUIRE_APPROVAL` config (default false); migration 0028 `registration_requests` (username/email/password_hash/note/status/moderator_note/reviewed_by/reviewed_at; partial unique indexes allow one PENDING per email/username, case-insensitive). When approval is on, `POST /api/v1/auth/register` files a pending request and returns **202 `{status:"pending"}`** with no token/account (audit `auth.registration.request`); when off, the direct-signup flow is unchanged. Admin-only endpoints (`requireRole("admin")`): `GET /api/v1/admin/registration-requests?status=pending` (paginated queue, reviewer resolved, password hash never exposed), `POST /api/v1/admin/registration-requests/:id/approve` (**atomically** creates the account from the stored hash + marks approved via a single data-modifying-CTE query — all-or-nothing; unknown/resolved → 404, email/username since-taken → 409; audit `auth.registration.approve`), `POST /api/v1/admin/registration-requests/:id/reject` (optional `{note}`; unknown/resolved → 404; audit `auth.registration.reject`). `internal/auth/registration.go` (`RequestRegistration`/`ListRegistrationRequests`/`ApproveRegistration`/`RejectRegistration` + `ErrRegistrationRequestNotFound`); sqlc `registration_requests.sql`; `internal/httpapi/admin_registration.go`; `GET /instance` now also returns `registration_requires_approval`. openapi documents register-202 + the 3 admin routes + `RegistrationPending`/`RegistrationRequest`/`RegistrationRequestListResponse`/`RejectRegistrationRequest` schemas + the instance field (drift guard green). Tested: 6 auth service (request→pending→dedup, existing-user conflict, approve→creates+login+re-approve-404, unknown-approve, reject→no-login→re-reject-404) + 2 handler (full approve flow: register-202-no-token→admin-queue→approve→login-works→non-admin 403→anon 401; reject flow: register-202→reject→no-login→re-reject-404). **This unblocks the `vidra-user` P10 registration-requests admin UI** (list + accept/reject). NB: the first admin must be created before enabling approval (bootstrap), otherwise no one can approve.)
- [x] Implement abuse reports for videos/comments/accounts. (Video + comment + **account** reports DONE: migration 0020 `reports` (reporter FK, `target_type` CHECK video/comment, nullable `video_id`/`comment_id` cascade FKs, `reason`, `status` CHECK open/accepted/rejected, `moderator_note`, `resolved_by`/`resolved_at`; `(status, created_at DESC)` queue index + partial unique `(reporter_id, video_id)` / `(reporter_id, comment_id)`); migration 0027 adds the **account** target (widened `target_type` CHECK + nullable `reported_user_id` FK → users `ON DELETE CASCADE` + partial unique `(reporter_id, reported_user_id)`). Endpoints: `POST /api/v1/videos/:id/report` (requireAuth, `publicVideoID` guard, idempotent), `POST /api/v1/comments/:id/report` (requireAuth, FK→404 on unknown comment), `POST /api/v1/users/:id/report` (requireAuth, self-report → 422, unknown account FK → 404, idempotent). `internal/moderation` `ReportVideo`/`ReportComment`/`ReportAccount` (FK 23503 → `ErrInvalidTarget`; self → `ErrCannotReportSelf`); `internal/httpapi/reports.go`. The admin queue (`ListReports` + `reportView`) now resolves + returns the reported account's id/username for account reports. openapi documents `POST /users/{id}/report` + the `account` target_type + `reported_user_id`/`reported_username` on `Report` (drift guard green). Tested: 1 service (`TestReportAccount`: report→list→dedup, self-report, FK) + 1 handler (`TestReportAccountAndModerate`: report→queue shows reported username, self 422, unknown 404, anon 401). **This unblocks the `vidra-user` P9 DEFERRED account/channel report flow** (backend `target_type` now includes account). DEFERRED: a dedicated channel-report target (channel reports can map to the owning account for now).)
- [~] Implement report accept/reject/delete/internal note. (Accept/reject + internal note DONE via the admin queue: `GET /api/v1/admin/reports?status=open` (requireRole admin/moderator, paginated, resolves reporter username + video title / comment body) + `POST /api/v1/admin/reports/:id/resolve` (requireRole, body `{status: accepted|rejected, note}`, sets `resolved_by`/`resolved_at`, emits a `moderation.report.resolve` audit event; unknown id → 404). `internal/moderation` `List`/`Resolve`. openapi documents all 4 ops + `Report`/`ReportReporter`/`ReportListResponse`/`CreateReportRequest`/`ResolveReportRequest` (drift guard extended). Tested: 3 service unit (report+list+dedup, comment-FK→invalid-target, resolve+not-found) + 3 handler (report→queue→resolve→leaves-open-queue, non-admin 403, report-comment + unknown-comment 404, validation 422 + auth 401 on all 4). DEFERRED: report delete (a hard-delete of a report row) + notifications to reporter.)
- [ ] Implement notifications to reporter where applicable.
- [x] Implement video block manual flow. (`POST /api/v1/admin/videos/:id/block` (requireRole admin/moderator, optional `{reason}` ≤2000) blocks a video so it disappears from every public surface: the feed/search/channel-public/subscriptions queries add `NOT EXISTS (SELECT 1 FROM video_blocks …)`, and the detail/stream/thumbnail + public-interaction guards (`videoHiddenByBlock`, shared via `publicVideoID`) return 404 to everyone **except** moderators/admins (who can still view to confirm before unblocking). Idempotent (re-block updates reason + acting moderator); unknown video → 404; emits a `moderation.video.block` audit event (reason never logged). `internal/moderation` `BlockVideo`/`IsBlocked` (sqlc `BlockVideo` upsert / `IsVideoBlocked`; FK 23503 → `ErrVideoNotFound`); `internal/httpapi/videos.go` (`handleBlockVideo`). openapi documents both block ops (drift guard green). Tested: 2 service unit (block→idempotent→unblock; FK→not-found) + 2 handler (block→public/regular 404 & mod 200 → unblock→public 200, block+unblock idempotency; unknown 404, over-length reason 422, non-mod 403, anon 401).)
- [x] Implement video unblock flow. (`DELETE /api/v1/admin/videos/:id/block` (requireRole admin/moderator) lifts the block — idempotent (unblocking a not-blocked video still 204) — and emits a `moderation.video.unblock` audit event. `moderation.UnblockVideo` (sqlc `UnblockVideo` delete-returning-rows). Covered by the block-flow handler tests above.)
- [x] Implement video block-list (read side of the block/unblock trio). (`GET /api/v1/admin/videos/blocked` (requireRole admin/moderator, paginated `limit`≤100/`offset`) lists currently-blocked videos newest-block-first with the owning channel (`handle` + `display_name`), the block `reason`, who blocked it (`blocked_by` username, omitted if that account was deleted), and `blocked_at` — the read endpoint a frontend moderation block-list UI consumes. sqlc `ListBlockedVideos` (JOIN videos+channels, LEFT JOIN users); `internal/moderation` `BlockedItem`/`ListBlocked`; `internal/httpapi/videos.go` `handleListBlockedVideos`. openapi documents it + `BlockedVideo`/`BlockedVideoListResponse` (drift guard green). Tested: 1 service unit (block 2 → newest-first + reason → unblock drops one) + 1 handler (empty → block 2 → list newest-first with channel/reason/blocked_by → non-mod 403 → anon 401 → unblock drops one). This unblocks the `vidra-user` P9 "video block list" + "manual block/unblock controls" UI.)
- [ ] Implement auto-block/quarantine setting.
- [ ] Implement video quarantine approve/reject.
- [x] Implement muted accounts. (Model + management DONE: a signed-in user mutes/unmutes another account and lists their mutes. migration 0022 `muted_accounts`; sqlc `muted_accounts.sql` (`MuteAccount` idempotent upsert / `UnmuteAccount` / `ListMutedAccounts` JOIN users for identity); `internal/mute` service (`Mute`/`Unmute`/`List`; self-mute → `ErrCannotMuteSelf`, unknown target FK 23503 → `ErrUserNotFound`). Endpoints (all requireAuth): `POST /api/v1/me/mutes/accounts/:id` (mute; self → 422, unknown → 404, idempotent 204), `DELETE /api/v1/me/mutes/accounts/:id` (unmute; idempotent 204), `GET /api/v1/me/mutes/accounts` (paginated list, newest mute first, with the muted account's username/display_name). `internal/httpapi/mutes.go`; wired via `WithMuteService` in `cmd/api`. To make a mute target reachable from the UI, the comment view now also exposes `author_id` (so the frontend can "mute this commenter"); openapi documents all three endpoints + `MutedAccount`/`MutedAccountListResponse` + the `Comment.author_id` field (drift guard green). Tested: 3 service unit (mute→list→unmute idempotent; self-mute; unknown-target) + 2 handler (full mute/list/unmute round trip + idempotency; self 422, unknown 404, anon 401 on all three). **Comment filtering effect now DONE**: `GET /api/v1/videos/:id/comments` is now `optionalAuth` and hides comments authored by accounts the viewer has muted — `ListCommentsByVideo` gained a nullable `viewer_id` filter (`NOT EXISTS muted_accounts WHERE muter_id = viewer AND muted_id = c.user_id`; NULL viewer = anon = no filtering, so the public behaviour is unchanged); `comment.Service.ListByVideo` takes `(viewerID, viewerAuthed)` and the handler passes the optional principal. openapi marks the operation optional-auth and notes the per-viewer mute filter. Tested: `TestCommentsHideMutedAuthors` (ada mutes bob → ada no longer sees bob's comment but an anon viewer still does → unmute restores it). **Feed/video filtering effect now DONE too**: the muter no longer sees a muted account's videos in the main feed (`GET /videos`), search (`GET /videos/search`), or subscriptions (`GET /me/subscriptions/videos`). `ListPublicVideosSorted`/`SearchPublicVideos` gained a nullable `viewer_id` filter (`NOT EXISTS muted_accounts WHERE muter_id = viewer AND muted_id = c.owner_id`; NULL/anon = no filtering); `ListSubscriptionVideos` reuses its `follower_id` as the muter; feed + search routes are now `optionalAuth` and `video.ListPublic`/`SearchPublic` take `(viewerID, viewerAuthed)`. Direct channel visits (`GET /channels/:handle/videos`) are intentionally NOT filtered — muting hides from discovery, not from a deliberate visit (PeerTube parity). openapi marks feed + search optional-auth + notes the per-viewer filter. Tested: `TestFeedHidesMutedAccounts` (charlie mutes bob → bob's video gone from charlie's feed + search but an anon viewer still sees it → unmute restores it). The account-mute feature is now complete backend-side; frontend mute UI is VERIFIED (see `vidra-user`). Muted **instances** (federation) is separate.)
- [x] Implement user blocks (harassment cut-off, distinct from mute). (A signed-in user blocks/unblocks another account and lists who they've blocked; a block **symmetrically** cuts off direct messaging (either direction → 403). migration 0033 `user_blocks` (PK (blocker_id, blocked_id), no-self CHECK, reverse index); sqlc `user_blocks.sql` (`BlockUser` idempotent upsert / `UnblockUser` / `ListBlockedUsers` JOIN users / `IsBlockedBetween` symmetric EXISTS); `internal/block` service (`Block`/`Unblock`/`List`/`IsBlockedBetween`; self → `ErrCannotBlockSelf`, unknown target FK 23503 → `ErrUserNotFound`). Endpoints (all requireAuth): `POST /api/v1/me/blocks/:id` (self → 422, unknown → 404, idempotent 204), `DELETE /api/v1/me/blocks/:id` (idempotent 204), `GET /api/v1/me/blocks` (paginated, newest first, with identity). `internal/httpapi/blocks.go`; wired via `WithBlockService` in `cmd/api`. Messaging enforcement via `messaging.Blocker` + `WithBlocker` (see P11.1): `StartConversation`/`SendMessage` → `ErrBlocked` → 403; participant check precedes the block check so a non-participant stays 404 (no existence leak). openapi documents all three endpoints + the messaging 403s + `BlockedUser`/`BlockedUserListResponse` (drift guard green). Tested: 4 block service unit (block→list→unblock idempotent; self; unknown FK; `IsBlockedBetween` symmetric) + 2 messaging service unit (start blocked; send blocked but non-participant still ErrNotParticipant) + 2 handler (`TestBlockUserFlow` CRUD + self 422 + unknown 404 + anon 401; `TestMessagingBlockedByUserBlock` start→block→both-directions-403→unblock→send-works). **Unblocks a `vidra-user` block/unblock UI + block-list page.** DEFERRED: extend block to also hide the blocked user's comments/videos (today it only gates DM).)
- [ ] Implement muted instances.
- [x] Implement watched words lists. (Instance-wide watched-terms list management for moderators/admins. Endpoints (all requireRole admin/moderator): `POST /api/v1/admin/watched-words` (body `{word}` ≤100, trims; adds a term, `201` with the created word; case-insensitive duplicate → `409`; blank/too-long → `422`), `GET /api/v1/admin/watched-words` (paginated list newest-first, each with the adder's username), `DELETE /api/v1/admin/watched-words/:id` (idempotent `204`). migration 0023 `watched_words` (unique `lower(word)`); sqlc `watched_words.sql` (`CreateWatchedWord` RETURNING; unique violation 23505 → `ErrAlreadyExists`; `ListWatchedWords` LEFT JOIN users; `DeleteWatchedWord` execrows); new `internal/watchword` service (`Add`/`List`/`Delete`); `internal/httpapi/admin_watched_words.go`; wired via `WithWatchWordService` in `cmd/api`. openapi documents all three + `WatchedWord`/`WatchedWordListResponse`/`CreateWatchedWordRequest` (drift guard green). Tested: 2 service unit (add→list→delete idempotent; case-insensitive duplicate → ErrAlreadyExists) + 2 handler (add→409 dup→422 blank→list-with-adder→delete idempotent→empty; non-mod 403 + anon 401 on all three). Unblocks the frontend watched-words admin UI.)
- [~] Implement watched words tagging for videos/comments. (**Comment detection + recording DONE**: migration 0030 `watched_word_matches` (`watched_word_id`+`comment_id` FKs `ON DELETE CASCADE`, `UNIQUE(watched_word_id, comment_id)`, `created_at` index). When a comment is posted, `handleCreateComment` best-effort calls `watchword.FlagComment(commentID, body)` which matches the body against the list (`MatchWatchedWords`: case-insensitive substring via `strpos(lower(text), lower(word))`) and records a row per matched term (`RecordWatchedWordMatch`, idempotent) — never blocks the post. Moderators review via `GET /api/v1/admin/watched-word-matches` (requireRole admin/moderator, paginated, newest match first, resolving the matched term + comment body/author/video). sqlc `MatchWatchedWords`/`RecordWatchedWordMatch`/`ListWatchedWordMatches`; `watchword.FlagComment`/`ListMatches`; `internal/httpapi/admin_watched_words.go`. openapi documents the route + `WatchedWordMatch`/`WatchedWordMatchListResponse` (drift guard green). Tested: 1 service (`TestFlagCommentAndListMatches`: 2-term match, clean=0, idempotent) + 1 handler (`TestWatchedWordMatchesFlow`: add term → comment with term flagged → appears in queue → clean comment adds none → non-mod 403 → anon 401). **DEFERRED**: auto-holding/hiding flagged content (needs a product decision on hold vs hide vs the moderator acting manually) and watched-word tagging for **video** titles/descriptions — this slice covers comment detection + review.)
- [x] Implement admin comments overview. (`GET /api/v1/admin/comments?q=&limit=&offset=` (requireRole admin/moderator) lists ALL comments newest-first, each with the author's identity and the video it's on (id + title). Optional `q` case-insensitive body filter. Paired with **moderator delete**: `DELETE /api/v1/comments/:id` now lets the comment's author OR a moderator/admin delete it (`comment.Service.Delete` gained an `isModerator` flag; the handler passes `role in {admin,moderator}`) — so the overview is actionable. sqlc `ListAdminComments` (JOIN users + videos, nullable `query` narg); `internal/comment` `AdminComment`/`ListForAdmin`; `internal/httpapi/admin_comments.go` `handleListAdminComments`. openapi documents the list op + `AdminComment`/`AdminCommentListResponse` and the broadened delete (drift guard green). Tested: 2 service unit (`ListForAdmin` list+filter; moderator-deletes-any + unknown-404) + 2 handler (`TestAdminCommentsOverview` list w/ author+video_title, `q` filter, non-mod 403, anon 401; `TestModeratorDeletesAnyComment` non-author-non-mod 403 → admin deletes → gone from overview). Unblocks a frontend comments-moderation surface.)
- [x] Implement admin videos overview. (`GET /api/v1/admin/videos?q=&limit=&offset=` (requireRole admin/moderator) lists ALL videos — any privacy/state, newest first — each with the owning channel (handle + display_name), view count, `created_at`, and a `blocked` flag (whether it's in `video_blocks`). Optional `q` case-insensitive title filter. Complements the blocked-only list (`GET /admin/videos/blocked`): this is the general moderation surface an admin browses to find + block/unblock any video. sqlc `ListAdminVideos` (JOIN channels, LEFT JOIN view counts, `EXISTS video_blocks`, nullable `query` narg); `internal/video` `AdminVideo`/`ListAdmin`; `internal/httpapi/admin_videos.go` `handleListAdminVideos`. openapi documents it + `AdminVideo`/`AdminVideoListResponse` (drift guard green). Tested: `TestAdminVideosOverview` (admin sees a public+blocked video AND a private draft with correct privacy/state/blocked flags; `q` filters by title; non-mod 403; anon 401). Unblocks the frontend admin videos-management surface (browse all + block/unblock any).)
- [x] Implement admin audit log. (Durable, append-only audit trail: migration 0029 `audit_log` (action, result, nullable `actor_id` stored **without an FK** so the trail survives account deletion, safe `reason`, `request_id`, `occurred_at`; indexes on `occurred_at DESC` and `(action, occurred_at)`). `internal/audit` service (`Record`/`List`); the httpapi `s.audit` choke point now persists every security-audit event best-effort (in addition to the slog line) when the audit-log service is wired — so auth/moderation/admin/registration events all land. `GET /api/v1/admin/audit-log?action=&limit=&offset=` (requireRole admin) lists newest-first, filterable by action, resolving `actor_username` best-effort via LEFT JOIN (null when the account was deleted); rows never carry secrets/PII. sqlc `audit_log.sql`; `internal/httpapi/admin_audit.go`; wired via `WithAuditLog` in `cmd/api`. openapi documents the route + `AuditLogEntry`/`AuditLogListResponse` (drift guard extended via `fullRouteOptions`). Tested: 2 audit-service (record→list newest-first + action filter; actor parsing null for empty/non-uuid) + 1 handler (register+login persist → admin lists them with actor_id → action filter → non-admin 403 → anon 401). **This unblocks the `vidra-user` P10 audit-log page.**)
- [ ] Implement rate-limit management endpoints or config-only decision.
- [ ] Add moderation integration tests.
- [ ] Add Postman admin collection tests.

---

# P10 — Federation

> **DESIGN LANDED** — `.ralph/specs/federation.md` grounds this whole phase: it decides the
> actor model (AP key columns on `users`/`channels`, remote actors in a separate table), the
> id/URL scheme (root, not `/api/v1`), content negotiation, the OpenAPI drift-guard handling
> (federation routes excluded like the dev endpoint), and — importantly — the **private-key
> at-rest** approach (envelope encryption via a `FEDERATION_KEY_KEK`, resolving
> security.md:31). It orders the 21 items below into 7 buildable slices. Build in that order;
> the first (keypair-free) slice is config `PUBLIC_BASE_URL`/`FEDERATION_ENABLED` + NodeInfo.
>
> **Slice 1 SHIPPED** (keypair-free discovery): config `FEDERATION_ENABLED` (default false,
> master gate) + `PUBLIC_BASE_URL` (validated http(s) origin, no path, https in prod; trailing
> slash trimmed) in `internal/config`, `.env.example`, `docker-compose.yml`. `internal/federation`
> service (NodeInfo usage counts) over new `CountPublicVideos`/`CountComments` sqlc queries
> (`CountUsers` reused). Root routes `GET /.well-known/nodeinfo` + `GET /nodeinfo/2.1`
> (`internal/httpapi/federation.go`) mounted ONLY when `FEDERATION_ENABLED && fedsvc != nil` —
> so they 404 by default and stay OUT of the REST OpenAPI drift guard (exclusion-by-omission,
> like the dev endpoint; `TestOpenAPIContract` green). NodeInfo 2.1 advertises software=vidra,
> protocols=[activitypub], openRegistrations=RegistrationEnabled, usage={users, localPosts=public
> published videos, localComments}. Tested: `internal/federation` (usage mapping + error
> propagation) + `internal/httpapi` (discovery link, 2.1 doc shape + profile content type,
> absent-when-disabled 404).
>
> **Slice 2a SHIPPED** (actor-key crypto + schema foundation): migration 0035 adds dedicated
> 1:1 side tables `account_actor_keys` + `channel_actor_keys` (public_key_pem, private_key_pem,
> created_at; `ON DELETE CASCADE`) — **NOT** columns on users/channels, because adding columns
> there made existing queries' RETURNING lists diverge from the shared sqlc model and churned
> every service Repository (spec §2 decision revised accordingly). `internal/secretbox`:
> AES-256-GCM envelope encryption (`Seal`→`enc:<base64 nonce||ct>`, `Open`, `IsSealed`,
> `NewCipherFromBase64`) for private keys at rest; fully unit-tested (round-trip, fresh-nonce,
> wrong-KEK/tamper reject, non-prefixed reject, bad length/base64). Config `FEDERATION_KEY_KEK`
> (base64 32-byte; required in production when federation on, dev stores raw) with validation
> tests; `.env.example` + compose passthrough. Banned-log-key guard extended (`private_key_pem`,
> `kek`).
>
> **Slice 2b SHIPPED** (actor identity HTTP surface): sqlc queries (`GetUserActorByUsername`,
> `GetAccountActorKey`/`GetChannelActorKey`, `InsertAccountActorKeyIfAbsent`/`Insert…Channel…`;
> reuses `GetChannelByHandle`). `internal/federation` gained lazy RSA-2048 keypair minting
> (`ensureAccountKey`/`ensureChannelKey`: get → mint → insert-if-absent → re-read, race-safe;
> private key sealed via `internal/secretbox` when a KEK is set, raw in dev), actor-document
> builders (`AccountActor` Person, `ChannelActor` Group; `@context` incl. security vocab, derived
> inbox/outbox/followers/following + `publicKey`), and `WebFinger` (acct:name@domain → self link;
> own-domain-only). httpapi: content-negotiated `GET /accounts/:handle` + `/video-channels/:handle`
> (406 without an AP Accept, 404 unknown) + `GET /.well-known/webfinger` (400 bad/missing resource,
> 404 foreign domain) — all gated on `FEDERATION_ENABLED`, excluded from the REST drift guard.
> cmd/api builds the secretbox cipher from `FEDERATION_KEY_KEK` (warns when unset with federation on).
> Tested: federation unit (mint-once, seal-with-cipher, Person/Group build, WebFinger resolve/reject),
> httpapi handler (Person/Group serve, 406/404, WebFinger), and an **integration test proving minting
> persists + is idempotent against real Postgres** (run in backend-integration).
>
> **Slice 3a SHIPPED** (HTTP Signatures primitive): `internal/httpsig` — standalone cavage-draft
> "Signing HTTP Messages" (RSA-SHA256) used across the fediverse. `Signer.Sign` sets Date + Digest
> (`SHA-256=<base64>` over the body) + a `Signature` header covering `(request-target) host date digest`;
> `Verifier.Verify` parses the header, enforces the minimum covered set (digest required when a body is
> present), checks Date within skew (default 5m, injectable clock), verifies the Digest against the body,
> and RSA-verifies against a caller-supplied `ResolveKey(keyID)` callback — so the package needs no
> network or DB. Fully unit-tested: sign→verify round-trip, tampered body/signature, wrong key, stale
> Date, missing digest-coverage, unsupported algorithm, missing/garbled Signature header, base64 parse.
> NEXT: Slice 3b — `remote_actors` table + SSRF-guarded (`internal/urlsafety`) remote-actor fetch/cache
> that supplies `ResolveKey` (parse the fetched actor's `publicKey.publicKeyPem` → rsa.PublicKey).

## P10.1 ActivityPub

- [x] Implement local actor model for accounts. (Person actor: `account_actor_keys` side table + lazy RSA-2048 keypair minting (`internal/federation` `ensureAccountKey`, sealed at rest via `internal/secretbox`), served as `GET /accounts/:handle` — Slice 2a/2b.)
- [x] Implement local actor model for channels. (Group actor: `channel_actor_keys` + `ensureChannelKey`, served as `GET /video-channels/:handle` — Slice 2a/2b.)
- [x] Implement WebFinger. (`GET /.well-known/webfinger?resource=acct:name@domain` → self link to the actor URL; own-domain-only, resolves account then channel — Slice 2b.)
- [x] Implement ActivityPub actor endpoints. (Content-negotiated actor documents — `application/activity+json`, 406 otherwise — with `@context` (incl. security vocab), derived inbox/outbox/followers/following collection URLs, and the `publicKey` block. The collection endpoints themselves land in Slices 4-5 — Slice 2b.)
- [ ] Implement inbox endpoint.
- [ ] Implement outbox endpoint.
- [~] Implement HTTP signatures. (Primitive DONE in Slice 3a — `internal/httpsig` RSA-SHA256 cavage sign/verify over `(request-target) host date digest` with Digest + skew, unit-tested. Not yet WIRED: signing outbound delivery lands with the outbox/delivery (Slice 5) and verifying inbound lands with the inbox (Slice 4, needs the Slice 3b remote-actor key resolver).)
- [x] Implement JSON-LD signature strategy or documented compatibility plan. (Documented compatibility plan — `.ralph/specs/federation.md` §7: Vidra authenticates server-to-server with **HTTP Signatures** (RSA-SHA256) and does NOT emit JSON-LD/LD-Signatures, matching Mastodon's default and PeerTube interop. Object integrity rides the signed Digest, not LD proofs.)
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

- [x] Implement conversations. (1:1 DM: `conversations.dm_key` = sorted `"<uuid>:<uuid>"`, UNIQUE → idempotent start-or-get; migration 0031; `internal/messaging`, `POST /api/v1/conversations`)
- [x] Implement conversation participants. (`conversation_participants`; `GET /api/v1/me/conversations` inbox with other participant + last-message preview, most-recently-active first)
- [x] Implement message send/list/read. (`messages` table; `POST/GET /api/v1/conversations/{id}/messages`, newest-first, participant-only → non-participant/unknown conversation is 404; 5000-char body cap; `TouchConversation` bumps recency. **Sending now notifies the recipient** — a `message`-type notification linking the conversation, via `OtherParticipant` + `notification.NotifyMessage`; see P8 notification item + migration 0032.)
- [ ] Implement message attachments. (deferred — normal-DM slice covers text only)
- [ ] Implement attachment virus scanning. (deferred with attachments)
- [ ] Implement link preview extraction with SSRF protection. (deferred)
- [ ] Implement read receipts. (deferred)
- [ ] Implement typing presence or explicitly defer. (deferred — polling model, no presence yet)
- [~] Implement blocking/reporting integration. (**Blocking DONE**: a user-to-user block model (migration 0033 `user_blocks`, `internal/block` service, `POST/DELETE/GET /api/v1/me/blocks[/:id]`) now gates direct messaging **symmetrically** — if EITHER user has blocked the other, `StartConversation` and `SendMessage` return `ErrBlocked` → HTTP 403 ("cannot message this user"). Wired via a small `messaging.Blocker` interface + `messaging.WithBlocker(blocksvc)` in `cmd/api` (participant check precedes the block check, so a non-participant is still 404, not a 403 existence-leak). See P9 "user blocks". **Reporting** integration is already available (a user can report the account they're messaging via `POST /users/:id/report`). DEFERRED: blocking a user could also hide their comments/videos like a mute (today block only gates DM); per-message report.)
- [x] Add messaging API tests. (`internal/messaging/service_test.go` — start/self/recipient-404/send/list/authz; `internal/httpapi/messaging_test.go` — full HTTP flow incl. idempotent start, sender identity, non-participant 404, self 422, unknown 404, anon 401)

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

- [x] Implement live stream create endpoint. (`POST /api/v1/channels/:handle/live` (requireAuth, owner-only; non-owner 403, unknown channel 404), body `{title, description?, privacy?, permanent?}` → 201 `{live_stream, stream_key, rtmp_url}`. migration 0034 `live_streams`; `internal/live` service; `internal/httpapi/live.go`. Also `GET /channels/:handle/live` (owner list, no keys), `GET /live/:id` (optionalAuth, privacy-gated public metadata, no key), `DELETE /live/:id` (owner). openapi documents all + schemas (drift guard green).)
- [~] Implement normal live vs permanent/recurring live model. (A `permanent` boolean on the stream (persisted, in the create request + views) distinguishes a reusable/recurring live — whose key persists across sessions — from a one-shot live. The session/recurrence scheduling behaviour that `permanent` will drive lands with the RTMP ingest boundary.)
- [x] Generate private stream key. (`live.generateStreamKey`: 256-bit crypto/rand token, base64url; returned to the streamer (OBS) exactly once on create and on `POST /live/:id/key` regeneration.)
- [x] Store stream key hashed or encrypted. (Only the SHA-256 hash is persisted (`live_streams.stream_key_hash`, unique — the ingest boundary will look a stream up by key hash); the raw key is never stored or re-returned. Mirrors the refresh/reset-token hashing approach.)
- [~] Implement RTMP ingestion integration boundary. (**Auth + state-flip DONE**: media-server-facing hooks `POST /api/v1/live/ingest/start` and `/stop` authenticate by the presented stream key (SHA-256 → `GetLiveStreamByKeyHash`) AND a shared secret `LIVE_INGEST_SECRET` (constant-time `X-Ingest-Secret` header; hooks 404 when the secret is unset, so they're only exposed when configured). Start flips `state`→`live`; stop → `offline` (permanent) or `ended` (one-shot). `live.StartIngest`/`StopIngest`; `internal/httpapi/live.go`; openapi documents both + `LiveIngestRequest` (drift guard green). Tested: service (start→live, stop→ended/offline-by-permanent, unknown key→ErrNotFound) + handler (disabled-without-secret 404, bad-secret 401, unknown-key 404, happy path flips state via GET). **Deploy wiring**: `docker-compose.yml` now passes `LIVE_INGEST_SECRET` + `LIVE_RTMP_URL` through to the api container (both empty by default → hooks stay disabled; a harness/deploy sets a strong `LIVE_INGEST_SECRET` to enable them). **VERIFIED end-to-end against the live compose stack + real Postgres** (`LIVE_INGEST_SECRET=…`): no-header/wrong-secret → 401, right-secret + unknown key → 404, and the full happy path register→create channel→create live stream (real 43-char key minted once)→ingest start (real key) → 200 `{state:live}` → owner list shows `live` → ingest stop → 204 → list shows `ended`. **Remaining**: the actual media-server (nginx-rtmp/SRS) config that CALLS these hooks + HLS packaging — that's external ops config, not app code.)
- [ ] Implement HLS output path.
- [x] Implement live status updates. (The `state` column (offline/live/ended) is surfaced on every view AND is now driven by the ingest boundary above — a publisher connecting flips it live, disconnecting ends/resets it.)
- [ ] Implement live replay conversion.
- [~] Implement live stream delete/archive. (Delete DONE — `DELETE /live/:id` (owner-only, 404 for non-owner/unknown, idempotent). Replay/archive-to-VOD is a later slice.)
- [~] Add smoke test for live metadata and HLS path without requiring full RTMP in CI. (Metadata lifecycle fully tested without RTMP: `internal/live/service_test.go` (create returns key once + stores only the hash, get/list/delete, regenerate rotates the hash) + `internal/httpapi/live_test.go` (lifecycle: create→list(no key leak)→public get→regenerate→delete→404; authz: anon 401, non-owner 403, private→404-to-others/200-to-owner, non-owner regenerate/delete 404; validation: empty title / bad privacy 422, unknown channel 404). HLS-path smoke awaits the ingest boundary.)
- [ ] Add optional integration test profile for RTMP.

---

# P13 — Captions and Whisper

- [x] Implement caption upload. (`POST /api/v1/videos/:id/captions` (requireAuth, owner-only, multipart `file` (.vtt) + `language` + optional `label`) validates the language tag + WebVTT signature, stores the .vtt via `internal/storage` at `captions/<id>/<lang>.vtt`, and upserts a `captions` row (re-uploading a language replaces it). Non-owner/unknown video → 404; bad language or non-WebVTT → 422; missing file → 400. `internal/video.AddCaption`; `internal/httpapi/captions.go`.)
- [x] Implement caption list/download/delete. (`GET /api/v1/videos/:id/captions` (public, published+public video via `publicVideoID`) → track metadata (language, label); `GET /api/v1/videos/:id/captions/:lang` (public) → serves the WebVTT with `Content-Type: text/vtt` (unknown lang → 404); `DELETE /api/v1/videos/:id/captions/:lang` (requireAuth, owner-only, idempotent → 204, also deletes the blob best-effort). `video.ListCaptions`/`OpenCaption`/`DeleteCaption`.)
- [x] Implement VTT validation. (`video.isWebVTT` requires the `WEBVTT` signature (optionally after a UTF-8 BOM) followed by EOF/newline/space/tab, per the WebVTT spec; upload is bounded to 4 MiB; the language tag must match a BCP-47-ish pattern (`^[A-Za-z]{2,3}(-[A-Za-z0-9]{1,8})*$`). Bad input → `ErrInvalidCaption` → 422.)
- [ ] Implement optional Whisper job adapter.
- [ ] Implement auto-caption request/status.
- [~] Implement language metadata. (Each caption carries its own `language` tag (validated) + `label`. The global taxonomy config endpoint now exists — `GET /api/v1/videos/config` (categories/licenses/languages/privacies; see P7) — so the frontend can offer a curated language dropdown. Per-video category/language/license STORAGE + validation against these maps is the remaining piece, P6.312.)
- [x] Add tests for manual captions. (`internal/video` unit: `TestIsWebVTT` (signature/BOM/glued-suffix/lowercase edge cases) + `TestLangPattern` (valid/invalid tags). `internal/httpapi` handler: `TestCaptionsFlow` (upload→list→download-with-`text/vtt`→re-upload-replaces→second-language→delete→gone→download-404) + `TestCaptionsValidationAndAuth` (non-WebVTT 422, bad-lang 422, missing-file 400, non-owner 404, anon upload/delete 401, list on a private draft 404). Real storage exercised via the handler test's `storage.Local`.)
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

- [x] Add SSRF protection package/policy for URL imports and link previews. (`internal/urlsafety`: `ValidateURL` (http/https only, host required, no userinfo, literal-IP range check), `IsBlockedIP` (fail-closed: loopback/unspecified/link-local/multicast via !GlobalUnicast, RFC1918+ULA via IsPrivate, CGNAT 100.64/10, IPv4-mapped), and `NewClient(timeout)` — an `http.Client` whose `net.Dialer.Control` re-checks every **resolved** candidate IP at DIAL time, so it defeats DNS-rebinding and blocks redirects that land internally; ignores proxy env; caps redirects; re-validates each redirect target's scheme. Per `.ralph/specs/security.md` SSRF rules. This is the reusable guard the URL-import (P6.1) + link-preview (P11) + federation-fetch (P10) slices must route outbound fetches through. Tested: accept/reject URL tables, `IsBlockedIP` table (incl. cloud-metadata 169.254.169.254, IPv6 ULA/loopback, CGNAT, IPv4-mapped), a live `httptest` loopback fetch that the client refuses, and `control` private/public. Callers still bound response body size themselves. **Refactored to a `Guard` type** (zero value = the secure default; `Guard{AllowPrivate:true}` relaxes ONLY the private/loopback IP checks — scheme/host/userinfo validation and fail-closed-on-unparseable still apply). `AllowPrivate` is a DEV/TEST escape hatch wired to config `HTTP_IMPORT_ALLOW_PRIVATE_URLS` (default false; loud boot warning; NEVER in production) so backed e2e can import from a loopback/compose-network origin (a public origin isn't reachable in CI). Tested: `TestGuardAllowPrivate` (allows loopback validate+dial, still rejects ftp + unparseable + secure-default still blocks) + `TestImportVideoAllowPrivateConfig` (config→guard→real client imports a literal-127.0.0.1 origin → published).)
- [x] Add upload file type allowlist. (original-file upload accepts only known video-container extensions — mp4/m4v/mov/webm/mkv/avi/ogv/ogg/mpg/mpeg/ts/flv/wmv/3gp; others → 415. See `internal/video.acceptedVideoExts`.)
- [ ] Add malware scan hooks.
- [ ] Add path traversal protections for local storage.
- [ ] Add CORS tests.
- [ ] Add rate-limit tests.
- [ ] Add JWT key rotation plan or documented defer.
- [ ] Add OAuth redirect validation.
- [x] Add secure headers. (`internal/httpapi/secure_headers.go` — a middleware mounted right after `Recover` (so even recovered 5xx carry them) setting on every response: `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY`, `Referrer-Policy: strict-origin-when-cross-origin`, `Cross-Origin-Opener-Policy: same-origin`, `X-Permitted-Cross-Domain-Policies: none`, plus `Strict-Transport-Security` (2y, includeSubDomains) **in production only** (`cfg.Environment == "production"`; omitted on plain-HTTP localhost). No CSP is set — this service serves JSON/media, not HTML, so a page CSP belongs to `vidra-user`; CORS stays with the dedicated CORS middleware. Tested: `TestSecureHeadersPresent` (all base headers, HSTS absent outside prod) + `TestSecureHeadersHSTSInProduction` (HSTS present + base headers still apply). No route/contract change.)
- [ ] Add audit logging for sensitive actions (typed audit events, no secrets; see P17.2 and `.ralph/specs/observability.md`).
- [x] Enforce no-secrets-in-logs via the secrets-in-logs guard test (P17.2). (`TestNoSensitiveLogKeys` in `internal/observability/logging_guard_test.go`, runs under `make ci`.)
- [x] Add fuzz tests for URL parsing. (`FuzzValidateURL` in `internal/urlsafety/urlsafety_test.go` — asserts `ValidateURL` never panics and that any accepted URL is http/https with a host and no userinfo; seed corpus runs under `make ci`, and a 10s `go test -fuzz` pass (270k+ execs) found no crash. Metadata/ActivityPub-parsing fuzz targets remain for those slices.)
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
- [x] Accept + echo the `X-Correlation-ID` header and log it as `correlation_id` (the OTel-off correlation contract that pairs with `vidra-user`). (`internal/httpapi/correlation.go` middleware, mounted after `RequestID` and before the request logger: reads inbound `X-Correlation-ID`, mints from the request id when absent, **sanitises** it (untrusted input — URL-safe token chars only, ≤128, so no CR/LF/log injection), echoes it on the response, and threads it through the request context so the request-log line carries `correlation_id`. Tested: echo inbound, mint-when-absent (== request id), sanitise unsafe input, fall back to mint when fully unsafe, and `correlation_id` present in the request log. The observability guards pass over the new code.)
- [x] Centralize logger construction in `internal/observability` and inject it. (`observability.NewLogger(w, level, format)` in `internal/observability/logger.go` is the single logger constructor — parses level (debug|info|warn|error) + format (json|text), returns a `*slog.Logger`, errors on bad input (`ParseLevel` exported for reuse). `cmd/api` builds it from config after `config.Load()`, `slog.SetDefault`s it, and injects it into the server via `httpapi.WithLogger`; a bootstrap Info/JSON logger covers pre-config-load errors. Tested: level filtering, json/text formats, empty→info/json defaults, invalid level/format rejected, `ParseLevel` table.)
- [x] Add `LOG_LEVEL` and `LOG_FORMAT` (json/text) config + `.env.example` + tests. (`config.LogLevel`/`LogFormat`, default info/json, lowercased on load, validated in `validate()` (unknown value → error). `.env.example` documents both in a logging section. Tested: defaults, override+case-normalisation, invalid level/format rejected.)
- [ ] Propagate the request-scoped logger (request_id/trace_id) through service and store layers via `context.Context`.

## P17.2 Security-friendly logging

- [~] Add a redaction helper + denylist of sensitive field names in `internal/observability`; route struct/config logging through it (never log `cfg` whole). (Denylist DONE + now enforced: `IsSensitiveKey`/`sensitiveKeys` in `audit.go` is the canonical list, and the secrets-in-logs guard below fails the build on any denylisted slog key. `cfg` is never logged whole today — `cmd/api` logs only individual non-sensitive fields. A value-scrubbing `Redact(...)` helper for the rare "must log a struct" case remains optional.)
- [x] Add the banned-logging guard test (`TestNoForbiddenLogging`): no `fmt.Print*`/`log.Print*`/`println` diagnostics outside `main`/tests. (`internal/observability/logging_guard_test.go` — an AST guard that parses every non-test, non-`package main` file in the module and fails on `fmt.Print*`, `log.Print*`/`Fatal*`/`Panic*`, or the `println`/`print` builtins (`fmt.Fprint*`/`Sprint*`/`Errorf` allowed). Runs under `make ci` via `test-race`; being an ordinary Go test IS the enforcement — no Makefile change. Green on the current tree; a negative check confirmed it catches a planted `fmt.Println`.)
- [x] Add the secrets-in-logs guard test: fail when a denylisted key is used as an slog/span/metric key. (`TestNoSensitiveLogKeys` in the same file — flags a denylisted `IsSensitiveKey` name used as a structured-log key in three idioms: inline in an slog key/value call (`Debug/Info/Warn/Error[Context]`, `Log`, top-level `slog.*` — key positions computed per signature), at an even index of a `[]any{}`/`[]interface{}{}` args slice (the slog args-builder idiom used by `server.go`/`audit.go`), and as an slog attribute-constructor key (`slog.String("token", …)`). Green on the current tree; a negative check confirmed it catches planted inline/slice/attr-ctor keys.)
- [x] Implement typed audit events for auth/admin/moderation actions (durable, no secrets); add per-action audit tests asserting no denylisted field. (See P15.) (Typed `observability.AuditEvent` + `Action*`/`Result*` constants emitted from the `s.audit` choke point across auth/registration/moderation/admin handlers; now ALSO persisted durably to the `audit_log` table (migration 0029, `internal/audit`) and exposed at `GET /admin/audit-log` — see the admin-audit-log item in P9. No denylisted field can reach a slog line (the `TestNoSensitiveLogKeys` guard) and audit rows carry only action/result/actor_id/reason/request_id; `audit_test.go` asserts no denylisted key + password never logged.)

## P17.3 OpenTelemetry (traces + metrics)

- [x] Add OTel Go SDK setup + graceful shutdown in `internal/observability`, wired in `cmd/api` (and worker), no-op when disabled. (`observability.SetupTracing(ctx, TracingConfig)` (`internal/observability/otel.go`): when disabled returns a no-op shutdown (global stays the no-op provider, zero cost); when enabled builds an OTLP trace exporter (grpc or http/protobuf, `WithInsecure`, lazy-connect so a down collector never blocks startup), a `sdktrace` provider with a `service.name`/`service.version` resource, sets it global + the W3C TraceContext+Baggage propagator, and returns `tp.Shutdown`. `cmd/api` calls it and defers shutdown. Tested: disabled→no-op, unknown-protocol→error, enabled→constructs+shutdown.)
- [x] Add config `OTEL_ENABLED`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `OTEL_EXPORTER_OTLP_PROTOCOL`, `OTEL_SERVICE_NAME` + `.env.example` + validation. (`config.OTelEnabled`/`OTelExporterEndpoint`/`OTelExporterProtocol` (grpc default) /`OTelServiceName` (default `vidra-core`); validated when enabled: protocol ∈ {grpc, http/protobuf}, endpoint required. `.env.example` documents all four. Tested: defaults, enabled-valid, missing-endpoint→error, bad-protocol→error. `METRICS_ENABLED` lands with the metrics slice below.)
- [~] Instrument HTTP (otelecho), datastore (pgx/Redis spans), and outbound HTTP calls. (HTTP DONE: `otelecho.Middleware(service)` is mounted (gated on `OTEL_ENABLED`, so zero cost off) after correlationID and before the request logger, so each request is a span and the logger sees its trace_id. Datastore (pgx/Redis) + outbound-HTTP spans are a follow-up.)
- [~] Accept inbound W3C `traceparent` from `vidra-user`; inject context on outbound calls. (Inbound DONE: the W3C `TraceContext` propagator is installed by `SetupTracing`, and otelecho extracts inbound `traceparent`/`tracestate`, so a `vidra-user` request continues its trace in `vidra-core`. Outbound injection lands with the datastore/outbound span work.)
- [ ] Export RED metrics with bounded label cardinality (no IDs/tokens/raw URLs as labels); gate the metrics surface behind `METRICS_ENABLED` and document any route in `api/openapi.yaml`.
- [x] Stamp `trace_id`/`span_id` into slog output when OTel is enabled. (The request logger adds `trace_id`/`span_id` from `trace.SpanContextFromContext` whenever a span is active (`HasTraceID()`), pairing with the OTel-off `correlation_id`. Tested: `TestRequestLogIncludesTraceID` injects a span context and asserts both fields on the `request` line.)
- [ ] Add optional Docker Compose profile for a local OTel Collector / Jaeger.

## P17.4 Operations

- [ ] Add health/readiness for dependencies. (done for postgres/redis)
- [ ] Add worker status reporting.
- [ ] Add job retry/dead-letter visibility.
- [x] Add admin-facing system status endpoint. (`GET /api/v1/admin/system` (requireRole admin) → an operational snapshot: `software` (name/version/commit/build_date/go_version from `internal/version`), `environment`, `uptime_seconds` (from a `Server.startedAt` stamped at `New()`), an overall `status` (ok|degraded), and per-dependency `components` (postgres/redis). Reuses a new shared `Server.componentHealth(ctx)` helper (extracted from `handleReady`, so readiness + status stay in lock-step); a nil Pinger reports `not_configured`. Always **200** (even when degraded) so the admin dashboard can render the degraded state, unlike `/readyz` which 503s. Reports only operational metadata — no secrets/PII. `internal/httpapi/admin_system.go`; registered gated on `s.authsvc != nil` (auth guards it) + in `fullRouteOptions` so the drift guard enforces it. openapi documents the route + `SystemStatus` schema (drift guard green). Tested: `TestSystemStatus` (admin 200 with vidra/go_version/env=test/status=ok/components present → non-admin 403 → anon 401). **This unblocks the `vidra-user` P10 system-status page.**)
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
