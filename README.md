# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)
[![Deploy](https://github.com/yegamble/athena/actions/workflows/deploy.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/deploy.yml)

A high-performance PeerTube backend implementation in Go with decentralized storage support, **production-ready** with comprehensive monitoring, security, and deployment automation.

## 🚀 Production Ready Features

- **🔒 Security**: JWT authentication, rate limiting, SSL/TLS, security headers
- **📊 Monitoring**: Prometheus metrics, Grafana dashboards, structured logging
- **🔄 CI/CD**: Automated testing, building, and deployment pipelines
- **💾 Backup**: Automated database and file backups with S3 support
- **📈 Scaling**: Horizontal and vertical scaling capabilities
- **🛡️ Health Checks**: Comprehensive health monitoring and alerting
- **🔧 DevOps**: Production deployment scripts and documentation

## Features

- 🚀 **High Performance** - Built with Go for maximum concurrency and speed
- 📝 **OpenAPI 3.0** - Complete API specification with automatic validation
- 🔐 **JWT Authentication** - HS256 access tokens with refresh rotation
- 🗄️ **PostgreSQL** - Robust database with full-text search capabilities
- ⚡ **Redis** - Fast caching and session management
- 🌐 **IPFS** - Decentralized storage support
- 🎥 **Video Processing** - FFmpeg integration for transcoding
- 🐳 **Docker Ready** - Full containerization with Docker Compose
- ✅ **CI/CD** - GitHub Actions with automated testing and deployment
- 📊 **Monitoring** - Prometheus metrics and Grafana dashboards
- 🔒 **Security** - Production-grade security configurations
- 💾 **Backup** - Automated backup and recovery systems

## Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15+
- Redis 7+
- Node.js 18+ (for API documentation tools)

### Development Setup

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Run complete setup
make setup
```

This will:
1. Copy `.env.example` to `.env`
2. Install dependencies
3. Install development tools
4. Start Docker services
5. Run database migrations
6. Set up the development environment

### Production Deployment

For production deployment, see the comprehensive [Production Guide](docs/PRODUCTION.md).

```bash
# Quick production setup
make setup
# Edit .env with production values
make deploy-prod
```

## Development

### Available Make Commands

```bash
make help          # Show all available commands
make deps          # Download dependencies
make lint          # Run linting
make test          # Run tests with coverage
make test-local    # Run tests with local Docker services
make build         # Build binary
make docker        # Build Docker image
make docker-up     # Start Docker services
make docker-down   # Stop Docker services
make docker-logs   # View Docker logs
make docker-reset  # Reset Docker environment
make dev           # Run development server
make clean         # Clean build artifacts

# Production Commands
make deploy-prod   # Deploy to production
make deploy-staging # Deploy to staging
make backup        # Create backup
make backup-s3     # Backup with S3 upload
make monitor       # Start monitoring stack
make proxy         # Start Nginx reverse proxy
make health-check  # Run health checks
make update        # Update all images
make cleanup       # Clean up Docker resources
make security-scan # Run security scan
```

### Running Tests

```bash
# Run all tests
make test

# Run tests with local Docker services
make test-local

# Run tests in CI environment
make test-ci

# View coverage report
open coverage.html
```

### Integration Tests

Integration tests use a real Postgres (and ping Redis) via the helpers in `internal/testutil`. You can configure the test database through environment variables. The loader checks in this order:

- `TEST_DATABASE_URL`: Full Postgres URL used only for tests
- `DATABASE_URL`: Fallback if `TEST_DATABASE_URL` is not set
- Granular fallbacks (if neither URL is set):
  - `TEST_DB_HOST` (default: `localhost`)
  - `TEST_DB_PORT` (default: `5433`)
  - `TEST_DB_NAME` (default: `athena_test`)
  - `TEST_DB_USER` (default: `test_user`)
  - `TEST_DB_PASSWORD` (default: `test_password`)
  - `TEST_DB_SSLMODE` (default: `disable`)

Additionally, the test bootstrap attempts to load `.env.test` first, then `.env` if present, so you can commit a dedicated test configuration.

Examples:

```bash
# Use a single URL for test DB
export TEST_DATABASE_URL=postgres://user:pass@localhost:5432/athena_test?sslmode=disable

# Or use granular overrides
export TEST_DB_HOST=localhost
export TEST_DB_PORT=5432
export TEST_DB_NAME=athena_test
export TEST_DB_USER=postgres
export TEST_DB_PASSWORD=postgres

# If your Redis differs from the default test instance
export REDIS_URL=redis://localhost:6379/0

# Run only integration tests in httpapi package
go test ./internal/httpapi -run Integration

# Or run all tests
go test ./...
```

### API Documentation

The API is defined using OpenAPI 3.0 specification in `api/openapi.yaml`.

```bash
# Validate OpenAPI spec
make validate-openapi

# Serve API documentation
make serve-docs
# Opens at http://localhost:8081
```

## API Endpoints

### Authentication

- `POST /auth/register` - Register new user
- `POST /auth/login` - Login with email/password
- `POST /auth/refresh` - Refresh access token
- `POST /auth/logout` - Logout (requires auth)

### Health Checks

- `GET /health` - Basic health check
- `GET /ready` - Readiness check (DB, Redis, IPFS)
- `GET /metrics` - Prometheus metrics

### Videos

**Public Endpoints:**
- `GET /api/v1/videos` - List public videos (supports pagination, filtering, sorting)
- `GET /api/v1/videos/search` - Search videos with full-text search and filters
- `GET /api/v1/videos/{id}` - Get video details
- `GET /api/v1/videos/{id}/stream` - Stream video (HLS playlist, `quality` query param supports 240p-4320p, default 720p)
- `GET /api/v1/videos/qualities` - List supported quality labels and the default
  - Response body (wrapped):
    - `data.qualities`: array of strings (e.g., `["240p","360p","480p","720p","1080p","1440p","2160p","4320p"]`)
    - `data.default`: default quality string (e.g., `"720p"`)
  - Notes:
    - The default is also used when `quality` is omitted in `/stream`.
    - The set returned here reflects server-side support and validation.

#### Resolution Detection Logic (Encoding)
When queuing an encoding job after upload completes, the service determines the source resolution using the following rules:

- Prefer exact height from metadata when available: `source = DetectResolutionFromHeight(height)`.
- If height is missing but width is available, estimate height using aspect ratio:
  - Accepts aspect ratio formats: `16:9`, `9/16`, and numeric (e.g., `1.7778`).
  - Defaults to `16:9` if aspect ratio is missing or invalid.
  - Estimated height: `round(width / aspectRatio)` then `source = DetectResolutionFromHeight(estimatedHeight)`.
- Out-of-range heights clamp to nearest supported (<= 240 → 240p, >= 4320 → 4320p).
- Ties are resolved by preferring the lower resolution (e.g., exactly between 720p and 1080p picks 720p).

Examples:
- `{ height: 900 }` → closest to 720p vs 1080p; tie prefers lower → `720p`.
- `{ width: 1280, aspect_ratio: "16:9" }` → estimated height `≈ 720` → `720p`.
- `{ width: 1920, aspect_ratio: "16:9" }` → estimated height `≈ 1080` → `1080p`.
- `{ width: 1024, aspect_ratio: "4:3" }` → estimated height `≈ 768` → `720p` (closer to 720p than 1080p).
- `{ width: 1920 }` (no AR) → defaults to `16:9` → `1080p`.

Operational note: Debug logs for width/aspect estimation emit only when `LOG_LEVEL` is `debug` or `trace`.

**Protected Endpoints (Require Authentication):**
- `POST /api/v1/videos` - Create video metadata
- `PUT /api/v1/videos/{id}` - Update video (owner only)
- `DELETE /api/v1/videos/{id}` - Delete video (owner only)
- `POST /api/v1/videos/{id}/upload` - Upload video chunk
- `POST /api/v1/videos/{id}/complete` - Complete chunked upload

### Users

- `GET /api/v1/users/me` - Get current user (requires auth)
- `PUT /api/v1/users/me` - Update current user (requires auth)
- `GET /api/v1/users/{id}` - Get user profile
- `GET /api/v1/users/{id}/videos` - Get user's videos

## Architecture

```
/cmd/server            # Application entry point
/internal/
  ├── config/         # Configuration management
  ├── domain/         # Domain models and errors
  ├── generated/      # OpenAPI generated types
  ├── httpapi/        # HTTP handlers and routes
  ├── middleware/     # HTTP middleware (auth, CORS, rate limit)
  ├── repository/     # Database repositories
  ├── testutil/       # Test utilities
  └── usecase/        # Business logic interfaces
/api/                 # OpenAPI specifications
/migrations/          # Database migrations
/scripts/             # Deployment and maintenance scripts
/monitoring/          # Prometheus and Grafana configurations
/nginx/               # Nginx reverse proxy configuration
/docs/                # Documentation
```

## Docker Deployment

### Development Deployment

```bash
# Build and run with Docker Compose
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
```

### Production Deployment

```bash
# Deploy with production configuration
docker compose -f docker-compose.prod.yml up -d

# With monitoring stack
docker compose -f docker-compose.prod.yml --profile monitoring up -d

# With Nginx reverse proxy
docker compose -f docker-compose.prod.yml --profile proxy up -d

# View logs
docker compose -f docker-compose.prod.yml logs -f

# Stop services
docker compose -f docker-compose.prod.yml down
```

### Environment Variables

Key environment variables (see `.env.example` for full list):

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/athena
REDIS_URL=redis://localhost:6379/0
JWT_SECRET=your-secret-key
PORT=8080
```

## CI/CD

GitHub Actions workflows run automatically on push/PR:

1. **Test Workflow** - Runs tests, linting, and builds
2. **OpenAPI CI** - Validates API spec and generates docs
3. **Deploy Workflow** - Automated deployment to production/staging

### Running CI Locally

```bash
# Run CI test pipeline
make ci-test

# Run CI build pipeline
make ci-build
```

## Database

PostgreSQL with extensions:
- `uuid-ossp` - UUID generation
- `pg_trgm` - Trigram matching for full-text search
- `unaccent` - Accent-insensitive search
- `btree_gin` - GIN index support

### Migrations

```bash
# Run migrations (requires DATABASE_URL)
make migrate-up

# Run test migrations
make migrate-test
```

## Monitoring & Observability

### Prometheus Metrics

The application exposes metrics at `/metrics` endpoint:

```bash
# View metrics
curl http://localhost:8080/metrics
```

### Grafana Dashboards

Access Grafana at `http://localhost:3000`:
- Username: `admin`
- Password: Set via `GRAFANA_PASSWORD` environment variable

### Health Checks

```bash
# Application health
curl http://localhost:8080/health

# Readiness check
curl http://localhost:8080/ready

# Automated health checks
make health-check
```

## Backup & Recovery

### Automated Backups

```bash
# Create full backup
make backup

# Database only backup
make backup-db

# Backup with S3 upload
make backup-s3
```

### Backup Schedule

```bash
# Daily database backup
0 2 * * * make backup-db

# Weekly full backup
0 3 * * 0 make backup
```

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

### Development Guidelines

- Follow Go best practices and conventions
- Update OpenAPI spec for API changes
- Write tests for new features
- Run `make lint` before committing
- Update documentation as needed

## Troubleshooting

### Common Issues

**Database connection errors:**
```bash
# Check if PostgreSQL is running
docker-compose ps
# Check logs
docker-compose logs postgres
```

**Port already in use:**
```bash
# Change ports in docker-compose.yml or .env
# Or stop conflicting services
```

**Tests failing:**
```bash
# Ensure test database is running
docker-compose -f docker-compose.test.yml up -d
# Check test database logs
docker-compose -f docker-compose.test.yml logs
```

**Production issues:**
```bash
# Check health status
make health-check

# View application logs
make logs-app

# Restart services
make restart

# Check monitoring
make monitor
```

## License

MIT License - see [LICENSE](LICENSE) file for details

## Acknowledgments

- Inspired by [PeerTube](https://github.com/Chocobozzz/PeerTube)
- Built with [Chi Router](https://github.com/go-chi/chi)
- Uses [SQLX](https://github.com/jmoiron/sqlx) for database operations

## Support

For issues and questions:
- Open an issue on [GitHub](https://github.com/yegamble/athena/issues)
- Check existing issues before creating new ones
- Provide detailed information for bug reports
- For production support, see [Production Guide](docs/PRODUCTION.md)

---

**Ready to get started?** Run `make setup` and start building!

### Auth & Sessions

- Access tokens are signed JWTs (HS256) containing `sub` (user ID), `iat`, and `exp`. Default access token TTL is 15 minutes.
- Refresh tokens are opaque UUIDs persisted in Postgres and rotated on each refresh. Old tokens are revoked.
- Sessions are stored in Redis; each session is keyed by the refresh token (`sess:<refresh-token> -> <userID>`) and indexed per user (`user:sessions:<userID>`). On login and refresh, the Redis session is created/rotated; on logout, all user sessions and refresh tokens are revoked.
- Required config: `JWT_SECRET`, `DATABASE_URL`, and `REDIS_URL`. Optional `SESSION_TIMEOUT` controls Redis session TTL (default 24h); refresh token TTL defaults to 7 days.
