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

-- name: DeleteUnusedEmailVerificationTokens :exec
DELETE FROM email_verification_tokens
WHERE user_id = $1 AND used_at IS NULL;

-- name: SetUserEmailVerified :exec
UPDATE users
SET email_verified = TRUE,
    updated_at      = now()
WHERE id = $1;
