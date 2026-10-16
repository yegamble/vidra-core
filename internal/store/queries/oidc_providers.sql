-- name: ListOIDCProviders :many
-- Every admin-managed OIDC provider (migration 0182), enabled or not. Read at
-- boot and on every settings-version bump; the table holds a handful of rows.
SELECT name, display_name, issuer_url, client_id, client_secret, scopes, enabled,
       auto_provision, allowed_email_domains, updated_by, created_at, updated_at
FROM oidc_providers
ORDER BY name;

-- name: GetOIDCProvider :one
SELECT name, display_name, issuer_url, client_id, client_secret, scopes, enabled,
       auto_provision, allowed_email_domains, updated_by, created_at, updated_at
FROM oidc_providers
WHERE name = $1;

-- name: CreateOIDCProvider :one
-- The secret is passed already sealed; this layer never sees plaintext.
INSERT INTO oidc_providers (name, display_name, issuer_url, client_id, client_secret,
                            scopes, enabled, auto_provision, allowed_email_domains, updated_by)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING name, display_name, issuer_url, client_id, client_secret, scopes, enabled,
          auto_provision, allowed_email_domains, updated_by, created_at, updated_at;

-- name: UpdateOIDCProvider :one
-- issuer_url is deliberately absent: it is fixed at create (see 0182).
UPDATE oidc_providers
SET display_name          = $2,
    client_id             = $3,
    client_secret         = $4,
    scopes                = $5,
    enabled               = $6,
    auto_provision        = $7,
    allowed_email_domains = $8,
    updated_by            = $9,
    updated_at            = now()
WHERE name = $1
RETURNING name, display_name, issuer_url, client_id, client_secret, scopes, enabled,
          auto_provision, allowed_email_domains, updated_by, created_at, updated_at;

-- name: DeleteOIDCProvider :execrows
DELETE FROM oidc_providers WHERE name = $1;

-- name: OAuthIdentitiesExistForProvider :one
-- Whether any account is linked through this provider name. A name with linked
-- identities can only be (re)bound to the issuer those subjects came from.
SELECT EXISTS (SELECT 1 FROM oauth_identities WHERE provider = $1);
