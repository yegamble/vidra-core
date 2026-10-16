-- 0182: admin-managed OpenID Connect login providers.
--
-- Until now an OIDC provider existed only as OAUTH_PROVIDERS + OAUTH_<NAME>_*
-- environment variables. This table lets an admin add one from the API. The
-- environment list still works: a row here OVERRIDES the environment provider of
-- the same name (database > environment > defaults, the instance-wide rule),
-- and a disabled row switches that name off entirely.
--
-- WHY NOT instance_settings: the client secret is a credential, and that
-- registry's doctrine forbids one (see 0151 mail_config for the same call). The
-- secret is stored sealed by internal/secretbox under the MFA KEK chain and is
-- never returned by any endpoint.
--
-- `name` is the primary key and is IMMUTABLE: it is the URL segment
-- (/auth/oauth/<name>) and the oauth_identities.provider value accounts are
-- linked by. Identities are matched by (provider, subject), and a subject is
-- only unique within ONE issuer, so the service also refuses to change a row's
-- issuer_url once saved and refuses to (re)create a name that already has
-- linked identities under a different issuer.
--
-- Additive: a new table no previous release reads (one-release schema-compat).
CREATE TABLE oidc_providers (
    name                  TEXT        PRIMARY KEY
                          CHECK (name ~ '^[a-z0-9]+(-[a-z0-9]+)*$' AND length(name) <= 40),
    display_name          TEXT        NOT NULL DEFAULT '',
    issuer_url            TEXT        NOT NULL,
    client_id             TEXT        NOT NULL,
    -- Sealed (`enc:` envelope). NEVER returned: the API reports
    -- secret_configured instead.
    client_secret         TEXT        NOT NULL,
    scopes                TEXT[]      NOT NULL DEFAULT '{}',
    enabled               BOOLEAN     NOT NULL DEFAULT TRUE,
    -- false: only identities already linked may sign in; an unknown subject
    -- is refused instead of creating an account.
    auto_provision        BOOLEAN     NOT NULL DEFAULT TRUE,
    -- Non-empty: a NEW account is created only for a verified email in one of
    -- these domains. Linked identities are unaffected.
    allowed_email_domains TEXT[]      NOT NULL DEFAULT '{}',
    updated_by            UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at            TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at            TIMESTAMPTZ NOT NULL DEFAULT now()
);
