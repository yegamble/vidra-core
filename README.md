# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)

A high-performance PeerTube backend implementation in Go with decentralized storage support and ATProto federation capabilities.

## Features

- **PeerTube API Compatibility** - Core PeerTube features including channels, subscriptions, comments, ratings, playlists, and captions
- **ATProto Federation** - Cross-platform content federation via AT Protocol with Bluesky integration
- **High Performance** - Built with Go for maximum concurrency and efficient resource usage
- **Decentralized Storage** - IPFS integration with hybrid storage tiers (hot/warm/cold)
- **Production Ready** - Comprehensive security, observability, and deployment support

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
make migrate
make run
```

### Testing

```bash
make test           # Run all tests
make test-unit      # Unit tests only
make test-integration # Integration tests
make lint           # Run linters
```

## Documentation

- [Architecture Overview](docs/architecture/README.md) - System design and package structure
- [API Documentation](docs/api/README.md) - OpenAPI spec and endpoint reference
- [Deployment Guide](docs/deployment/README.md) - Production deployment instructions
- [Development Guide](docs/DEVELOPMENT.md) - Contributing and local setup
- [PeerTube Compatibility](docs/PEERTUBE_COMPAT.md) - API compatibility matrix
- [Federation Guide](docs/federation/README.md) - ATProto federation setup

### For Claude AI Contributors

- [Claude Architecture Guide](docs/claude/architecture.md) - System layout for AI assistance
- [Claude Contributing Guide](docs/claude/contributing.md) - AI workflow guidelines
- [Claude Operations Runbook](docs/claude/runbooks.md) - Command snippets and procedures

## Project Status

### ✅ Completed Features
- Full PeerTube API compatibility (Sprints A-G)
- ATProto federation foundations (Sprint H)
- Federation social features (Sprint J)
- Federation hardening and reliability (Sprint K)

### 🚧 In Progress
- ATProto video federation (Sprint I)

See [Sprint History](docs/sprints.md) for detailed progress tracking.

## Configuration

Configuration is managed through environment variables. See [.env.example](.env.example) for all available options.

Key configuration areas:
- **Database**: PostgreSQL with connection pooling
- **Cache**: Redis for sessions and rate limiting
- **Storage**: Local, IPFS, or S3-compatible backends
- **Federation**: ATProto and Bluesky integration settings
- **Security**: JWT, rate limiting, CORS configuration

## Contributing

We welcome contributions! Please see our [Development Guide](docs/DEVELOPMENT.md) for:
- Code style and standards
- Testing requirements
- Pull request process
- Architecture guidelines

## License

[MIT License](LICENSE)

## Links

- [GitHub Issues](https://github.com/yegamble/athena/issues)
- [PeerTube Project](https://github.com/Chocobozzz/PeerTube)
- [AT Protocol](https://atproto.com/)