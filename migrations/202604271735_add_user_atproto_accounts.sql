-- +goose Up
-- +goose StatementBegin
-- Phase 11 — per-user ATProto identity + encrypted session storage.
-- Replaces the singleton atproto_sessions row (id=1, kept for instance auto-publish)
-- with a per-user table. One row per user; one DID per user (unique).
-- Tokens encrypted at rest with the same instance master key the singleton uses.
CREATE TABLE IF NOT EXISTS user_atproto_accounts (
    user_id            UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    did                TEXT        NOT NULL UNIQUE,
    handle             TEXT        NOT NULL,
    pds_url            TEXT        NOT NULL DEFAULT 'https://bsky.social',
    access_jwt_enc     BYTEA       NOT NULL,
    access_nonce       BYTEA       NOT NULL,
    refresh_jwt_enc    BYTEA       NOT NULL,
    refresh_nonce      BYTEA       NOT NULL,
    last_refreshed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_atproto_accounts_did ON user_atproto_accounts(did);

COMMENT ON TABLE user_atproto_accounts IS 'Per-user ATProto identity + encrypted session tokens. Distinct from atproto_sessions (singleton, instance auto-publish).';
COMMENT ON COLUMN user_atproto_accounts.did IS 'ATProto DID, e.g. did:plc:abc123. Unique across users — one Bluesky identity links to one Vidra user.';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Dev-only rollback. Production cannot rollback this table without re-prompting all
-- linked users for app-passwords (encrypted tokens cannot be recovered).
DROP TABLE IF EXISTS user_atproto_accounts;
-- +goose StatementEnd
