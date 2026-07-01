-- name: InsertAuditLog :exec
-- Append a security-audit row. actor_id is nullable (unauthenticated events).
INSERT INTO audit_log (action, result, actor_id, reason, request_id)
VALUES ($1, $2, $3, $4, $5);

-- name: ListAuditLog :many
-- The admin audit view, newest first, optionally filtered by action.
-- actor_username is resolved best-effort (LEFT JOIN) and is null when the
-- account was deleted or the actor is unknown/unauthenticated.
SELECT a.id, a.action, a.result, a.actor_id, a.reason, a.request_id, a.occurred_at,
       u.username AS actor_username
FROM audit_log a
LEFT JOIN users u ON u.id = a.actor_id
WHERE (sqlc.narg('action')::text IS NULL OR a.action = sqlc.narg('action')::text)
ORDER BY a.occurred_at DESC, a.id DESC
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');
