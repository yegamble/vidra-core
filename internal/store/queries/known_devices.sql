-- New-device sign-in notices (migration 0175). See internal/auth/devices.go.

-- name: CountUserKnownDevices :one
SELECT count(*)::bigint FROM user_known_devices WHERE user_id = $1;

-- name: TouchKnownDevice :one
-- Mark a device seen. Returns whether it already existed (xmax = 0 is an insert).
INSERT INTO user_known_devices (user_id, device_hash, label)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, device_hash) DO UPDATE
SET last_seen_at = now(), label = EXCLUDED.label
RETURNING (xmax = 0)::bool AS is_new;

-- name: ListKnownDevices :many
SELECT id, device_hash, label, first_seen_at, last_seen_at
FROM user_known_devices
WHERE user_id = $1
ORDER BY last_seen_at DESC
LIMIT 100;

-- name: DeleteKnownDevice :execrows
DELETE FROM user_known_devices WHERE user_id = $1 AND id = $2;

-- name: GetSecurityPrefs :one
-- A missing row is the default (new-device email ON).
SELECT new_device_email FROM user_security_prefs WHERE user_id = $1;

-- name: SetNewDeviceEmailPref :exec
INSERT INTO user_security_prefs (user_id, new_device_email)
VALUES ($1, $2)
ON CONFLICT (user_id) DO UPDATE SET new_device_email = EXCLUDED.new_device_email;

-- name: ClaimNewDeviceEmailSlot :one
-- Rate limit: true (and the clock advances) only if no new-device mail went to
-- this account within the last @cooldown_seconds. One statement, so two
-- simultaneous sign-ins cannot both send.
WITH claimed AS (
    INSERT INTO user_security_prefs (user_id, last_new_device_email_at)
    VALUES (sqlc.arg('user_id'), sqlc.arg('now')::timestamptz)
    ON CONFLICT (user_id) DO UPDATE
    SET last_new_device_email_at = sqlc.arg('now')::timestamptz
    WHERE user_security_prefs.last_new_device_email_at IS NULL
       OR user_security_prefs.last_new_device_email_at <= sqlc.arg('now')::timestamptz - make_interval(secs => sqlc.arg('cooldown_seconds')::float8)
    RETURNING 1
)
SELECT EXISTS (SELECT 1 FROM claimed)::bool;

-- name: HasKnownDevice :one
SELECT EXISTS (SELECT 1 FROM user_known_devices WHERE user_id = $1 AND device_hash = $2)::bool;

-- name: PruneKnownDevices :execrows
-- Keep an account's device list bounded: drop rows unseen since the cutoff and
-- everything past the newest @keep (a cookieless client adds a row per sign-in).
DELETE FROM user_known_devices AS k
WHERE k.user_id = sqlc.arg('user_id')
  AND (k.last_seen_at < sqlc.arg('cutoff')::timestamptz
       OR k.id IN (SELECT d.id FROM user_known_devices d
                 WHERE d.user_id = sqlc.arg('user_id')
                 ORDER BY d.last_seen_at DESC, d.first_seen_at DESC
                 OFFSET sqlc.arg('keep')::int));
