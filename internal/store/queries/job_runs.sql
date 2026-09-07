-- Unified operational job-run read side (migration 0083). The source queue
-- tables remain authoritative for execution; these queries expose only the
-- sanitized projection maintained transactionally by database triggers.

-- name: ListOperationalJobRuns :many
SELECT *
FROM job_runs j
WHERE (sqlc.narg('state')::text IS NULL OR state = sqlc.narg('state'))
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('queue')::text IS NULL OR queue = sqlc.narg('queue'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('resource_id')::text IS NULL OR resource_id = sqlc.narg('resource_id'))
  AND (sqlc.narg('worker_id')::text IS NULL OR worker_id = sqlc.narg('worker_id'))
  AND (sqlc.narg('failure')::boolean IS NULL
       OR ((state IN ('failed', 'dead_lettered')) = sqlc.narg('failure')))
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR created_at >= sqlc.narg('created_after'))
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR created_at <= sqlc.narg('created_before'))
  AND NOT (j.queue = 'transcode_jobs' AND EXISTS (
      SELECT 1 FROM job_runs child WHERE child.parent_job_id = j.id
  ))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountOperationalJobRuns :one
SELECT count(*)::bigint
FROM job_runs j
WHERE (sqlc.narg('state')::text IS NULL OR state = sqlc.narg('state'))
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('queue')::text IS NULL OR queue = sqlc.narg('queue'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('resource_id')::text IS NULL OR resource_id = sqlc.narg('resource_id'))
  AND (sqlc.narg('worker_id')::text IS NULL OR worker_id = sqlc.narg('worker_id'))
  AND (sqlc.narg('failure')::boolean IS NULL
       OR ((state IN ('failed', 'dead_lettered')) = sqlc.narg('failure')))
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR created_at >= sqlc.narg('created_after'))
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR created_at <= sqlc.narg('created_before'))
  AND NOT (j.queue = 'transcode_jobs' AND EXISTS (
      SELECT 1 FROM job_runs child WHERE child.parent_job_id = j.id
  ));

-- name: GetOperationalJobRun :one
SELECT * FROM job_runs WHERE id = $1;

-- name: ListOperationalJobEvents :many
SELECT *
FROM job_events
WHERE job_id = sqlc.arg('job_id')
  AND cursor <= sqlc.arg('through_cursor')
ORDER BY cursor DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountOperationalJobEvents :one
SELECT count(*)::bigint
FROM job_events
WHERE job_id = sqlc.arg('job_id')
  AND cursor <= sqlc.arg('through_cursor');

-- name: ListOperationalJobEventsAfter :many
SELECT e.*
FROM job_events e
JOIN job_runs j ON j.id = e.job_id
WHERE e.cursor > sqlc.arg('after_cursor')
  AND (sqlc.narg('job_id')::uuid IS NULL OR e.job_id = sqlc.narg('job_id'))
  AND (sqlc.narg('state')::text IS NULL OR j.state = sqlc.narg('state'))
  AND (sqlc.narg('type')::text IS NULL OR j.type = sqlc.narg('type'))
  AND (sqlc.narg('queue')::text IS NULL OR j.queue = sqlc.narg('queue'))
  AND (sqlc.narg('resource_type')::text IS NULL OR j.resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('resource_id')::text IS NULL OR j.resource_id = sqlc.narg('resource_id'))
  AND (sqlc.narg('worker_id')::text IS NULL OR j.worker_id = sqlc.narg('worker_id'))
  AND (sqlc.narg('failure')::boolean IS NULL
       OR ((j.state IN ('failed', 'dead_lettered')) = sqlc.narg('failure')))
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR j.created_at >= sqlc.narg('created_after'))
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR j.created_at <= sqlc.narg('created_before'))
ORDER BY e.cursor
LIMIT sqlc.arg('result_limit');

-- name: MaxOperationalJobEventCursor :one
SELECT COALESCE(max(cursor), 0)::bigint FROM job_events;

