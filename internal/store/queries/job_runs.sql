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
-- Called by an enqueue path with the originating request's ids. Empty strings
-- are written as empty strings: a job enqueued by a scheduler genuinely has no
-- request behind it, and inventing one would be worse than an honest blank.
WITH run AS (
    UPDATE job_runs
       SET request_id     = sqlc.arg('request_id'),
           correlation_id = sqlc.arg('correlation_id'),
           trace_id       = sqlc.arg('trace_id'),
           -- COALESCE, never overwrite: the projection may already have derived
           -- the actor from the queue row itself (account_exports, peertube).
           actor_id       = COALESCE(job_runs.actor_id, sqlc.narg('actor_id')),
           parent_job_id  = COALESCE(job_runs.parent_job_id, sqlc.narg('parent_job_id')),
           updated_at     = updated_at
     WHERE queue = sqlc.arg('queue') AND source_id = sqlc.arg('source_id')
     RETURNING id
), backfill AS (
    UPDATE job_events e
       SET request_id     = sqlc.arg('request_id'),
           correlation_id = sqlc.arg('correlation_id'),
           trace_id       = sqlc.arg('trace_id')
      FROM run
     WHERE e.job_id = run.id
       AND e.request_id = '' AND e.correlation_id = '' AND e.trace_id = ''
    RETURNING e.cursor
)
SELECT id FROM run;

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

-- name: GetJobRunIDBySource :one
-- The projection's id for one queue row, so an audit envelope written by the
-- originating request can link job_id forward to the run it created.
SELECT id FROM job_runs WHERE queue = sqlc.arg('queue') AND source_id = sqlc.arg('source_id');
