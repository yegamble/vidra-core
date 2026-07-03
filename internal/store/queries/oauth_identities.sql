-- name: CreateOAuthIdentity :one
INSERT INTO oauth_identities (provider, subject, user_id, email)
VALUES ($1, $2, $3, $4)
RETURNING id, provider, subject, user_id, email, created_at;

-- name: GetOAuthIdentity :one
-- Resolves an external identity to its local account link. The (provider,
-- subject) pair is the stable OIDC identity key.
SELECT id, provider, subject, user_id, email, created_at
FROM oauth_identities
WHERE provider = $1 AND subject = $2;

-- name: ListOAuthIdentitiesByUser :many
-- The caller's linked identities, oldest link first (stable settings list).
SELECT id, provider, subject, user_id, email, created_at
FROM oauth_identities
WHERE user_id = $1
ORDER BY created_at ASC, id ASC;

-- name: CountOAuthIdentitiesByUser :one
SELECT count(*) FROM oauth_identities WHERE user_id = $1;

-- name: DeleteOAuthIdentity :execrows
-- Unlink a provider from an account. execrows so the service can distinguish
-- "unlinked" from "was never linked".
DELETE FROM oauth_identities
WHERE user_id = $1 AND provider = $2;

-- name: UsernameExists :one
-- Case-insensitive username existence probe, used to derive a unique username
-- for accounts created via OAuth login (the unique index remains the backstop).
SELECT EXISTS (
    SELECT 1 FROM users WHERE lower(username) = lower($1)
) AS taken;
