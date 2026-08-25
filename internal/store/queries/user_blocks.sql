-- name: BlockUser :execrows
-- Block an account (idempotent). The service rejects a self-block before this
-- runs; an unknown target raises a foreign-key violation (SQLSTATE 23503) mapped
-- to "user not found".
INSERT INTO user_blocks (blocker_id, blocked_id)
VALUES ($1, $2)
ON CONFLICT (blocker_id, blocked_id) DO NOTHING;

-- name: UnblockUser :execrows
-- Unblock an account (idempotent). Returns rows deleted (0 = it was not blocked).
DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2;

-- name: ListBlockedUsers :many
-- A user's blocked accounts, newest block first, with the blocked account's identity.
SELECT b.blocked_id, u.username, u.display_name, b.created_at
FROM user_blocks b
JOIN users u ON u.id = b.blocked_id
WHERE b.blocker_id = $1
ORDER BY b.created_at DESC, b.blocked_id
LIMIT sqlc.arg('result_limit') OFFSET sqlc.arg('result_offset');

-- name: CountBlockedUsers :one
-- How many rows ListBlockedUsers would return, ignoring pagination. The users
-- JOIN is part of the predicate (a block on a deleted account is not listed).
SELECT count(*)::bigint
FROM user_blocks b
JOIN users u ON u.id = b.blocked_id
WHERE b.blocker_id = $1;

-- name: IsBlockedBetween :one
-- True if either user has blocked the other — the symmetric check that gates
-- direct messaging in both directions.
SELECT EXISTS (
    SELECT 1 FROM user_blocks
    WHERE (blocker_id = $1 AND blocked_id = $2)
       OR (blocker_id = $2 AND blocked_id = $1)
);
