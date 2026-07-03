-- 0055: OAuth/OIDC identities (fix_plan P4 "OAuth2 provider abstraction").
--
-- One row per external identity linked to a local account. The identity is
-- keyed by (provider, subject) — the OIDC issuer-scoped stable subject — so a
-- given external identity can be linked to at most one local account. email is
-- a point-in-time snapshot of the claim used at link time (display/debugging
-- only; never used for auth decisions after linking). No tokens are stored:
-- Vidra issues its own sessions after the OIDC code exchange.
CREATE TABLE oauth_identities (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    provider    TEXT NOT NULL,
    subject     TEXT NOT NULL,
    user_id     UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    email       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, subject)
);

-- A user's linked identities (settings page list); also constrains one link
-- per provider per user — linking the same provider twice replaces nothing,
-- it is refused.
CREATE UNIQUE INDEX oauth_identities_user_provider_idx ON oauth_identities (user_id, provider);
