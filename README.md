# Athena PeerTube Backend

A high-performance PeerTube backend implementation in Go with OpenAPI specification and code generation.

## Features

- **OpenAPI 3.0 Specification** - Complete API documentation with validation
- **Code Generation** - Automatic Go code generation using oapi-codegen
- **Authentication** - JWT-based auth with login, register, refresh, and logout
- **Health Checks** - Comprehensive health and readiness endpoints
- **CI/CD Pipeline** - Automated testing and documentation generation

## Architecture

```
/api/                 # OpenAPI specifications
/internal/generated/  # Generated Go code from OpenAPI
/internal/httpapi/    # HTTP handlers implementing generated interfaces
/internal/domain/     # Domain models and business logic
/internal/config/     # Configuration management
/.github/workflows/   # CI/CD pipelines
```

## Quick Start

### Prerequisites

- Go 1.21+
- PostgreSQL 15+
- Redis 7+

### Development Setup

```bash
# Install development tools
make install-tools

# Download dependencies
make deps

# Generate code from OpenAPI spec
make generate

# Run tests
make test

# Start development server with live reload
make dev
```

### API Documentation

The API is defined using OpenAPI 3.0 specification in `api/openapi.yaml`.

**View Documentation:**
```bash
# Serve interactive docs locally
make serve-docs
# Opens at http://localhost:8081
```

**Generate Static Documentation:**
```bash
# Generates HTML documentation
make generate-docs
```

## OpenAPI Integration

### Code Generation

The project uses [oapi-codegen](https://github.com/oapi-codegen/oapi-codegen) to generate:
- Go types from OpenAPI schemas
- Chi server interfaces and stubs
- HTTP client code

```bash
# Regenerate code after OpenAPI changes
make generate
```

### Generated Files

- `internal/generated/api.go` - Complete generated code
  - Type definitions for all request/response models
  - `ServerInterface` for implementing handlers
  - Chi router integration
  - HTTP client code

### Implementation Pattern

1. **Define API in OpenAPI** (`api/openapi.yaml`)
2. **Generate Go Code** (`make generate`)
3. **Implement ServerInterface** (`internal/httpapi/handlers.go`)
4. **Register Routes** (`internal/httpapi/routes.go`)

## Authentication Endpoints

### POST /auth/register
Register a new user account.

**Request:**
```json
{
  "username": "johndoe",
  "email": "john@example.com",
  "password": "securepassword123",
  "display_name": "John Doe"
}
```

**Response:**
```json
{
  "user": {
    "id": "user123",
    "username": "johndoe",
    "email": "john@example.com",
    "display_name": "John Doe",
    "role": "user",
    "is_active": true
  },
  "access_token": "eyJhbGciOiJIUzI1NiIs...",
  "refresh_token": "eyJhbGciOiJIUzI1NiIs...",
  "expires_in": 900
}
```

### POST /auth/login
Authenticate with email and password.

### POST /auth/refresh
Refresh access token using refresh token.

### POST /auth/logout
Logout and invalidate session (requires authentication).

## Health Endpoints

### GET /health
Basic health check - returns 200 if service is alive.

### GET /ready
Readiness check - validates database, Redis, and IPFS connectivity.

## Development Workflow

### 1. Modify OpenAPI Specification
Edit `api/openapi.yaml` to add/modify endpoints.

### 2. Validate Changes
```bash
make validate-openapi
```

### 3. Generate Code
```bash
make generate
```

### 4. Implement Handlers
Implement the `ServerInterface` methods in `internal/httpapi/handlers.go`.

### 5. Test Changes
```bash
make test
```

## CI/CD Pipeline

The GitHub Actions workflow automatically:

1. **Validates** OpenAPI specification
2. **Generates** Go code and verifies it's up to date
3. **Runs** linting and tests
4. **Builds** the application
5. **Tests** API endpoints
6. **Generates** HTML documentation
7. **Deploys** docs to GitHub Pages (main branch)
8. **Comments** on PRs with results

## Configuration

Environment variables:

```bash
DATABASE_URL=postgres://user:pass@localhost:5432/athena
REDIS_URL=redis://localhost:6379
JWT_SECRET=your-secret-key
PORT=8080
```

## Make Commands

```bash
make help          # Show all available commands
make deps          # Download dependencies
make generate      # Generate code from OpenAPI
make lint          # Run linting
make test          # Run tests with coverage
make build         # Build binary
make dev           # Start development server
make validate-openapi  # Validate OpenAPI spec
make serve-docs    # Serve API documentation
make install-tools # Install development tools
```

## Project Structure

```
athena/
├── api/
│   └── openapi.yaml              # OpenAPI 3.0 specification
├── internal/
│   ├── generated/
│   │   └── api.go                # Generated code (DO NOT EDIT)
│   ├── httpapi/
│   │   ├── handlers.go           # Handler implementations
│   │   ├── routes.go             # Route registration
│   │   └── response.go           # Response utilities
│   ├── domain/                   # Business logic
│   ├── config/                   # Configuration
│   └── middleware/               # HTTP middleware
├── cmd/server/                   # Application entrypoint
├── .github/workflows/
│   └── openapi-ci.yml           # CI/CD pipeline
├── Makefile                      # Development commands
└── README.md
```

## Benefits of OpenAPI Approach

1. **API-First Development** - Design API before implementation
2. **Type Safety** - Generated types prevent runtime errors
3. **Documentation** - Always up-to-date API docs
4. **Code Generation** - Reduces boilerplate and inconsistencies
5. **Validation** - Request/response validation built-in
6. **Testing** - Clear contracts for testing
7. **Client Generation** - Auto-generate clients in various languages

## Contributing

1. Follow the API-first development approach
2. Update OpenAPI spec before implementation
3. Run `make generate` after spec changes
4. Ensure tests pass with `make test`
5. Validate spec with `make validate-openapi`

## License

MIT License