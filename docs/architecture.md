# Architecture Overview

Athena is a high-performance PeerTube backend implementation in Go, following **Clean Architecture** principles with clear separation of concerns across layers.

## Table of Contents

- [Architecture Principles](#architecture-principles)
- [Project Structure](#project-structure)
- [Layer Responsibilities](#layer-responsibilities)
- [Data Flow](#data-flow)
- [Key Subsystems](#key-subsystems)
- [Technology Stack](#technology-stack)
- [Patterns and Practices](#patterns-and-practices)

## Architecture Principles

1. **Dependency Inversion**: Business logic (usecase) depends only on interfaces (ports), never on concrete implementations
2. **Clean Architecture Layers**: Domain → Usecase (ports) → Infrastructure (adapters)
3. **Context-First APIs**: All service methods accept `context.Context` for cancellation and deadlines
4. **Narrow Interfaces**: Small, focused interfaces over large monolithic ones
5. **Error Wrapping**: Consistent error handling with `fmt.Errorf("...: %w", err)` for traceability
6. **No Global State**: Dependency injection via constructors, no package-level singletons
7. **Feature Slices**: Business logic organized by feature/bounded context, not by technical layer

## Project Structure

```
athena/
├── cmd/
│   └── server/              # Application entry point
│       └── main.go          # Wires up dependencies and starts HTTP server
├── internal/
│   ├── app/                 # Application bootstrap and lifecycle management
│   │   └── app.go           # Dependency injection, initialization, graceful shutdown
│   ├── config/              # Configuration loading and validation
│   ├── domain/              # Core business entities and errors
│   │   ├── video.go         # Video aggregate
│   │   ├── user.go          # User aggregate
│   │   ├── channel.go       # Channel aggregate
│   │   ├── message.go       # Messaging domain
│   │   ├── federation.go    # Federation domain models
│   │   └── errors.go        # Domain-specific errors
│   ├── port/                # Repository interfaces (dependency inversion)
│   │   ├── video.go         # VideoRepository interface
│   │   ├── user.go          # UserRepository interface
│   │   ├── auth.go          # AuthRepository interface
│   │   └── ...              # Other repository contracts
│   ├── usecase/             # Business logic organized by feature
│   │   ├── video/           # Video feature slice
│   │   ├── upload/          # Upload feature slice
│   │   ├── encoding/        # Encoding feature slice
│   │   ├── channel/         # Channel feature slice
│   │   ├── comment/         # Comment feature slice
│   │   ├── rating/          # Rating feature slice
│   │   ├── notification/    # Notification feature slice
│   │   ├── message/         # Messaging feature slice
│   │   ├── auth/            # Authentication (planned)
│   │   ├── federation/      # Federation (planned)
│   │   └── e2ee/            # End-to-end encryption (planned)
│   ├── repository/          # PostgreSQL implementations of port interfaces
│   │   ├── video_repository.go
│   │   ├── user_repository.go
│   │   └── ...
│   ├── httpapi/             # HTTP handlers and routing
│   │   ├── routes.go        # Route registration
│   │   ├── videos.go        # Video endpoints
│   │   ├── auth.go          # Auth endpoints
│   │   └── ...
│   ├── middleware/          # HTTP middleware (auth, CORS, rate limiting)
│   ├── scheduler/           # Background job schedulers
│   ├── crypto/              # Cryptography utilities
│   ├── email/               # Email service
│   ├── metrics/             # Prometheus metrics
│   └── generated/           # OpenAPI generated types
├── migrations/              # Goose database migrations
├── docs/                    # Documentation
│   ├── architecture.md      # This file
│   └── claude/              # Claude AI-specific guides
└── pkg/                     # Shared utilities (planned)
```

## Layer Responsibilities

### Domain Layer (`internal/domain`)

**Purpose**: Core business entities and rules, independent of infrastructure concerns.

- **Entities**: `Video`, `User`, `Channel`, `Comment`, `Message`, `FederatedPost`
- **Value Objects**: Privacy levels, video statuses, notification types
- **Domain Errors**: `ErrVideoNotFound`, `ErrUnauthorized`, `ErrInvalidInput`
- **No Dependencies**: This layer has zero external dependencies

### Port Layer (`internal/port`)

**Purpose**: Interface contracts between business logic and infrastructure.

- **Repository Interfaces**: `VideoRepository`, `UserRepository`, `MessageRepository`
- **Service Interfaces**: Defined by usecase packages, implemented by infrastructure
- **Dependency Inversion**: Business logic depends on these abstractions, not concrete implementations

### Usecase Layer (`internal/usecase/*`)

**Purpose**: Application business logic organized by feature/bounded context.

**Feature Slices**:
- `upload/` - Video upload and chunking logic
- `encoding/` - FFmpeg transcoding workflows
- `channel/` - Channel management
- `comment/` - Comment operations
- `rating/` - Like/dislike logic
- `notification/` - Notification dispatch
- `message/` - User messaging
- `views/` - View tracking and analytics

**Characteristics**:
- Each feature slice is self-contained with its own service file
- Services depend only on port interfaces
- Cross-feature communication via port interfaces
- Rich unit testing with mock repositories

### Infrastructure Layer

#### Application Bootstrap (`internal/app`)

**Purpose**: Dependency injection and application lifecycle management.

- **Initialization**: Database, Redis, IPFS connection setup
- **Wiring**: Creates all repositories and services with proper dependencies
- **Lifecycle**: Manages startup/shutdown of schedulers and workers
- **Graceful Shutdown**: Ensures clean resource cleanup

#### Repository Layer (`internal/repository`)

**Purpose**: Data persistence using PostgreSQL via SQLX.

- **Implements**: Port interfaces from `internal/port`
- **Transaction Support**: Methods accept `*sqlx.Tx` for atomic operations
- **Query Optimization**: Prepared statements, connection pooling, indexes
- **Migration**: Schema managed via Goose

#### HTTP API (`internal/httpapi`)

**Purpose**: HTTP request/response handling and routing.

- **Router**: Chi router with middleware chain
- **Handlers**: Map HTTP requests to usecase service calls
- **Validation**: Request validation via struct tags
- **Error Mapping**: Domain errors → HTTP status codes

#### Middleware (`internal/middleware`)

- **Authentication**: JWT validation and user context injection
- **Authorization**: OAuth scopes and permission checks
- **Rate Limiting**: Redis-backed sliding window
- **CORS**: Configurable origin policies
- **Security Headers**: CSP, HSTS, X-Frame-Options
- **Request ID**: Tracing correlation

#### Background Workers

- **Encoding Scheduler** (`internal/scheduler`): Polls for pending encoding jobs
- **Federation Scheduler**: Ingests posts from ATProto feeds
- **Firehose Poller**: Real-time federation event processing

## Data Flow

### Typical Request Flow

```
HTTP Request
    ↓
Middleware Chain (auth, rate-limit, logging)
    ↓
Handler (internal/httpapi)
    ↓
Usecase Service (internal/usecase/<feature>)
    ↓
Repository (internal/repository via internal/port interface)
    ↓
Database (PostgreSQL)
```

### Dependency Flow (Clean Architecture)

```
main.go
    ↓
internal/app (bootstrap)
    ↓ creates
Repository Implementations (infrastructure)
    ↓ injected into
Usecase Services (business logic, depends on port interfaces)
    ↓ injected into
HTTP Handlers (presentation)
```

### Video Upload Flow

```
1. Client → POST /api/v1/videos/upload/init
   Handler → UploadService.InitiateUpload()
   UploadService → UploadRepository.CreateUpload()
   Response: upload_id, chunk_size

2. Client → POST /api/v1/videos/upload/{id}/chunk/{index}
   Handler → UploadService.UploadChunk()
   UploadService → Redis.SetChunkReceived()

3. Client → POST /api/v1/videos/upload/{id}/complete
   Handler → UploadService.CompleteUpload()
   UploadService → Merge chunks, verify checksum
   UploadService → EncodingRepository.CreateJob()
   Background: EncodingScheduler picks up job
```

### Encoding Flow

```
EncodingScheduler (polls every N seconds)
    ↓
EncodingService.ProcessNextJob()
    ↓
FFmpeg: Transcode to variants (2160p → 360p)
    ↓
IPFS: Pin variants and HLS playlists
    ↓
VideoRepository.UpdateStatus(COMPLETED)
    ↓
NotificationService.NotifySubscribers()
    ↓
FederationService.PublishToATProto()
```

## Key Subsystems

### Authentication & Authorization

- **JWT Access Tokens**: Short-lived (15 min), signed with HS256
- **Refresh Tokens**: Long-lived (7 days), stored in Redis and PostgreSQL
- **OAuth2**: Support for external providers (Google, GitHub)
- **Session Management**: Redis-backed with composite repository pattern
- **Email Verification**: Token + 6-digit code, 24-hour expiry

### Video Encoding

- **Worker Pool**: Configurable number of concurrent encoding workers
- **Job Queue**: PostgreSQL-backed with status tracking
- **Variants**: H.264/AAC at multiple resolutions (360p-2160p)
- **HLS**: 4-second segments, VOD playlists, per-variant
- **Progress Tracking**: Redis pubsub for real-time client updates
- **IPFS Integration**: Automatic pinning of encoded variants

### Federation (ATProto)

- **Outbound**: Publish videos as Bluesky posts with embed metadata
- **Inbound**: Ingest posts from configured actors, store in `federated_posts`
- **Job Queue**: Retry with exponential backoff for publish failures
- **Session Management**: Encrypted token storage, background refresh
- **Hardening**: Circuit breaker, backpressure, deduplication, DLQ

### Messaging

- **Direct Messages**: User-to-user conversations
- **Attachments**: Images, videos, documents (MIME validation, virus scanning)
- **E2EE**: Ed25519 signing keys, X25519 encryption, conversation key rotation
- **Conversation Threading**: Proper ordering, unread counts
- **Security**: Blocked file types, SSRF protection for link previews

### Notifications

- **Triggers**: PostgreSQL functions for automatic creation
- **Types**: `new_video`, `video_processed`, `comment`, `new_subscriber`, etc.
- **Delivery**: Poll-based API with filtering and pagination
- **Batch Operations**: Mark all as read, delete by type
- **Statistics**: Unread counts by type

## Technology Stack

### Core

- **Language**: Go 1.22+
- **HTTP Router**: Chi v5
- **Database**: PostgreSQL 15+ with SQLX
- **Cache**: Redis 7+
- **Storage**: IPFS (Kubo), S3-compatible (optional)

### Infrastructure

- **Migrations**: Goose (SQL migrations)
- **Containerization**: Docker + Docker Compose
- **Orchestration**: Kubernetes-ready with health probes
- **Observability**: Prometheus metrics, structured logging (slog/zap)

### Video Processing

- **Encoder**: FFmpeg
- **Formats**: HLS (H.264/AAC), WebM support planned
- **Thumbnails**: Extracted at multiple timestamps
- **Storyboards**: Preview images for scrubbing

### Federation

- **Protocol**: AT Protocol (ATProto)
- **Client**: Official Bluesky Go SDK
- **Features**: Posts, replies, reposts, likes

## Patterns and Practices

### Dependency Injection

```go
// Bad: global state
var db *sqlx.DB

// Good: constructor injection
type VideoService struct {
    repo port.VideoRepository
}

func NewVideoService(repo port.VideoRepository) *VideoService {
    return &VideoService{repo: repo}
}
```

### Error Handling

```go
// Wrap errors for context
if err != nil {
    return fmt.Errorf("failed to create video: %w", err)
}

// Domain errors for expected failures
if video == nil {
    return domain.ErrVideoNotFound
}
```

### Context Propagation

```go
// All service methods accept context
func (s *VideoService) GetByID(ctx context.Context, id string) (*domain.Video, error) {
    // Context carries deadlines, cancellation, and request-scoped values
    return s.repo.GetByID(ctx, id)
}
```

### Transaction Management

```go
// Repository methods accept optional transaction
func (r *VideoRepository) Create(ctx context.Context, video *domain.Video, tx *sqlx.Tx) error {
    executor := r.getExecutor(tx) // Use tx if provided, otherwise db
    // ...
}
```

### Testing

```go
// Unit tests use mock repositories
func TestVideoService_Create(t *testing.T) {
    mockRepo := &MockVideoRepository{}
    service := NewVideoService(mockRepo)

    mockRepo.On("Create", mock.Anything, mock.Anything).Return(nil)

    err := service.Create(context.Background(), &domain.Video{})
    assert.NoError(t, err)
}
```

## Deployment Considerations

### Scalability

- **Stateless API**: Horizontal scaling behind load balancer
- **Worker Scaling**: Encoding workers scale independently
- **Database**: Read replicas for analytics queries
- **Redis**: Sentinel or Cluster for HA

### Security

- **Rate Limiting**: Per-IP and per-user limits
- **Input Validation**: Struct tags + custom validators
- **SQL Injection**: Parameterized queries via SQLX
- **File Upload**: MIME validation, size limits, virus scanning
- **Secrets**: Environment variables, never committed

### Monitoring

- **Metrics**: `/metrics` endpoint (Prometheus format)
- **Health Checks**: `/health` (liveness), `/ready` (readiness)
- **Logging**: Structured JSON logs with correlation IDs
- **Tracing**: OpenTelemetry-ready (planned)

### Performance

- **Connection Pooling**: DB (25 max), Redis (10 max)
- **Caching**: Redis for sessions, video metadata, rate limits
- **Indexes**: Optimized for common query patterns
- **Chunked Uploads**: 32MB chunks for resumability

---

For more specific guides, see:
- [Development Guide](DEVELOPMENT.md)
- [Deployment Guide](deployment/README.md)
- [API Documentation](api/README.md)
- [Claude AI Guides](claude/)
