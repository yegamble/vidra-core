# Athena - PeerTube Backend in Go

## Quick Reference

**Stack**: Go (Chi router), PostgreSQL (SQLX), Redis, IPFS, FFmpeg, IOTA payments (Phase 2)
**Architecture**: Clean architecture with domain/usecase/repository layers

## Mandatory Validation

**Before claiming ANY code change is complete, run:**

```bash
make validate-all   # or ./scripts/validate-all.sh
```

This runs: `gofmt`, `goimports`, `golangci-lint` (with gosec), unit tests, and build verification.

**Claude Web users**: Cannot run validations directly. Tell user to run `make validate-all` before merging.

## Project Layout

```
/cmd/server         → main entry point
/internal/
  activitypub/      → Federation (see internal/activitypub/CLAUDE.md)
  app/              → application wiring and lifecycle
  backup/           → backup/restore system
  chat/             → live stream chat (WebSocket)
  config/           → environment, flags, validation
  domain/           → core models, no infra dependencies
  httpapi/          → Chi routes, handlers (see internal/httpapi/CLAUDE.md)
  middleware/       → auth, rate-limit, cors, tracing
  plugin/           → plugin system with hooks
  repository/       → SQLX repos for Postgres
  security/         → SSRF, virus scanning, crypto (see internal/security/CLAUDE.md)
  setup/            → setup wizard (first-run config)
  storage/          → hybrid local/IPFS/S3
  usecase/          → business logic with interfaces
  worker/           → async jobs (ffmpeg, GC, pins)
/migrations/        → Goose SQL migrations (see migrations/CLAUDE.md)
/scripts/           → build, validation, install scripts
/terraform/         → infrastructure as code
```

## Key Principles

1. **DI via constructors** - No global state
2. **Context-first APIs** - `context.Context` as first parameter
3. **Error wrapping** - `fmt.Errorf("operation: %w", err)`
4. **Small interfaces** - Repository interfaces in `internal/port/`, service interfaces in `usecase/`
5. **Table-driven tests** - Standard pattern for all unit tests

## Common Commands

```bash
make build          # Build binary
make test           # Run tests
make lint           # Run linter
make fmt            # Format code
make migrate-up     # Apply migrations
make docker         # Build Docker image
```

## Domain-Specific Documentation

For detailed guidance in specific areas, see:

- **Security**: `internal/security/CLAUDE.md` (SSRF, virus scanning, encryption)
- **API/HTTP**: `internal/httpapi/CLAUDE.md` (routes, handlers, validation)
- **Federation**: `internal/activitypub/CLAUDE.md` (ActivityPub, WebFinger)
- **Migrations**: `migrations/CLAUDE.md` (Goose patterns, schema changes)
- **Architecture**: `docs/architecture/CLAUDE.md` (deep-dive on all systems)
- **Validation**: `docs/development/VALIDATION_REQUIRED.md` (pre-commit requirements)

## Best Practices

### Go Patterns

- Use `defer` for cleanup
- Recover panics in goroutines
- Set timeouts on all network/DB calls
- Use context for cancellation

### Security (Critical)

- Validate all user inputs
- Use parameterized queries (no string concatenation)
- No secrets in logs or code
- See `internal/security/CLAUDE.md` for SSRF, virus scanning

### Testing

- Unit tests per package
- Use `sqlmock` for DB mocking
- Integration tests with `docker-compose.yml` (`--profile test`)
