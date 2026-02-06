# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)
[![Database Migrations](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml)

A high-performance, feature-complete PeerTube backend implementation in Go with P2P distribution, live streaming, plugin system, and multi-protocol federation (ActivityPub + ATProto).

## IMPORTANT: Code Quality and Validation

**All code changes must pass validation checks before use.** This project uses automated validation to ensure code quality, security, and reliability.

**Quick Validation**: `make validate-all`

See [VALIDATION_REQUIRED.md](VALIDATION_REQUIRED.md) for complete requirements, especially if you're using Claude AI or other AI assistants.

## 📊 Project Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 467 | Total Go source files |
| **Test Files** | 179 | Comprehensive test coverage |
| **Lines of Code** | 151,000+ | ~75K source + ~76K test code |
| **Database Migrations** | 61 | Goose SQL migrations |
| **API Endpoints** | 123+ | RESTful + WebSocket + Federation |
| **Security Tests** | 50+ | Including SSRF, virus scanning, auth |

## Features

### Core Video Platform
- **PeerTube API Compatibility** - Full compatibility with channels, subscriptions, comments, ratings, playlists, and captions
- **Video Import System** - Import from 1000+ platforms (YouTube, Vimeo, etc.) via yt-dlp integration
- **Advanced Transcoding** - Multi-codec support (H.264, VP9, AV1) with 30-50% bandwidth savings
- **HLS Adaptive Streaming** - Multi-resolution adaptive bitrate streaming with automatic quality selection
- **User Messaging** - Direct messaging with optional end-to-end encryption (E2EE) support
- **Notifications System** - Real-time notifications with automatic triggers and flexible delivery

### Live Streaming
- **RTMP Server** - Professional RTMP ingestion compatible with OBS, Streamlabs, and other streaming software
- **Real-time Chat** - WebSocket-based chat supporting 10,000+ concurrent connections with role-based moderation
- **Stream Scheduling** - Advanced scheduling system with waiting rooms, automated status transitions, and subscriber notifications
- **Stream Analytics** - Real-time metrics with 30-second intervals, session tracking, peak viewer counts, and engagement rates
- **VOD Conversion** - Automatic conversion of live streams to on-demand videos with IPFS support

### P2P Distribution
- **WebTorrent P2P** - Browser-compatible P2P delivery with automatic torrent generation and seeding
- **DHT & PEX Support** - Trackerless operation with distributed hash table and peer exchange
- **Smart Seeding** - Multi-factor prioritization with automatic bandwidth management
- **Hybrid Distribution** - Configurable IPFS + Torrent hybrid distribution for maximum resilience
- **IPFS Streaming** - Optional IPFS gateway streaming for HLS content with automatic fallback to local delivery

### Federation
- **ActivityPub** - Full PeerTube-compatible federation with WebFinger, NodeInfo, and HTTP Signatures
- **ATProto Integration (BETA)** - Optional Bluesky integration for cross-platform content syndication
- **Video Redundancy** - Cross-instance video replication with automatic sync and health monitoring

### Analytics & Monitoring
- **Video Analytics** - Comprehensive analytics with view tracking, retention curves, and engagement metrics
- **Real-time Metrics** - Active viewer tracking with 30-second heartbeat intervals
- **Channel Analytics** - Aggregated channel-level statistics and daily reporting

### Extensibility
- **Plugin System** - Extensible hook-based plugin architecture with 30+ event types
- **Security** - Ed25519 signature verification, permission system with 17 permission types
- **Plugin Marketplace** - Upload API with ZIP validation and automatic installation

### Security & Authentication
- **Two-Factor Authentication (2FA)** - TOTP-based 2FA with authenticator app support (RFC 6238)
- **Backup Codes** - 10 one-time recovery codes for account recovery
- **OAuth2 with PKCE** - Secure authorization with Proof Key for Code Exchange
- **End-to-End Encrypted Messaging** - Client-side encryption with user-managed keys
- **Content Moderation** - Abuse reporting, user/instance blocklists, and automated filtering
- **Rate Limiting** - Per-endpoint rate limiting with sliding window algorithm
- **Virus Scanning** - Mandatory ClamAV scanning for all uploads with quarantine and audit logging

### Production Ready
- **High Performance** - Built with Go for maximum concurrency and efficient resource usage
- **Hybrid Storage** - Multi-tier storage (local/IPFS/S3-compatible) with automatic promotion/demotion
- **S3-Compatible Storage** - Support for AWS S3, Backblaze B2, DigitalOcean Spaces
- **Comprehensive Testing** - 719+ automated tests with >85% code coverage
- **Observability** - Structured logging, metrics, and health monitoring

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

### Deployment & Operations

- **[Deployment Guide](docs/deployment/README.md)** - Production deployment instructions
- **[Operations Runbook](docs/deployment/OPERATIONS_RUNBOOK.md)** - Monitoring, incident response, backup procedures
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
- **[Sprint Documentation](docs/project-management/sprints/README.md)** - Sprint history and completion reports
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

### 🎯 88% COMPLETE - STABILIZATION PHASE

**Current Focus**: Restoring test infrastructure and verifying build integrity.

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
- Testing comprehensive (85%+ coverage)
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
| **Go Files** | 467 | Total Go source files (288 non-test) |
| **Test Files** | 179 | 38% test-to-code ratio |
| **Lines of Code** | 151,000+ | ~75K source + ~76K test code |
| **Database Migrations** | 61 | Goose SQL migrations |
| **API Endpoints** | 123+ | RESTful + WebSocket + Federation |
| **Test Coverage** | 85%+ | Average across all packages |
| **Security Tests** | 50+ | SSRF, virus scanning, auth, input validation |
| **Automated Tests** | 750+ | All passing in CI |

See [Project Management Documentation](docs/project-management/README.md) and [Sprint History](docs/project-management/sprints/README.md) for detailed progress tracking.

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
