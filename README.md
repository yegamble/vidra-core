# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)
[![Database Migrations](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml)

A high-performance, PeerTube-compatible backend implementation in Go with P2P distribution, live streaming, plugin system, and multi-protocol federation (ActivityPub + ATProto).

## IMPORTANT: Code Quality and Validation

**All code changes must pass validation checks before use.** This project uses automated validation to ensure code quality, security, and reliability.

**Quick Validation**: `make validate-all`

See [VALIDATION_REQUIRED.md](docs/development/VALIDATION_REQUIRED.md) for complete requirements, especially if you're using Claude AI or other AI assistants.

## 📊 Project Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 516 | Total Go files (284 source, 232 test) |
| **Test Files** | 232 | Test files across unit, integration, and E2E suites |
| **Lines of Code** | 185,654 | 76,034 source + 109,620 test code |
| **Database Migrations** | 61 | Goose SQL migrations |
| **API Endpoints** | ~184 | RESTful + WebSocket + Federation (OpenAPI-documented) |
| **Coverage** | 48.7% | Full-repo short baseline (2026-02-12, up from 23.8%) |
| **Security Tests** | 50+ | Including SSRF, virus scanning, auth |
| **Automated Tests** | 2,139 | `func Test*` count across `*_test.go` files |

## Features

Status legend:
- `✅` Implemented and available
- `🧪` Implemented but beta/experimental
- `🛣️` Planned roadmap work

### Implemented Features (`✅`)

#### Core Video Platform
- **PeerTube API Compatibility** - Channels, subscriptions, comments, ratings, playlists, and captions
- **Video Import System** - Import from external platforms via `yt-dlp` integration
- **Transcoding + HLS** - FFmpeg processing with adaptive streaming outputs
- **User Messaging** - Direct messaging with optional end-to-end encryption (E2EE)
- **Notifications System** - Real-time notifications with automatic triggers and delivery controls

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
- **Video Redundancy** - Cross-instance replication and sync workflows

#### Security, Auth, and Extensibility
- **Two-Factor Authentication (2FA)** - TOTP and backup codes
- **OAuth2 with PKCE** - Authorization code and secure token flows
- **Content Moderation** - Abuse reporting and blocklist moderation tooling
- **Rate Limiting + Virus Scanning** - Endpoint limits and ClamAV-backed scanning pipeline
- **Plugin System** - Hook-based plugin architecture and plugin upload APIs
- **Observability** - Structured logging, metrics, and health endpoints

### Implemented Beta Features (`🧪`)

- **ATProto Integration (BETA)** - Optional Bluesky syndication and interoperability workflows

### Planned / In-Progress Roadmap (`🛣️`)

- **ATProto Stabilization** - Move ATProto integration from beta to stable production profile
- **Codec Matrix Expansion** - Additional codec and container expansion beyond current baseline defaults
- **Payments Integration** - IOTA-based payments features
- **Advanced Analytics Enhancements** - Additional analytics/reporting depth
- **Kubernetes + Blue/Green Hardening** - Expanded production deployment automation

See the detailed status section in [Project Status](#project-status) and planning docs in [Project Management](docs/project-management/README.md).

## Quick Start

### Development

**Prerequisites:**
To run integration tests that pull Docker images, you must be authenticated with Docker Hub to avoid rate limits.
```bash
docker login
```

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Copy environment template
cp .env.example .env

# Run with Docker Compose
# Note: Ensure you are authenticated with Docker Hub to avoid rate limits
docker compose up --build

# Or run locally
make deps

# Install Goose migration tool
go install github.com/pressly/goose/v3/cmd/goose@latest

# Apply database migrations
make migrate-up    # Apply all pending migrations
# or manually:
# goose -dir migrations postgres "$DATABASE_URL" up

make run
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

- **[OpenAPI Specifications](api/README.md)** - Comprehensive OpenAPI 3.0 API documentation (98%+ coverage)
  - [Authentication & 2FA](api/openapi_auth_2fa.yaml) - Two-factor authentication (TOTP + backup codes)
  - [Uploads & Encoding](api/openapi_uploads.yaml) - Chunked uploads, resume, encoding status
  - [Analytics & Views](api/openapi_analytics.yaml) - View tracking, analytics, trending
  - [Live Streaming](api/openapi_livestreaming.yaml) - RTMP ingest, HLS delivery, scheduling, analytics
  - [Video Imports](api/openapi_imports.yaml) - External URL imports
  - [Comments](api/openapi_comments.yaml), [Channels](api/openapi_channels.yaml), [Captions](api/openapi_captions.yaml)
  - [Ratings & Playlists](api/openapi_ratings_playlists.yaml), [Notifications](docs/openapi_notifications.yaml)
  - [Chat](api/openapi_chat.yaml), [Moderation](api/openapi_moderation.yaml), [Federation](api/openapi_federation.yaml)
  - [Plugins](api/openapi_plugins.yaml), [Redundancy](api/openapi_redundancy.yaml)
- **[API Examples](docs/API_EXAMPLES.md)** - API usage examples and patterns

### Architecture & Design

- **[Architecture Overview](docs/architecture.md)** - Clean architecture layers, data flow, and design patterns
- **[CLAUDE.md](docs/architecture/CLAUDE.md)** - Comprehensive architecture guide for AI-assisted development
- **[PeerTube Compatibility](docs/PEERTUBE_COMPAT.md)** - API compatibility matrix
- **[PeerTube Migration](docs/PEERTUBE_MIGRATION.md)** - High-level migration guide from PeerTube to Athena

### Deployment & Operations

- **[Deployment Guide](docs/deployment/README.md)** - Production deployment instructions
- **[Operations Runbook](docs/deployment/OPERATIONS_RUNBOOK.md)** - Incident response, backup procedures
- **[Monitoring Guide](docs/deployment/MONITORING.md)** - Prometheus & Grafana setup
- **[S3/Backblaze B2 Setup](docs/S3_MIGRATION_SETUP.md)** - Hybrid storage configuration and migration
- **[Virus Scanner Runbook](docs/VIRUS_SCANNER_RUNBOOK.md)** - ClamAV integration and troubleshooting

### Security

- **[Security Policy](docs/security/SECURITY.md)** - Security advisories including CVE-ATHENA-2025-001
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

### 🎯 88% COMPLETE - SECURITY HARDENING & STABILIZATION

**Current Focus**: Restoring test infrastructure, verifying build integrity, and applying security hardening measures (credential rotation, git history cleanup).

**Recent Achievements** (Last 24-48 hours):
- ✅ **Test Infrastructure Reliability** - Implemented "Fail Fast" logic to skip integration tests when Docker is unavailable
- ✅ **Migration from Atlas to Goose** - Eliminated authentication issues, simplified workflow
- ✅ **P1 Security Fix** - CVE-ATHENA-2025-001 (virus scanner retry logic bypass) resolved
- ✅ **Pre-commit Hooks** - Prevents credential leaks, enforces YAML linting
- ✅ **Code Quality** - Struct field alignment, formatting standardization

### Feature Completion by Category

| Category | Completion | Status | Notes |
|----------|-----------|--------|-------|
| **Core Platform** | 98% | ✅ Production Ready | Channels, subscriptions, comments, ratings, playlists, captions |
| **Security & Auth** | 90% | ⚠️ Action Required | 2FA, E2EE, OAuth2 complete; credential rotation pending |
| **Federation** | 93% | ✅ Ready | ActivityPub 100%, ATProto 75% (BETA) |
| **P2P Distribution** | 92% | ✅ Proven | IPFS/WebTorrent operational, HLS streaming experimental |
| **Live Streaming** | 100% | ✅ Complete | RTMP, HLS, chat, scheduling, analytics, VOD conversion |
| **Video Import** | 100% | ✅ Complete | 1000+ platforms via yt-dlp |
| **Analytics** | 96% | ✅ Complete | Video analytics, retention curves, channel stats |
| **Plugin System** | 94% | ✅ Complete | Hook architecture, security, marketplace |
| **Operational Readiness** | 87% | ⚠️ K8s Prep Needed | Docker ready, monitoring ready, K8s configs needed |

**Overall: 88% Complete** (up from 85%)

### Production Readiness Assessment

**✅ Phase 1 Launch Ready** (After credential rotation, 1-2 weeks):
- Core video platform fully functional
- Security hardened (P1 vulnerability fixed)
- Federation operational (ActivityPub)
- Testing baseline established (48.7% full-package short coverage, up from 23.8%)
- Docker deployment proven

**⚠️ Action Items Before Launch**:
1. Complete credential rotation (security advisory compliance)
2. Finalize Kubernetes deployment configs
3. Production environment setup and load testing
4. Performance optimization review

**📋 Phase 2 Enhancements** (Future):
- IOTA payments integration (strategic decision pending)
- ATProto enhancements (move from BETA to stable)
- Advanced analytics features

### Test Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 516 | Total Go files (284 source, 232 test) |
| **Test Files** | 232 | Test files across unit, integration, and E2E suites |
| **Lines of Code** | 185,654 | 76,034 source + 109,620 test code |
| **Database Migrations** | 61 | Goose SQL migrations |
| **API Endpoints** | ~184 | RESTful + WebSocket + Federation (OpenAPI-documented) |
| **Coverage** | 48.7% | Full-repo short baseline (2026-02-12, up from 23.8%) |
| **Security Tests** | 50+ | SSRF, virus scanning, auth, input validation |
| **Automated Tests** | 2,139 | `func Test*` count across `*_test.go` files |

See [Project Management Documentation](docs/project-management/README.md) and [Sprint History](docs/sprints/README.md) for detailed progress tracking.

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

See [SECURITY.md](docs/security/SECURITY.md) for security advisories including CVE-ATHENA-2025-001 and [Security Deployment Guide](docs/deployment/security.md) for detailed configuration.

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

- [GitHub Issues](https://github.com/yegamble/athena/issues)
- [PeerTube Project](https://github.com/Chocobozzz/PeerTube)
- [AT Protocol](https://atproto.com/)
