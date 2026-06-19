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
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
```

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
