-- name: InsertAuditLog :exec
-- Append a typed security-audit envelope. actor_id/job_id/pipeline_run_id are
-- nullable. metadata and changes have already passed the audit package's strict
-- allowlists and bounds; DB constraints are the final size/shape backstop.
INSERT INTO audit_log (
    id, schema_version, domain, action, result,
    actor_id, actor_kind, actor_role,
    reason, request_id, correlation_id, trace_id,
    pipeline_run_id, job_id, resource_type, resource_id,
    metadata, changes
)
VALUES (
    sqlc.arg('id'), sqlc.arg('schema_version'), sqlc.arg('domain'),
    sqlc.arg('action'), sqlc.arg('result'), sqlc.narg('actor_id'),
    sqlc.arg('actor_kind'), sqlc.arg('actor_role'), sqlc.arg('reason'),
    sqlc.arg('request_id'), sqlc.arg('correlation_id'), sqlc.arg('trace_id'),
    sqlc.narg('pipeline_run_id'), sqlc.narg('job_id'),
    sqlc.arg('resource_type'), sqlc.arg('resource_id'),
    sqlc.arg('metadata')::jsonb, sqlc.arg('changes')::jsonb
);

-- name: ListAuditLog :many
-- The admin audit view, newest first, optionally filtered by action.
-- actor_username is resolved best-effort (LEFT JOIN) and is null when the
-- account was deleted or the actor is unknown/unauthenticated.
SELECT a.id, a.schema_version, a.domain, a.action, a.result,
       a.actor_id, a.actor_kind, a.actor_role,
       a.reason, a.request_id, a.correlation_id, a.trace_id,
       a.pipeline_run_id, a.job_id, a.resource_type, a.resource_id,
       a.metadata, a.changes, a.occurred_at,
       u.username AS actor_username
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE (sqlc.narg('action')::text IS NULL OR a.action = sqlc.narg('action')::text)
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
