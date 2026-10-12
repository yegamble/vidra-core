-- name: UpsertUserFeedToken :exec
-- Create or ROTATE the caller's feed token: the old hash is overwritten, so the
-- previous URL dies in the same statement.
INSERT INTO user_feed_tokens (user_id, token_hash)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE
SET token_hash = EXCLUDED.token_hash, created_at = now();

-- name: GetActiveUserByFeedTokenHash :one
-- The owner of a feed token, only while the account is active.
SELECT t.user_id
FROM user_feed_tokens t
JOIN users u ON u.id = t.user_id
WHERE t.token_hash = $1 AND u.is_active;

-- name: GetUserFeedTokenCreatedAt :one
SELECT created_at FROM user_feed_tokens WHERE user_id = $1;

-- name: DeleteUserFeedToken :exec
DELETE FROM user_feed_tokens WHERE user_id = $1;
