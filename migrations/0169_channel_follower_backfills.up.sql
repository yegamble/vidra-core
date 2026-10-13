-- 0169: the cooldown ledger for the new-follower channel backfill. When a remote
-- actor's Follow of a local channel is accepted, the follower is pushed the
-- channel's newest public videos (bounded) so the followed channel does not look
-- empty until the next upload. One row per (remote HOST, channel): a host that
-- follows a second channel gets that channel's backfill too, while a Follow/Undo
-- loop on one channel hits its 24h cooldown, and one host minting many channel
-- follows hits the per-host daily channel cap. Keyed by host because one host can
-- mint any number of actors. Kept apart from remote_follows on purpose: Undo
-- deletes that row, and the loop is exactly what this must outlive. No foreign
-- key.
CREATE TABLE channel_follower_backfills (
    host             TEXT        NOT NULL,
    channel_id       UUID        NOT NULL,
    last_backfill_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (host, channel_id)
);

CREATE INDEX channel_follower_backfills_recent_idx ON channel_follower_backfills (last_backfill_at);
