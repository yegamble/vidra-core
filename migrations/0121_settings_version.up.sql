-- 0121: one counter row that tells every api replica when the admin-editable
-- state it holds IN MEMORY has been rewritten by somebody else.
--
-- Three caches are loaded once at boot and refreshed only by the process that
-- served the write: the instance-settings overlay (internal/instancesettings),
-- the ToS/privacy/homepage/custom-CSS documents (internal/instancedocs), and
-- the branding images (internal/profileimage). They exist because the hot
-- public paths — GET /instance on every page load, /instance/custom.css,
-- the branding block — must not round-trip to the database per request.
--
-- With ONE api process that is correct. With N behind a load balancer it is a
-- silent correctness bug: an admin turns registrations off, the replica that
-- happened to serve the PATCH obeys immediately, and the other N-1 keep
-- answering with the old value until they are restarted. Nothing errors,
-- nothing logs, and the admin sees the change "work" because their next read
-- has a 1/N chance of landing on the replica that wrote it. Debugging it from
-- the outside looks like a flaky cache with no cache.
--
-- This table is the cross-process invalidation signal. Every write to any of
-- the three stores increments `version`; every replica re-reads it on a short
-- jittered ticker and, when the number has moved, reloads all three caches.
-- Bounded staleness (one poll interval), no new infrastructure.
--
-- Why a polled counter and not something push-shaped:
--   * LISTEN/NOTIFY would cost a second PERMANENTLY PINNED connection per
--     replica (a listening connection cannot be pooled), on a pool whose size
--     is already an operator-tuned scarce resource, and it silently loses
--     notifications across a reconnect — so a poller would still be needed as
--     the backstop, which is the whole feature.
--   * Redis pub/sub would put a hard dependency on Redis into a CORRECTNESS
--     path. Everything Vidra does with Redis today fails open (rate limits
--     degrade rather than block), and /readyz was deliberately changed to
--     treat a Redis blip as degraded rather than fatal. A Redis outage must
--     not be able to fork the fleet's view of its own settings.
--
-- One row, forever: `id` is pinned to 1 by the CHECK so the table cannot grow
-- a second, ambiguous counter. `version` is bigint because it is incremented
-- per admin write and never resets — it is a change token, not a quantity, and
-- readers only ever compare it for inequality.
CREATE TABLE settings_version (
    id         SMALLINT    PRIMARY KEY CHECK (id = 1),
    version    BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Seed the singleton so the read path is a plain PK lookup rather than a
-- create-if-missing. The bump statement is an upsert anyway, so a database
-- whose row was removed out of band heals itself on the next admin write.
INSERT INTO settings_version (id, version) VALUES (1, 0)
ON CONFLICT (id) DO NOTHING;
