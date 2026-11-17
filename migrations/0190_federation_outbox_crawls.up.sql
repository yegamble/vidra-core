-- 0190: the queue of remote outboxes to read.
--
-- A remote server pushes only what it publishes AFTER a follow is accepted.
-- What it published before reaches this instance only by reading the followed
-- actor's outbox (federation.CrawlOutbox). An accepted Follow — of a channel by
-- a user, or of an instance by the admin — queues one read here, and the
-- federation worker drains the queue with retry and backoff.
--
-- One row per followed ACTOR, not per follow. A second local user following
-- the same channel, or a follow, unfollow, follow, must not make this instance
-- read one outbox over and over: a re-queue inside the cooldown is a no-op
-- (EnqueueOutboxCrawl). The row is the queue entry and the record of the last
-- read. It holds no content.
--
-- No foreign key to remote_actors: the actor may be evicted from the cache (an
-- inbound Delete) while its read is queued, and the read then finds nobody
-- following it and stops.
CREATE TABLE federation_outbox_crawls (
    actor_url       TEXT        PRIMARY KEY CHECK (char_length(actor_url) BETWEEN 1 AND 2048),
    state           TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (state IN ('pending', 'done', 'failed')),
    attempts        INT         NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    queued_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending reads cheaply.
CREATE INDEX federation_outbox_crawls_due_idx
    ON federation_outbox_crawls (next_attempt_at)
    WHERE state = 'pending';
