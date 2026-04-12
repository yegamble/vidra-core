-- +goose Up
-- PeerTube v8.0 parity: channel activity log
CREATE TABLE IF NOT EXISTS channel_activities (
    id UUID PRIMARY KEY,
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    action_type VARCHAR(50) NOT NULL,
    target_type VARCHAR(50) NOT NULL,
    target_id VARCHAR(255) NOT NULL,
    metadata JSONB NOT NULL DEFAULT '{}',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_channel_activities_channel_id ON channel_activities(channel_id);
CREATE INDEX idx_channel_activities_created_at ON channel_activities(created_at DESC);

-- +goose Down
DROP TABLE IF EXISTS channel_activities;
