-- +goose Up
-- +goose StatementBegin
-- Create watched_word_lists table for content moderation word filtering.
-- When account_name is NULL, the list applies at the server level.
CREATE TABLE IF NOT EXISTS watched_word_lists (
    id BIGSERIAL PRIMARY KEY,
    account_name VARCHAR(255),  -- NULL = server-level
    list_name VARCHAR(200) NOT NULL,
    words JSONB NOT NULL DEFAULT '[]'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_watched_word_lists_account ON watched_word_lists(account_name);
CREATE INDEX idx_watched_word_lists_server ON watched_word_lists(account_name) WHERE account_name IS NULL;

-- Auto-update updated_at
CREATE OR REPLACE FUNCTION update_watched_word_lists_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_watched_word_lists_updated_at
    BEFORE UPDATE ON watched_word_lists
    FOR EACH ROW
    EXECUTE FUNCTION update_watched_word_lists_updated_at();
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER IF EXISTS trigger_watched_word_lists_updated_at ON watched_word_lists;
DROP FUNCTION IF EXISTS update_watched_word_lists_updated_at();
DROP TABLE IF EXISTS watched_word_lists;
