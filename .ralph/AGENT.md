# Agent Build Instructions — vidra-core (Go backend)

> Scope: the `vidra-core` Go backend only. Run all commands from the `vidra-core/`
> project root. Do not touch the sibling `../vidra-user/` project.

## Stack
Go · Echo · PostgreSQL (pg_trgm, uuid-ossp) · pgx · sqlc · Redis · Docker.
Module path: `github.com/vidra/vidra-core`.

## Project setup
```bash
cp .env.example .env       # safe local dev defaults
go mod download
```

## Local development
```bash
# Bring up just the datastores, then run the API on the host:
docker compose --profile core up postgres redis
make migrate-up            # requires the `migrate` CLI
make run                   # runs the API against local Postgres/Redis

# Or run the whole stack in Docker:
make up                    # postgres + redis + migrations + api
make down                  # stop the stack
```

Verify a running instance:
```bash
curl localhost:8080/healthz          # liveness
curl localhost:8080/readyz           # readiness (postgres + redis)
curl localhost:8080/version          # build version / commit / date
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
curl localhost:8080/api/v1/instance  # public about/config (name, description, software, registration_enabled, terms/privacy/contact)

# Auth (returns {token, token_type, expires_in, user}):
curl -sX POST localhost:8080/api/v1/auth/register \
  -H 'content-type: application/json' \
  -d '{"username":"ada","email":"ada@example.test","password":"supersecret"}'
curl -sX POST localhost:8080/api/v1/auth/login \
  -H 'content-type: application/json' \
  -d '{"email":"ada@example.test","password":"supersecret"}'

# Authenticated request (current account):
curl -s localhost:8080/api/v1/auth/me -H 'authorization: Bearer <token>'
# Update profile (partial: display_name, bio):
curl -sX PATCH localhost:8080/api/v1/auth/me -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"display_name":"Ada L.","bio":"builder"}'

# Rotate / revoke a refresh token:
curl -sX POST localhost:8080/api/v1/auth/refresh \
  -H 'content-type: application/json' -d '{"refresh_token":"<refresh>"}'
curl -sX POST localhost:8080/api/v1/auth/logout \
  -H 'content-type: application/json' -d '{"refresh_token":"<refresh>"}'
# Sign out everywhere (revokes all sessions for the bearer's account):
curl -sX POST localhost:8080/api/v1/auth/logout-all -H 'authorization: Bearer <token>'

# Deactivate the current account (re-confirms password; disables + signs out
# everywhere; reversible by an admin):
curl -sX POST localhost:8080/api/v1/auth/me/deactivate -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"password":"<current-password>"}'

# Password reset (request is always 202 — never reveals if the email exists; the
# raw token is delivered by the mailer adapter, a no-op until a provider is wired):
curl -sX POST localhost:8080/api/v1/auth/password-reset \
  -H 'content-type: application/json' -d '{"email":"ada@example.test"}'
curl -sX POST localhost:8080/api/v1/auth/password-reset/confirm \
  -H 'content-type: application/json' -d '{"token":"<reset-token>","password":"<new-password>"}'

# Email verification (request is behind auth — sends to the bearer's own email,
# 202 and a no-op if already verified; confirm is public and flips email_verified):
curl -sX POST localhost:8080/api/v1/auth/verify-email -H 'authorization: Bearer <token>'
curl -sX POST localhost:8080/api/v1/auth/verify-email/confirm \
  -H 'content-type: application/json' -d '{"token":"<verification-token>"}'

# DEV-ONLY token retrieval (test seam). With DEV_MAIL_CAPTURE_ENABLED=true the raw
# reset/verify token is captured in memory and readable here, so e2e tests can
# complete the confirm steps. The route exists only when the flag is on (the api
# WARNs on boot); NEVER enable in production. Not in api/openapi.yaml by design.
# kind=reset (default) | verification.
curl -s 'localhost:8080/api/v1/dev/email-token?email=ada@example.test&kind=reset'

# Channels:
curl -sX POST localhost:8080/api/v1/channels -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' \
  -d '{"handle":"ada_makes","display_name":"Ada Makes","description":"things"}'
curl -s localhost:8080/api/v1/me/channels -H 'authorization: Bearer <token>'  # list own
curl -s localhost:8080/api/v1/channels/ada_makes                              # public page
curl -sX PATCH localhost:8080/api/v1/channels/ada_makes -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"description":"updated"}'          # owner-only
curl -sX DELETE localhost:8080/api/v1/channels/ada_makes -H 'authorization: Bearer <token>'  # owner-only
curl -sX POST   localhost:8080/api/v1/channels/ada_makes/follow -H 'authorization: Bearer <token>'  # follow
curl -sX DELETE localhost:8080/api/v1/channels/ada_makes/follow -H 'authorization: Bearer <token>'  # unfollow

# Videos:
curl -sX POST localhost:8080/api/v1/channels/ada_makes/videos -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"title":"My upload","privacy":"public"}'  # create draft (owner-only)
curl -s localhost:8080/api/v1/videos/<id>                                            # public/unlisted; private => owner only
curl -s localhost:8080/api/v1/channels/ada_makes/videos                              # owner: all; else public-only
curl -s 'localhost:8080/api/v1/videos?sort=trending&limit=20&offset=0'               # public feed (sort: recent|popular|trending; cards carry views + has_thumbnail)
curl -s 'localhost:8080/api/v1/videos/search?q=go'                                   # fuzzy title search (public)
curl -s 'localhost:8080/api/v1/me/subscriptions/videos?limit=20' -H 'authorization: Bearer <token>'  # videos from followed channels
curl -sX PATCH  localhost:8080/api/v1/videos/<id> -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"privacy":"public"}'                       # owner-only
curl -sX DELETE localhost:8080/api/v1/videos/<id> -H 'authorization: Bearer <token>' # owner-only
curl -sX POST localhost:8080/api/v1/videos/<id>/file -H 'authorization: Bearer <token>' \
  -F 'file=@clip.mp4'                                                                 # upload original (owner-only) -> published (no prober yet)
curl -s localhost:8080/api/v1/videos/<id>/original -o out.mp4                         # stream original (Range-capable); private => owner only
curl -s localhost:8080/api/v1/videos/<id>/thumbnail -o poster.jpg                     # poster image (if ffmpeg generated one)

# Captions (WebVTT; owner uploads/removes, anyone lists/downloads on a public video):
curl -sX POST localhost:8080/api/v1/videos/<id>/captions -H 'authorization: Bearer <token>' \
  -F 'language=en' -F 'label=English' -F 'file=@subs.vtt'                             # upload a caption (owner-only; bad vtt/lang -> 422)
curl -s localhost:8080/api/v1/videos/<id>/captions                                    # list caption tracks (public)
curl -s localhost:8080/api/v1/videos/<id>/captions/en -o subs.vtt                     # download a track (text/vtt)
curl -sX DELETE localhost:8080/api/v1/videos/<id>/captions/en -H 'authorization: Bearer <token>'  # remove a track (owner-only, idempotent)

# Comments (on public, published videos):
curl -s localhost:8080/api/v1/videos/<id>/comments                                   # list (public, newest-first, paginated)
curl -sX POST localhost:8080/api/v1/videos/<id>/comments -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"body":"nice video"}'
curl -sX DELETE localhost:8080/api/v1/comments/<comment-id> -H 'authorization: Bearer <token>'  # delete a comment (author's own; a moderator/admin may delete anyone's)

# Ratings (like/dislike on public, published videos):
curl -s localhost:8080/api/v1/videos/<id>/rating                                     # counts (+ my_rating if authed)
curl -sX PUT localhost:8080/api/v1/videos/<id>/rating -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"rating":"like"}'                          # or "dislike"
curl -sX DELETE localhost:8080/api/v1/videos/<id>/rating -H 'authorization: Bearer <token>'  # clear your rating

# Saved videos / watch later (public, published videos):
curl -sX POST localhost:8080/api/v1/videos/<id>/save -H 'authorization: Bearer <token>'    # save (idempotent)
curl -sX DELETE localhost:8080/api/v1/videos/<id>/save -H 'authorization: Bearer <token>'  # unsave
curl -s 'localhost:8080/api/v1/me/saved?limit=20' -H 'authorization: Bearer <token>'        # your library (cards, newest-saved first)
curl -sX POST localhost:8080/api/v1/videos/<id>/view                                  # record a view (deduped per viewer/hour) -> 204

# Watch history & resume position (public, published videos):
curl -sX PUT localhost:8080/api/v1/videos/<id>/watch-progress -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"position_seconds":42}'                    # record resume position -> 204
curl -s localhost:8080/api/v1/videos/<id>/watch-progress -H 'authorization: Bearer <token>'      # {video_id, position_seconds} (0 if none)
curl -s 'localhost:8080/api/v1/me/history?limit=20' -H 'authorization: Bearer <token>'           # history (cards + position_seconds + watched_at, newest-watched first)
curl -sX DELETE localhost:8080/api/v1/me/history/<id> -H 'authorization: Bearer <token>'         # remove one entry (idempotent)
curl -sX DELETE localhost:8080/api/v1/me/history -H 'authorization: Bearer <token>'              # clear all history (idempotent)

# Notifications (created as a side effect of follow/comment; never self-notify):
curl -s 'localhost:8080/api/v1/me/notifications?unread=true&limit=20' -H 'authorization: Bearer <token>'  # {notifications, unread_count, ...}
curl -s localhost:8080/api/v1/me/notifications/unread-count -H 'authorization: Bearer <token>'   # {unread_count} (for a badge)
curl -sX POST localhost:8080/api/v1/me/notifications/<id>/read -H 'authorization: Bearer <token>' # mark one read (idempotent; 404 if not yours)
curl -sX POST localhost:8080/api/v1/me/notifications/read-all -H 'authorization: Bearer <token>'  # mark all read

# Playlists (named collections; visibility public/unlisted/private, default private):
curl -sX POST localhost:8080/api/v1/playlists -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"title":"Faves","visibility":"public"}'  # create -> Playlist
curl -s localhost:8080/api/v1/me/playlists -H 'authorization: Bearer <token>'                  # own playlists (+ video_count)
curl -s localhost:8080/api/v1/playlists/<id>                                                    # detail + ordered video cards (private => owner only)
curl -sX PATCH  localhost:8080/api/v1/playlists/<id> -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"title":"Renamed"}'                                  # owner-only partial update
curl -sX DELETE localhost:8080/api/v1/playlists/<id> -H 'authorization: Bearer <token>'         # owner-only
curl -sX POST localhost:8080/api/v1/playlists/<id>/videos -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"video_id":"<vid>"}'                                 # add (public+published only; idempotent)
curl -sX DELETE localhost:8080/api/v1/playlists/<id>/videos/<vid> -H 'authorization: Bearer <token>'  # remove (idempotent)

# Abuse reports (any authed user files; the queue is moderator/admin-only):
curl -sX POST localhost:8080/api/v1/videos/<id>/report -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"reason":"spam"}'                            # report a video -> 204 (idempotent)
curl -sX POST localhost:8080/api/v1/comments/<id>/report -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"reason":"abuse"}'                           # report a comment -> 204
curl -s 'localhost:8080/api/v1/admin/reports?status=open' -H 'authorization: Bearer <admin-token>'   # moderation queue (403 if not mod/admin)
curl -sX POST localhost:8080/api/v1/admin/reports/<id>/resolve -H 'authorization: Bearer <admin-token>' \
  -H 'content-type: application/json' -d '{"status":"accepted","note":"removed"}'      # accept|reject + internal note -> 204

# Video blocks (moderator/admin only; a blocked video disappears from all public
# surfaces but stays visible to moderators/admins):
curl -sX POST localhost:8080/api/v1/admin/videos/<id>/block -H 'authorization: Bearer <admin-token>' \
  -H 'content-type: application/json' -d '{"reason":"copyright"}'                       # block (idempotent) -> 204
curl -sX DELETE localhost:8080/api/v1/admin/videos/<id>/block -H 'authorization: Bearer <admin-token>'  # unblock (idempotent) -> 204
curl -s 'localhost:8080/api/v1/admin/videos/blocked?limit=20' -H 'authorization: Bearer <admin-token>'  # block-list (newest first; channel, reason, who/when)
curl -s 'localhost:8080/api/v1/admin/videos?q=cat&limit=20' -H 'authorization: Bearer <admin-token>'    # admin videos overview: ALL videos (any privacy/state) + blocked flag; optional q title filter
curl -s 'localhost:8080/api/v1/admin/comments?q=spam&limit=20' -H 'authorization: Bearer <admin-token>' # admin comments overview: ALL comments + author + video; optional q body filter (delete any via DELETE /comments/:id)

# Watched words (instance-wide moderation term list; moderator/admin only; the
# matching/flagging effect on content is a later slice):
curl -sX POST localhost:8080/api/v1/admin/watched-words -H 'authorization: Bearer <admin-token>' \
  -H 'content-type: application/json' -d '{"word":"spam"}'                                # add a term -> 201 (case-insensitive dup -> 409)
curl -s 'localhost:8080/api/v1/admin/watched-words?limit=20' -H 'authorization: Bearer <admin-token>'  # list (newest first, with adder)
curl -sX DELETE localhost:8080/api/v1/admin/watched-words/<id> -H 'authorization: Bearer <admin-token>' # remove (idempotent -> 204)

# Account mutes (a signed-in user mutes another account by user id; the muted
# account's comments AND videos are hidden from them — an authed GET of
# /videos/:id/comments filters muted authors, and the feed/search/subscriptions
# (/videos, /videos/search, /me/subscriptions/videos) hide muted owners' videos.
# Direct channel visits (/channels/:handle/videos) are intentionally unfiltered):
curl -sX POST   localhost:8080/api/v1/me/mutes/accounts/<user-id> -H 'authorization: Bearer <token>'  # mute (idempotent; self -> 422, unknown -> 404)
curl -sX DELETE localhost:8080/api/v1/me/mutes/accounts/<user-id> -H 'authorization: Bearer <token>'  # unmute (idempotent)
curl -s 'localhost:8080/api/v1/me/mutes/accounts?limit=20' -H 'authorization: Bearer <token>'         # your muted accounts (newest first, with identity)

# Admin user management (admin-only; the first registered account is admin):
curl -s 'localhost:8080/api/v1/admin/users?q=ada&limit=20' -H 'authorization: Bearer <admin-token>'  # list/search accounts (no password hash)
curl -sX PATCH localhost:8080/api/v1/admin/users/<id> -H 'authorization: Bearer <admin-token>' \
  -H 'content-type: application/json' -d '{"role":"moderator"}'                        # change role (user|moderator|admin)
curl -sX PATCH localhost:8080/api/v1/admin/users/<id> -H 'authorization: Bearer <admin-token>' \
  -H 'content-type: application/json' -d '{"is_active":false}'                          # deactivate (revokes the user's sessions); self-demote/deactivate -> 422
```
All non-2xx responses use the `ErrorResponse` envelope
(`{"error":{"code","message","request_id"}}`; validation failures add a `fields`
array). Handlers decode+validate input with `bindAndValidate` (400 on malformed
body, 422 with field errors on failed `Validate()`). `make build` injects version
metadata into `/version` via `-ldflags`. The `/api` surface is rate limited
(Redis fixed-window, per IP, `RATE_LIMIT_*` env, default 120/min) with
`X-RateLimit-*` headers and a `429 rate_limited` envelope; system probes are
exempt and the limiter fails open if Redis is down. The Redis limiter has a
gated integration test:
`REDIS_URL=redis://localhost:6379/0 go test -tags=integration ./internal/ratelimit/...`.
Media metadata is extracted by `internal/media.FFProbe` (ffprobe); its pure JSON
parser is unit-tested in `make ci`, while the real-ffprobe test is gated behind
`-tags=integration` (needs ffmpeg): `go test -tags=integration ./internal/media/...`.

