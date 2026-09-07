-- Per-process heartbeats (migration 0133). One row per running process, written
-- by the settings-version poller — the one loop that runs in EVERY role — so the
-- api can report on the worker instead of only on itself.

-- name: UpsertProcessHeartbeat :exec
-- Idempotent per process. started_at is preserved across ticks and only set by
-- the first write of a run; a restart on the same key (container pid 1) resets
-- it through the EXCLUDED value because state returns to 'running'.
INSERT INTO process_heartbeats (
    process_id, role, hostname, pid, version, build_commit, state,
    started_at, last_seen_at, last_settings_poll_success_at, last_settings_poll_error
) VALUES (
    sqlc.arg('process_id'), sqlc.arg('role'), sqlc.arg('hostname'), sqlc.arg('pid'),
    sqlc.arg('version'), sqlc.arg('build_commit'), 'running',
    sqlc.arg('started_at'), now(),
    sqlc.narg('last_settings_poll_success_at'), sqlc.arg('last_settings_poll_error')
)
ON CONFLICT (process_id) DO UPDATE SET
    role         = EXCLUDED.role,
    hostname     = EXCLUDED.hostname,
    pid          = EXCLUDED.pid,
    version      = EXCLUDED.version,
    build_commit = EXCLUDED.build_commit,
    state        = 'running',
    -- A row left behind by an earlier run of the same key is adopted, not
    -- appended to: the new process's own boot time replaces it.
    started_at   = EXCLUDED.started_at,
    last_seen_at = now(),
    last_settings_poll_success_at = EXCLUDED.last_settings_poll_success_at,
    last_settings_poll_error      = EXCLUDED.last_settings_poll_error,
    stopped_at   = NULL;

-- name: MarkProcessStopped :exec
-- A clean shutdown says goodbye, so the fleet view can tell a decommissioned
-- replica from a crashed one. Best effort: a SIGKILLed process never gets here
-- and shows as a stale 'running' row instead, which is the honest report.
UPDATE process_heartbeats
SET state = 'stopped', stopped_at = now(), last_seen_at = now()
WHERE process_id = sqlc.arg('process_id');

-- name: ListProcessHeartbeats :many
-- The fleet, freshest first. Rows not seen inside the forget window are dropped
-- from the answer entirely: a replica that was decommissioned without a clean
-- shutdown must eventually stop degrading the instance it no longer belongs to,
-- and an operator staring at a machine they scrapped last month learns nothing.
SELECT * FROM process_heartbeats
WHERE last_seen_at >= now() - sqlc.arg('forget_after')::interval
ORDER BY last_seen_at DESC;

-- name: ForgetStaleProcessHeartbeats :execrows
-- Sweep the rows ListProcessHeartbeats already hides, so the table stays the
-- size of the fleet rather than the size of its history.
DELETE FROM process_heartbeats
WHERE last_seen_at < now() - sqlc.arg('forget_after')::interval;
