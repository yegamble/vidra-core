-- Refresh-token sessions. The raw refresh token is never stored — only its
-- SHA-256 hash — and rotation revokes the prior row. ip_address is intentionally
-- left out of these queries (handled at a later slice) to keep the Go surface
-- free of the INET type for now.

-- name: CreateSession :one
INSERT INTO sessions (user_id, refresh_hash, user_agent, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING id, user_id, refresh_hash, user_agent, revoked_at, expires_at, created_at;

-- name: GetSessionByRefreshHash :one
SELECT id, user_id, refresh_hash, user_agent, revoked_at, revoked_reason, expires_at, created_at
FROM sessions
WHERE refresh_hash = $1;

-- name: RevokeSession :exec
-- Deliberate sign-out of ONE session (logout). Presenting its refresh token
-- afterwards is a signed-out client retrying, not a replay, so it is recorded
-- as such and does not escalate.
UPDATE sessions
SET revoked_at     = now(),
    revoked_reason = 'signed_out'
WHERE id = $1 AND revoked_at IS NULL;

-- name: RotateSession :exec
-- Revoke a session because its refresh token was just EXCHANGED for a new one.
-- Presenting that token again is the compromise signal reuse detection exists
-- for, so this reason (and only this one) escalates to revoking everything.
UPDATE sessions
SET revoked_at     = now(),
    revoked_reason = 'rotated'
WHERE id = $1 AND revoked_at IS NULL;

-- name: RevokeAllUserSessions :exec
UPDATE sessions
SET revoked_at     = now(),
    revoked_reason = 'signed_out'
WHERE user_id = $1 AND revoked_at IS NULL;

-- name: DeleteExpiredSessions :exec
DELETE FROM sessions
WHERE expires_at < now();

-- name: GetActiveSessionForAccessToken :one
-- The per-request revocation check for a session-bound access token (AUTH-05).
-- It is deliberately the cheapest thing that makes revocation reach ACCESS
-- tokens and not just refresh tokens: one primary-key lookup on sessions plus
-- its primary-key join to users, so an authenticated request costs a single
-- indexed read. Re-reading the account here (rather than trusting the JWT) is
-- what stops a DEACTIVATED or hard-deleted account's unexpired token on EVERY
-- route at once, instead of only on the handful of handlers that happen to load
-- the user row. It returns no row for a revoked, expired, disabled or
-- tombstoned principal, all of which the caller maps to the same 401.
--
-- It also returns the account's CURRENT role, so the principal the middleware
-- builds carries the role the database holds rather than the copy the JWT was
-- minted with: a demoted moderator loses the staff routes on the token they are
-- already holding, and a promoted one gains them, instead of both waiting out
-- JWT_ACCESS_TTL. One extra column on a row this query already reads.
SELECT s.id, s.user_id, u.role
FROM sessions s
         JOIN users u ON u.id = s.user_id
WHERE s.id = $1
  AND s.revoked_at IS NULL
  AND s.expires_at > now()
  AND u.is_active
  AND u.deleted_at IS NULL;

-- name: RevokeOtherUserSessions :exec
-- "Sign out my other devices" — every session for the user EXCEPT the one the
-- caller is currently using. A password change uses it so the changer is not
-- signed out of the browser they just changed it in.
UPDATE sessions
SET revoked_at     = now(),
    revoked_reason = 'signed_out'
WHERE user_id = $1
  AND id <> $2
  AND revoked_at IS NULL;
