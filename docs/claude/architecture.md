# Claude Architecture Guide - Athena Backend

## System Overview

Athena is a PeerTube backend implementation in Go following clean architecture principles with decentralized storage (IPFS) and ATProto federation support.

## Project Layout

```
/cmd/server            # main entry (flags, middleware, lifecycle)
/internal/app          # DI + bootstrap + lifecycle (DB, Redis, schedulers)
/internal/config       # env, .env, flags, defaults, validation
/internal/httpapi      # Chi routes, handlers, request/response DTOs (registration-only)
/internal/middleware   # auth, rate-limit, cors, tracing, recovery
/internal/domain       # core models & errors (no infra deps)
/internal/port         # repository/service contracts (interfaces)
/internal/usecase      # business logic by feature (+ aliases during migration)
/internal/repository   # SQLX repos (Postgres)
/internal/scheduler    # background schedulers (encoding, federation, firehose)
/internal/storage      # Hybrid (local/IPFS/S3-compatible)
/internal/metrics      # Prometheus metrics exposition
/migrations            # SQL migration files
/pkg                   # shared utility packages (e.g., imageutil)
```

## Architecture Principles

- **Dependency Injection**: Small constructors, no global state
- **Context-First APIs**: All functions accept context for cancellation/tracing
- **Narrow Interfaces**: Define interfaces in usecase layer
- **Error Handling**: Wrap errors with context using `fmt.Errorf("...: %w", err)`
- **Testing**: Unit tests per package, integration tests with Docker

## Core Components

### HTTP API (Chi Router)

Middleware stack (in order):

1. `RequestID` - Unique request tracking
2. `RealIP` - Client IP extraction
3. `Logger` - Structured logging
4. `Recoverer` - Panic recovery
5. `Timeout(60s)` - Request timeout
6. `Compress` - Response compression
7. `CORS` - Cross-origin support
8. `Auth` - JWT/OAuth validation
9. `RateLimit` - Request throttling

### Database (PostgreSQL + SQLX)

- Connection pooling: `MaxOpen=25`, `MaxIdle=5`
- Extensions: `pg_trgm`, `unaccent`, `uuid-ossp`, `btree_gin`
- Optimized indexes on search fields and foreign keys
- Transaction support via context or explicit `*sqlx.Tx`

### Caching (Redis)

- Session management (24h TTL)
- Rate limiting (sliding window)
- Video processing status
- Chunk upload tracking
- Federation queue state

### Storage Tiers

1. **Hot** - Local filesystem cache for frequently accessed content
2. **Warm** - IPFS distributed storage with pinning
3. **Cold** - S3-compatible object storage (Backblaze/DO/AWS)

### Video Processing

- FFmpeg worker pool with bounded concurrency
- HLS segmentation (4s chunks, VOD playlists)
- Automatic quality variants (2160p → 360p)
- Progress tracking via Redis
- Thumbnail generation

### Federation (ATProto)

- Instance DID document serving
- Bluesky firehose subscription
- Social graph synchronization
- Content ingestion pipeline
- Moderation label support

## Data Flow Examples

### Video Upload Flow

1. Client chunks upload (32MB pieces) → `/api/v1/videos/upload`
2. Redis tracks received chunks
3. Async merge when complete
4. Enqueue processing job
5. FFmpeg transcodes variants
6. IPFS pins content
7. Update database with CIDs
8. Send notifications

### Federation Sync Flow

1. Subscribe to Bluesky firehose
2. Filter relevant events (posts, likes, follows)
3. Queue ingestion tasks
4. Process with exponential backoff
5. Update local social graph
6. Persist to PostgreSQL
7. Cache hot data in Redis

## Key Interfaces

### Repository Pattern

```go
type VideoRepository interface {
    Create(ctx context.Context, video *domain.Video) error
    GetByID(ctx context.Context, id uuid.UUID) (*domain.Video, error)
    Update(ctx context.Context, video *domain.Video) error
    Delete(ctx context.Context, id uuid.UUID) error
}
```

### UseCase Pattern

```go
type VideoUseCase interface {
    UploadVideo(ctx context.Context, req UploadRequest) (*domain.Video, error)
    ProcessVideo(ctx context.Context, videoID uuid.UUID) error
    GetVideoStream(ctx context.Context, videoID uuid.UUID, quality string) (io.ReadCloser, error)
}
```

## Error Handling

- Domain errors are typed (e.g., `ErrVideoNotFound`)
- Transport layer maps to HTTP status codes
- Structured error responses with problem details
- Request ID correlation for debugging

## Testing Strategy

- **Unit Tests**: Mock repositories, test business logic
- **Integration Tests**: Real database with test containers
- **E2E Tests**: Full upload → process → playback flow
- **Load Tests**: Concurrent uploads and streaming

## Performance Considerations

- Database connection pooling
- Redis for hot data caching
- IPFS gateway pooling with circuit breakers
- Request coalescing for duplicate fetches
- Graceful degradation when services unavailable
