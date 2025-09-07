# Claude.md — PeerTube Backend in Go (Concise, with Best Practices)

## Overview

Rebuild PeerTube’s backend in Go using: **Chi**, **SQLX+PostgreSQL** (`pg_trgm`, `unaccent`, `uuid-ossp`), **Redis**, **IPFS** (Kubo + Cluster), **FFmpeg**, **IOTA**, **Hybrid storage** (local/IPFS/S3), **Docker/K8s**, **Go-Atlas** (migrations). Designed for concurrency, resiliency, and cost-efficient delivery.

---

## Project Layout (Go)

```
/cmd/server            # main entry (wire up deps, flags)
/internal/config       # env, .env, flags, defaults, validation
/internal/httpapi      # Chi routes, handlers, request/response DTOs
/internal/middleware   # auth, rate-limit, cors, tracing, recovery
/internal/auth         # JWT/OAuth, sessions
/internal/domain       # core models & errors (no infra deps)
/internal/usecase      # business logic (interfaces over repos/gateways)
/internal/repository   # SQLX repos (Postgres)
/internal/stream       # HLS, range streaming
/internal/processing   # FFmpeg workers, queue, progress
/internal/ipfs         # IPFS/Cluster clients, pinning
/internal/payments     # IOTA wallet/tx
/internal/storage      # Hybrid (local/IPFS/S3-compatible)
/internal/worker       # async jobs (chunk merge, GC, pins)
/internal/obs          # logging, metrics, tracing
/migrations            # Go-Atlas SQL files
/pkg                   # optional shared util packages
```

**Principles**: DI via small constructors, context-first APIs, no global state, narrow interfaces in `usecase`, error wrapping (`fmt.Errorf("...: %w", err)`), unit tests per package.

---

## API — Chi

- Middleware: `RequestID`, `RealIP`, `Logger`, `Recoverer`, `Timeout(60s)`, `Compress`, **CORS**, **Auth**, **RateLimit**.
- Routes: `/api/v1/videos` (CRUD/search/upload/chunk), `/auth/login`, `/health`, `/ready`.
- Validation: struct tags + centralized error mapper → JSON problem details.
- Idempotency: support `Idempotency-Key` for uploads and POSTs.

## Database — SQLX + Postgres

- Pool: `MaxOpen=25`, `MaxIdle=5`, `ConnMaxLifetime=5m`, `ConnMaxIdleTime=2m`.
- Extensions: `pg_trgm`, `unaccent`, `uuid-ossp`, `btree_gin`.
- Indexes on: `processing_status`, `privacy`, `upload_date`, GIN on `title`, `description`, `tags`, `metadata`.
- Migrations: Go-Atlas (see below).
- Transactions: repo methods accept `*sqlx.Tx` or context key to join existing tx.

## Redis

- Sessions (24h TTL), rate limiting (sliding window), video status cache, chunk-state.
- Persistence: enable AOF (appendonly yes) for progress/session durability.

## IPFS

- Kubo API client with 5m timeout; CIDv1, raw leaves, 256 KiB chunker.
- Cluster pinning (replication ≥3), health checks, GC job.
- Range streaming support; gateway pool fallback.

## IOTA

- Wallet per node seed; generate unique address per purchase.
- Embed payment metadata (videoId,userId,amount) in tagged data.
- Poll for confirmations; persist tx history.

## Video Processing (FFmpeg)

- Worker pool (bounded channel); per-job context deadlines.
- Variants: 2160p→360p (auto subset based on source); H.264/AAC, `+faststart`.
- HLS: 4s segments, VOD playlists; thumbnails.
- Progress via Redis; store CIDs per variant + bundle.

## Chunked Uploads

- 32MB chunks, resumable; Redis tracks received set.
- Async merge → temp file → checksum verify → enqueue processing.
- Enforce content-type/size; backpressure via queue length.

## Hybrid Storage

- **Hot** local cache → **Warm** IPFS → **Cold** S3-compatible (Backblaze/DO/AWS).
- Promotion/demotion by access metrics; async seeding between tiers.

## Notifications

Real-time notification system with automatic triggers and flexible delivery:

- **Types**: `new_video`, `video_processed`, `video_failed`, `new_subscriber`, `comment`, `mention`, `new_message`, `message_read`, `system`
- **Database**: `notifications` table with JSONB data field, optimized indexes for user queries
- **Triggers**: PostgreSQL functions for automatic notification creation:
  - `notify_subscribers_on_video_upload()` - Creates notifications when public videos are uploaded
  - `notify_on_new_message()` - Creates notifications for new messages (excludes system messages)
- **Service**: `NotificationService` handles business logic, batch operations, filtering
- **API**: Full CRUD with pagination, filtering, statistics, bulk operations
- **Performance**: Indexed on `user_id`, `read` status, `created_at`, composite for unread queries
- **Testing**: Comprehensive integration and unit tests, CI/CD compatible

## User Messaging

User messaging is implemented with a dedicated schema and functions to ensure conversations are ordered correctly. The system supports:

