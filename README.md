# Vidra Core

The Go backend for **Vidra** — a clean-room, PeerTube-inspired federated video
platform. This repository (`vidra-core`) exposes the Vidra HTTP API. The Next.js
frontend lives in a separate `vidra-user` repository and consumes this API.

Vidra Core serves a 209-path OpenAPI 3.1 contract backed by 98 SQL migrations. It
runs the full creator pipeline — upload → transcode → HLS/VP9 — plus live streaming
over RTMP with replay-to-VOD, end-to-end-encrypted DMs, OAuth/OIDC and TOTP auth,
ATProto (Bluesky) identity, dual-tier IPFS mirroring, Whisper auto-captions, and a
one-way PeerTube importer. Auth, sessions, storage, health/readiness probes,
observability, and CI are all in place.

**Related repos:** frontend <https://github.com/yegamble/vidra-user> · search
<https://github.com/yegamble/vidra-search> · meta-repo
<https://github.com/yegamble/vidra>.

## Prerequisites

- **Container path:** Docker + Docker Compose. Everything else (Go toolchain,
  ffmpeg/ffprobe, migrations) is baked into the images.
- **Host development extras:** Go 1.26; the [`migrate`](https://github.com/golang-migrate/migrate)
  CLI (schema migrations); [`sqlc`](https://sqlc.dev) v1.31.1 (codegen + `sqlc-verify`);
  `node`/`npx` (`openapi-lint`, `postman`); and `ffmpeg`/`ffprobe` on `PATH` for
  transcoding/thumbnails/probing (already inside the Docker image — a host without
  them degrades gracefully to originals-only).

## Quick start

```bash
cp .env.example .env
make up        # postgres + redis + migrations + api via Docker Compose
```

Then:

```bash
curl localhost:8080/healthz          # liveness
curl localhost:8080/readyz           # readiness (postgres + redis)
curl localhost:8080/version          # build version / commit / date
curl localhost:8080/api/v1/nodeinfo  # instance discovery metadata
curl localhost:8080/api/v1/instance  # public about/config (name, software, policy, features)
curl localhost:8080/api/v1/instance/about # long-form markdown about-page content
```

## Development & testing

No-Docker path (run the API on the host against dockerised datastores):

```bash
cp .env.example .env
docker compose --profile core up postgres redis  # just the datastores
make migrate-up                                   # requires the `migrate` CLI
make run                                           # API against local Postgres/Redis
```

**`make ci` is the canonical gate.** It runs `fmt-check vet openapi-verify
sqlc-verify test-race` — the exact set `backend-ci.yml` runs, so "passes locally"
== "passes in GitHub". Add any new required check there, never only in the workflow.

- `make check` — fast local loop (`fmt vet test`).
- `make test-race` — unit tests under the race detector.
- `make test-integration` — `-tags=integration` suite; each test self-skips when its
  dependency (`DATABASE_URL`, `REDIS_URL`, ffmpeg) is absent.
- `make test-ipfs-integration` / `make test-ipfs-private-integration` — real-Kubo
  round-trips behind build tags; both self-skip without their nodes and stay out of
  `make ci`.
- `make bench` — hot-path benchmarks (set `DATABASE_URL` for the feed/search benches);
  exploratory signal, not part of the gate. The `bench-fuzz` workflow runs these
  plus a `go test -fuzz` pass on demand.
- `make build` — build `./bin/api`, injecting version/commit/date via `-ldflags`.
- `make help` — the full target list (fmt, vet, migrate-up/down, sqlc, up/down, …).

## Configuration

`.env.example` documents the full configuration surface (123 keys) — this is not a
full table. The load-bearing knobs:

| Key | Default | Notes |
| --- | --- | --- |
| `JWT_SECRET` | dev placeholder | **Required in production** — the HS256 signing secret. |
| `PUBLIC_BASE_URL` | empty | Required for OAuth redirect URIs and `Secure` cookies. |
| `STORAGE_BACKEND` | `local` | `local` \| `s3`. Local writes under `STORAGE_LOCAL_ROOT`. |
| `STORAGE_LOCAL_ROOT` | `./data/media` | Root directory for the local backend. |
| `TRANSCODING_ENABLED` | `true` | HLS ladder; set `false` to serve originals only. |
| `RATE_LIMIT_REQUESTS` / `RATE_LIMIT_WINDOW` | `120` / `1m` | Per-IP `/api` budget; fails **open** if Redis is down. |
| `HTTP_BODY_LIMIT` | `8M` | JSON API body cap (upload routes are exempt). |
| `REGISTRATION_ENABLED` | `true` | Close signup per-instance. |

Feature gates default **OFF**: yt-dlp URL import, channel auto-sync, ATProto
cross-posting, Whisper captions, IPFS mirroring, and malware scanning are all opt-in.

> **Honest note:** the `YTDLP_*` and `CHANNEL_SYNC_*` keys are real, wired config
> (`internal/config`) but are not yet listed in `.env.example`. See
> [docs/features.md](docs/features.md#video-pipeline) for their names and defaults.

## Compose profiles & ports

Optional Docker Compose profiles layer services onto the `core` stack:

| Profile | Adds |
| --- | --- |
| `core` | postgres, redis, migrate, api |
| `storage` | MinIO (S3-compatible object store) |
| `scan` | ClamAV (malware scanning) |
| `captions` | Whisper caption server |
| `media` | nginx-rtmp (live ingest + HLS) |
| `otel` | OpenTelemetry collector + Jaeger |
| `ipfs` / `full` | public IPFS (Kubo) mirror node |
| `ipfs-private` | private `swarm.key`'d Kubo node |
| `ipfs-private-cluster` | private keyed node + IPFS Cluster peer |
| `ytdlp` | forward-egress proxy sketch for yt-dlp import |

| Service | Host port |
| --- | --- |
| api | `8080` |
| RTMP ingest | `1935` |
| MinIO | `9000` |
| OTLP (gRPC / HTTP) | `4317` / `4318` |
| Jaeger UI | `16686` |
| public IPFS gateway (RPC `127.0.0.1:5001`) | `9090` |
| private Kubo RPC | `127.0.0.1:5002` |
| private IPFS Cluster REST | `127.0.0.1:9094` |

## Features

Every cluster below links to its detailed reference in
[docs/features.md](docs/features.md).

### Auth & accounts

HS256 JWT access tokens with rotating, reuse-detecting refresh sessions (opaque,
SHA-256-hashed). Optional cookie-mode sessions for browser SPAs, OAuth/OIDC login
with server-derived redirect URIs, and TOTP two-factor with recovery codes.
Includes `/auth/me`, reversible deactivation, irreversible hard-delete with
anonymisation, and a JSON account export/import. See
[Auth & accounts](docs/features.md#auth--accounts).

### Channels & social

Channels are user-owned publishing identities with immutable handles, follows +
follower counts, and a subscriptions feed. Accounts and channels carry avatars and
banners; creators list non-custodial crypto donation addresses (ethereum ownership is
provable via EIP-191). Channel auto-sync mirrors an external platform channel's uploads
into a local channel. See [Channels & social](docs/features.md#channels--social).

### Video pipeline

Draft → processing → published lifecycle with direct, chunked/resumable, and async
URL uploads (SSRF-guarded, optional sandboxed yt-dlp extractor). Per-user storage
quotas, optional ClamAV malware scanning, an ffprobe finalisation seam, an H.264/AAC
HLS ladder with I-frame trick-play playlists, an optional VP9/WebM download alternate,
plus thumbnails, storyboards, chapters, and reference-counted media GC. See
[Video pipeline](docs/features.md#video-pipeline).

### Playback

Range-aware progressive streaming of the original, plus HLS with the same visibility
gating. Password-protected videos mint 6-hour video-scoped HMAC playback tokens; embed
privacy is enforced at the frontend embed page. Per-user player settings, hourly
de-duplicated view recording, and WebVTT captions with optional Whisper
auto-generation. See [Playback](docs/features.md#playback).

### Live streaming

A channel owner creates a stream (key shown once, only its hash stored) and publishes
over RTMP through the `media` profile's nginx-rtmp server, which drives the api via
ingest hooks; the api serves privacy-gated HLS while live. Optional replay recordings
become ordinary VOD videos through the normal pipeline. Run the whole live plane
locally:

```bash
LIVE_INGEST_SECRET=$(openssl rand -hex 24) LIVE_HLS_ROOT=/live-hls \
  docker compose --profile core --profile media up --build
# publish to rtmp://localhost:1935/live/<stream-key>
```

See [Live streaming](docs/features.md#live-streaming).

### Messaging & E2EE

1:1 direct messages with block enforcement, attachments (ClamAV fail-closed,
Messenger-parity limits), async OpenGraph link previews, read receipts, and per-message
delete/report. Opt-in end-to-end-encrypted conversations use client-side Olm; the
backend is only a public-key directory + opaque envelope store and cannot decrypt,
with optional disappearing messages. See [Messaging & E2EE](docs/features.md#messaging--e2ee).

### Federation & identity

ATProto / Bluesky cross-posting (a Vidra extension, off by default and independent of
ActivityPub). v1 is outbound-only: a creator links a Bluesky app password (sealed at
rest) and public videos auto-post an `app.bsky.feed.post` with a link embed. See
[Federation & identity](docs/features.md#federation--identity).

### Storage

Media flows through a small `Backend` interface: a path-traversal-safe `local` backend
or any S3-compatible store (MinIO, AWS S3, B2, Spaces) via the MinIO Go SDK. IPFS is an
orthogonal, opt-in mirror sidecar — public CIDs redirect eligible public assets to a
gateway; a fully separate `swarm.key`'d private tier replicates non-public media without
ever exposing it. See [Storage](docs/features.md#storage).

### Platform

Instance metadata + about/legal surfaces, a runtime-mutable settings overlay (edited
live via the admin API without a restart), a single error envelope
(`{"error":{"code","message","request_id"}}`), request validation with field-level
`422`s, optional SMTP email delivery, body-size/timeout request guards, and a
fail-open Redis rate limiter. See [Platform](docs/features.md#platform).

## API contract

`api/openapi.yaml` (OpenAPI 3.1, 209 paths) is the source of truth for the HTTP API and
is consumed by the `vidra-user` frontend. A drift guard keeps it honest:
`make openapi-verify` (the `TestOpenAPIContract` test) fails if a route is added,
removed, or renamed without a matching spec edit, and the `openapi.yml` CI workflow
runs the same check on every change. Lint locally with `make openapi-lint`; regenerate
the curated Postman/Newman collection under `docs/postman/` with `make postman`. The
`/metrics` endpoint is deliberately excluded from the spec.

## Observability

Structured `slog` logging is always on (`LOG_LEVEL`, `LOG_FORMAT=json|text`), one line
per request with `request_id`/`correlation_id`. Two opt-in layers add zero-cost-when-off
telemetry: Prometheus RED metrics at `GET /metrics` (`METRICS_ENABLED=true`) and OTLP
tracing (`OTEL_ENABLED=true`) for HTTP, PostgreSQL, Redis, and outbound calls. Run a
local Jaeger with `docker compose --profile core --profile otel up` (UI at
<http://localhost:16686>). Admin dashboards read `GET /api/v1/admin/system` and
`GET /api/v1/admin/jobs`. Details: [docs/operations.md](docs/operations.md) and
[docs/operational-observability-phase1.md](docs/operational-observability-phase1.md).

## Migrating from PeerTube

A one-way, read-only, idempotent, resumable, dry-runnable import brings an existing
**PeerTube** instance (PostgreSQL + media) into Vidra: accounts (bcrypt passwords kept
working), channels, videos + files/thumbnails/captions, comments, playlists, tags, and
subscriptions. Drive it from the `cmd/peertube-import` CLI, or the admin API
(server-configured source only — the browser never sends a DSN) gated by
`PEERTUBE_IMPORT_ENABLED` + `PEERTUBE_SOURCE_DATABASE_URL`. Supported schema versions
and what is imported vs. regenerated afterwards live in the operator guide
[docs/peertube-migration.md](docs/peertube-migration.md).

## Project layout

```
cmd/api/               HTTP service entrypoint (build metadata via -ldflags)
cmd/peertube-import/   one-way PeerTube importer CLI
internal/              57 packages: httpapi, auth, video, transcode, live, storage,
                       messaging, e2ee, ipfs, atproto, config, store (sqlc), …
migrations/            98 up/down migration pairs
api/openapi.yaml       OpenAPI 3.1 contract (source of truth, 209 paths)
deploy/                compose sidecar configs (ipfs, rtmp, otel, …)
docs/                  operator + feature docs (this README links here)
.ralph/specs/          product/architecture specs, preserved as docs
```

## Project docs

| Doc | What it covers |
| --- | --- |
| [docs/features.md](docs/features.md) | Full feature-by-feature reference |
| [docs/operations.md](docs/operations.md) | Backup, restore, production deploy, observability |
| [docs/peertube-migration.md](docs/peertube-migration.md) | PeerTube import operator guide |
| [docs/postman/README.md](docs/postman/README.md) | Curated Postman/Newman smoke collection |
| [.ralph/specs/architecture.md](.ralph/specs/architecture.md) | System architecture |
| [.ralph/specs/security.md](.ralph/specs/security.md) | Security model |
| [.ralph/specs/testing.md](.ralph/specs/testing.md) | Testing strategy |
| [.ralph/specs/observability.md](.ralph/specs/observability.md) | Observability design |
| [.ralph/specs/peertube-feature-ledger.md](.ralph/specs/peertube-feature-ledger.md) | PeerTube parity ledger |

## Tech stack

Go 1.26 · Echo v4 · PostgreSQL (pg_trgm, uuid-ossp) · pgx · sqlc · Redis · Docker.

## License

vidra-core is free software licensed under the [GNU Affero General Public License v3.0](LICENSE).
