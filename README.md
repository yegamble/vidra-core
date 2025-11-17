# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)
[![Database Migrations](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/goose-migrate.yml)

A high-performance, feature-complete PeerTube backend implementation in Go with P2P distribution, live streaming, plugin system, and multi-protocol federation (ActivityPub + ATProto).

## 📊 Project Metrics

| Metric | Count | Description |
|--------|-------|-------------|
| **Go Files** | 426 | Total Go source files |
| **Test Files** | 156 | Comprehensive test coverage |
| **Lines of Code** | 136,000+ | Total lines including tests |
| **Database Migrations** | 58 | Goose migrations |
| **API Endpoints** | 100+ | RESTful + WebSocket |
| **Security Tests** | 50+ | Including P1 vulnerability fixes |

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
- **Real-time Chat** - WebSocket-based chat supporting 10,000+ concurrent connections with moderation
- **Stream Scheduling** - Advanced scheduling system with waiting rooms and automatic notifications
- **VOD Conversion** - Automatic conversion of live streams to on-demand videos with IPFS support

### P2P Distribution
- **WebTorrent P2P** - Browser-compatible P2P delivery with automatic torrent generation and seeding
- **DHT & PEX Support** - Trackerless operation with distributed hash table and peer exchange
- **Smart Seeding** - Multi-factor prioritization with automatic bandwidth management
- **Hybrid Distribution** - Configurable IPFS + Torrent hybrid distribution for maximum resilience
- **IPFS Streaming** - Optional IPFS gateway streaming for HLS content with automatic fallback to local delivery

### Federation
- **ActivityPub** - Full PeerTube-compatible federation with WebFinger, NodeInfo, and HTTP Signatures
- **ATProto Integration** - Optional Bluesky integration for cross-platform content syndication
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

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Copy environment template
cp .env.example .env

# Run with Docker Compose
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

```bash
make test           # Run all tests
make test-unit      # Unit tests only
make test-integration # Integration tests
make lint           # Run linters
```

## Documentation

- [OpenAPI Specifications](api/README.md) - Comprehensive OpenAPI 3.0 API documentation (98%+ coverage)
  - [Authentication & 2FA](api/openapi_auth_2fa.yaml) - Two-factor authentication (TOTP + backup codes)
  - [Uploads & Encoding](api/openapi_uploads.yaml) - Chunked uploads, resume, encoding status
  - [Analytics & Views](api/openapi_analytics.yaml) - View tracking, analytics, trending
  - [Live Streaming](api/openapi_livestreaming.yaml) - RTMP ingest, HLS delivery
  - [Video Imports](api/openapi_imports.yaml) - External URL imports
  - [Comments](api/openapi_comments.yaml), [Channels](api/openapi_channels.yaml), [Captions](api/openapi_captions.yaml)
  - [Ratings & Playlists](api/openapi_ratings_playlists.yaml), [Notifications](docs/openapi_notifications.yaml)
  - [Chat](api/openapi_chat.yaml), [Moderation](api/openapi_moderation.yaml), [Federation](api/openapi_federation.yaml)
  - [Plugins](api/openapi_plugins.yaml), [Redundancy](api/openapi_redundancy.yaml)
- [Architecture Overview](docs/architecture.md) - Clean architecture layers, data flow, and design patterns
- [API Examples](docs/API_EXAMPLES.md) - API usage examples and patterns
- [Deployment Guide](docs/deployment/README.md) - Production deployment instructions
- [S3/Backblaze B2 Setup](docs/S3_MIGRATION_SETUP.md) - Hybrid storage configuration and migration
- [PeerTube Compatibility](docs/PEERTUBE_COMPAT.md) - API compatibility matrix
- [OAuth2 Guide](docs/OAUTH2.md) - Authentication and authorization setup
- [Notifications API](docs/NOTIFICATIONS_API.md) - Real-time notification system
- [Email Verification](docs/EMAIL_VERIFICATION_API.md) - Email verification flow

### For Claude AI Contributors

- [Claude Architecture Guide](docs/claude/architecture.md) - System layout for AI assistance
- [Claude Contributing Guide](docs/claude/contributing.md) - AI workflow guidelines
- [Claude Operations Runbook](docs/claude/runbooks.md) - Command snippets and procedures

## Project Status

### 🎉 100% COMPLETE - ALL FEATURES DELIVERED

| Category | Features | Status |
|----------|----------|--------|
| **Core Platform** | Channels, Subscriptions, Comments, Ratings, Playlists, Captions | ✅ Complete |
| **Video Import** | 1000+ platforms via yt-dlp | ✅ Complete |
| **Transcoding** | Multi-codec (H.264, VP9, AV1), HLS streaming | ✅ Complete |
| **Live Streaming** | RTMP server, HLS transcoding, Real-time chat, Scheduling | ✅ Complete |
| **P2P Distribution** | WebTorrent, DHT, PEX, Smart seeding | ✅ Complete |
| **Federation** | ActivityPub (PeerTube compatible), ATProto (Bluesky) | ✅ Complete |
| **Analytics** | Video analytics, Retention curves, Channel stats | ✅ Complete |
| **Plugin System** | Hook architecture, Security, Marketplace API | ✅ Complete |
| **Video Redundancy** | Cross-instance replication, Health monitoring | ✅ Complete |
| **Security & Auth** | Two-Factor Authentication (2FA), E2EE Messaging, OAuth2 | ✅ Complete |
| **Storage** | Hybrid storage (Local/IPFS/S3), Backblaze B2, DigitalOcean Spaces | ✅ Complete |

**Total Progress:** 14/14 sprints complete (100%)
**Lines of Code:** ~42,886 (production + tests)
**Automated Tests:** 719+ passing
**Code Coverage:** >85%

See [Sprint Documentation](docs/sprints/README.md) for detailed sprint history and completion documentation.

## Configuration

Configuration is managed through environment variables. See [.env.example](.env.example) for all available options.

Key configuration areas:
- **Database**: PostgreSQL with connection pooling
- **Cache**: Redis for sessions and rate limiting
- **Storage**: Local, IPFS, or S3-compatible backends
- **Federation**: ATProto and Bluesky integration settings
- **Security**: JWT, rate limiting, CORS configuration
- **Virus Scanning**: ClamAV integration with configurable fallback modes

### Critical Security Configuration

For production deployments, ensure virus scanning is properly configured:

```bash
# ClamAV virus scanning (REQUIRED for production)
CLAMAV_ADDRESS=clamav:3310              # ClamAV daemon address
CLAMAV_FALLBACK_MODE=strict             # MUST be 'strict' in production
CLAMAV_TIMEOUT=300                      # 5-minute scan timeout
CLAMAV_MAX_RETRIES=3                    # Connection retry attempts
QUARANTINE_DIR=/var/quarantine          # Isolated quarantine directory
CLAMAV_AUTO_QUARANTINE=true             # Auto-quarantine infected files
```

See [SECURITY.md](SECURITY.md) for security advisories and [Security Deployment Guide](docs/deployment/security.md) for detailed configuration.

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
