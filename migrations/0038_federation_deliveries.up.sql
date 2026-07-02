-- 0038: Outbound federation delivery queue. Activities to send to remote inboxes
-- are enqueued here (payload = the UNSIGNED activity JSON; it is HTTP-signed at
-- send time so the Date/Signature are fresh). A worker claims due rows, signs as
-- the given local channel, POSTs to inbox_url, and marks delivered — or reschedules
-- with exponential backoff, dead-lettering (state 'failed') after max attempts.
-- See .ralph/specs/federation.md §8.
CREATE TABLE federation_deliveries (
    id                     UUID        PRIMARY KEY DEFAULT uuid_generate_v4(),
    inbox_url              TEXT        NOT NULL,
    payload                BYTEA       NOT NULL,
    signing_channel_id     UUID        REFERENCES channels (id) ON DELETE CASCADE,
    signing_channel_handle TEXT        NOT NULL DEFAULT '',
    state                  TEXT        NOT NULL DEFAULT 'pending',
    attempts               INT         NOT NULL DEFAULT 0,
    next_attempt_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_error             TEXT        NOT NULL DEFAULT '',
    created_at             TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Find due, still-pending deliveries cheaply.
CREATE INDEX federation_deliveries_due_idx
    ON federation_deliveries (next_attempt_at)
    WHERE state = 'pending';
