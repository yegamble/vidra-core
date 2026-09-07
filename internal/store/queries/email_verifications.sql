-- name: CreateEmailVerificationToken :one
INSERT INTO email_verification_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token_hash, expires_at, used_at, created_at;

-- name: GetEmailVerificationToken :one
SELECT id, user_id, token_hash, expires_at, used_at, created_at
FROM email_verification_tokens
WHERE token_hash = $1;

-- name: MarkEmailVerificationTokenUsed :exec
UPDATE email_verification_tokens
SET used_at = now()
WHERE id = $1;

-- name: LatestUnusedEmailVerificationTokenAt :one
-- When the account's newest UNUSED verification token was issued. It is the
-- per-address resend cooldown's clock: the anonymous resend route must answer
-- the same 202 to everybody, so it cannot refuse a rapid repeat — it declines
-- to SEND one, and this is the fact it decides on. No row means nothing is
-- outstanding and a send is due.
SELECT created_at
FROM email_verification_tokens
WHERE user_id = $1 AND used_at IS NULL
ORDER BY created_at DESC
LIMIT 1;

-- name: DeleteUnusedEmailVerificationTokens :exec
DELETE FROM email_verification_tokens
WHERE user_id = $1 AND used_at IS NULL;

-- name: SetUserEmailVerified :exec
UPDATE users
SET email_verified = TRUE,
    updated_at      = now()
WHERE id = $1;