-- name: MinOperationalJobEventCursor :one
SELECT COALESCE(min(cursor), 0)::bigint FROM job_events;

-- name: OperationalJobQueueMetrics :many
SELECT
    queue,
    COALESCE(EXTRACT(EPOCH FROM (now() - min(created_at)
        FILTER (WHERE state IN ('queued', 'retry_scheduled'))))::bigint, 0)::bigint
        AS oldest_queued_age_seconds,
    count(*) FILTER (
        WHERE state IN ('claimed', 'running')
          AND ((lease_expires_at IS NOT NULL AND lease_expires_at < now())
               OR (lease_expires_at IS NULL
                   AND updated_at < now() - (sqlc.arg('stale_seconds')::int * interval '1 second')))
    )::bigint AS stale_running
FROM job_runs
GROUP BY queue
ORDER BY queue;

-- name: PruneOperationalJobEvents :execrows
DELETE FROM job_events
WHERE cursor IN (
    SELECT e.cursor
    FROM job_events e
    JOIN job_runs j ON j.id = e.job_id
    WHERE e.occurred_at < sqlc.arg('cutoff')
      AND j.state IN ('succeeded', 'failed', 'dead_lettered', 'cancelled')
    ORDER BY e.cursor
    LIMIT sqlc.arg('batch_size')
);

-- name: PruneTerminalOperationalJobRuns :execrows
DELETE FROM job_runs target
WHERE target.id IN (
    SELECT candidate.id FROM job_runs candidate
    WHERE candidate.state IN ('succeeded', 'failed', 'dead_lettered', 'cancelled')
      AND candidate.finished_at IS NOT NULL
      AND candidate.finished_at < sqlc.arg('cutoff')
    ORDER BY candidate.finished_at, candidate.id
    LIMIT sqlc.arg('batch_size')
);

-- name: PruneTerminalPipelineRuns :execrows
DELETE FROM pipeline_runs p
WHERE p.id IN (
    SELECT candidate.id FROM pipeline_runs candidate
    WHERE candidate.state IN ('succeeded', 'failed', 'cancelled', 'partial')
      AND candidate.finished_at IS NOT NULL
      AND candidate.finished_at < sqlc.arg('cutoff')
      AND NOT EXISTS (SELECT 1 FROM job_runs j WHERE j.pipeline_run_id = candidate.id)
    ORDER BY candidate.finished_at, candidate.id
    LIMIT sqlc.arg('batch_size')
);

-- IDENTITY STAMPS (migration 0133). The projection triggers maintain state; they
-- cannot know the request that caused the work or the process doing it, because
-- neither is a column of the queue table they read. These two statements carry
-- that knowledge in from Go, keyed by the (queue, source_id) pair the projection
-- already indexes uniquely.
--
-- Each also backfills the events written BEFORE it could run. The projection is
-- an AFTER trigger on the queue table, so the 'enqueued' event exists by the time
-- the enqueue path returns and the 'started' event by the time a claim returns —
-- earlier than any Go statement can be. Later events inherit through the 0133
-- BEFORE INSERT trigger and need no help.

