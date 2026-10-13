-- 0176: Blocklist subscriptions -- shared fediverse denylists (Mastodon CSV,
-- plain domain list, JSON) an admin subscribes to; a periodic sync applies the
-- list as instance blocks.
--
-- blocked_instances.source records WHO owns a block: 'manual' (an admin typed
-- it; the default, so every existing row and every old binary's insert stays
-- manual) or 'subscription:<id>'. The sync removes ONLY rows carrying its own
-- source, so a manual block can never be taken away by a list, and a manual
-- re-block of a subscription-owned domain adopts it (source flips to manual).
ALTER TABLE blocked_instances
    ADD COLUMN source TEXT NOT NULL DEFAULT 'manual'
    CONSTRAINT blocked_instances_source_chk
    CHECK (source = 'manual' OR source ~ '^subscription:[0-9a-f-]{36}$');

CREATE TABLE blocklist_subscriptions (
    id              UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    url             TEXT        NOT NULL CHECK (char_length(url) BETWEEN 1 AND 2048),
    label           TEXT        NOT NULL DEFAULT '' CHECK (char_length(label) <= 200),
    format          TEXT        NOT NULL CHECK (format IN ('mastodon_csv', 'domain_list', 'json')),
    enabled         BOOLEAN     NOT NULL DEFAULT true,
    -- Validator of the last list that was fully APPLIED (never one that was
    -- fetched but failed to apply, or the next conditional GET would 304 and the
    -- list would never land).
    etag            TEXT        NOT NULL DEFAULT '',
    last_attempt_at TIMESTAMPTZ,
    last_synced_at  TIMESTAMPTZ,
    last_status     TEXT        NOT NULL DEFAULT 'never' CHECK (last_status IN ('never', 'ok', 'not_modified', 'error')),
    last_error      TEXT        NOT NULL DEFAULT '',
    last_added      INTEGER     NOT NULL DEFAULT 0,
    last_removed    INTEGER     NOT NULL DEFAULT 0,
    entry_count     INTEGER     NOT NULL DEFAULT 0,
    created_by      UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (url)
);

-- Domains an admin exempts from EVERY subscription. The allowlist wins: a
-- listed domain is neither added by a sync nor kept if a subscription had added
-- it. It does not touch a manual block.
CREATE TABLE blocklist_allowlist (
    domain     TEXT        PRIMARY KEY,
    added_by   UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
