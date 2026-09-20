-- 0151: admin-configurable outbound email — the transport document.
--
-- WHY A DEDICATED TABLE AND NOT instance_settings. That registry's doctrine is
-- explicit (see 0065 and internal/instancesettings): secrets must never live in
-- it, every value is TEXT, GET echoes the stored value back, and the audit trail
-- records key names. An SMTP password or a provider API key satisfies none of
-- those. So outbound mail gets its own single-row document instead, with a
-- write-only secret column and endpoints that never echo it — the same shape
-- ipfs_control_config (0148) uses for a configuration that must be saved whole
-- or not at all.
--
-- ONE ROW, enforced by the primary key: `id` is a boolean that may only ever be
-- true, so a second INSERT collides instead of creating a second, invisible
-- configuration. (ipfs_control_config's `singleton` column is the same trick.)
--
-- settings holds only NON-SECRET per-transport fields (relay host/port/username
-- and encryption mode, the Mailgun domain and region, the Postmark message
-- stream). Exactly one credential is stored, in `secret`, sealed by
-- internal/secretbox under the MFA KEK chain — '' when the transport needs none
-- (an anonymous local relay is a real shape). A row whose secret is not
-- `enc:`-prefixed was written by a deployment with no KEK at all, which the
-- service refuses to do; the column is TEXT rather than BYTEA because the
-- sealed envelope is already base64 text and every other sealed secret in this
-- schema is stored the same way.
--
-- Additive: a brand-new table no previous release reads, so release N-1 keeps
-- running against this schema unchanged (one-release schema-compat policy).
CREATE TABLE mail_config (
    id           BOOLEAN     PRIMARY KEY DEFAULT TRUE CHECK (id),
    transport    TEXT        NOT NULL,
    from_address TEXT        NOT NULL,
    from_name    TEXT        NOT NULL DEFAULT '',
    reply_to     TEXT        NOT NULL DEFAULT '',
    settings     JSONB       NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(settings) = 'object'),
    -- The sealed credential, or '' when this transport stores none. NEVER
    -- returned by any endpoint: the API reports a per-block *_set boolean.
    secret       TEXT        NOT NULL DEFAULT '',
    -- The admin who last wrote the document. Nullable + ON DELETE SET NULL so
    -- the configuration survives that admin's account deletion — the same
    -- audit-integrity choice instance_settings.updated_by makes.
    updated_by   UUID        REFERENCES users (id) ON DELETE SET NULL,
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);
