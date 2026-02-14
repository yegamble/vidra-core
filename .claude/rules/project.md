# Project: Athena

**Last Updated:** 2026-02-14

## Overview

High-performance PeerTube-compatible backend in Go with P2P distribution, live streaming, plugin system, and multi-protocol federation (ActivityPub + ATProto).

## Technology Stack

- **Language:** Go 1.24
- **Web Framework:** Chi router (v5)
- **Database:** PostgreSQL with SQLX
- **Cache:** Redis
- **Storage:** Hybrid (Local/IPFS/S3-compatible)
- **Migrations:** Goose
- **Testing:** Standard Go testing with testify, sqlmock
- **Linting:** golangci-lint (with gosec)
- **Video Processing:** FFmpeg
- **P2P:** WebTorrent + IPFS
- **Federation:** ActivityPub, ATProto (beta)

## Directory Structure

```
/cmd/server         → Main application entry point
/internal/          → Application code
  config/           → Environment and configuration
  httpapi/          → Chi routes and HTTP handlers
  middleware/       → Auth, rate limiting, CORS, tracing
  domain/           → Core models (no infrastructure deps)
  usecase/          → Business logic with interfaces
  repository/       → SQLX repositories for PostgreSQL
  security/         → SSRF protection, virus scanning, crypto
  activitypub/      → Federation support
  worker/           → Async jobs (FFmpeg, GC, pins)
  storage/          → Hybrid storage (local/IPFS/S3)
  testutil/         → Test helpers and utilities
/migrations/        → Goose SQL migrations
/api/               → OpenAPI 3.0 specifications
/docs/              → Documentation
/scripts/           → Build and validation scripts
```

## Key Files

- **Configuration:** `.env.example` (template), `internal/config/`
- **Entry Point:** `cmd/server/main.go`
- **Migrations:** `migrations/*.sql` (Goose)
- **Tests:** `**/*_test.go` (232 test files)
- **Build:** `Makefile`

## Development Commands

**Setup:**
```bash
cp .env.example .env          # Copy environment template
make deps                     # Download dependencies
make migrate-up               # Apply database migrations
```

**Development:**
```bash
make run                      # Run the server
make test                     # Run all tests
make test-unit                # Unit tests only (fast)
make lint                     # Run golangci-lint
make fmt                      # Format code (gofmt + goimports)
make build                    # Build binary
```

**Validation (CRITICAL):**
```bash
make validate-all             # REQUIRED before claiming completion
# Runs: gofmt, goimports, golangci-lint, tests, build
```

**Docker:**
```bash
docker compose up --build     # Start all services
docker compose up postgres redis  # Infrastructure only
```

**Migrations:**
```bash
make migrate-up               # Apply all pending migrations
make migrate-down             # Rollback last migration
make migrate-status           # Show migration status
make migrate-create NAME=add_feature  # Create new migration
```

**Coverage:**
```bash
make coverage-check           # Test with coverage threshold
make coverage-per-package     # Per-package coverage report
```

## Architecture Notes

**Clean Architecture:**
- Domain layer: Pure business models, no dependencies
- Usecase layer: Business logic, defines interfaces
- Repository layer: Implements data access
- HTTP layer: Handlers, routes, middleware

**Dependency Injection:**
- Constructor-based DI, no globals
- Interfaces defined in usecase, implemented in repository

**Context Propagation:**
- `context.Context` as first parameter in all functions
- Timeouts on network/DB calls

**Error Handling:**
- Wrap errors with `fmt.Errorf("operation: %w", err)`
- Custom `DomainError` type for business errors

**Testing:**
- Table-driven tests preferred
- `testutil` package for test helpers
- `testing.Short()` to skip integration tests
- sqlmock for database mocking

## Domain-Specific Documentation

See subdirectory CLAUDE.md files for detailed guidance:
- **Security:** `internal/security/CLAUDE.md`
- **API/HTTP:** `internal/httpapi/CLAUDE.md`
- **Federation:** `internal/activitypub/CLAUDE.md`
- **Migrations:** `migrations/CLAUDE.md`
- **Architecture:** `docs/architecture/CLAUDE.md`
- **Validation:** `docs/development/VALIDATION_REQUIRED.md`

## Additional Context

**Sprint Status:** Quality Programme (Sprint 17/20)
- Feature parity: 100% complete
- Current focus: Core services 100% test coverage
- Full test suite: 2,364 automated tests
- Coverage: 52.9% (target: 90%+ for core packages)

**Key Features:**
- PeerTube API compatibility
- Live streaming (RTMP/HLS)
- Video transcoding with FFmpeg
- P2P distribution (WebTorrent + IPFS)
- ActivityPub federation
- OAuth2 + 2FA authentication
- Plugin system with hooks