| **Category**        | **Supported Formats**                                   | **Max File Size**                           | **Notes** |
|----------------------|---------------------------------------------------------|---------------------------------------------|-----------|
| **Images**          | JPEG, PNG, GIF, WebP, HEIC (auto-converted to JPEG)     | ~25 MB                                      | SVG not supported. Animated GIFs display inline. |
| **Videos**          | MP4, MOV                                                | ~25 MB (mobile), up to 100–150 MB (desktop/web) | Messenger compresses video for playback. |
| **Audio**           | MP3, M4A, WAV, AAC                                      | ~25 MB                                      | Also supports in-app recorded voice notes. |
| **Documents/Files** | PDF, DOC, DOCX, XLS, XLSX, PPT, TXT, ZIP, RAR, others   | ~25 MB                                      | Wide support on web/desktop upload. |
| **Links**           | Any URL                                                 | N/A                                         | Auto-generates preview (title, description, thumbnail). |
| **Stickers & GIFs** | Giphy, Tenor                            | N/A                                         | Only via integrations, not as raw files. |
| **Other**           | Contact cards, locations, payments (region-specific)    | N/A                                         | Context-specific in mobile app. |


- Tests should check multiple users sending and reciving messages, including edge cases like large attachments, network interruptions, and concurrent sends.
- Support for end-to-end encrypted conversations with user-managed keys; see E2EE notes below.

### Security Best Practices

- MIME sniff + extension match: detect type server-side; reject mismatches.
- Antivirus scanning: route uploads through a ClamAV-compatible scanner; quarantine until clean; keep signatures updated.
- Image/PDF sanitization: strip EXIF; convert HEIC→JPEG; remove PDF JavaScript/embedded files or convert to PDF/A.
- Processing sandbox: run thumbnails/transcoding in isolated containers with seccomp/AppArmor, read-only fs, and CPU/mem/time limits.
- Content limits: enforce caps on size, duration, resolution, and archive depth/file-count to prevent zip-bombs and resource exhaustion.
- Output encoding: treat user text as plain or a safe Markdown subset; HTML is escaped and sanitized on render.
- Safe delivery: serve attachments with `Content-Disposition: attachment`, correct `Content-Type`, `X-Content-Type-Options: nosniff`, and strict `Content-Security-Policy` on preview pages.
- Privacy: avoid logging message bodies or attachment bytes; retain only minimal metadata for delivery/abuse prevention.

### Blocked Files

Reject the following outright regardless of size:

- Executables: `.exe`, `.msi`, `.com`, `.scr`, `.dll`, `.bin`, `.elf`, `.dylib`, `.so`.
- Scripts: `.bat`, `.cmd`, `.ps1`, `.psm1`, `.vbs`, `.js`, `.jar`, `.sh`, `.bash`, `.zsh`, `.py`, `.pl`, `.rb`, `.php`.
- OS/App bundles & installers: `.apk`, `.aab`, `.ipa`, `.app`, `.pkg`, `.dmg`.
- Disk/virtual images: `.iso`, `.img`, `.vhd`, `.vhdx`.
- Macro-enabled Office: `.docm`, `.dotm`, `.xlsm`, `.xltm`, `.pptm`, `.ppam`.
- Shortcuts/links/registry: `.lnk`, `.url`, `.webloc`, `.desktop`, `.reg`, `.cpl`, `.hta`, `.chm`, `.scf`.
- Encrypted/password-protected archives: encrypted `.zip`, `.7z`, `.rar` (reject or allow only with explicit admin policy; never preview).
- Active media: `.svg`, `.swf`.

Also reject archives that exceed nesting depth, total file count, or uncompressed size thresholds (zip-bomb protection) or that contain any blocked type inside.

### Link Previews (SSRF-Safe)

- Only fetch `http`/`https` URLs; block `file:`, `data:`, and `ftp:`.
- Deny private/link-local/loopback CIDRs (RFC1918/4193, link-local, loopback) and `.onion`.
- Use dedicated egress; verify TLS; limit redirects (≤3), body size (≤512 KB), and content types (HTML only). No cookies/auth headers; do not execute JS.

### E2EE Notes

- In E2EE mode, messages and attachments are encrypted client-side; the server stores ciphertext only. Scanning and previews are disabled by design.
- Display an “Unscanned (E2EE)” badge and require explicit user action to open attachments; integrity verified via client-side MAC.
- Keys are per-conversation with periodic rotation; passphrase/6‑digit code gates local key unlock; per-message nonces prevent replay.

---

## Linting & Code Quality

- `golangci-lint` (run in CI + pre-commit):
    - `gofmt`, `govet`, `errcheck`, `staticcheck`, `gosimple`, `ineffassign`, `revive`, `gocritic`, `nestif`, `dupl`, `gosec`.
    - Config example (`.golangci.yml`): set max cyclomatic complexity, line length, enable `gosec` except false positives on `exec.CommandContext` with validated args.
- API schemas: OpenAPI (oapi-codegen) or protobuf for future gRPC; keep HTTP first.
- Error policy: domain errors typed; transport maps to HTTP 4xx/5xx consistently.

## Migrations — Go-Atlas

