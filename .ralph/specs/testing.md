# Vidra Core — Testing

Status: living document. Tests serve implementation; they are not busywork.

## Layers

- **Unit** — pure logic and handlers with test doubles. Fast, no external deps.
  Example: `internal/config` (env parsing/validation),
  `internal/httpapi` (health/readiness/nodeinfo via `httptest` + fake pingers).
- **Integration** — against live PostgreSQL + Redis (Docker Compose or CI service
  containers). Added with the first DB-backed feature (auth).
- **Migration** — apply all up-migrations against a fresh database. Enforced in
  CI (`backend-ci.yml`) before tests run.
- **API smoke / Newman** — Postman collection in `api/` once endpoints exist.
- **Fuzz** — URL normalization, SSRF filters, AP/ATProto payloads, media paths,
  import/link-preview inputs (added with those subsystems).
- **Benchmarks** — auth checks, feed queries, search, permission checks, status
  lookups (added with those hot paths).

## How to run

```bash
make check        # fmt + vet + unit tests (fast local gate)
make test-race    # race detector
make cover        # coverage summary
make up           # full Docker stack (postgres, redis, migrate, api)
```

CI (`backend-ci.yml`) runs: gofmt check, `go vet`, fresh-DB migration, and
`go test -race ./...` with Postgres + Redis service containers.

## Conventions

- A feature is not complete if only mocks pass when it needs a live service.
- Tests that require Docker document the command + profile here and in AGENT.md.
- Prefer behavior assertions over coverage-chasing.
- Keep the full gate green before flipping any fix_plan item to done; record
  anything not run.

## Media/transcoding integration tests (ffmpeg-gated)

The HLS transcoding pipeline keeps exec out of the unit gate: ladder planning,
ffmpeg argument building, and master-playlist rendering are pure functions
unit-tested in `internal/media/hls_test.go`, and the worker state machine
(claim/backoff/dead-letter) is unit-tested with a fake runner in
`internal/transcode`. The real exec + DB paths run under `-tags=integration`
(`make test-integration`), each self-skipping when its dependency is absent:

- `internal/media` `TestHLSTranscoderRealVideo` — real ffmpeg/ffprobe on a 2s
  320x240 testsrc fixture → storage assertions (needs ffmpeg on PATH).
- `internal/transcode` queue tests — `transcode_jobs` + `streaming_playlists` +
  `video_renditions` against real PostgreSQL (needs `DATABASE_URL`).
- `internal/httpapi` `TestHLSPipelineEndToEnd` — HTTP upload → publish-hook
  enqueue → `DrainJobs` with the real transcoder → the `/hls/*` endpoints serve
  master/variant/segments with correct content types (needs ffmpeg on PATH).

## S3 storage integration tests (MinIO-gated)

The S3-compatible storage backend (`internal/storage.S3`) keeps the network out
of the unit gate: config validation, the invalid-key contract
(`ErrInvalidKey` before any request), and the not-a-PathProvider design pin are
unit-tested in `internal/storage/s3_test.go`, and `internal/httpapi`
`TestStreamOriginalRangeViaSeekableBackend` proves Range/206 serving through a
seekable path-less backend with a fake. The real-store round trip runs under
`-tags=integration`, gated on `S3_TEST_ENDPOINT` (self-skips when unset):

```bash
docker compose --profile storage up -d minio
S3_TEST_ENDPOINT=localhost:9000 go test -tags=integration ./internal/storage/...
```

Covers Put/Open/Exists/Delete round trip, overwrite, idempotent delete,
`ErrNotFound`, `http.ServeContent` Range/206 through the real seekable object
reader (`TestS3ServeContentRange`), and the probe/transcode temp-download
fallback contract. Optional env overrides: `S3_TEST_ACCESS_KEY` /
`S3_TEST_SECRET_KEY` / `S3_TEST_BUCKET` / `S3_TEST_USE_SSL` (defaults match the
compose minio service: vidra / vidra-dev-secret / vidra-test / false).

## Dev-only mail capture (email-token test seam)

The account-security token flows (password reset, email verification) deliver a
single-use raw token out-of-band via the `Mailer` adapter (a no-op by default),
storing only its SHA-256 hash. That makes the *confirm* steps impossible to drive
from an automated end-to-end test without a real mailer.

`DEV_MAIL_CAPTURE_ENABLED=true` (default false) wires a `CaptureMailer`
(`internal/auth/capture.go`) that holds the most recent raw token per
(kind, email) **in memory** — never logged, never written to disk or the DB — and
registers `GET /api/v1/dev/email-token?email=<email>&kind=reset|verification`,
which returns `{"token":"…"}` (200), 404 when nothing is captured, or 422 on a bad
request. The route exists **only** when the flag is on (so production never carries
it), and the process logs a loud WARN on boot.

This is a deliberate test seam and is **intentionally excluded from
`api/openapi.yaml`** (not a public contract surface); `TestOpenAPIContract` does
not mount it because `fullRouteOptions` omits the option. The dev compose stack
passes it through (`DEV_MAIL_CAPTURE_ENABLED` in `docker-compose.yml`) so the
frontend backend-backed suite can complete reset/verify round trips. NEVER enable
it in production — it exposes single-use credentials.

## Current status

- Unit tests passing: `internal/config`, `internal/httpapi`.
- Integration/migration tests: scaffolded in CI; first DB-backed suite arrives
  with the auth slice.
