-- Failed-login backoff (migration 0175). See internal/auth/loginlock.go.

-- name: EnsureLoginThrottle :exec
-- Make sure the (account, scope) row exists so RecordLoginAttempt always has a
-- row to lock and update.
INSERT INTO login_throttles (user_id, scope, fail_count, window_start)
VALUES (sqlc.arg('user_id'), sqlc.arg('scope'), 0, sqlc.arg('now')::timestamptz)
ON CONFLICT (user_id, scope) DO NOTHING;

-- name: RecordLoginAttempt :one
-- Count one password ATTEMPT against (account, scope) BEFORE the password is
-- compared, restarting the count when the previous window has lapsed. One
-- statement over a row lock, so a parallel burst gets distinct, increasing
-- counts and cannot all slip under the threshold. probe is true for the single
-- attempt that finds an EXPIRED lock (the lock is cleared in the same
-- statement, so concurrent attempts see no lock and a high count): the first
-- try after a lock ends is a real try, a burst is not.
UPDATE login_throttles t
SET fail_count   = CASE WHEN t.window_start <= sqlc.arg('now')::timestamptz - make_interval(secs => sqlc.arg('window_seconds')::float8)
                        THEN 1 ELSE t.fail_count + 1 END,
    window_start = CASE WHEN t.window_start <= sqlc.arg('now')::timestamptz - make_interval(secs => sqlc.arg('window_seconds')::float8)
                        THEN sqlc.arg('now')::timestamptz ELSE t.window_start END,
    locked_until = CASE WHEN t.locked_until <= sqlc.arg('now')::timestamptz THEN NULL ELSE t.locked_until END
FROM (
    SELECT l.user_id, l.scope, l.locked_until
    FROM login_throttles l
    WHERE l.user_id = sqlc.arg('user_id') AND l.scope = sqlc.arg('scope')
    FOR UPDATE
) old
WHERE t.user_id = old.user_id AND t.scope = old.scope
RETURNING t.fail_count::int4 AS fail_count,
          COALESCE(old.locked_until <= sqlc.arg('now')::timestamptz, false)::bool AS probe;

-- name: SetLoginLock :exec
-- GREATEST: concurrent writers can only ever lengthen a lock. reset_count
-- restarts the counter as the lock is set (account-wide cap).
UPDATE login_throttles
SET locked_until = GREATEST(COALESCE(locked_until, '-infinity'::timestamptz), sqlc.arg('locked_until')::timestamptz),
    fail_count   = CASE WHEN sqlc.arg('reset_count')::bool THEN 0 ELSE fail_count END
WHERE user_id = sqlc.arg('user_id') AND scope = sqlc.arg('scope');

-- name: CountActiveLoginLocks :one
-- How many of these scopes are locked for the account right now.
SELECT count(*)::bigint
FROM login_throttles
WHERE user_id = sqlc.arg('user_id')
  AND scope = ANY(sqlc.arg('scopes')::text[])
  AND locked_until > sqlc.arg('now')::timestamptz;

-- name: RecordLoginSuccess :exec
-- Remember that this (account, prefix) recently signed in successfully
-- ('ok:<prefix>', window_start = when). A prefix the owner demonstrably uses is
-- not locked out by the account-wide cap.
INSERT INTO login_throttles (user_id, scope, fail_count, window_start)
VALUES (sqlc.arg('user_id'), sqlc.arg('scope'), 0, sqlc.arg('now')::timestamptz)
ON CONFLICT (user_id, scope) DO UPDATE SET window_start = EXCLUDED.window_start;

-- name: HasRecentLoginSuccess :one
SELECT EXISTS (
    SELECT 1 FROM login_throttles
    WHERE user_id = sqlc.arg('user_id') AND scope = sqlc.arg('scope')
      AND window_start > sqlc.arg('since')::timestamptz
)::bool;

-- name: ReleaseLoginAttempt :exec
-- A correct password hands its pre-counted attempt back (never below zero).
UPDATE login_throttles SET fail_count = GREATEST(fail_count - 1, 0)
WHERE user_id = $1 AND scope = $2;

-- name: ClearLoginThrottleScope :exec
DELETE FROM login_throttles WHERE user_id = $1 AND scope = $2;

-- name: ClearLoginThrottles :execrows
-- Admin clear: drops every counter and lock for the account. The row count says
-- whether there was anything to clear.
DELETE FROM login_throttles WHERE user_id = $1;
