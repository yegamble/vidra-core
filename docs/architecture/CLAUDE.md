# Architecture Deep-Dive

This document provides comprehensive architectural context. For quick reference, see the root `CLAUDE.md`. For domain-specific details, see the CLAUDE.md files in each module.

## System Overview

PeerTube-compatible backend in Go with decentralized storage (IPFS), hybrid delivery, and federation support.

**Core Technologies**:

- **Router**: Chi with middleware stack
- **Database**: PostgreSQL (SQLX) with extensions: `pg_trgm`, `unaccent`, `uuid-ossp`, `btree_gin`
- **Cache**: Redis (sessions, rate limiting, job state)
- **Storage**: Hybrid (local hot → IPFS warm → S3 cold)
- **Processing**: FFmpeg worker pool
- **Payments**: IOTA cryptocurrency
- **Federation**: ActivityPub (see `internal/activitypub/CLAUDE.md`)

## Database Configuration

```go
// Connection pool settings
MaxOpen=25, MaxIdle=5, ConnMaxLifetime=5m, ConnMaxIdleTime=2m
```

### Key Indexes

- `processing_status`, `privacy`, `upload_date` on videos
- GIN indexes on `title`, `description`, `tags`, `metadata`
- Composite indexes for notification queries

## Redis Usage

| Purpose | TTL | Notes |
|---------|-----|-------|
| Sessions | 24h | User auth tokens |
| Rate limits | sliding window | Per-IP/user |
| Video status | varies | Processing progress |
| Chunk state | 1h | Upload resumption |

Enable AOF persistence (`appendonly yes`) for durability.

## Video Processing Pipeline

```
Upload → Virus Scan → Chunk Merge → FFmpeg → IPFS Pin → Ready
```

### FFmpeg Configuration

- Variants: 2160p → 360p (auto subset based on source)
- Codec: H.264/AAC with `+faststart`
- HLS: 4s segments, VOD playlists
- Worker pool with bounded channel and per-job deadlines

### Encoding Job Resilience & Progress Tracking

- **Heartbeat**: Active encoding jobs emit a heartbeat every 5 minutes (updates `updated_at`), keeping long-running encodes (e.g., 4K on low-spec hardware) alive
- **Stale Job Recovery**: On worker startup, `ResetStaleJobs` resets orphaned `processing` jobs (no heartbeat for 30+ minutes) back to `pending` for automatic re-processing
- **Safety margin**: 6x ratio (5-min heartbeat vs 30-min threshold) prevents false resets of active jobs
- **Atomic job claiming**: `FOR UPDATE SKIP LOCKED` prevents duplicate processing across workers
- **Real-time Progress**: FFmpeg stderr parsing provides 0-100% progress updates, stored in DB and exposed via API
- **Progress API Access**: Restricted to video owner, administrators, and moderators via JWT role validation
- **Aggregate Progress**: When multiple resolutions encode in parallel, overall progress is calculated as average

## Chunked Upload Flow

1. Client initiates upload → receives `sessionId`
2. Upload 32MB chunks with resumption support
3. Server tracks received chunks in Redis
4. Async merge → checksum verify → enqueue processing

## Hybrid Storage Strategy

```
Hot (local cache) → Warm (IPFS) → Cold (S3-compatible)
```

- Promotion/demotion based on access metrics
- Async seeding between tiers
- S3 providers: AWS, Backblaze B2, DO Spaces

## Pinning Strategy

Score-based with factors:

- Views: 40%
- Recency: 30%
- Age: 20%
- Size efficiency: 10%

Actions:

- Unpin when score < 0.3 and storage > 90%
- Replicate when score > 0.7

## Health Endpoints

| Endpoint | Type | Checks |
|----------|------|--------|
| `/health` | Liveness | Event loop alive |
| `/ready` | Readiness | DB, Redis, IPFS, queue depth |

## Observability Stack

### Logging (slog)

Request-scoped fields: `request_id`, `user_id`, `ip`, `route`, `duration_ms`

### Metrics (Prometheus)

Key metrics:

- `http_request_duration_seconds`
- `video_encoding_queue_depth`
- `video_processing_errors_total{error_type}`
- `iota_payment_confirmation_duration_seconds`

### Tracing (OpenTelemetry)

- W3C Trace Context propagation
- Spans: ingest → processing → IPFS → DB

## Notifications System

Types: `new_video`, `video_processed`, `video_failed`, `new_subscriber`, `comment`, `mention`, `new_message`, `system`

Database triggers:

- `notify_subscribers_on_video_upload()` - Auto-notify on public video
- `notify_on_new_message()` - Notify on new messages

## User Messaging

Supported formats: Images (JPEG, PNG, GIF, WebP, HEIC), Videos (MP4, MOV), Audio (MP3, M4A), Documents (PDF, DOC, XLS), Links with preview

Max file size: ~25MB (mobile), ~100-150MB (desktop)

See `internal/security/CLAUDE.md` for blocked file types and security measures.

## Docker/K8s Deployment

### Resource Guidelines

| Service | vCPU | Memory |
|---------|------|--------|
| App | 2 | 4GB |
| FFmpeg | 4 | 8GB |
| Postgres | 2 | 2GB |
| Redis | 1 | 1GB |
| IPFS | 2 | 2GB |

### K8s Considerations

- Probes: `/health` (liveness), `/ready` (readiness)
- HPA: CPU + custom QPS metric
- VPA for FFmpeg workers
- Pod topology spread with anti-affinity

## Security Architecture

**Authentication**: JWT access (15min) + refresh (7d), optional OAuth2 with PKCE, TOTP 2FA

**Cryptography** (see `internal/security/CLAUDE.md`):

- HSM interface with software fallback
- AES-256-GCM for data at rest
- Argon2id for key derivation

**Input Validation**:

- MIME sniff + extension match
- Virus scanning (ClamAV)
- HTML sanitization (bluemonday)

## Reliability Patterns

- Context timeouts on all network/DB/IPFS calls
- Retries with jittered exponential backoff
- Circuit breakers around IPFS gateways
- Graceful shutdown: drain HTTP → stop jobs → flush progress
- Rate limiting: 429 when queue saturated

## Domain-Specific References

| Area | Location |
|------|----------|
| Security | `internal/security/CLAUDE.md` |
| HTTP API | `internal/httpapi/CLAUDE.md` |
| Federation | `internal/activitypub/CLAUDE.md` |
| Migrations | `migrations/CLAUDE.md` |
