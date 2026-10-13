-- 0169: the cooldown ledger for the new-follower channel backfill. When a remote
-- actor's Follow of a local channel is accepted, the follower is pushed the
-- channel's newest public videos (bounded) so the followed channel does not look
-- empty until the next upload. One row per remote HOST (one host can mint any
-- number of actors), so a Follow/Undo loop cannot turn this instance into a
-- delivery amplifier. Kept apart from remote_follows on purpose: Undo deletes
-- that row, and the loop is exactly what this must outlive. No foreign key.
CREATE TABLE channel_follower_backfills (
    host             TEXT        PRIMARY KEY,
    last_backfill_at TIMESTAMPTZ NOT NULL
);
