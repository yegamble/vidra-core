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
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
```

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

## Lint / format / generate
```bash
make fmt           # gofmt / go fmt ./...
make vet           # go vet ./...
make check         # fmt + vet + test (standard local gate)
make sqlc          # regenerate typed SQL access code (requires sqlc)
golangci-lint run  # if installed
staticcheck ./...  # if installed
```

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
1. `make fmt` / `gofmt -l .` is clean
2. `go vet ./...`
3. `go test ./...` (and `go test -race ./...` where practical)
4. `staticcheck` / `golangci-lint` if available
5. migration test against a fresh DB
6. integration smoke profile up
7. Newman/Postman API suite when API behavior changed
8. `make openapi-verify` — OpenAPI contract matches the router (no doc drift)

Run `make help` for the full target list.

## Key learnings
- Backend lives in `vidra-core/` (monorepo subdir). The module path is
  `github.com/vidra/vidra-core`.
- CI workflows live at the monorepo root `../.github/workflows/` (GitHub ignores
  workflows in subdirectories); scope backend jobs with `vidra-core/**` path filters.
- Redis is wired through `internal/store` (combined open) and/or `internal/cache`;
  Redis is never the durable source of truth.
- Update this file whenever build/test/run commands change.
