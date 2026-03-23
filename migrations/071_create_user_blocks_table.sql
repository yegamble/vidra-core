-- +goose Up
CREATE TABLE IF NOT EXISTS user_blocks (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id     UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    block_type  TEXT NOT NULL CHECK (block_type IN ('account', 'server')),
    target_account_id UUID REFERENCES users(id) ON DELETE CASCADE,
    target_server_host TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT user_blocks_account_xor_server CHECK (
        (block_type = 'account' AND target_account_id IS NOT NULL AND target_server_host IS NULL) OR
        (block_type = 'server'  AND target_server_host IS NOT NULL AND target_account_id IS NULL)
    ),
    CONSTRAINT user_blocks_unique_account UNIQUE (user_id, target_account_id),
    CONSTRAINT user_blocks_unique_server  UNIQUE (user_id, target_server_host)
);

CREATE INDEX IF NOT EXISTS idx_user_blocks_user_id ON user_blocks (user_id);

-- +goose Down
DROP TABLE IF EXISTS user_blocks;
