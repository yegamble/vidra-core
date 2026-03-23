-- Create channels table
-- Channels are separate from users and own videos (PeerTube model)
CREATE TABLE IF NOT EXISTS channels (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    handle VARCHAR(50) NOT NULL UNIQUE, -- channel username/handle
    display_name VARCHAR(255) NOT NULL,
    description TEXT,
    support TEXT, -- Support/donation text
    is_local BOOLEAN DEFAULT true,
    actor_id VARCHAR(500) UNIQUE, -- For federation (ActivityPub actor ID)
    inbox_url VARCHAR(500), -- ActivityPub inbox
    outbox_url VARCHAR(500), -- ActivityPub outbox
    followers_url VARCHAR(500), -- ActivityPub followers collection
    following_url VARCHAR(500), -- ActivityPub following collection
    avatar_filename VARCHAR(255),
    avatar_ipfs_cid VARCHAR(255),
    banner_filename VARCHAR(255),
    banner_ipfs_cid VARCHAR(255),
    followers_count INTEGER DEFAULT 0,
    following_count INTEGER DEFAULT 0,
    videos_count INTEGER DEFAULT 0,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for performance
CREATE INDEX idx_channels_account_id ON channels(account_id);
CREATE INDEX idx_channels_handle ON channels(handle);
CREATE INDEX idx_channels_created_at ON channels(created_at DESC);
CREATE INDEX idx_channels_is_local ON channels(is_local);

-- Trigger to update updated_at
CREATE TRIGGER update_channels_updated_at
    BEFORE UPDATE ON channels
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Create default channel for each existing user
-- This ensures every user has at least one channel
INSERT INTO channels (account_id, handle, display_name, description)
SELECT
    id as account_id,
    username as handle,
    COALESCE(display_name, username) as display_name,
    bio as description
FROM users
WHERE NOT EXISTS (
    SELECT 1 FROM channels WHERE channels.account_id = users.id
);

-- Add comment explaining the channel model
COMMENT ON TABLE channels IS 'Channels own videos and are managed by user accounts (PeerTube model)';
COMMENT ON COLUMN channels.handle IS 'Unique channel handle/username';
COMMENT ON COLUMN channels.account_id IS 'User account that owns this channel';
COMMENT ON COLUMN channels.is_local IS 'Whether this channel is local or federated';
COMMENT ON COLUMN channels.actor_id IS 'ActivityPub actor ID for federation';
