# Vidra Core

The Go backend for **Vidra** — a clean-room, PeerTube-inspired federated video
platform. This repository (`vidra-core`) exposes the Vidra HTTP API. The Next.js
frontend lives in a separate `vidra-user` repository and consumes this API.

> Status: early bootstrap. The HTTP service, configuration, health/readiness
> probes, database/Redis wiring, migrations, and CI are in place. Product
> features are tracked in `.ralph/fix_plan.md` and the parity ledgers under
> `.ralph/specs/`.

## Quick start

```bash
cp .env.example .env
make up        # postgres + redis + migrations + api via Docker Compose
```

Then:

```bash
curl localhost:8080/healthz          # liveness
curl localhost:8080/readyz           # readiness (postgres + redis)
curl localhost:8080/version          # build version / commit / date
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
curl localhost:8080/api/v1/instance  # public about/config (name, software, registration_enabled)
```

Registration can be closed per-instance with `REGISTRATION_ENABLED=false`: signup then
returns `403` and `GET /api/v1/instance` reports `registration_enabled: false` so the
frontend can hide the form. The instance endpoint also surfaces optional about/legal
metadata — `description`, `terms_url`, `privacy_url`, `contact_email` (from the matching
`INSTANCE_*` env vars; empty when unset) — for the frontend's footer/about pages.

All non-2xx responses share one envelope: `{"error":{"code","message","request_id"}}`
(see `api/openapi.yaml` → `ErrorResponse`). The readiness probe returns its own
`ReadinessResponse` on 503. `make build` injects version/commit/date into `/version`
via `-ldflags`.

Request validation: handlers decode+validate input via `bindAndValidate`. Malformed
bodies get `400 bad_request`; failed validation gets `422 unprocessable_entity` with a
`fields` array (`{field, message}`) so forms can highlight the offending inputs.

Auth: `POST /api/v1/auth/register` and `POST /api/v1/auth/login` create an account /
verify credentials and return an HS256 JWT access token plus a rotating refresh token
(`{token, refresh_token, token_type, expires_in, user}`). Passwords are bcrypt-hashed;
the first account on a fresh instance is granted the `admin` role. Login reports
unknown-account and wrong-password identically (`401`) to prevent enumeration. Configure
signing via `JWT_SECRET` (required in production), `JWT_ISSUER`, `JWT_AUDIENCE`,
`JWT_ACCESS_TTL`, `JWT_REFRESH_TTL`.

Sessions: `POST /api/v1/auth/refresh` exchanges a refresh token for a new pair and
revokes the old one (rotation); reusing an already-rotated token is treated as
compromise and revokes all of that user's sessions. `POST /api/v1/auth/logout` revokes
the presented refresh token (idempotent `204`); `POST /api/v1/auth/logout-all`
(bearer-authenticated) signs the account out everywhere. Refresh tokens are opaque
256-bit values; only their SHA-256 hash is stored in the `sessions` table.

Authorization: routes are gated by `requireAuth` (valid bearer token) and, where
role-restricted, `requireRole(...)` off the JWT's `role` claim — an authenticated
principal lacking an allowed role gets `403`.

Channels: a channel is a publishing identity owned by a user. `POST /api/v1/channels`
(auth) creates one (`handle` 3–30 chars `[A-Za-z0-9_]`, unique case-insensitively →
`409`); `GET /api/v1/me/channels` (auth) lists the caller's channels;
`GET /api/v1/channels/{handle}` is the public channel page lookup (`404` when absent).
`PATCH /api/v1/channels/{handle}` (owner-only, partial: `display_name`/`description`)
and `DELETE /api/v1/channels/{handle}` (owner-only) manage it — a non-owner gets `403`.
The handle is immutable after creation. `POST`/`DELETE /api/v1/channels/{handle}/follow`
(auth, idempotent `204`) follow/unfollow a channel; every channel view carries a
`follower_count`.

Videos: `POST /api/v1/channels/{handle}/videos` (owner-only) creates a draft video
(`title`, optional `description`/`privacy`; starts `state: draft`, `privacy` defaults
`private`). `GET /api/v1/videos/{id}` returns public/unlisted videos to anyone with the
id; a `private` video is returned only to its owner (bearer token) and is `404` to
everyone else so its existence is not leaked. `PATCH`/`DELETE /api/v1/videos/{id}`
(owner-only; non-owner/unknown → `404`) edit/remove it. `GET /api/v1/channels/{handle}/videos`
lists a channel's videos — all of them for the owner, public-only for everyone else.
`GET /api/v1/videos` is the public cross-channel feed (newest-first; paginated with
`?limit` 1–100 default 20 and `?offset`). `GET /api/v1/videos/search?q=` fuzzy-searches
public titles (pg_trgm, ranked by similarity then recency; same pagination).
`POST /api/v1/videos/{id}/file` (owner-only, `multipart/form-data` with a single
`file` part) stores the original through the storage backend, then finalises the
video: `draft → processing → published` (or `failed` if a configured media probe
rejects it). Re-uploading replaces the prior original, and non-owner/unknown → `404`.
The file extension must be an accepted video container (else `415`) and the body must
be within `UPLOAD_MAX_SIZE` (else `413`; this route is exempt from the small
`HTTP_BODY_LIMIT` that guards the JSON API). The stored file is tracked in
`video_files`. Finalisation runs through an injected `Prober` seam: at startup the server uses
the FFprobe-backed prober when `ffprobe` is on `PATH` (it is in the Docker image),
extracting technical metadata (duration, width, height) that the detail endpoint
exposes and persisting it to `video_metadata`; a probe error marks the video
`failed`. Where `ffprobe` is absent the original is trusted and published unprobed
(no metadata). The public discovery surfaces — `GET /api/v1/videos`,
`/videos/search`, and the public view of a channel's videos — return only
`published` videos. (Real transcoding into multiple renditions reuses this same
seam in a later slice.)

