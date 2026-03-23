-- +goose Up
-- +goose StatementBegin
-- Create auto_tag_policies table for automatic comment tagging/moderation.
CREATE TABLE IF NOT EXISTS auto_tag_policies (
    id BIGSERIAL PRIMARY KEY,
    account_name VARCHAR(255),  -- NULL = server-level
    tag_type VARCHAR(50) NOT NULL CHECK (tag_type IN ('external-link', 'watched-words')),
    review_type VARCHAR(50) NOT NULL CHECK (review_type IN ('review-comments', 'block-comments')),
    list_id BIGINT REFERENCES watched_word_lists(id) ON DELETE SET NULL
);

CREATE INDEX idx_auto_tag_policies_account ON auto_tag_policies(account_name);
CREATE INDEX idx_auto_tag_policies_server ON auto_tag_policies(account_name) WHERE account_name IS NULL;
CREATE INDEX idx_auto_tag_policies_list ON auto_tag_policies(list_id) WHERE list_id IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS auto_tag_policies;
