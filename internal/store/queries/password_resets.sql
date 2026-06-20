-- name: CreatePasswordResetToken :one
INSERT INTO password_reset_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING id, user_id, token_hash, expires_at, used_at, created_at;

-- name: GetPasswordResetToken :one
SELECT id, user_id, token_hash, expires_at, used_at, created_at
FROM password_reset_tokens
WHERE token_hash = $1;

-- name: MarkPasswordResetTokenUsed :exec
UPDATE password_reset_tokens
SET used_at = now()
WHERE id = $1;

-- name: DeleteUnusedPasswordResetTokens :exec
DELETE FROM password_reset_tokens
WHERE user_id = $1 AND used_at IS NULL;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = $2,
    updated_at    = now()
WHERE id = $1;
