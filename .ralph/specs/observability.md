# Observability spec — vidra-core (Go backend)

> Authoritative spec for logging and OpenTelemetry in `vidra-core`. Ralph must
> follow this when adding any handler, service, worker, or store code. Logging
> and tracing are part of the definition of done, not a later phase.
>
> ✅ STATUS: the mechanical **logging guards** under "Enforcement" are **built**
> (fix_plan P17.2). `internal/observability` holds the redaction denylist
> (`IsSensitiveKey`) plus two AST guards — `TestNoForbiddenLogging` (no banned
> diagnostics) and `TestNoSensitiveLogKeys` (no denylisted slog key) — which run
> under `make ci` via `go test -race ./...` (being ordinary Go tests IS the
> enforcement; no Makefile change was needed). Centralized logger construction
> (`NewLogger`) + `LOG_LEVEL`/`LOG_FORMAT` config are built too (P17.1).
> OpenTelemetry **tracing** is built (P17.3): `SetupTracing` (OTLP, no-op when
> off), `otelecho` HTTP spans, inbound W3C `traceparent`, and `trace_id`/`span_id`
> in request logs. Still planned: OTel **metrics** (RED + `METRICS_ENABLED`),
> datastore/outbound span instrumentation, and propagating the request-scoped
> logger through the service/store layers via context (P17.1).

## Goals

1. **Developer-friendly** — every request and significant event is observable
   without a debugger: structured, leveled, correlated by request and trace ID,
   readable locally and machine-parseable in production.
2. **Security-friendly** — logs and traces never become a data-leak vector:
   no secrets, credentials, tokens, or unnecessary PII ever reach a log line,
   span attribute, metric label, or error returned to a client.

---

## 1. Logging

### Library and configuration
- Use the standard library `log/slog` only. Do **not** introduce a second
  logging library, and do **not** use `fmt.Print*`, `log.Print*`, `println`, or
  `panic` for diagnostics in non-`main`, non-test code.
- A single logging setup lives in `internal/observability`:
  `NewLogger(w, level, format)` constructs the `*slog.Logger`; `cmd/api` sets it
  as `slog.Default()` and injects it into the server via `httpapi.WithLogger`.
- Configured via env (`internal/config` + `.env.example`):
  - `LOG_LEVEL` — `debug|info|warn|error` (default `info`).
  - `LOG_FORMAT` — `json` (default, production) or `text` (developer-friendly,
    for local runs).
- One log line per request is emitted by the existing Echo `RequestLogger`
  middleware (`internal/httpapi/server.go`). Keep it: method, uri, status,
  latency_ms, request_id, and `error` on failure, with level escalating by
  status class (info <400, warn 4xx, error 5xx).

### Field conventions (developer-friendly)
- Always structured key/value pairs — never interpolate values into the message.
- Reuse stable keys: `request_id`, `trace_id`, `span_id`, `actor_id` (the
  authenticated user once auth lands), `resource_type`, `resource_id`, `action`,
  `status`, `error_code`, `latency_ms`.
- Propagate the request-scoped logger (with `request_id`/`trace_id` bound)
  through `context.Context` into service and store layers so downstream logs
  correlate to the originating request.

### Security-friendly logging (hard rules)
Never log, never put in a span attribute, never use as a metric label, and never
return to a client:
- Secrets/credentials: passwords, password hashes, JWTs/refresh tokens, session
  cookies, `Authorization` / `Cookie` / `Set-Cookie` headers, API keys, OAuth
  client secrets, TOTP seeds, signing keys, wallet private keys, stream keys.
- Full request or response bodies, and full header dumps.
- PII beyond what an event needs: do not log raw email addresses, full IPs, or
  message contents. Prefer IDs (`actor_id`) over identifying values; when an
  identifier must be logged for abuse/security investigation, document it in
  this spec and the security spec.
- A redaction helper in `internal/observability` (e.g. `Redact(...)`) provides
  the canonical denylist of sensitive field names; config/struct logging must
  route through it. `config` must never be logged as a whole struct.
- 5xx error detail is logged operator-side only; the client receives the generic
  `ErrorResponse` envelope (already enforced in `internal/httpapi/errors.go`).

### Audit logging (security events)
- Security-sensitive actions (login success/failure, logout, password/email
  change, token issuance/revocation, role changes, admin actions, moderation
  decisions, account deletion) emit a typed **audit event** — durable, distinct
  from request logs, never containing secrets.
- Minimum audit fields: `actor_id`, `action`, `resource_type`, `resource_id`,
  `result` (allow/deny/success/failure), `request_id`, `occurred_at`, and a
  safe `reason`/`detail`. (Implementation table tracked in fix_plan P15/P17.)
