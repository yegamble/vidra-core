-- 0033: User blocks. A user blocks another account to cut off direct interaction.
-- Unlike a mute (which one-directionally hides the muted account's content), a
-- block is enforced symmetrically for messaging: if EITHER user has blocked the
-- other, neither can start or send a direct message to them. One row per
-- (blocker, blocked) pair; a user cannot block themselves.
CREATE TABLE user_blocks (
    blocker_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    blocked_id UUID NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (blocker_id, blocked_id),
    CONSTRAINT user_blocks_no_self CHECK (blocker_id <> blocked_id)
);

-- List a user's blocks newest-first.
CREATE INDEX user_blocks_blocker_idx ON user_blocks (blocker_id, created_at DESC);
-- Reverse lookup (who has blocked this user) for the symmetric messaging check.
CREATE INDEX user_blocks_blocked_idx ON user_blocks (blocked_id);
