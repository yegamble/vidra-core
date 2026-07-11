# Operational observability — Phase 1

This document records the first implementation boundary for Vidra's operator
surfaces. The running source and migrations remain authoritative.

## Three separate, correlated streams

Vidra deliberately keeps these records separate:

- **Job events** explain where background work is, how it progressed, and why it
  retried or failed.
- **System logs** diagnose software and infrastructure behavior. They remain
  structured `slog` output until a safe multi-instance searchable sink exists.
- **Security audit events** record who changed a protected resource and the
  outcome. Routine, high-volume content activity does not belong in this
  long-retention ledger.

The common correlation vocabulary is `request_id`, `correlation_id`,
`trace_id`, `pipeline_run_id`, `job_id`, `actor_id`, `resource_type`, and
`resource_id`. An identifier may be absent when the originating legacy worker
does not yet carry it; it must never be invented from an unrelated value.

## Phase 1 delivery stages

1. **Migration** — add `pipeline_runs`, `job_runs`, and append-only
   `job_events`. Metadata is JSON-object-only and byte-bounded. Error class,
   code, and detail are separately bounded; error detail is sanitized before it
   is stored. Index time, state/type/queue, resource, worker, correlation, stale
   leases, and the global job-event replay sequence.
2. **Compatibility adapters** — keep each existing queue table authoritative
   and transactionally project only executions with an honest one-row/one-run
   mapping. Never copy an inbox/source URL, storage key, payload, credentials,
   signed URL, email/message body, process output, or arbitrary arguments.
3. **Read API** — preserve the existing aggregate `GET /api/v1/admin/jobs` and
   add an individual filtered/paginated run list, run detail with bounded event
   history, and a resumable event stream.
4. **Admin UI** — retain queue summary cards, add individual executions and
   filters, render metadata/errors as escaped text, and combine live updates
   with manual refresh and polling fallback.
5. **Verification** — migration/query integration tests, service and handler
   tests (including RBAC and replay), OpenAPI drift checks, generated frontend
   types, client/parser tests, mocked browser tests, then the broad backend and
   frontend gates.

## Job semantics and transitional limits

Canonical states are `queued`, `claimed`, `running`, `retry_scheduled`,
`succeeded`, `failed`, `dead_lettered`, `cancel_requested`, and `cancelled`. A
legacy terminal `failed` row maps to `dead_lettered`; a failed attempt scheduled
for another try maps to `retry_scheduled`.
`progress_percent` is 0–100. Unknown progress stays unknown/zero rather than
being inferred from elapsed time.

The compatibility projection uses a unique `(queue, source_id)` identity so
duplicate delivery is idempotent. A durable global `job_events` sequence is the
SSE replay cursor; the event UUID remains the stable event identity. REST
snapshots return an `event_cursor` sampled before the data read, so an event
racing the snapshot can be replayed (duplicates are harmless; gaps are not).

The following are not safe to claim as unified executions yet:

- `channel_syncs` describes a recurring schedule, not one row per sync pass;
- scheduled publish, media GC, upload/E2EE sweeps, and live replay do not have a
  durable execution row;
- IPFS pin rows are a mutable object-action ledger and may represent both pin
  and unpin work during their lifetime;
- most queue claims have no worker lease, heartbeat, or abandoned-run reclaim;
- some enqueue hooks execute after the domain transaction and can be lost if
  the process fails between the domain write and enqueue.

Those workers need native adapters or execution tables, transactional
enqueue/outbox writes, idempotency keys, `FOR UPDATE SKIP LOCKED` claims,
bounded leases, heartbeats, and reclaim tests. The Phase 1 schema contains the
fields needed for that migration, but nullable fields do not imply a guarantee.

## API and stream behavior

The individual-run list filters by state, type, queue, resource, worker,
failure, and creation time. Run detail includes correlation fields, lease and
heartbeat timestamps, bounded sanitized metadata/error information, and event
history.

The SSE endpoint:

- is admin-only and accepts the durable cursor through `Last-Event-ID` (with a
  non-secret `after` query fallback);
- emits persisted events in ascending sequence order and sends comment
  heartbeats;
- disables proxy transformation/buffering and flushes each event;
- has a short bounded lifetime, then reconnects so the bearer token and current
  admin role are revalidated;
- is exempt from the ordinary REST deadline only for that bounded stream
  lifetime.

The browser uses authenticated streaming `fetch`, not native `EventSource`:
Vidra keeps the bearer token in memory and never puts it in a URL. REST remains
authoritative. Stream events debounce a REST refresh, reconnect with bounded
backoff, and fall back to periodic polling while the page is visible. Manual
refresh is always available.

## Controls

Phase 1 is read-only. Retry, cancel, pause, and resume are not exposed merely
because a row can be edited. They require per-job capability semantics, an
idempotent operation, ownership/authorization rules, audit events, and workers
that observe the control safely. Global queue pause/resume is deferred because
Vidra's independent ticker workers do not share BullMQ-style pause semantics.

## Retention and metrics

Job events and terminal runs use separate retention windows and 10,000-row
pruning batches; active work is never age-pruned. Phase 1 exposes DB-derived
oldest-queued-age and stale-running gauges. Native worker adapters must still
add honest monotonic retry/failure counters and duration histograms. Labels are
limited to stable job type/queue/state/error class — never an ID, URL, actor,
correlation value, or error text.

## System logs follow-up

PeerTube reads rotated local JSON files and refreshes its web view manually.
Vidra needs a multi-replica-safe design, so Phase 1 does not expose current
stdout lines directly. Before adding a System Logs API, the backend must have:

- path/route logging that never records raw query values;
- a central recursive value sanitizer and record-size bound (key denylisting is
  insufficient for secrets embedded in URLs and error strings);
- a non-blocking bounded sink with a drop counter and no recursive self-logging;
- independent retention and batched pruning;
- admin-only time/level/message/tag/correlation filters and keyset pagination.

Stdout remains canonical for deployment log collection. A process-local ring
buffer is not an acceptable substitute for restart and multi-replica history.

## Audit foundation and activity follow-up

Migration 0084 evolves the existing security ledger compatibly to a versioned
typed envelope. New rows carry a safe actor snapshot (user UUID/kind/role only),
domain/action/outcome/resource identifiers, request/correlation/trace/job links,
strictly allowlisted bounded metadata, and safe before/after scalar changes. DB
checks provide a final shape/size backstop. Existing `action`, `result`,
`actor_id`, `reason`, and `request_id` fields and the admin API remain compatible.

Audit persistence is still best-effort after most business operations; the new
schema does not claim transaction coupling. Free-form moderation prose and
resource bodies stay in their domain tables. Routine video, channel, comment,
playlist, upload, publish, processing, and federation lifecycle events belong in
a separate, shorter-retention activity stream when its transactional
outbox/unit-of-work seam is built.
