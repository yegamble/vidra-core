# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Development Commands

### Local Development
- Build the project: `go build -o gotube ./cmd/server`
- Run locally: `./gotube`
- Run tests: `go test ./...`
- Run a single test: `go test -run TestSpecificFunction ./internal/auth`

### Docker Development
- Start all services: `docker-compose -f build/docker-compose.yml up --build`
- Build Docker image: `docker build -f build/Dockerfile -t gotube .`

### Database Setup
- Run migrations: Execute `scripts/migrations.sql` against MySQL database
- Local setup requires MySQL with credentials from environment variables

## Architecture Overview

GoTube is a Go-based decentralized video platform following Clean Architecture principles:

### Core Layers
- **cmd/server**: Application entry point with dependency injection
- **internal/api**: HTTP handlers, middleware, routing (using chi router) 
- **internal/usecase**: Business logic layer orchestrating repositories and services
- **internal/repository**: Data access layer (MySQL with sqlx)
- **internal/service**: External service integrations (IPFS, IOTA, SMTP)

### Key Components
- **Authentication**: JWT-based auth with Redis sessions, email verification workflow
- **Video Processing**: Async transcoding pipeline using Redis queues and FFmpeg
- **Storage**: Hybrid approach with local files + IPFS pinning for decentralization
- **Chunked Uploads**: Large file upload support via chunked transfer

### External Dependencies
- **MySQL**: Primary database for users, videos, metadata
- **Redis**: Session storage and job queues
- **IPFS**: Decentralized file storage and pinning
- **IOTA**: Blockchain metadata anchoring (stubbed implementation)

## Configuration

Environment variables are centrally managed in `internal/config/config.go`. Required variables:
- `DB_DSN`: MySQL connection string
- `JWT_SECRET`: JWT signing key
- `REDIS_ADDR`, `IPFS_API_URL`: Service endpoints
- `SMTP_*`: Email configuration for user verification

## Testing

Tests use Go's standard testing package. Currently minimal test coverage with `internal/auth/auth_test.go` as the main example.