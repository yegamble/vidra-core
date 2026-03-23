# Docker / Remote Service Combination Test Plan

This document maps every combination of Docker vs. remote (external/managed) services
that the setup wizard and CLI can produce. The goal: verify that only the required
Docker containers start, and no unused containers waste resources.

## Service Inventory

### Services with Docker / External Toggle

| # | Service | Config Key | Docker Container | External Alternative | Compose Profile |
|---|---------|-----------|-----------------|---------------------|-----------------|
| 1 | PostgreSQL | `POSTGRES_MODE` | `postgres:15-alpine` | Any Postgres 13+ URL | _(default)_ |
| 2 | Redis | `REDIS_MODE` | `redis:7-alpine` | Any Redis URL | _(default)_ |
| 3 | IPFS | `IPFS_MODE` | `ipfs/kubo:v0.32.1` | External IPFS API | `ipfs` / `full` |
| 4 | IOTA | `IOTA_MODE` | `iotaledger/iota-node` | External JSON-RPC | `iota` / `full` |
| 5 | Email | `SMTP_MODE` | Mailpit | External SMTP server | `mail` / `full` |
| 6 | Nginx | `NGINX_ENABLED` | nginx:1.27-alpine | External reverse proxy | _(default)_ |

### Services with Enable/Disable Only (always Docker when enabled)

| # | Service | Config Key | Docker Container | Compose Profile |
|---|---------|-----------|-----------------|-----------------|
| 7 | ClamAV | `ENABLE_CLAMAV` | `clamav/clamav:stable` | `media` / `full` |
| 8 | Whisper | `ENABLE_WHISPER` | whisper ASR | `media` / `full` |
| 9 | Certbot | `NGINX_TLS_MODE=letsencrypt` | `certbot/certbot` | `letsencrypt` |

### Services with No Docker Component

| Service | Config Key | Notes |
|---------|-----------|-------|
| ActivityPub | `ENABLE_ACTIVITYPUB` | Config only, runs in app binary |
| ATProto | `ENABLE_ATPROTO` | Config only, connects to external PDS |
| Live Streaming | `ENABLE_LIVE_STREAMING` | Built into app binary |
| WebTorrent | `ENABLE_TORRENTS` | Built into app binary |
| S3 Storage | `ENABLE_S3` | Always external endpoint |
| Backup | `BACKUP_ENABLED` | Target: local / S3 / SFTP |

## Two-Layer Control Mechanism

Container lifecycle is controlled by two complementary mechanisms:

### Layer 1: `COMPOSE_PROFILES` (in `.env`)

Written by `WriteEnvFile()`. Activates Docker Compose profiles for optional services.

| Condition | Profile Added |
|-----------|--------------|
| IPFS enabled + docker mode | `ipfs` |
| IOTA enabled + docker mode | `iota` |
| ClamAV enabled | `media` |
| Whisper enabled | `media` |
| Email enabled + docker mode | `mail` |
| Nginx TLS = letsencrypt | `letsencrypt` |

### Layer 2: `docker-compose.override.yml`

Written by `WriteComposeOverride()`. Sets `profiles: ["disabled"]` for services that
should NOT start, even if a user runs `docker compose --profile full up`.

This is the safety net: even `--profile full` won't start external/disabled services.

## Test Scenarios

### Scenario Matrix

Each row is a test scenario. `D` = Docker, `E` = External, `OFF` = Feature disabled, `-` = N/A.

| # | Scenario | PG | Redis | Nginx | IPFS | IOTA | ClamAV | Whisper | Email | Docker Containers Running |
|---|----------|-----|-------|-------|------|------|--------|---------|-------|--------------------------|
| 1 | All Docker | D | D | D | D | D | D | D | D | postgres, redis, nginx, app, ipfs, iota-node, clamav, whisper, mailpit |
| 2 | All External | E | E | OFF | E | E | OFF | OFF | E | app only |
| 3 | Minimal Dev | D | D | D | OFF | OFF | OFF | OFF | OFF | postgres, redis, nginx, app |
| 4 | Core Docker + Opt External | D | D | D | E | E | OFF | OFF | E | postgres, redis, nginx, app |
| 5 | Core External + Opt Docker | E | E | D | D | D | D | D | D | nginx, app, ipfs, iota-node, clamav, whisper, mailpit |
| 6 | Mixed: PG Docker, Redis Ext | D | E | D | D | OFF | OFF | OFF | OFF | postgres, nginx, app, ipfs |
| 7 | Mixed: PG Ext, Redis Docker | E | D | D | OFF | D | OFF | OFF | OFF | redis, nginx, app, iota-node |
| 8 | Production-like | E | E | OFF | E | OFF | OFF | OFF | E | app only |
| 9 | Dev + Email | D | D | D | OFF | OFF | OFF | OFF | D | postgres, redis, nginx, app, mailpit |
| 10 | Media Stack | D | D | D | D | OFF | D | D | OFF | postgres, redis, nginx, app, ipfs, clamav, whisper |
| 11 | Payments Stack | D | D | D | OFF | D | OFF | OFF | OFF | postgres, redis, nginx, app, iota-node |
| 12 | No Nginx | D | D | OFF | OFF | OFF | OFF | OFF | OFF | postgres, redis, app |

