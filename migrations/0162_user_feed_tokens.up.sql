-- 0162: Per-user private feed token.
--
-- Backs GET /feeds/subscriptions.xml?token=…, the subscriptions feed a feed
-- reader can poll without a bearer header. One row per user: rotating replaces
-- the row, so the previous URL stops working at once; deleting it revokes the
-- feed. Only the SHA-256 hash is stored (the raw token is high-entropy, so a
-- fast hash is right — same reasoning as password_reset_tokens); the raw value
-- is shown to its owner once, at rotation. The token authorises that one feed
-- and nothing else.
--
-- (Migrations 0159-0161 are in open PRs; the gap is expected.)
CREATE TABLE user_feed_tokens (
    user_id     UUID PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX user_feed_tokens_token_hash_idx ON user_feed_tokens (token_hash);
