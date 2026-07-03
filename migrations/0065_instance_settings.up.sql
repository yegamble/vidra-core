-- 0065: DB-backed instance settings overlay (fix_plan P10 admin instance
-- configuration + feature toggles).
--
-- A single-row-per-key table that OVERLAYS the static config for a defined
-- MUTABLE subset of instance settings (instance name/description/legal-contact
-- metadata, the registration gates, upload quarantine, and the
-- uploads/imports/live/comments feature toggles). The settings service loads
-- every row at boot and caches them; a typed accessor per key returns the DB
-- override or the config default, and a write invalidates + reloads the cache.
--
-- Boot-time-only secrets and infrastructure — the database DSN, the KEKs
-- (federation/MFA), the JWT signing secret, and the storage backend selection —
-- intentionally STAY in config and are NOT represented here: changing them
-- safely requires a restart (and they are secrets that must never be written to
-- a queryable table).
--
-- value is TEXT for every key: booleans are stored canonically as 'true'/'false'.
-- A row's mere presence means the key is overridden; its absence means the
-- config default applies (so an operator can even override a string setting to
-- the empty string to hide a config-provided default).
CREATE TABLE instance_settings (
    key        TEXT        PRIMARY KEY,
    value      TEXT        NOT NULL,
    -- The admin who last wrote this setting. Nullable + ON DELETE SET NULL so
    -- the setting survives that admin's account deletion (audit-integrity
    -- parity with blocked_instances.blocked_by).
    updated_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