## Build / run
```bash
make build     # build the api binary into ./bin
make run       # run the api locally (needs Postgres + Redis)
go build ./...
```

## Tests
```bash
make test          # go test ./...
make test-race     # go test -race ./...
make cover         # coverage summary
go test ./internal/config/...   # focused package run
```
Integration tests expect a live PostgreSQL + Redis (use `make up` or the `core`
Compose profile). Migration tests must apply cleanly against a fresh database.

Password hashing: production uses bcrypt cost 12, but test binaries call
`auth.UseFastPasswordHashingForTests()` (from an `init()` in `internal/auth` and
`internal/httpapi` `*_test.go` files) to drop to bcrypt's min cost — keeping
suites that register many accounts fast. Add the same `init()` to any new package
whose tests register users. Production never lowers the cost.

## Lint / format / generate
```bash
make fmt           # gofmt / go fmt ./...
make fmt-check     # fail if not gofmt-clean (non-mutating, used by make ci)
make vet           # go vet ./...
make check         # fmt + vet + test (quick local gate)
make ci            # CANONICAL gate: fmt-check + vet + openapi-verify + test-race
make sqlc          # regenerate typed SQL access code (requires sqlc)
golangci-lint run  # if installed
staticcheck ./...  # if installed
```
`make ci` is the single source of truth for the gate — `backend-ci.yml` runs this
exact target, so a local pass and a GitHub pass are the same fact. Add any new
required check to `make ci`, never only to the workflow (`ci-guard.yml` enforces
this and forbids `continue-on-error` cheating).

