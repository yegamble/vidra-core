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
curl -sX PATCH  localhost:8080/api/v1/videos/<id> -H 'authorization: Bearer <token>' \
  -H 'content-type: application/json' -d '{"privacy":"public"}'                       # owner-only
curl -sX DELETE localhost:8080/api/v1/videos/<id> -H 'authorization: Bearer <token>' # owner-only
curl -sX POST localhost:8080/api/v1/videos/<id>/file -H 'authorization: Bearer <token>' \
  -F 'file=@clip.mp4'                                                                 # upload original (owner-only) -> published (no prober yet)
curl -s localhost:8080/api/v1/videos/<id>/original -o out.mp4                         # stream original (Range-capable); private => owner only
curl -s localhost:8080/api/v1/videos/<id>/thumbnail -o poster.jpg                     # poster image (if ffmpeg generated one)
curl -sX POST localhost:8080/api/v1/videos/<id>/view                                  # record a view (deduped per viewer/hour) -> 204
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
