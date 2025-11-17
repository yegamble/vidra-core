-- +goose Up
-- +goose StatementBegin
-- Migration: Create chat tables for live stream chat functionality
-- Description: Adds chat messages, moderators, and bans tables
-- Author: Claude Code
-- Date: 2025-10-20

-- ============================================================================
-- Chat Messages Table
-- ============================================================================
-- Stores all chat messages sent during live streams
CREATE TABLE IF NOT EXISTS chat_messages (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    username VARCHAR(255) NOT NULL, -- Denormalized for performance
    message TEXT NOT NULL CHECK (length(message) > 0 AND length(message) <= 500),
    type VARCHAR(20) NOT NULL DEFAULT 'message' CHECK (type IN ('message', 'system', 'moderation')),
    metadata JSONB DEFAULT '{}',
    deleted BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Indexes for chat_messages
CREATE INDEX IF NOT EXISTS idx_chat_messages_stream_id
    ON chat_messages(stream_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_chat_messages_user_id
    ON chat_messages(user_id);

CREATE INDEX IF NOT EXISTS idx_chat_messages_deleted
    ON chat_messages(stream_id, deleted) WHERE deleted = FALSE;

-- Comments for chat_messages
COMMENT ON TABLE chat_messages IS 'Stores chat messages for live streams';
COMMENT ON COLUMN chat_messages.id IS 'Unique message identifier';
COMMENT ON COLUMN chat_messages.stream_id IS 'Reference to the live stream';
COMMENT ON COLUMN chat_messages.user_id IS 'User who sent the message';
COMMENT ON COLUMN chat_messages.username IS 'Username at time of message (denormalized)';
COMMENT ON COLUMN chat_messages.message IS 'Message content (max 500 chars)';
COMMENT ON COLUMN chat_messages.type IS 'Message type: message, system, moderation';
COMMENT ON COLUMN chat_messages.metadata IS 'Additional message metadata (emotes, mentions, etc)';
COMMENT ON COLUMN chat_messages.deleted IS 'Soft delete flag for moderation';
COMMENT ON COLUMN chat_messages.created_at IS 'Message timestamp';

-- ============================================================================
-- Chat Moderators Table
-- ============================================================================
-- Stores moderators for each stream
CREATE TABLE IF NOT EXISTS chat_moderators (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    granted_by UUID NOT NULL REFERENCES users(id),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_stream_moderator UNIQUE(stream_id, user_id)
);

-- Indexes for chat_moderators
CREATE INDEX IF NOT EXISTS idx_chat_moderators_stream_id
    ON chat_moderators(stream_id);

CREATE INDEX IF NOT EXISTS idx_chat_moderators_user_id
    ON chat_moderators(user_id);

-- Comments for chat_moderators
COMMENT ON TABLE chat_moderators IS 'Stores chat moderators for live streams';
COMMENT ON COLUMN chat_moderators.id IS 'Unique moderator assignment identifier';
COMMENT ON COLUMN chat_moderators.stream_id IS 'Reference to the live stream';
COMMENT ON COLUMN chat_moderators.user_id IS 'User granted moderator privileges';
COMMENT ON COLUMN chat_moderators.granted_by IS 'User who granted moderator privileges';
COMMENT ON COLUMN chat_moderators.created_at IS 'When moderator was added';

-- ============================================================================
-- Chat Bans Table
-- ============================================================================
-- Stores chat bans (timeouts and permanent bans)
CREATE TABLE IF NOT EXISTS chat_bans (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    banned_by UUID NOT NULL REFERENCES users(id),
    reason TEXT,
    expires_at TIMESTAMP, -- NULL = permanent ban
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT unique_stream_ban UNIQUE(stream_id, user_id)
);

-- Indexes for chat_bans
CREATE INDEX IF NOT EXISTS idx_chat_bans_stream_id
    ON chat_bans(stream_id);

CREATE INDEX IF NOT EXISTS idx_chat_bans_user_id
    ON chat_bans(user_id);

CREATE INDEX IF NOT EXISTS idx_chat_bans_expires_at
    ON chat_bans(expires_at) WHERE expires_at IS NOT NULL;

-- Comments for chat_bans
COMMENT ON TABLE chat_bans IS 'Stores chat bans and timeouts for live streams';
COMMENT ON COLUMN chat_bans.id IS 'Unique ban identifier';
COMMENT ON COLUMN chat_bans.stream_id IS 'Reference to the live stream';
COMMENT ON COLUMN chat_bans.user_id IS 'Banned user';
COMMENT ON COLUMN chat_bans.banned_by IS 'User who issued the ban';
COMMENT ON COLUMN chat_bans.reason IS 'Reason for the ban';
COMMENT ON COLUMN chat_bans.expires_at IS 'Ban expiration (NULL for permanent)';
COMMENT ON COLUMN chat_bans.created_at IS 'When ban was issued';

-- ============================================================================
-- Helper Functions
-- ============================================================================

-- Function to check if user is banned from a stream
CREATE OR REPLACE FUNCTION is_user_banned(p_stream_id UUID, p_user_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1
        FROM chat_bans
        WHERE stream_id = p_stream_id
        AND user_id = p_user_id
        AND (expires_at IS NULL OR expires_at > NOW())
    );
END;
$$ LANGUAGE plpgsql STABLE;

COMMENT ON FUNCTION is_user_banned(UUID, UUID) IS
    'Check if a user is currently banned from a stream chat';

-- Function to check if user is a moderator for a stream
CREATE OR REPLACE FUNCTION is_chat_moderator(p_stream_id UUID, p_user_id UUID)
RETURNS BOOLEAN AS $$
BEGIN
    RETURN EXISTS (
        SELECT 1
        FROM chat_moderators
        WHERE stream_id = p_stream_id
        AND user_id = p_user_id
    );
END;
$$ LANGUAGE plpgsql STABLE;

COMMENT ON FUNCTION is_chat_moderator(UUID, UUID) IS
    'Check if a user is a moderator for a stream chat';

-- Function to get chat message count for a stream
CREATE OR REPLACE FUNCTION get_chat_message_count(p_stream_id UUID)
RETURNS INTEGER AS $$
BEGIN
    RETURN (
        SELECT COUNT(*)::INTEGER
        FROM chat_messages
        WHERE stream_id = p_stream_id
        AND deleted = FALSE
    );
END;
$$ LANGUAGE plpgsql STABLE;

COMMENT ON FUNCTION get_chat_message_count(UUID) IS
    'Get total non-deleted message count for a stream';

-- Function to cleanup expired bans
CREATE OR REPLACE FUNCTION cleanup_expired_bans()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    WITH deleted AS (
        DELETE FROM chat_bans
        WHERE expires_at IS NOT NULL
        AND expires_at <= NOW()
        RETURNING *
    )
    SELECT COUNT(*)::INTEGER INTO deleted_count FROM deleted;

    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_expired_bans() IS
    'Remove expired bans and return count of deleted bans';

-- ============================================================================
-- Views
-- ============================================================================

-- View for active chat statistics per stream
CREATE OR REPLACE VIEW chat_stream_stats AS
SELECT
    cm.stream_id,
    COUNT(DISTINCT cm.user_id) as unique_chatters,
    COUNT(*) FILTER (WHERE cm.deleted = FALSE) as message_count,
    COUNT(*) FILTER (WHERE cm.type = 'moderation') as moderation_actions,
    MAX(cm.created_at) as last_message_at,
    (
        SELECT COUNT(*)
        FROM chat_moderators
        WHERE stream_id = cm.stream_id
    ) as moderator_count,
    (
        SELECT COUNT(*)
        FROM chat_bans
        WHERE stream_id = cm.stream_id
        AND (expires_at IS NULL OR expires_at > NOW())
    ) as active_ban_count
FROM chat_messages cm
GROUP BY cm.stream_id;

COMMENT ON VIEW chat_stream_stats IS
    'Aggregate chat statistics per stream';

-- ============================================================================
-- Grants
-- ============================================================================
-- Grant permissions if using restricted database user
-- GRANT SELECT, INSERT, UPDATE, DELETE ON chat_messages TO athena_app;
-- GRANT SELECT, INSERT, DELETE ON chat_moderators TO athena_app;
-- GRANT SELECT, INSERT, DELETE ON chat_bans TO athena_app;
-- GRANT EXECUTE ON FUNCTION is_user_banned(UUID, UUID) TO athena_app;
-- GRANT EXECUTE ON FUNCTION is_chat_moderator(UUID, UUID) TO athena_app;
-- GRANT EXECUTE ON FUNCTION get_chat_message_count(UUID) TO athena_app;
-- GRANT EXECUTE ON FUNCTION cleanup_expired_bans() TO athena_app;
-- GRANT SELECT ON chat_stream_stats TO athena_app;
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