## API documentation / drift guard
```bash
make openapi-verify  # route<->api/openapi.yaml drift guard (TestOpenAPIContract)
make openapi-lint    # lint the OpenAPI contract with Redocly (needs npx)
make docs-check      # documentation stop guard (runs openapi-verify)
```
`api/openapi.yaml` is the source of truth for the HTTP API. Add/remove/rename a
route and you MUST update the spec in the same change, or `go test ./...` and the
`openapi.yml` workflow fail. See "Documentation Requirements" in `.ralph/PROMPT.md`.

## Migrations
```bash
make migrate-up    # apply migrations against DATABASE_URL (requires migrate CLI)
make migrate-down  # roll back one migration
```
Migrations live in `migrations/`, numbered and ordered. Never edit an applied
migration; add a new one.

## Backend quality gate (run before declaring completion)
1. `make ci` is green — fmt-check + vet + openapi-verify + test-race (the exact
   gate CI runs; "passes locally" must equal "passes in GitHub")
2. `staticcheck` / `golangci-lint` if available
3. migration test against a fresh DB
4. integration smoke profile up
5. Newman/Postman API suite when API behavior changed
6. observability: structured logs, audit events for sensitive actions, and OTel
   follow `.ralph/specs/observability.md`. NOTE: the banned-logging + secrets-in-logs
   guard tests do NOT exist yet and are NOT in `make ci` — building them is a
   fix_plan P17.2 task, not a check to run today.
7. branch CI is green (same `make ci`); `ci-guard.yml` passes — a local green alone
   is not done

Run `make help` for the full target list.

## Key learnings
- Backend lives in `vidra-core/` (monorepo subdir). The module path is
  `github.com/vidra/vidra-core`.
- CI workflows live at the monorepo root `../.github/workflows/` (GitHub ignores
  workflows in subdirectories); scope backend jobs with `vidra-core/**` path filters.
- Redis is wired through `internal/store` (combined open) and/or `internal/cache`;
  Redis is never the durable source of truth.
- Update this file whenever build/test/run commands change.
