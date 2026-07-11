-- Unified operational job-run read side (migration 0083). The source queue
-- tables remain authoritative for execution; these queries expose only the
-- sanitized projection maintained transactionally by database triggers.

-- name: ListOperationalJobRuns :many
SELECT *
FROM job_runs
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
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountOperationalJobRuns :one
SELECT count(*)::bigint
FROM job_runs
WHERE (sqlc.narg('state')::text IS NULL OR state = sqlc.narg('state'))
  AND (sqlc.narg('type')::text IS NULL OR type = sqlc.narg('type'))
  AND (sqlc.narg('queue')::text IS NULL OR queue = sqlc.narg('queue'))
  AND (sqlc.narg('resource_type')::text IS NULL OR resource_type = sqlc.narg('resource_type'))
  AND (sqlc.narg('resource_id')::text IS NULL OR resource_id = sqlc.narg('resource_id'))
  AND (sqlc.narg('worker_id')::text IS NULL OR worker_id = sqlc.narg('worker_id'))
  AND (sqlc.narg('failure')::boolean IS NULL
       OR ((state IN ('failed', 'dead_lettered')) = sqlc.narg('failure')))
  AND (sqlc.narg('created_after')::timestamptz IS NULL OR created_at >= sqlc.narg('created_after'))
  AND (sqlc.narg('created_before')::timestamptz IS NULL OR created_at <= sqlc.narg('created_before'));

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
