-- 0022: Muted accounts. A user mutes another account; the muted account's content
-- (comments, and later videos) becomes hidden from the muter. One row per
-- (muter, muted) pair; a user cannot mute themselves. This migration plus the
-- mute/unmute/list endpoints are the model + management surface — the filtering
-- effect on each content surface (comments, feed) rolls out in later slices.
CREATE TABLE muted_accounts (
    muter_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    muted_id   UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (muter_id, muted_id),
    CONSTRAINT muted_accounts_no_self CHECK (muter_id <> muted_id)
);

-- List a user's mutes newest-first.
CREATE INDEX muted_accounts_muter_idx ON muted_accounts (muter_id, created_at DESC);
