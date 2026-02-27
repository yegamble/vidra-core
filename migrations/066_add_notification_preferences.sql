-- +goose Up
CREATE TABLE notification_preferences (
    user_id     UUID PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    comment_enabled   BOOLEAN NOT NULL DEFAULT true,
    like_enabled      BOOLEAN NOT NULL DEFAULT true,
    subscribe_enabled BOOLEAN NOT NULL DEFAULT true,
    mention_enabled   BOOLEAN NOT NULL DEFAULT true,
    reply_enabled     BOOLEAN NOT NULL DEFAULT true,
    upload_enabled    BOOLEAN NOT NULL DEFAULT true,
    system_enabled    BOOLEAN NOT NULL DEFAULT true,
    email_enabled     BOOLEAN NOT NULL DEFAULT true
);

-- +goose Down
-- NOTE: This project uses forward-only migrations for safety.
-- Rollback is intentionally a no-op. To revert, create a compensating migration.
