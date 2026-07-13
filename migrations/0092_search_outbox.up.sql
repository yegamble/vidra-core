-- 0092: Durable outbound queue for the vidra-search integration (search-service
-- W4, MASTER-PLAN §2.1). Domain events (video/channel/user upserts + suppressions,
-- reconcile pages, settings pushes) and behavioural events destined for the search
-- service are enqueued here as JSONB payloads. A worker (runSearchOutboxWorker)
-- claims due pending rows oldest-first, POSTs them BATCHED to the search service's
-- /internal/v1/events endpoint, and marks each delivered — or reschedules with
-- exponential backoff, dead-lettering (state 'dead') after the attempt cap.
--
-- Mirrors the outbound federation delivery queue (migration 0038): a single
-- durable per-domain queue drained by one ticker worker. event_id is the
-- cross-service idempotency key (the search service dedupes via events_inbox);
-- it defaults here so every row is uniquely addressable end to end.
CREATE TABLE search_outbox (
    id              BIGSERIAL   PRIMARY KEY,
    event_id        UUID        NOT NULL UNIQUE DEFAULT uuid_generate_v4(),
    event_type      TEXT        NOT NULL,
    payload         JSONB       NOT NULL,
    state           TEXT        NOT NULL DEFAULT 'pending'
                                CHECK (state IN ('pending', 'delivered', 'dead')),
    attempts        INT         NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error      TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending events cheaply (oldest first). Partial index keeps the
-- claim scan tiny once rows are delivered/dead.
CREATE INDEX search_outbox_due_idx
    ON search_outbox (next_attempt_at, id)
    WHERE state = 'pending';
