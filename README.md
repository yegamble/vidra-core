# Vidra Core - PeerTube Backend in Go

[![Test](https://github.com/yegamble/vidra-core/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/vidra-core/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/vidra-core/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/vidra-core/actions/workflows/openapi-ci.yml)

A high-performance, PeerTube-compatible backend implementation in Go with P2P distribution, live streaming, plugin system, and multi-protocol federation (ActivityPub + ATProto).

## IMPORTANT: Code Quality and Validation

**All code changes must pass validation checks before use.** This project uses automated validation to ensure code quality, security, and reliability.

**Quick Validation**: `make validate-all`

See [VALIDATION_REQUIRED.md](docs/development/VALIDATION_REQUIRED.md) for complete requirements, especially if you're using Claude AI or other AI assistants.

## 📊 Project Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 855 | Total Go files (428 non-test) |
| **Test Files** | 456 | Test files across unit, integration, and E2E suites |
| **Lines of Code** | 287,316+ | ~94,227 source + ~193,089 test code |
| **Database Migrations** | 85 | Goose SQL migrations |
| **OpenAPI Files** | 39 | 39 specs in `api/` |
| **Security Tests** | 50+ | Including SSRF, virus scanning, auth |
| **Automated Tests** | 4,879 | `func Test*` count across `*_test.go` files |

## Features

Status legend:

- `✅` Implemented and available
- `🧪` Implemented but beta/experimental
- `🛣️` Planned roadmap work

### Implemented Features (`✅`)

#### Core Video Platform

- **PeerTube API Compatibility** - Channels, subscriptions, comments, ratings, playlists, and captions
- **Video Import System** - Import from external platforms via `yt-dlp` integration
- **Transcoding + HLS** - FFmpeg processing with H.264/VP9/AV1 multi-codec encoding, adaptive streaming, heartbeat-based stale job recovery on server restart
- **User Messaging** - Direct messaging with optional end-to-end encryption (E2EE)
- **Notifications System** - Real-time notifications with automatic triggers and delivery controls
- **Social Features** - Follow/unfollow, timelines, likes, shares, and activity feeds
- **Video Analytics** - Retention curves, view tracking, channel statistics, and data aggregation
- **Auto-Captioning** - AI-powered caption generation via Whisper integration
- **Video Studio Editing** - Server-side video editing with FFmpeg (cut, add intro/outro, watermark)
- **Migration ETL** - PeerTube dump → Vidra Core import pipeline with dry-run support

#### Live Streaming

- **RTMP Server** - RTMP ingestion compatible with OBS/Streamlabs and similar tooling
- **Real-time Chat** - WebSocket-based stream chat with moderation controls
- **Stream Scheduling** - Waiting rooms, scheduled lifecycle transitions, and subscriber notifications
- **Stream Analytics** - Near real-time stream metrics and session tracking
- **VOD Conversion** - Conversion of live streams to on-demand videos

#### P2P and Storage

- **WebTorrent + IPFS Hybrid Distribution** - P2P delivery and decentralized storage options
- **DHT and Peer Exchange** - Trackerless peer discovery capabilities
- **Smart Seeding Controls** - Automated seeding behavior and fallback routing
- **S3-Compatible Storage** - AWS S3, Backblaze B2, and DigitalOcean Spaces support

#### Federation

- **ActivityPub** - PeerTube-compatible federation with WebFinger, NodeInfo, and HTTP signatures
- **ATProto Integration** - Bluesky syndication via AT Protocol: video publishing, session management, blob uploads, `.well-known/atproto-did` endpoint
- **Video Redundancy** - Cross-instance replication and sync workflows

#### Security, Auth, and Extensibility

- **Two-Factor Authentication (2FA)** - TOTP and backup codes
- **OAuth2 with PKCE** - Authorization code and secure token flows
- **Content Moderation** - Abuse reporting and blocklist moderation tooling
- **Rate Limiting + Virus Scanning** - Endpoint limits and ClamAV-backed scanning pipeline
- **Plugin System** - Hook-based plugin architecture and plugin upload APIs
- **Observability** - OpenTelemetry tracing, Prometheus metrics, Grafana dashboards, structured logging, and health endpoints

#### Deployment

- **Kubernetes + Blue/Green** - Kustomize base/overlay manifests, blue/green deployment overlays, pre-switch validation jobs, smoke tests, Prometheus monitoring, Grafana dashboards

### Implemented Beta Features (`🧪`)

- **Bitcoin Payments (BTCPay Server)** - Invoice creation, payment tracking, and webhook callbacks via self-hosted BTCPay Server (feature-flagged via `ENABLE_BITCOIN`)

### Planned / In-Progress Roadmap (`🛣️`)
- **Advanced Analytics Enhancements** - Additional analytics/reporting depth

See the detailed status section in [Project Status](#project-status) and planning docs in [Project Management](docs/project-management/README.md).

## Quick Start

### Option 1: Fresh Install (Docker)

For new deployments on a server or clean machine:

```bash
# One-command install — clones repo, creates .env, starts services
curl -sSL https://raw.githubusercontent.com/yegamble/vidra/main/scripts/install.sh | bash

# Or inspect before running
curl -O https://raw.githubusercontent.com/yegamble/vidra/main/scripts/install.sh
less install.sh
bash install.sh
```

This installs Docker if needed, clones Vidra Core to `~/vidra`, starts all services, and opens the **Setup Wizard** at `http://localhost:8080/setup/welcome`.

### Option 2: From an Existing Clone

If you already have the repository:

```bash
cd vidra

# Run the install script (auto-detects repo root, skips clone)
bash scripts/install.sh

# Or do it manually:
cp .env.example .env
docker compose up --build
```

Then open `http://localhost:8080/setup/welcome` to configure.

### Option 3: Local Development (No Docker)

```bash
git clone https://github.com/yegamble/vidra-core.git
cd vidra
make deps

# Start Postgres and Redis (Docker or local installs)
docker compose up -d postgres redis

# Apply database migrations
go install github.com/pressly/goose/v3/cmd/goose@latest
make migrate-up

# Copy and edit environment
cp .env.example .env

# Run the server
make run
```

Without `DATABASE_URL`, `REDIS_URL`, or `JWT_SECRET` set, the server starts in **setup mode** automatically.

### Setup Wizard

On first run (or when required config is missing), Vidra Core serves a web-based wizard at `http://localhost:8080/setup/welcome`:

1. **Welcome** — System resource detection and deployment overview
2. **Quick Install** — Optional shortcut path for guided local setup
3. **Database** — Choose "Local Docker" (auto-provisioned) or "External Service" (provide your own Postgres URL). Tests connectivity.
4. **Services** — Configure Redis, IPFS, ClamAV, and Whisper with "Local Docker" / "External Service" toggles.
5. **Email** — Configure SMTP/mail delivery settings and test connectivity.
6. **Networking** — Set public URL, reverse-proxy, and ingress-related options.
7. **Storage** — Set the storage path and configure backup settings (local, S3, or SFTP).
8. **Security** — Auto-generated JWT secret (or provide your own, minimum 32 chars). Create the initial admin account.
9. **Review** — Summary of all settings with edit links per section.
10. **Complete** — Configuration saved. Restart the server to apply.

The wizard writes a `.env` file and sets `SETUP_COMPLETED=true`. After restart, Vidra Core boots normally.

#### CLI Alternative

```bash
# Interactive setup
./vidra-cli setup

# Non-interactive setup from template
./vidra-cli setup --from-env .env.example
```

### Docker Compose Profiles

```bash
# Minimal (Postgres + Redis + App) — works on any machine
docker compose up --build

# Full (adds IPFS, ClamAV, Whisper)
docker compose --profile full up --build

# Media processing only (ClamAV + Whisper, no IPFS)
docker compose --profile media up --build

# External services — skip local containers for services you provide
POSTGRES_MODE=external REDIS_MODE=external docker compose up --build
```

### Database Migrations

We use [Goose](https://github.com/pressly/goose) for database migrations (no authentication required, simple and reliable).

```bash
# Install Goose
go install github.com/pressly/goose/v3/cmd/goose@latest

# Migration commands
make migrate-up       # Apply all pending migrations
make migrate-down     # Rollback last migration
make migrate-status   # Show migration status
make migrate-version  # Show current version
make migrate-create NAME=add_feature  # Create new migration

# Manual Goose commands
goose -dir migrations postgres "$DATABASE_URL" up
goose -dir migrations postgres "$DATABASE_URL" status
```

### Testing

The test suite includes both unit tests and integration tests.

**Running Unit Tests (Fast):**
Unit tests that do not require external infrastructure (Postgres/Redis) can be run quickly:

```bash
make test-unit
```

**Running Integration Tests (Requires Docker):**
Integration tests require running Postgres and Redis instances. The test runner uses a "Fail Fast" mechanism: if infrastructure is not available, these tests will be skipped automatically to save time.

To run full integration tests:

1. Ensure you are logged into Docker Hub (to avoid rate limits):

   ```bash
   docker login
   ```

2. Start the test infrastructure:

   ```bash
   docker compose up -d postgres redis
   ```

3. Run the tests:

   ```bash
   make test
   ```

*Note: In some constrained environments (like certain CI sandboxes), Docker mount issues may prevent the database from starting. In this case, integration tests will skip, and only unit tests will execute.*

```bash
make test           # Run all tests (skips integration if no DB)
make test-unit      # Unit tests only
make lint           # Run linters
```

## Documentation

See the full documentation index at [docs/README.md](docs/README.md).

### API Documentation

- **OpenAPI Reference** - Modular OpenAPI 3.0 specs for major API areas; the live router in `internal/httpapi/routes.go` remains the source of truth for the newest routes
  - [API Index](api/README.md) - Current spec inventory, legacy-spec notes, and sync status
  - [Main API](api/openapi.yaml) - Core auth, video, messaging, notifications, categories, runners, and PeerTube-compatible/admin routes
  - [Authentication & 2FA](api/openapi_auth_2fa.yaml) - Two-factor authentication (TOTP + backup codes)
  - [Uploads & Encoding](api/openapi_uploads.yaml) - Chunked uploads, resume, encoding status
  - [Analytics & Views](api/openapi_analytics.yaml) - View tracking, analytics, trending
  - [Live Streaming](api/openapi_livestreaming.yaml) - RTMP ingest, HLS delivery, and session history
  - [Video Imports](api/openapi_imports.yaml) - External URL imports
  - [Comments](api/openapi_comments.yaml), [Channels](api/openapi_channels.yaml), [Captions](api/openapi_captions.yaml)
  - [Ratings & Playlists](api/openapi_ratings_playlists.yaml), [Social & ATProto](api/openapi_social.yaml)
  - [Chat](api/openapi_chat.yaml), [Moderation](api/openapi_moderation.yaml), [Federation](api/openapi_federation.yaml)
  - [Plugins](api/openapi_plugins.yaml), [Redundancy](api/openapi_redundancy.yaml), [Payments](api/openapi_payments.yaml)
  - [Video Studio](api/openapi_video_studio.yaml) - Video studio editing jobs
  - [Migration ETL](api/openapi_migration.yaml) - PeerTube migration import
  - [Admin](api/openapi_admin.yaml), [Backup](api/openapi_backup.yaml), [Extensions](api/openapi_extensions.yaml), [Watched Words](api/openapi_watched_words.yaml), [Auto Tags](api/openapi_auto_tags.yaml)
  - [Video Passwords](api/openapi_video_passwords.yaml), [Video Storyboards](api/openapi_video_storyboards.yaml), [Video Embed Privacy](api/openapi_video_embed_privacy.yaml), [Video Files](api/openapi_video_files.yaml)
  - [Channel Sync](api/openapi_channel_sync.yaml), [Player Settings](api/openapi_player_settings.yaml), [Server Debug](api/openapi_server_debug.yaml), [User Archives](api/openapi_user_archives.yaml)
  - [Compat Aliases](api/openapi_compat_aliases.yaml), [Static Files](api/openapi_static.yaml), [Messaging](api/openapi_messaging.yaml), [Runners](api/openapi_runners.yaml), [Notifications](api/openapi_notifications.yaml)
  - Notifications are documented in [Main API](api/openapi.yaml); [Legacy Notifications Spec](docs/openapi_notifications.yaml) is retained for compatibility.
- **[API Examples](docs/API_EXAMPLES.md)** - API usage examples and patterns
- **[API Contract Policy](docs/API_CONTRACT_POLICY.md)** - API change governance and CI enforcement

### Architecture & Design

- **[Architecture Overview](docs/architecture.md)** - Clean architecture layers, data flow, and design patterns
- **[CLAUDE.md](docs/architecture/CLAUDE.md)** - Comprehensive architecture guide for AI-assisted development
- **[PeerTube Compatibility](docs/PEERTUBE_COMPAT.md)** - API compatibility matrix
- **[PeerTube Migration](docs/PEERTUBE_MIGRATION.md)** - High-level migration guide from PeerTube to Vidra Core

### Deployment & Operations

- **[Deployment Guide](docs/deployment/README.md)** - Production deployment instructions
- **[Operations Runbook](docs/operations/RUNBOOK.md)** - Incident response, backup procedures
- **[Monitoring Guide](docs/operations/MONITORING.md)** - Prometheus & Grafana setup
- **[Performance Tuning](docs/operations/PERFORMANCE.md)** - Optimization and scaling guide
- **[S3/Backblaze B2 Setup](docs/S3_MIGRATION_SETUP.md)** - Hybrid storage configuration and migration
- **[Virus Scanner Runbook](docs/VIRUS_SCANNER_RUNBOOK.md)** - ClamAV integration and troubleshooting

### Security

- **[Security Policy](docs/security/SECURITY.md)** - Security advisories including CVE-VIDRA-2025-001
- **[Security Advisory](docs/security/SECURITY_ADVISORY.md)** - Credential exposure mitigation
- **[End-to-End Encryption](docs/security/SECURITY_E2EE.md)** - E2EE messaging implementation
- **[IPFS Security](docs/security/IPFS_SECURITY_IMPLEMENTATION.md)** - IPFS security hardening
- **[Penetration Testing Report](docs/security/SECURITY_PENTEST_REPORT.md)** - Security assessment results

### Federation

- **[Federation Overview](docs/federation/README.md)** - Federation protocols and interoperability
- **[ATProto Setup Guide](docs/federation/ATPROTO_SETUP.md)** - Bluesky integration (BETA)
- **[ActivityPub Test Coverage](docs/federation/ACTIVITYPUB_TEST_COVERAGE.md)** - Federation testing

### Development

- **[Testing Strategy](docs/development/TESTING_STRATEGY.md)** - Comprehensive testing approach
- **[Claude Code Hooks](docs/development/CLAUDE_HOOKS.md)** - Automated code quality assurance
- **[Migration Guide](docs/MIGRATION_TO_GOOSE.md)** - Atlas to Goose migration
- **[Code Quality Review](docs/development/CODE_QUALITY_REVIEW.md)** - Quality standards and metrics

### Project Management

- **[Project Status](docs/project-management/README.md)** - Current status and roadmap
- **[Sprint Documentation](docs/sprints/README.md)** - Sprint history and completion reports
- **[PM Assessment](docs/project-management/PM_COMPREHENSIVE_ASSESSMENT.md)** - Comprehensive project assessment

### Features

- **[Notifications API](docs/NOTIFICATIONS_API.md)** - Real-time notification system
- **[Email Verification](docs/EMAIL_VERIFICATION_API.md)** - Email verification flow
- **[IPFS Streaming](docs/IPFS_STREAMING.md)** - IPFS gateway streaming
- **[OAuth2 Guide](docs/OAUTH2.md)** - Authentication and authorization setup

### For Claude AI Contributors

- **[Claude Architecture Guide](docs/claude/architecture.md)** - System layout for AI assistance
- **[Claude Contributing Guide](docs/claude/contributing.md)** - AI workflow guidelines
- **[Claude Operations Runbook](docs/claude/runbooks.md)** - Command snippets and procedures

## Project Status

### 🎯 Feature Parity Complete - Quality Programme Complete

**Current Phase**: Quality Programme (Sprints 15-20) - **100% COMPLETE** - Stabilization, coverage uplift, and release hardening.

**Sprints 1-14**: PeerTube Feature Parity - **100% COMPLETE**

- 94,227+ lines of production code
- 4,879 automated tests
- 85 database migrations
- 39 OpenAPI specification files
- Full ActivityPub federation
- Video Studio Editing with FFmpeg
- Migration ETL pipeline for PeerTube imports

**Sprint 20** (Complete): Release Hardening

- Full regression and security validation; see current CI and audit reports for the latest pass counts
- Coverage sign-off (all 30 per-package thresholds met)
- CHANGELOG.md and maintenance plan finalized
- Final Release Checklist completed (12/14 items verified)

### Feature Completion by Category

| Category | Completion | Status | Notes |
|----------|-----------|--------|-------|
| **Core Platform** | 100% | ✅ Complete | Channels, subscriptions, comments, ratings, playlists, captions |
| **Security & Auth** | 100% | ✅ Complete | 2FA, E2EE, OAuth2, CORS origin validation, privilege escalation fix |
| **Federation** | 100% | ✅ Complete | ActivityPub 100%, ATProto 100% |
| **P2P Distribution** | 100% | ✅ Complete | IPFS/WebTorrent, DHT, PEX, smart seeding |
| **Live Streaming** | 100% | ✅ Complete | RTMP, HLS, chat, scheduling, analytics, VOD conversion |
| **Video Import** | 100% | ✅ Complete | 1000+ platforms via yt-dlp |
| **Analytics** | 100% | ✅ Complete | Video analytics, retention curves, channel stats |
| **Plugin System** | 100% | ✅ Complete | Hook architecture, Ed25519 signatures, 17 permissions |
| **Video Redundancy** | 100% | ✅ Complete | Cross-instance replication, health monitoring |
| **Quality Programme** | 100% | ✅ Complete | API contract, coverage uplift, docs accuracy, release hardening |
| **Video Studio** | 100% | ✅ Complete | Server-side editing pipeline (cut, intro, outro, watermark) |
| **Migration ETL** | 100% | ✅ Complete | PeerTube dump import with dry-run support |

### Quality Programme (Sprints 15-20)

| Sprint | Focus | Status |
|--------|-------|--------|
| **Sprint 15** | Stabilize mainline; integrate PR queue | ✅ Complete |
| **Sprint 16** | API contract reproducibility | ✅ Complete |
| **Sprint 17** | Core services 80%+ coverage | ✅ Complete |
| **Sprint 18** | Handlers/repos 90%+ coverage | ✅ Complete |
| **Sprint 19** | Documentation accuracy pass | ✅ Complete |
| **Sprint 20** | Release hardening | ✅ Complete |

See [Quality Programme](docs/sprints/QUALITY_PROGRAMME.md) for full details.

### Test Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 855 | Total Go files (428 non-test) |
| **Test Files** | 456 | Test files across unit, integration, and E2E suites |
| **Lines of Code** | 287,316+ | ~94,227 source + ~193,089 test code |
| **Database Migrations** | 85 | Goose SQL migrations |
| **OpenAPI Files** | 39 | 39 specs in `api/` |
| **Security Tests** | 50+ | SSRF, virus scanning, auth, input validation |
| **Automated Tests** | 4,879 | `func Test*` count across `*_test.go` files |

See [Project Management Documentation](docs/project-management/README.md), [Sprint History](docs/sprints/README.md), and [Quality Programme](docs/sprints/QUALITY_PROGRAMME.md) for detailed progress tracking.

## Configuration

Configuration is managed through environment variables. See [.env.example](.env.example) for all available options.

Key configuration areas:

- **Database**: PostgreSQL with connection pooling and extensions (pg_trgm, uuid-ossp)
- **Cache**: Redis for sessions, rate limiting, and video status
- **Storage**: Local, IPFS, or S3-compatible backends (AWS, Backblaze B2, DigitalOcean Spaces)
- **Federation**: ActivityPub and ATProto/Bluesky integration settings
- **Security**: JWT, OAuth2, 2FA (TOTP), rate limiting, CORS, SSRF protection
- **Virus Scanning**: ClamAV integration with configurable fallback modes
- **Transcoding**: H.264, VP9, AV1 codec options with FFmpeg
- **Live Streaming**: RTMP ingestion with HLS output
- **Caption Generation**: Optional Whisper integration for auto-captions

### Critical Security Configuration

For production deployments, ensure these security features are properly configured:

```bash
# ClamAV virus scanning (REQUIRED for production)
CLAMAV_ADDRESS=clamav:3310              # ClamAV daemon address
CLAMAV_FALLBACK_MODE=strict             # MUST be 'strict' in production
CLAMAV_TIMEOUT=300                      # 5-minute scan timeout
CLAMAV_MAX_RETRIES=3                    # Connection retry attempts
QUARANTINE_DIR=/var/quarantine          # Isolated quarantine directory
CLAMAV_AUTO_QUARANTINE=true             # Auto-quarantine infected files

# ActivityPub key encryption (REQUIRED for federation)
ACTIVITYPUB_KEY_ENCRYPTION_KEY=         # 32-byte base64 key for encrypting AP private keys
```

**Built-in Security Features:**

- **SSRF Protection**: Blocks private IPs (RFC1918), metadata services (169.254.169.254), obfuscated IPs (octal/hex/integer), DNS rebinding
- **File Type Blocking**: 40+ dangerous file types blocked, polyglot detection, ZIP bomb protection
- **HTML Sanitization**: Multiple policies for XSS prevention using bluemonday
- **Input Validation**: UUID validation, string sanitization, file size limits (5GB video, 50MB image)
- **Cryptographic Security**: AES-256-GCM encryption, Argon2id key derivation, HSM interface support

See [SECURITY.md](docs/security/SECURITY.md) for security advisories including CVE-VIDRA-2025-001 and [Security Deployment Guide](docs/deployment/security.md) for detailed configuration.

## Contributing

We welcome contributions! Please see our documentation for:

- [Contributing Guide](CONTRIBUTING.md) - Setup, workflow, and PR requirements
- [Code of Conduct](CODE_OF_CONDUCT.md) - Community participation expectations
- [Architecture Guidelines](docs/architecture.md) - System design and patterns
- [Claude Contributing Guide](docs/claude/contributing.md) - AI-assisted development workflow
- Code style enforced via `golangci-lint`
- Testing requirements and CI/CD in [test workflow](.github/workflows/test.yml)

## License

[MIT License](LICENSE)

## Links

- [GitHub Issues](https://github.com/yegamble/vidra-core/issues)
- [PeerTube Project](https://github.com/Chocobozzz/PeerTube)
- [AT Protocol](https://atproto.com/)
