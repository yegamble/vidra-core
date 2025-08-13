# Athena - PeerTube Backend in Go

[![Test](https://github.com/yegamble/athena/actions/workflows/test.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/test.yml)
[![OpenAPI CI](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml/badge.svg)](https://github.com/yegamble/athena/actions/workflows/openapi-ci.yml)

A high-performance PeerTube backend implementation in Go with decentralized storage support.

## Features

- 🚀 **High Performance** - Built with Go for maximum concurrency and speed
- 📝 **OpenAPI 3.0** - Complete API specification with automatic validation
- 🔐 **JWT Authentication** - HS256 access tokens with refresh rotation
- 🗄️ **PostgreSQL** - Robust database with full-text search capabilities
- ⚡ **Redis** - Fast caching and session management
- 🌐 **IPFS** - Decentralized storage support
- 🎥 **Video Processing** - FFmpeg integration for transcoding
- 🐳 **Docker Ready** - Full containerization with Docker Compose
- ✅ **CI/CD** - GitHub Actions with automated testing

## Quick Start

### Prerequisites

- Go 1.21+
- Docker & Docker Compose
- PostgreSQL 15+ (optional, if not using Docker)
- Redis 7+ (optional, if not using Docker)
- Node.js 18+ (for API documentation tools)

### One-Command Setup

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

### Manual Setup

#### 1. Clone and Configure

```bash
# Clone the repository
git clone https://github.com/yegamble/athena.git
cd athena

# Copy environment variables
cp .env.example .env
# Edit .env with your configuration
```

#### 2. Start Services with Docker

```bash
# Start all services (PostgreSQL, Redis, App)
make docker-up

# Or using docker-compose directly
docker-compose up -d
```

#### 3. Run Development Server

```bash
# Install dependencies
make deps

# Run development server with hot reload
make dev

# Or without hot reload
go run ./cmd/server
```

The API will be available at `http://localhost:8080`

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

### Videos (Protected)

- `GET /api/v1/videos` - List videos
- `GET /api/v1/videos/search` - Search videos
- `GET /api/v1/videos/{id}` - Get video details
- `POST /api/v1/videos` - Create video
- `PUT /api/v1/videos/{id}` - Update video
- `DELETE /api/v1/videos/{id}` - Delete video
- `POST /api/v1/videos/{id}/upload` - Upload video chunk
- `GET /api/v1/videos/{id}/stream` - Stream video (HLS)

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
```

## Docker Deployment

### Production Deployment

```bash
# Build and run with Docker Compose
docker-compose up -d

# View logs
docker-compose logs -f

# Stop services
docker-compose down
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

---

**Ready to get started?** Run `make setup` and start building!
### Auth & Sessions

- Access tokens are signed JWTs (HS256) containing `sub` (user ID), `iat`, and `exp`. Default access token TTL is 15 minutes.
- Refresh tokens are opaque UUIDs persisted in Postgres and rotated on each refresh. Old tokens are revoked.
- Sessions are stored in Redis; each session is keyed by the refresh token (`sess:<refresh-token> -> <userID>`) and indexed per user (`user:sessions:<userID>`). On login and refresh, the Redis session is created/rotated; on logout, all user sessions and refresh tokens are revoked.
- Required config: `JWT_SECRET`, `DATABASE_URL`, and `REDIS_URL`. Optional `SESSION_TIMEOUT` controls Redis session TTL (default 24h); refresh token TTL defaults to 7 days.
