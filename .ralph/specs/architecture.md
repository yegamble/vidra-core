# Vidra Core — Architecture

Status: living document. Updated as subsystems land.

## Repo identity

This checkout (`vidra`) is the **vidra-core** backend: a Go service exposing the
Vidra HTTP API. The frontend (`vidra-user`, Next.js) is a separate repo that
consumes this API. The API contract is the source of truth; the frontend never
shadow-copies guessed DTOs.

## Stack

- Go (stable) — application language.
- Echo v4 — HTTP routing/middleware.
- PostgreSQL 16 — durable system of record. Extensions: `pg_trgm`, `uuid-ossp`.
- sqlc + pgx/v5 — typed SQL access over a connection pool.
- Redis 7 — sessions, rate limiting, idempotency keys, hot status caches.
  Never the durable source of truth for restart-surviving data.
- Docker Compose — local dev parity with CI.

## Layering

Handlers are thin; business logic is testable without HTTP.

```
cmd/api/                  process entrypoint, signal handling, dependency wiring
internal/config/          environment parsing + validation (only env reader)
internal/httpapi/         Echo handlers, routing, middleware, request binding
internal/store/           pgx pool, sqlc-generated queries (sqlcgen), repositories
internal/cache/           Redis client
migrations/               numbered SQL migrations (golang-migrate format)
api/                      OpenAPI + Postman/Newman collections (added as API grows)
```

Planned packages (added with their features): `internal/auth`, `internal/media`,
`internal/storage`, `internal/federation`, `internal/messaging`,
`internal/security`, `internal/service`.

## Request lifecycle

1. `cmd/api` loads `config`, opens `store` (Postgres) and `cache` (Redis),
   fails fast if either is unreachable within a bounded startup window.
2. `httpapi.New` registers middleware (recover, request-id, CORS allow-list)
   and routes, then serves with graceful shutdown on SIGINT/SIGTERM.
3. Handlers delegate to service packages; services use `store`/`cache`.

## Health & readiness

- `GET /healthz` — liveness only; no dependency checks. 200 when the process is up.
- `GET /readyz` — pings Postgres and Redis; 200 when all healthy, 503 otherwise,
  with per-component status in the body.
- `GET /api/v1/nodeinfo` — minimal instance discovery metadata (expands toward
  NodeInfo 2.1 as federation lands).

## Configuration

All runtime config comes from the environment via `internal/config`. Safe local
defaults target the Docker Compose service addresses. Production requires
explicit `DATABASE_URL` and `REDIS_URL` and rejects wildcard CORS origins.

## Migrations

`golang-migrate` numbered pairs (`NNNN_name.up.sql` / `.down.sql`). CI applies
all up-migrations against a fresh database on every run. sqlc reads the same
`migrations/` directory as its schema source, so generated types track schema.

## Decisions (ADR-lite)

- **pgx pool over database/sql**: native Postgres types, better performance,
  first-class sqlc support.
- **Liveness vs readiness split**: lets orchestrators distinguish "process up"
  from "dependencies reachable" and avoids restart loops during transient DB blips.
- **Single config reader**: `os.Getenv` is confined to `internal/config` so
  configuration is auditable and testable in one place.