- Every sensitive action added must ship with an audit-event test asserting the
  event is recorded and contains no denylisted field.

---

## 2. OpenTelemetry

### Scope
- Use the OpenTelemetry Go SDK for **traces** and **metrics**, and correlate
  **logs** to traces. Disabled by default; opt-in via config so local dev and
  unit tests pay zero cost (no-op providers).
- Config (add to `internal/config` + `.env.example`):
  - `OTEL_ENABLED` — bool, default `false`.
  - `OTEL_EXPORTER_OTLP_ENDPOINT` — e.g. `http://localhost:4317`.
  - `OTEL_EXPORTER_OTLP_PROTOCOL` — `grpc` (default) or `http/protobuf`.
  - `OTEL_SERVICE_NAME` — default `vidra-core`.
  - `METRICS_ENABLED` — bool, default `false`.
- Setup + graceful shutdown live in `internal/observability` and are wired in
  `cmd/api/main.go` (and the worker entrypoint) alongside the existing
  Postgres/Redis lifecycle.

### Tracing
- Instrument the HTTP server with the OTel Echo middleware so every request is a
  root/child span carrying `request_id`.
- **Trust boundary for propagation**: accept inbound W3C `traceparent`/`tracestate`
  from `vidra-user` so frontend→backend traces correlate end to end (see the
  matching rule in `vidra-user/.ralph/specs/observability.md`). Outbound calls
  the backend makes (federation, link-preview/import fetches) must inject
  context. Never trust client-supplied span content as security input.
- Add spans around datastore work (pgx queries, Redis ops) and external HTTP.

### Metrics
- Export RED-style metrics: request count by route+status, request latency
  histogram, error rate; datastore call duration; plus key business counters as
  features land (uploads, transcode jobs, federation deliveries).
- Metric label cardinality must stay bounded — never use IDs, tokens, or raw
  URLs (use the route template) as labels; never a secret as a label.
- A metrics surface (`/metrics` Prometheus scrape or OTLP push) is gated behind
  `METRICS_ENABLED`; document any HTTP route for it in `api/openapi.yaml`.

### Logs ↔ traces
- When `OTEL_ENABLED`, every slog line emitted within a request must include
  `trace_id` and `span_id` from the active span context.
- **Correlation header (BUILT)**: `internal/httpapi/correlation.go` accepts an
  inbound `X-Correlation-ID` request header from `vidra-user`, mints one from the
  server request ID when absent, sanitises it (untrusted client input — only
  URL-safe token chars, bounded to 128, so no CR/LF or log injection), echoes it
  on the response, and threads it through the request context so the request
  logger emits it as `correlation_id`. This is the OTel-off correlation contract
  that pairs with `vidra-user`'s spec — same header name on both sides. Works with
  OTel off; the `trace_id`/`span_id` correlation above layers on top when OTel lands.

---

## 3. Enforcement

The two logging guards below are **built** (`internal/observability/logging_guard_test.go`)
and run under `make ci` (`go test -race ./...`). The OTel/metrics enforcement is
still planned (fix_plan P17.3).

- **Banned-logging guard (built, P17.2)** — `TestNoForbiddenLogging` parses every
  non-test, non-`package main` source file in the module and fails the build if
  it uses `fmt.Print*`, `log.Print*`/`log.Fatal*`/`log.Panic*`, or the
  `println`/`print` builtins for diagnostics, steering all logging through `slog`.
  (`fmt.Fprint*`, `fmt.Sprint*`, and `fmt.Errorf` are intentionally allowed.)
- **Secrets-in-logs guard (built, P17.2)** — `TestNoSensitiveLogKeys` fails when a
  denylisted sensitive field name (`IsSensitiveKey`: password, token, secret,
  authorization, cookie, private_key, stream_key, …) is used as a structured-log
  key — inline in an slog key/value call, at an even index of a `[]any{}` args
  slice (the slog args-builder idiom), or as an slog attribute-constructor key
  (`slog.String("token", …)`). Config/secret structs must never be logged whole.
- **OTel (planned, P17.3)** — once added, OTel must compile and initialise (no-op
  path) cleanly in tests as part of `make ci`.
- **Release gates** (fix_plan P19) include: structured logging configurable via
  `LOG_LEVEL`/`LOG_FORMAT`; no denylisted data in logs/spans/labels; audit
  events exist and are tested for sensitive actions; OTel traces/metrics behind
  config flags with logs carrying `trace_id` when enabled.

See `.ralph/specs/security.md` (no-secrets-in-logs, audit logging) and the
per-loop documentation rule in `.ralph/PROMPT.md`.
