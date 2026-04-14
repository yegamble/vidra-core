# Vidra Core - PeerTube Backend in Go

## Vision

Vidra Core is a high-performance, PeerTube-compatible video platform backend in Go. It maintains full API compatibility with [PeerTube](https://github.com/Chocobozzz/PeerTube) while extending beyond it with Bitcoin payments (BTCPay Server), ATProto/BlueSky federation, IPFS-native storage, and secure messaging. Every change must preserve PeerTube parity and Vidra-specific extensions.

## Quick Reference

**Stack**: Go (Chi router), PostgreSQL (SQLX), Redis, IPFS, FFmpeg, Bitcoin payments (BTCPay Server)
**Architecture**: Clean architecture with domain/usecase/repository layers
**Upstream Reference**: https://github.com/Chocobozzz/PeerTube

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
  crypto/           → Cryptographic utilities
  database/         → Database connection and pooling
  domain/           → core models, no infra dependencies
  email/            → Email sending and verification
  generated/        → Auto-generated code (OpenAPI types)
  health/           → Health check endpoints
  httpapi/          → Chi routes, handlers (see internal/httpapi/CLAUDE.md)
  importer/         → Video import from external platforms
  ipfs/             → IPFS client and pinning
  livestream/       → Live streaming (RTMP/HLS)
  metrics/          → Prometheus metrics
  middleware/       → auth, rate-limit, cors, tracing
  obs/              → Observability (logging, tracing)
  payments/         → Bitcoin payment integration (BTCPay Server client)
  plugin/           → plugin system with hooks
  port/             → Interface definitions (ports)
  repository/       → SQLX repos for Postgres
  scheduler/        → Cron and scheduled jobs
  security/         → SSRF, virus scanning, crypto (see internal/security/CLAUDE.md)
  setup/            → setup wizard (first-run config)
  storage/          → hybrid local/IPFS/S3
  sysinfo/          → System information reporting
  testutil/         → Test helpers and utilities
  torrent/          → WebTorrent tracker and P2P
  usecase/          → business logic with interfaces
  validation/       → Input validation utilities
  whisper/          → Audio transcription (Whisper)
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

## Stop Hooks (Autonomous Mode Guardrails)

Hard constraints enforced during all autonomous operations. See `.claude/rules/stop-hooks.md` for full details.

These guardrails are enforced by `.claude/settings.json`, `.githooks/pre-commit`, and `scripts/verify-autonomous-stop-hooks.sh`.

**10 Stop Conditions — violations halt work until fixed:**

1. **Feature Removal** — never remove/disable existing features (see `.claude/rules/feature-parity-registry.md`)
2. **Test Coverage** — no production code without tests (TDD mandatory)
3. **API Contract** — no breaking changes to response shapes
4. **Architecture Violation** — respect clean architecture layers
5. **Security Regression** — no SQL injection, missing auth, SSRF, or secrets in code
6. **Federation Compatibility** — WebFinger, NodeInfo, ActivityPub must stay functional
7. **Build/Lint/Test** — `make validate-all` must pass
8. **Migration Safety** — reversible migrations only, no destructive DDL without data migration
9. **PeerTube Parity Drift** — upstream-compatible behavior must be preserved or explicitly documented
10. **Requested Feature Completion** — user-requested features must be tracked, implemented end-to-end, and fully tested before completion

**Autonomous mode also requires (see `.claude/rules/autonomous-mode.md`):**

- Record every requested feature in the feature parity registry before coding
- Update README and documentation for every user-visible change
- Update Postman/Newman collections for every API change
- Update OpenAPI specs for every endpoint change
- Update feature parity registry for every feature change
- Do not mark a feature `Done` until Go tests, API tests, docs, and registry updates land together
- Run `make verify-openapi` after API modifications

## Guardrail Rules Files

| File | Purpose |
|------|---------|
| `.claude/rules/stop-hooks.md` | 10 stop conditions, pre/post change checklists |
| `.claude/rules/feature-parity-registry.md` | Canonical feature list, PeerTube parity tracking |
| `.claude/rules/autonomous-mode.md` | Documentation, Postman, and completeness requirements |
| `.claude/settings.json` | Claude Code hooks (generated file protection, auto-format, commit safety) |
| `.githooks/pre-commit` | Git-side validation entrypoint for autonomous stop hooks |
| `scripts/verify-autonomous-stop-hooks.sh` | Shared parity/test/docs drift enforcement used by Claude + Git hooks |

## Additional Context

**Sprint Status:** Quality Programme + PeerTube Parity Complete (20/20 sprints + 3 parity sprints done)

- Feature parity: 100% complete (44 PeerTube categories, all gap endpoints + Video Studio + Migration ETL implemented)
- Full test suite: ~4,879 test functions (456 test files)
- Coverage: 69.9% overall unit test coverage (90%+ for core packages)
- Newman: 44 Postman collections in CI runner, all validated
- ATProto (BlueSky) `PublishVideo` fully implemented and verified
- Bitcoin payments: BTCPay Server integration with Greenfield API, invoice management, webhook callbacks

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
- Migration ETL: PeerTube dump → Vidra Core import pipeline with dry-run and cancel support

## Skill routing

When the user's request matches an available skill, ALWAYS invoke it using the Skill
tool as your FIRST action. Do NOT answer directly, do NOT use other tools first.
The skill has specialized workflows that produce better results than ad-hoc answers.

Key routing rules:
- Product ideas, "is this worth building", brainstorming → invoke office-hours
- Bugs, errors, "why is this broken", 500 errors → invoke investigate
- Ship, deploy, push, create PR → invoke ship
- QA, test the site, find bugs → invoke qa
- Code review, check my diff → invoke review
- Update docs after shipping → invoke document-release
- Weekly retro → invoke retro
- Design system, brand → invoke design-consultation
- Visual audit, design polish → invoke design-review
- Architecture review → invoke plan-eng-review
- Save progress, checkpoint, resume → invoke checkpoint
- Code quality, health check → invoke health

## gstack (REQUIRED — global install)

**Before doing ANY work, verify gstack is installed:**

```bash
test -d ~/.claude/skills/gstack/bin && echo "GSTACK_OK" || echo "GSTACK_MISSING"
```

If GSTACK_MISSING: STOP. Do not proceed. Tell the user:

> gstack is required for all AI-assisted work in this repo.
> Install it:
> ```bash
> git clone --depth 1 https://github.com/garrytan/gstack.git ~/.claude/skills/gstack
> cd ~/.claude/skills/gstack && ./setup --team
> ```
> Then restart your AI coding tool.

Do not skip skills, ignore gstack errors, or work around missing gstack.

Using gstack skills: After install, skills like /qa, /ship, /review, /investigate,
and /browse are available. Use /browse for all web browsing.
Use ~/.claude/skills/gstack/... for gstack file paths (the global path).
