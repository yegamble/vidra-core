# vidra-core operations guide

Operator runbook for the Go backend: backups, restore, observability surfaces,
and production deployment notes. The end-to-end single-host deployment (TLS proxy,
env matrix, staging→prod promotion) lives in the meta-repo
[`deploy/README.md`](../../deploy/README.md) and
[`.ralph/specs/environments.md`](../.ralph/specs/environments.md); this file is the
backend-specific companion.

## System of record vs derived state

| Store | Role | Backup? |
|-------|------|---------|
| **PostgreSQL** | Durable system of record (users, videos, jobs, moderation, federation). | **Yes — authoritative.** |
| **Media blobs** (local disk / S3 / IPFS) | Uploaded + transcoded media, thumbnails, captions, export archives. | **Yes — must stay consistent with the DB.** |
| **Redis** | Cache, rate-limit counters, idempotency keys, view-dedupe, short locks. | **No.** Ephemeral; safe to flush. It is never the source of truth for data that must survive a restart. |

Back up PostgreSQL and media on the **same cadence** so blob references in the DB
resolve after a restore.

## PostgreSQL backup & restore

Custom-format dump (compressed, supports selective/parallel restore):

```bash
# Backup (nightly). Keep e.g. 14 daily + 8 weekly.
docker exec vidra-core-postgres-1 \
  pg_dump -U vidra -Fc vidra > vidra-$(date +%F).dump

# Restore into a fresh/empty database.
docker exec -i vidra-core-postgres-1 \
  pg_restore -U vidra -d vidra --clean --if-exists < vidra-2026-07-03.dump
```

Migrations are forward-only and are applied by the `migrate` compose service (or
`make migrate-up`). They are proven to apply cleanly against **both** an empty DB
and a **populated** one (`TestMigrationsAgainstExistingFixture`,
`internal/store`), so restoring an older dump and letting migrations catch up is
safe.

## Media backup — per storage backend

- **`STORAGE_BACKEND=s3`** (production default): rely on the object store's own
  versioning/replication/lifecycle rules (AWS S3, Backblaze B2, DigitalOcean
  Spaces). Nothing bespoke to run; keep the bucket's retention aligned with the
  DB retention. Credentials (`STORAGE_S3_ACCESS_KEY`/`STORAGE_S3_SECRET_KEY`) are
  secrets — never commit or log them.
- **`STORAGE_BACKEND=local`** (dev / small single-host): snapshot the media volume
  (`docker volume` or a filesystem snapshot) on the same schedule as the DB dump.
- **IPFS**: content is addressed by CID and pinned; ensure the pinning service /
  local node persists its datastore. CIDs are recorded in PostgreSQL, so the DB
  dump plus a healthy pinset reconstructs availability.

## Restore drill

Quarterly: restore the latest DB dump + media snapshot into a throwaway stack,
run `make migrate-up`, boot the api, and verify `/readyz` is 200 and a known
video plays. A backup you have never restored is not a backup.

## Observability surfaces (P17)

All opt-in and zero-cost when disabled.

- **Structured logs**: `LOG_LEVEL` (`debug|info|warn|error`) + `LOG_FORMAT`
  (`json` prod / `text` dev). One `slog` line per request with `request_id`,
  `correlation_id`, and (with tracing on) `trace_id`/`span_id`. No secrets/PII —
  enforced by the `TestNoSensitiveLogKeys` / `TestNoForbiddenLogging` guards in
  `make ci`.
- **Metrics** (`METRICS_ENABLED=true`): Prometheus RED metrics at **`GET /metrics`**
  (`vidra_http_requests_total`, `vidra_http_request_duration_seconds` labelled by
  method / route template / status class — bounded cardinality, no ids or raw
  URLs) plus a `vidra_queue_depth{queue,state}` gauge. The endpoint is
  unauthenticated and lives at the root like the health probes — **network-scope
  it** (internal Prometheus scrape only), do not expose it publicly. It is
  intentionally omitted from the public OpenAPI contract (an ops surface).
- **Tracing** (`OTEL_ENABLED=true`): OTLP spans for HTTP requests, PostgreSQL
  queries, Redis commands, and outbound HTTP (federation/import/whisper/atproto),
  with inbound + outbound W3C `traceparent` propagation. Run a local backend with
  `docker compose --profile core --profile otel up` and set
  `OTEL_EXPORTER_OTLP_ENDPOINT=otel-collector:4317`; view traces at
  http://localhost:16686 (Jaeger).
- **Admin dashboards**: `GET /api/v1/admin/system` (build/uptime/dependency
  health) and `GET /api/v1/admin/jobs` (per-queue depth + oldest-pending age +
  recent failures) back the admin ops pages. A rising `oldest_pending_age_seconds`
  or growing `pending` is the stuck-worker signal; dead-lettered jobs appear as
  `failed` + in `recent_failures`.

## Production deployment notes

The reference deployment is one host per environment behind a TLS proxy — see
[`deploy/README.md`](../../deploy/README.md). Backend-specific fail-secure rules
enforced at boot in `VIDRA_ENV=production`:

- Refuses the dev `JWT_SECRET`; requires a strong secret.
- Refuses `DEV_MAIL_CAPTURE_ENABLED`.
- Requires `FEDERATION_KEY_KEK` when `FEDERATION_ENABLED=true` (actor private keys
  are envelope-encrypted at rest); likewise set `MFA_KEY_KEK` for TOTP secrets.
- Marks auth cookies `Secure`; `PUBLIC_BASE_URL` / `CORS_ALLOWED_ORIGINS` must
  match the public domains.
- Keep `HTTP_IMPORT_ALLOW_PRIVATE_URLS=false` (the SSRF guard) — it is a dev/e2e
  escape hatch only.

Health/readiness for orchestrators: `GET /healthz` (liveness) and `GET /readyz`
(503 until PostgreSQL + Redis are reachable). See the top-level
[`README.md`](../README.md) for the full env-var and endpoint reference.
