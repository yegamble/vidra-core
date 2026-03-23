-- +goose Up
CREATE TABLE IF NOT EXISTS user_registrations (
    id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username            TEXT NOT NULL,
    email               TEXT NOT NULL,
    channel_name        TEXT,
    reason              TEXT,
    status              TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'accepted', 'rejected')),
    moderator_response  TEXT,
    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_user_registrations_status ON user_registrations(status);

-- +goose Down
DROP TABLE IF EXISTS user_registrations;
