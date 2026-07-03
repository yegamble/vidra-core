-- name: UpsertUserMFA :one
-- Starts (or restarts) a TOTP enrollment: writes the sealed secret with
-- enabled=FALSE. Restarting a PENDING enrollment replaces the secret; the
-- service refuses to touch an already-enabled configuration before calling
-- this, and the WHERE guard makes the database enforce it too.
INSERT INTO user_mfa (user_id, totp_secret_sealed, enabled)
VALUES ($1, $2, FALSE)
ON CONFLICT (user_id) DO UPDATE
SET totp_secret_sealed = EXCLUDED.totp_secret_sealed,
    created_at         = now()
WHERE NOT user_mfa.enabled
RETURNING user_id, totp_secret_sealed, enabled, created_at;

-- name: GetUserMFA :one
SELECT user_id, totp_secret_sealed, enabled, created_at
FROM user_mfa
WHERE user_id = $1;

-- name: EnableUserMFA :execrows
-- Flips a pending enrollment on. execrows so the service can detect the
-- no-pending-enrollment case (0 rows).
UPDATE user_mfa
SET enabled = TRUE
WHERE user_id = $1 AND NOT enabled;

-- name: DeleteUserMFA :execrows
-- Disables MFA entirely (drops pending or enabled configuration). execrows so
-- the service can distinguish "disabled" from "was never enabled".
DELETE FROM user_mfa
WHERE user_id = $1;

-- name: CreateRecoveryCode :exec
INSERT INTO mfa_recovery_codes (user_id, code_hash)
VALUES ($1, $2);

-- name: DeleteRecoveryCodes :exec
-- Drops every recovery code for the user (on disable, and before issuing a
-- fresh set on enable).
DELETE FROM mfa_recovery_codes
WHERE user_id = $1;

-- name: UseRecoveryCode :execrows
-- Redeems a recovery code: marks the matching UNUSED row used. execrows is the
-- single-use guarantee — a second redemption matches 0 rows.
UPDATE mfa_recovery_codes
SET used_at = now()
WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL;

-- name: CountUnusedRecoveryCodes :one
SELECT count(*)
FROM mfa_recovery_codes
WHERE user_id = $1 AND used_at IS NULL;
