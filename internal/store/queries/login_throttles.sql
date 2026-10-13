-- Failed-login backoff (migration 0175). See internal/auth/loginlock.go.

-- name: RecordLoginFailure :one
-- Count one failed password against (account, scope), restarting the count when
-- the previous window has lapsed. Atomic, so concurrent guesses cannot undercount.
INSERT INTO login_throttles (user_id, scope, fail_count, window_start)
VALUES (sqlc.arg('user_id'), sqlc.arg('scope'), 1, sqlc.arg('now')::timestamptz)
ON CONFLICT (user_id, scope) DO UPDATE
SET fail_count   = CASE WHEN login_throttles.window_start <= sqlc.arg('now')::timestamptz - make_interval(secs => sqlc.arg('window_seconds')::float8)
                        THEN 1 ELSE login_throttles.fail_count + 1 END,
    window_start = CASE WHEN login_throttles.window_start <= sqlc.arg('now')::timestamptz - make_interval(secs => sqlc.arg('window_seconds')::float8)
                        THEN sqlc.arg('now')::timestamptz ELSE login_throttles.window_start END
RETURNING fail_count;

-- name: SetLoginLock :exec
-- reset_count restarts the counter as the lock is set: the account-wide cap
-- uses it so one slow drip of failures cannot re-lock the account forever.
UPDATE login_throttles
SET locked_until = sqlc.arg('locked_until'),
    fail_count   = CASE WHEN sqlc.arg('reset_count')::bool THEN 0 ELSE fail_count END
WHERE user_id = sqlc.arg('user_id') AND scope = sqlc.arg('scope');

-- name: CountActiveLoginLocks :one
-- How many of these scopes are locked for the account right now.
SELECT count(*)::bigint
FROM login_throttles
WHERE user_id = sqlc.arg('user_id')
  AND scope = ANY(sqlc.arg('scopes')::text[])
  AND locked_until > sqlc.arg('now')::timestamptz;

-- name: ClearLoginThrottleScope :exec
DELETE FROM login_throttles WHERE user_id = $1 AND scope = $2;

-- name: ClearLoginThrottles :execrows
-- Admin clear: drops every counter and lock for the account. The row count says
-- whether there was anything to clear.
DELETE FROM login_throttles WHERE user_id = $1;