-- name: StampJobRunCorrelation :one
-- Called by an enqueue path with the originating request's ids.
--
-- PRECEDENCE, and each step earns its place: the caller's own ids first; then
-- the PARENT's, when this enqueue happened inside another job (an upload
-- finalize spawning a transcode — that worker's context carries no request, but
-- the run it is executing does, and the chain would break here otherwise); then
-- whatever the row already had, so a second, id-less call never WIPES a stamp.
-- The 0133 BEFORE INSERT trigger cannot do this job: it runs when the
-- projection trigger inserts the row, which is before any Go statement can name
-- the parent.
--
-- A job enqueued by a scheduler with no parent genuinely has no request behind
-- it and keeps an honest blank; inventing one would be worse.
WITH parent AS (
    SELECT p.request_id, p.correlation_id, p.trace_id, p.actor_id, p.pipeline_run_id
    FROM job_runs p WHERE p.id = sqlc.narg('parent_job_id')
), run AS (
    UPDATE job_runs
       SET request_id     = COALESCE(NULLIF(sqlc.arg('request_id')::text, ''),
                                     (SELECT NULLIF(parent.request_id, '') FROM parent),
                                     job_runs.request_id),
           correlation_id = COALESCE(NULLIF(sqlc.arg('correlation_id')::text, ''),
                                     (SELECT NULLIF(parent.correlation_id, '') FROM parent),
                                     job_runs.correlation_id),
           trace_id       = COALESCE(NULLIF(sqlc.arg('trace_id')::text, ''),
                                     (SELECT NULLIF(parent.trace_id, '') FROM parent),
                                     job_runs.trace_id),
           -- COALESCE, never overwrite: the projection may already have derived
           -- the actor from the queue row itself (account_exports, peertube).
           actor_id       = COALESCE(job_runs.actor_id, sqlc.narg('actor_id'),
                                     (SELECT parent.actor_id FROM parent)),
           parent_job_id  = COALESCE(job_runs.parent_job_id, sqlc.narg('parent_job_id')),
           pipeline_run_id = COALESCE(job_runs.pipeline_run_id, (SELECT parent.pipeline_run_id FROM parent)),
           updated_at     = updated_at
     WHERE job_runs.queue = sqlc.arg('queue') AND job_runs.source_id = sqlc.arg('source_id')
     RETURNING job_runs.id, job_runs.request_id, job_runs.correlation_id, job_runs.trace_id
), backfill AS (
    UPDATE job_events e
       SET request_id     = run.request_id,
           correlation_id = run.correlation_id,
           trace_id       = run.trace_id
      FROM run
     WHERE e.job_id = run.id
       AND e.request_id = '' AND e.correlation_id = '' AND e.trace_id = ''
    RETURNING e.cursor
)
SELECT run.id FROM run;

-- name: StampJobRunWorker :exec
-- Called by a worker the moment it claims a job. lease_expires_at is the queue
-- row's own lease (transcode/import claim to now() + 30 minutes and renew on a
-- ticker), so the projection reports the same deadline the sweeper enforces
-- rather than a second, drifting copy of it.
WITH run AS (
    UPDATE job_runs
       SET worker_id        = sqlc.arg('worker_id'),
           heartbeat_at     = now(),
           lease_expires_at = sqlc.narg('lease_expires_at'),
           claimed_at       = COALESCE(claimed_at, now()),
           updated_at       = updated_at
     WHERE queue = sqlc.arg('queue') AND source_id = sqlc.arg('source_id')
     RETURNING id
)
UPDATE job_events e
   SET worker_id = sqlc.arg('worker_id')
  FROM run
 WHERE e.job_id = run.id AND e.worker_id = '';

-- name: TouchJobRunHeartbeat :exec
-- The worker is still on this job. Called from the same ticker that renews the
-- queue row's lease, so heartbeat_at answers "when did a process last say it was
-- alive on this run" — the question the run detail's Heartbeat field asks.
UPDATE job_runs
SET heartbeat_at     = now(),
    lease_expires_at = sqlc.narg('lease_expires_at'),
    updated_at       = updated_at
WHERE queue = sqlc.arg('queue') AND source_id = sqlc.arg('source_id')
  AND worker_id = sqlc.arg('worker_id');

-- name: GetJobRunIdentityBySource :one
-- The projection's id and stamped ids for one queue row. Two callers, one row:
-- an audit envelope written by the originating request links job_id forward to
-- the run it created, and a WORKER — whose context is a background one and
-- carries no request — reads the originating ids back OFF the run so its failure
-- log line shares them. That read-back is what makes the chain walkable in both
-- directions from a single grep.
SELECT id, request_id, correlation_id, trace_id
FROM job_runs WHERE queue = sqlc.arg('queue') AND source_id = sqlc.arg('source_id');