- Config (`atlas.hcl`): dev shadow DB, migration dir, destructive lint.
- Generate diff:
  ```bash
  atlas migrate diff add_table --dir "file://migrations" \
    --to "file://schema.hcl" \
    --dev-url "postgres://user:pass@localhost:5433/db_shadow?sslmode=disable"
  ```
- Apply:
  ```bash
  atlas migrate apply --dir "file://migrations" \
    --url "postgres://user:pass@localhost:5432/video_platform?sslmode=disable"
  ```
- Lint:
  ```bash
  atlas migrate lint --dir "file://migrations" \
    --dev-url "postgres://user:pass@localhost:5433/db_shadow?sslmode=disable"
  ```
- Policy: forward-only, no `DROP` without lint waiver, PR requires plan + checksum.

---

## Running with Docker

**Dev**

```bash
docker compose up --build
```

- Ensure volumes exist; provide `.env` with DB/Redis/IPFS creds.
- Useful overrides: mount source as bind for live reload (air/CompileDaemon).

**Production**

- `restart: unless-stopped`, healthchecks on all services.
- Resource hints: app (2 vCPU/4GB), ffmpeg (4 vCPU/8GB), Postgres (2GB), Redis (1GB), IPFS (2GB).
- Volumes:
    - `uploads` (xfs/ext4), `processed`, `tmp` as `tmpfs` for speed
    - IPFS datastore separate disk; enable `server` profile
- ulimits: raise `nofile` (e.g., 131072) for IPFS/ffmpeg.
- Logging: `max-size=100m`, `max-file=3` on containers.
- Secrets: pass via Docker/Swarm/K8s, not env files in git.

---

## Kubernetes Notes

- Probes: `/health` (liveness) and `/ready` (readiness).
- PVC: fast storage for hot cache; object storage via CSI/S3 for cold.
- HPA: CPU + custom QPS metric; VPA for ffmpeg workers.
- Pod topology spread; anti-affinity for app + IPFS.

---

## Observability

- Logging: structured (zap/slog), request-scoped fields (req\_id, user\_id, ip, route, dur\_ms).
- Metrics: Prometheus (HTTP latency, QPS, queue depth, transcode time, IPFS pin time, Redis ops, DB pool stats).
- Tracing: OpenTelemetry (ingest → processing → IPFS → DB). Export OTLP.

---

## Security & Auth

- JWT access + refresh tokens; short-lived access, rotate refresh.
- Optional OAuth providers; rate-limit login/refresh.
- CORS allowlist; CSRF not needed for pure API + JWT.
- Validate media: mime sniff + ffprobe; reject executables.
- Least-privilege DB user; separate read replica role.
- Secret management: KMS/SealedSecrets; never log secrets.

---

## Reliability & Backpressure

- Context timeouts on all network/DB/ffmpeg/IPFS calls.
- Retries with jittered backoff; circuit-breaker around IPFS gateways.
- Graceful shutdown: drain HTTP, stop accepting jobs, flush progress.
- Job queue limits; 429 when saturated.

---

## Build, Test, CI/CD

**Makefile targets**

```
make deps        # go mod download
make lint        # golangci-lint run ./...
make test        # unit tests
make build       # binary
make docker      # docker build
make migrate     # atlas migrate apply
```

**Testing**

- Unit: usecase/repo with sqlmock for DB.
- Integration: dockerized Postgres/Redis/IPFS via `docker compose -f docker-compose.test.yml`.
- E2E: upload → process → HLS play. **CI** (GitHub Actions example stages)
- `lint → test → build → docker push → atlas plan/lint → deploy`.

---

## Configuration

- Precedence: flags > env > `.env` > defaults.
- Required vars: `DATABASE_URL`, `REDIS_URL`, `IPFS_API`, `IPFS_CLUSTER_API`, `IOTA_NODE_URL`, `FFMPEG_PATH`.
- Feature flags (env): `ENABLE_IOTA`, `ENABLE_IPFS_CLUSTER`, `ENABLE_S3`.

---

## S3-Compatible Wrapper (Hybrid)

- Single interface with providers: AWS S3, Backblaze B2, DO Spaces.
- Signed URLs for private objects; range reads; multipart uploads.
- Background seeding to IPFS on cold fetches.

---

## Pinning Strategy

- Score: views (40), recency (30), age (20), size efficiency (10).
- Storage cap: unpin <0.3 score at >90% usage; replicate >0.7.
- Backup to external pinning services (Pinata/Infura).

---

## Minimal Health Contract

- `/health` → 200 if event loop alive.
- `/ready` → checks DB ping, Redis ping, IPFS API reachable, queue depth under thresholds.

---

## Summary

Highly concurrent Go backend mirroring PeerTube features with decentralized storage (IPFS), hybrid delivery, robust processing, and production-grade ops: migrations (Atlas), linting (golangci), Docker/K8s deploys, observability, and strict reliability/security practices.

This design emphasizes modularity, testability, and maintainability while ensuring a smooth user experience for video uploads, processing, and playback. The architecture is built to scale efficiently with the growth of the platform, leveraging Go's strengths in concurrency and performance.
```
