-- name: UpsertUserMFA :one
-- Starts (or restarts) a TOTP enrollment: writes the sealed secret with
-- enabled=FALSE. Restarting a PENDING enrollment replaces the secret; the
-- service refuses to touch an already-enabled configuration before calling
-- this, and the WHERE guard makes the database enforce it too.
INSERT INTO user_mfa (user_id, totp_secret_sealed, enabled)
VALUES ($1, $2, FALSE)
ON CONFLICT (user_id) DO UPDATE
SET totp_secret_sealed = EXCLUDED.totp_secret_sealed,
    created_at         = now(),
    -- A restarted enrollment carries a NEW secret, so the burn high-water mark
    -- (0134) is about codes that can never be presented again; keeping it would
    -- only refuse the first code of the new secret inside the same 30s step.
    last_totp_step     = NULL
WHERE NOT user_mfa.enabled
RETURNING user_id, totp_secret_sealed, enabled, created_at, last_totp_step;

-- name: GetUserMFA :one
SELECT user_id, totp_secret_sealed, enabled, created_at, last_totp_step
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

-- name: BurnTOTPStep :execrows
-- Records the RFC 6238 time step of an accepted TOTP code and, by the same
-- statement, refuses a replay: the UPDATE matches only when the presented step
-- is strictly NEWER than the last accepted one, so a second use of the same
-- code (or of an older ±1-skew code) touches 0 rows and the caller treats it
-- exactly like a wrong code. Doing the check and the write in one statement is
-- what makes it safe under concurrency — two simultaneous replays cannot both
-- read "not yet used" and then both write.
UPDATE user_mfa
SET last_totp_step = sqlc.arg('step')::bigint
WHERE user_id = sqlc.arg('user_id')
  AND enabled
  AND (last_totp_step IS NULL OR last_totp_step < sqlc.arg('step')::bigint);

-- name: BurnPendingTOTPStep :execrows
-- The enrollment-verification counterpart of BurnTOTPStep: the row is still
-- pending (enabled=FALSE) at the moment the first code is checked, so the
-- enabled guard above would never match. Confirming an enrollment burns the
-- code it was confirmed with, so the code that turned two-factor on cannot
-- then be spent again on a login challenge.
UPDATE user_mfa
SET last_totp_step = sqlc.arg('step')::bigint
WHERE user_id = sqlc.arg('user_id')
  AND (last_totp_step IS NULL OR last_totp_step < sqlc.arg('step')::bigint);
