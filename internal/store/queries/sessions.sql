-- Refresh-token sessions. The raw refresh token is never stored — only its
-- SHA-256 hash — and rotation revokes the prior row. ip_address is intentionally
-- left out of these queries (handled at a later slice) to keep the Go surface
-- free of the INET type for now.

-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_hash, user_agent, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, refresh_hash, user_agent, revoked_at, expires_at, created_at;

-- name: GetSessionByRefreshHash :one
SELECT id, user_id, refresh_hash, user_agent, revoked_at, expires_at, created_at
FROM sessions
WHERE refresh_hash = $1;

-- name: RevokeSession :exec
UPDATE sessions
SET revoked_at = now()
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE sessions
SET revoked_at = now()
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < now();