### Expected `COMPOSE_PROFILES` Per Scenario

| # | Scenario | COMPOSE_PROFILES |
|---|----------|-----------------|
| 1 | All Docker | `ipfs,iota,mail,media` |
| 2 | All External | _(empty)_ |
| 3 | Minimal Dev | _(empty)_ |
| 4 | Core Docker + Opt External | _(empty)_ |
| 5 | Core External + Opt Docker | `ipfs,iota,mail,media` |
| 6 | Mixed: PG Docker, Redis Ext | `ipfs` |
| 7 | Mixed: PG Ext, Redis Docker | `iota` |
| 8 | Production-like | _(empty)_ |
| 9 | Dev + Email | `mail` |
| 10 | Media Stack | `ipfs,media` |
| 11 | Payments Stack | `iota` |
| 12 | No Nginx | _(empty)_ |

### Expected Disabled Services in `docker-compose.override.yml`

| # | Scenario | Disabled Services |
|---|----------|------------------|
| 1 | All Docker | _(none)_ |
| 2 | All External | postgres, redis, nginx, ipfs, iota-node, clamav, whisper, mailpit |
| 3 | Minimal Dev | ipfs, iota-node, clamav, whisper, mailpit |
| 4 | Core Docker + Opt External | ipfs, iota-node, clamav, whisper, mailpit |
| 5 | Core External + Opt Docker | postgres, redis |
| 6 | Mixed: PG Docker, Redis Ext | redis, iota-node, clamav, whisper, mailpit |
| 7 | Mixed: PG Ext, Redis Docker | postgres, ipfs, clamav, whisper, mailpit |
| 8 | Production-like | postgres, redis, nginx, ipfs, iota-node, clamav, whisper, mailpit |
| 9 | Dev + Email | ipfs, iota-node, clamav, whisper |
| 10 | Media Stack | iota-node, mailpit |
| 11 | Payments Stack | ipfs, clamav, whisper, mailpit |
| 12 | No Nginx | nginx, ipfs, iota-node, clamav, whisper, mailpit |

### Expected App `depends_on` Per Scenario

| # | PG Mode | Redis Mode | App depends_on |
|---|---------|-----------|---------------|
| 1-4, 6, 9-12 | docker | docker | `postgres: service_healthy`, `redis: service_healthy` |
| 6 | docker | external | `postgres: service_healthy` |
| 7 | external | docker | `redis: service_healthy` |
| 2, 5, 8 | external | external | `{}` (no dependencies) |

## Validation Commands

### Unit Tests

```bash
# Run compose override tests
go test ./internal/setup/ -run TestWriteComposeOverride -v

# Run compose profiles tests (env file)
go test ./internal/setup/ -run TestWriteEnvFile_ComposeProfiles -v

# Run all setup tests
go test ./internal/setup/ -v
```

### Manual Docker Validation

For each scenario above, verify correct containers start:

```bash
# 1. Generate config for the scenario (wizard or .env)
# 2. Generate override
# 3. Check which containers would start:
docker compose config --services

# 4. Start and verify:
docker compose up -d
docker compose ps

# 5. Verify no unexpected containers:
# Compare running containers against the "Docker Containers Running" column
```

### Automated Smoke Test (CI)

```bash
# For each scenario, generate .env and override, then verify config output:
docker compose --env-file .env.scenario-N config --services | sort > actual.txt
diff expected-scenario-N.txt actual.txt
```

## Combination Count

| Dimension | Options | Count |
|-----------|---------|-------|
| PostgreSQL | docker, external | 2 |
| Redis | docker, external | 2 |
| Nginx | enabled, disabled | 2 |
| IPFS | disabled, docker, external | 3 |
| IOTA | disabled, docker, external | 3 |
| ClamAV | disabled, enabled | 2 |
| Whisper | disabled, enabled | 2 |
| Email | disabled, docker, external | 3 |
| **Total** | | **2 x 2 x 2 x 3 x 3 x 2 x 2 x 3 = 864** |

The 12 scenarios above cover the key boundary conditions. Full combinatorial
testing is impractical; the table-driven unit tests in `compose_override_test.go`
verify the override generation logic for each service toggle independently,
and the scenario tests verify representative real-world configurations.

## Files

| File | Purpose |
|------|---------|
| `internal/setup/compose_override.go` | Generates `docker-compose.override.yml` |
| `internal/setup/compose_override_test.go` | Combination tests (12 scenarios + toggle matrix) |
| `internal/setup/writer.go` | Generates `.env` with `COMPOSE_PROFILES` |
| `internal/setup/writer_compose_profiles_test.go` | Profile generation tests |
| `docker-compose.yml` | Service definitions with profiles |
| `docker-compose.override.yml.example` | Override template |
