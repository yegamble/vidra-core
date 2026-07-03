-- Instance-level moderation (migration 0051,
-- .ralph/specs/federation-remote-content.md §8).

-- name: MuteInstance :execrows
-- Per-user mute of a whole remote instance (idempotent). Domains are
-- normalized lowercase by the service before this runs.
INSERT INTO muted_instances (muter_id, domain)
VALUES ($1, $2)
ON CONFLICT (muter_id, domain) DO NOTHING;

-- name: UnmuteInstance :execrows
-- Lift an instance mute (idempotent). Returns rows deleted (0 = not muted).
DELETE FROM muted_instances WHERE muter_id = $1 AND domain = $2;

-- name: ListMutedInstances :many
-- A user's muted instances, newest mute first.
SELECT domain, created_at
FROM muted_instances
WHERE muter_id = $1
ORDER BY created_at DESC, domain
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: BlockInstance :execrows
-- Admin-block a remote instance (idempotent; re-blocking refreshes the reason
-- and blocking admin).
INSERT INTO blocked_instances (domain, reason, blocked_by)
VALUES ($1, $2, $3)
ON CONFLICT (domain) DO UPDATE SET reason = EXCLUDED.reason, blocked_by = EXCLUDED.blocked_by;

-- name: UnblockInstance :execrows
-- Lift an instance block (idempotent). Returns rows deleted (0 = not blocked).
DELETE FROM blocked_instances WHERE domain = $1;

-- name: ListBlockedInstances :many
-- The admin blocklist, newest block first.
SELECT domain, reason, blocked_by, created_at
FROM blocked_instances
ORDER BY created_at DESC, domain
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: IsInstanceBlocked :one
-- The cheap inbox/drain check: is this (lowercased) domain on the blocklist?
SELECT EXISTS (SELECT 1 FROM blocked_instances WHERE domain = $1);
