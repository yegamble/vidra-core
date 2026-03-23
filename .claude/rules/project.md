# Project: Athena

**Last Updated:** 2026-03-21

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
- **Federation:** ActivityPub, ATProto (BlueSky)
- **Payments:** IOTA Rebased (Ed25519 transaction signing + submission)

## Directory Structure

```
/cmd/server         → Main application entry point
/internal/          → Application code
  activitypub/      → Federation support (ActivityPub, WebFinger)
  app/              → Application wiring and lifecycle
  backup/           → Backup/restore system
  chat/             → Live stream chat (WebSocket)
  config/           → Environment and configuration
  crypto/           → Cryptographic utilities
  database/         → Database connection and pooling
  domain/           → Core models (no infrastructure deps)
  email/            → Email sending and verification
  generated/        → Auto-generated code (OpenAPI types)
  health/           → Health check endpoints
  httpapi/          → Chi routes and HTTP handlers
  importer/         → Video import from external platforms
  ipfs/             → IPFS client and pinning
  livestream/       → Live streaming (RTMP/HLS)
  metrics/          → Prometheus metrics
  middleware/       → Auth, rate limiting, CORS, tracing
  obs/              → Observability (logging, tracing)
  payments/         → IOTA payment integration
  plugin/           → Plugin system with hooks
  port/             → Interface definitions (ports)
  repository/       → SQLX repositories for PostgreSQL
  scheduler/        → Cron and scheduled jobs
  security/         → SSRF protection, virus scanning, crypto
  setup/            → Setup wizard (first-run configuration)
  storage/          → Hybrid storage (local/IPFS/S3)
  sysinfo/          → System information reporting
  testutil/         → Test helpers and utilities
  torrent/          → WebTorrent tracker and P2P
  usecase/          → Business logic with interfaces
  validation/       → Input validation utilities
  whisper/          → Audio transcription (Whisper)
  worker/           → Async jobs (FFmpeg, GC, pins)
/migrations/        → Goose SQL migrations
/api/               → OpenAPI 3.0 specifications
/docs/              → Documentation
/k8s/               → Kubernetes manifests
/pkg/               → Public packages
/postman/           → Postman collections for API testing
/scripts/           → Build, validation, and install scripts
/terraform/         → Infrastructure as code
/tests/             → E2E and integration test suites
```

## Key Files

- **Configuration:** `.env.example` (template), `internal/config/`
- **Entry Point:** `cmd/server/main.go`
- **Migrations:** `migrations/*.sql` (Goose)
- **Tests:** `**/*_test.go` (428 test files, ~4,641 test functions)
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

**Pre-commit hook env vars (override defaults):**

```bash
PRECOMMIT_SKIP_HOOKS=""       # Default skips go-unit-tests,test-coverage. Set "" to run all.
LINT_TIMEOUT_SECONDS=180      # Lint timeout (0 = unlimited)
HOOK_TIMEOUT_SECONDS=240      # Total hook timeout (0 = unlimited)
```

See `.claude/rules/pre-commit-hook.md` for full details.

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
make migrate-dev              # Apply to dev DB (reads .env)
make migrate-test             # Apply to test DB (reads .env.test)
make migrate-custom           # Apply to DATABASE_URL env var
```

**Integration testing with mock services:**

```bash
make test-mock-services-up    # Start Docker mock services (MinIO, ATProto PDS, IOTA, Mailpit, etc.)
make test-mock-services-down  # Stop mock services
make test-external-integration  # Run external service integration tests (auto starts/stops)
```

**OpenAPI code generation:**

```bash
make generate-openapi         # Regenerate types from api/*.yaml spec
make verify-openapi           # Fail if generated code drifts from spec
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
- Wiring centralized in `internal/app/app.go`
- Usecase packages aliased as `uc*` (e.g., `ucbackup`, `ucchannel`)

**Interface Locations:**

- Repository interfaces: `internal/port/` (20 files)
- Service interfaces: `internal/usecase/` (co-located with implementations)

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

**Sprint Status:** Quality Programme + PeerTube Parity Complete (20/20 sprints + 3 parity sprints done)

- Feature parity: 100% complete (44 PeerTube categories, all gap endpoints + Video Studio + Migration ETL implemented)
- Full test suite: ~4,879 test functions (456 test files)
- Coverage: 69.9% overall unit test coverage (90%+ for core packages)
- Newman: 44 Postman collections in CI runner, all validated
- ATProto (BlueSky) `PublishVideo` fully implemented and verified
- IOTA Rebased payments: Ed25519 transaction signing + submission implemented

**Key Features:**

- PeerTube API compatibility
- Live streaming (RTMP/HLS)
- Video transcoding with FFmpeg
- P2P distribution (WebTorrent + IPFS)
- ActivityPub federation
- OAuth2 + 2FA authentication
- Plugin system with hooks
- Setup wizard (first-run web configuration)
- Backup/restore system with CLI
- Video import from external platforms
- Audio transcription (Whisper)
- Kubernetes and Terraform deployment
- Video Studio Editing: server-side FFmpeg pipeline (cut, intro, outro, watermark)
- Migration ETL: PeerTube dump → Athena import pipeline with dry-run and cancel support