`GET /api/v1/videos/{id}/original` streams the stored original bytes for direct
playback — same visibility as the detail endpoint (private → owner only, else
`404`; a video with no stored original is `404`). It honours HTTP `Range`
requests (`206 Partial Content`) so a `<video>` element can seek; the local
backend serves via `http.ServeContent`. HLS/rendition manifests come with
transcoding later.

During finalisation an FFmpeg-backed thumbnailer (when `ffmpeg` is on `PATH`)
extracts a poster frame and stores it as a `thumbnail` file;
`GET /api/v1/videos/{id}/thumbnail` serves the JPEG (same visibility as the
detail endpoint), and the detail response carries a `has_thumbnail` flag.
Thumbnail generation is best-effort — a failure never blocks publishing.

`POST /api/v1/videos/{id}/view` records a view (same visibility as detail; only
published videos accrue views), de-duplicated per viewer per hour in Redis
(`SETNX` over the authenticated user id, else the client IP, hashed — raw
ids/IPs are never used as keys). It always returns `204`; the running `views`
total is exposed on the detail endpoint. (View counts live in a `video_view_counts`
side table; surfacing them on feed cards and a trending sort are later slices.)

Authenticated requests send `Authorization: Bearer <token>`. `GET /api/v1/auth/me`
(protected) returns the current account, reloaded from the database so it reflects
live role/verification state. A missing, malformed, invalid, or expired token yields
`401` without revealing which check failed; a deactivated account is treated as `401`.
`PATCH /api/v1/auth/me` updates the profile (`display_name`, `bio`; partial); identity
fields (username/email) are not editable there pending a re-verification flow.

Request guards: bodies over `HTTP_BODY_LIMIT` (default `8M`) are rejected with `413`;
each request carries a `HTTP_REQUEST_TIMEOUT` (default `30s`) context deadline that
handlers and DB/Redis calls observe (a fired deadline renders as a `503`
`request_timeout`), with the server `WriteTimeout` as the hard backstop.

Rate limiting: the `/api` surface is rate limited per client IP with a Redis
fixed-window limiter (`RATE_LIMIT_REQUESTS` per `RATE_LIMIT_WINDOW`, default 120/min;
disable with `RATE_LIMIT_ENABLED=false`). Responses carry `X-RateLimit-Limit`,
`X-RateLimit-Remaining`, and `X-RateLimit-Reset`; over-budget requests get `429`
`rate_limited` with `Retry-After`. System probes (`/healthz`, `/readyz`, `/version`)
are exempt. If Redis is unreachable the limiter fails open (logs a warning) so a
Redis blip degrades protection, not availability.

Media storage goes through a small `internal/storage.Backend` interface
(Put/Open/Delete/Exists over forward-slash object keys). The default `local` backend
(`STORAGE_BACKEND=local`, `STORAGE_LOCAL_ROOT`) writes under a root directory with
path-traversal-safe key resolution; S3-compatible and IPFS backends land later behind
the same interface. The video original-file upload endpoint writes through it.

## Local development (without Docker for the app)

```bash
cp .env.example .env
# bring up just the datastores:
docker compose --profile core up postgres redis
make migrate-up   # requires the `migrate` CLI
make run          # runs the API against local Postgres/Redis
```

## Developer commands

Run `make help` for the full list (fmt, vet, test, test-race, cover, build,
run, sqlc, migrate-up, up/down).

## Tech stack

Go · Echo · PostgreSQL (pg_trgm, uuid-ossp) · pgx · sqlc · Redis · Docker.

## API contract

`api/openapi.yaml` is the source of truth for the HTTP API and is consumed by the
`vidra-user` frontend. It is kept in lock-step with the code by a drift guard:
`make openapi-verify` (the `TestOpenAPIContract` test) fails if a route is added,
removed, or renamed without a matching spec edit, and the `openapi.yml` CI workflow
lints the spec and runs the same check on every change. Lint locally with
`make openapi-lint`.

## Project docs

- API contract: `api/openapi.yaml`
- Architecture: `.ralph/specs/architecture.md`
- Security: `.ralph/specs/security.md`
- Testing: `.ralph/specs/testing.md`
- PeerTube parity ledgers: `.ralph/specs/peertube-*.md`

## License

TBD.
