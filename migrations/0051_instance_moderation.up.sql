-- 0051: Instance-level moderation (.ralph/specs/federation-remote-content.md §8).
--
-- muted_instances: a per-user mute of a whole remote instance. Remote content
-- from that domain is filtered out of that user's surfaces (same NOT EXISTS
-- pattern as muted_accounts). Domains are stored normalized lowercase.
CREATE TABLE muted_instances (
    muter_id   UUID        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    domain     TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (muter_id, domain)
);

-- List a user's instance mutes newest-first.
CREATE INDEX muted_instances_muter_idx ON muted_instances (muter_id, created_at DESC);

-- blocked_instances: the admin blocklist. Inbound activities from actors on a
-- blocked domain are dropped at the inbox (after signature verification),
-- existing remote content from the domain is hidden from all surfaces, and
-- outbound deliveries to it are cancelled in the drain.
CREATE TABLE blocked_instances (
    domain     TEXT        PRIMARY KEY,
    reason     TEXT        NOT NULL DEFAULT '',
    blocked_by UUID        REFERENCES users (id) ON DELETE SET NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
