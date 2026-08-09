-- name: CreateOAuthIdentity :one
-- handle is the display-only remote handle (ATProto sign-in); NULL for OIDC.
INSERT INTO oauth_identities (provider, subject, user_id, email, handle)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, provider, subject, user_id, email, created_at, handle;

-- name: GetOAuthIdentity :one
-- Resolves an external identity to its local account link. The (provider,
-- subject) pair is the stable OIDC identity key.
SELECT id, provider, subject, user_id, email, created_at, handle
FROM oauth_identities
WHERE provider = $1 AND subject = $2;

-- name: ListOAuthIdentitiesByUser :many
-- The caller's linked identities, oldest link first (stable settings list).
SELECT id, provider, subject, user_id, email, created_at, handle
FROM oauth_identities
WHERE user_id = $1
ORDER BY created_at ASC, id ASC;

-- name: UpdateOAuthIdentityHandle :exec
-- Refresh the stored display handle for a link. Remote handles are mutable, so
-- an ATProto re-login re-resolves and rewrites it. Keyed by the stable
-- (provider, subject) identity; a no-op if the link is absent.
UPDATE oauth_identities
SET handle = $3
WHERE provider = $1 AND subject = $2;

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
