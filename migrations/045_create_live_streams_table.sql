-- Migration: Create live streaming infrastructure
-- This migration adds support for RTMP live streaming with HLS output

-- Create live_streams table to track stream sessions
CREATE TABLE IF NOT EXISTS live_streams (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title TEXT NOT NULL,
    description TEXT,
    stream_key TEXT NOT NULL UNIQUE,
    status TEXT NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'live', 'ended', 'error')),
    privacy TEXT NOT NULL DEFAULT 'public' CHECK (privacy IN ('public', 'unlisted', 'private')),
    rtmp_url TEXT,
    hls_playlist_url TEXT,
    viewer_count INTEGER DEFAULT 0 CHECK (viewer_count >= 0),
    peak_viewer_count INTEGER DEFAULT 0 CHECK (peak_viewer_count >= 0),
    started_at TIMESTAMP,
    ended_at TIMESTAMP,
    save_replay BOOLEAN DEFAULT true,
    replay_video_id UUID REFERENCES videos(id) ON DELETE SET NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT valid_stream_times CHECK (
        (started_at IS NULL OR ended_at IS NULL OR ended_at > started_at)
    ),
    CONSTRAINT valid_status_times CHECK (
        (status = 'live' AND started_at IS NOT NULL) OR
        (status = 'ended' AND started_at IS NOT NULL AND ended_at IS NOT NULL) OR
        (status IN ('waiting', 'error'))
    )
);

COMMENT ON TABLE live_streams IS 'Tracks RTMP live streaming sessions';
COMMENT ON COLUMN live_streams.stream_key IS 'Unique authentication key for RTMP connection';
COMMENT ON COLUMN live_streams.status IS 'Stream status: waiting (not started), live (broadcasting), ended (finished), error (failed)';
COMMENT ON COLUMN live_streams.save_replay IS 'Whether to save stream as VOD after it ends';
COMMENT ON COLUMN live_streams.replay_video_id IS 'Reference to the VOD created from this stream';

-- Create stream_keys table for key rotation and management
CREATE TABLE IF NOT EXISTS stream_keys (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    key_hash TEXT NOT NULL UNIQUE,
    last_used_at TIMESTAMP,
    is_active BOOLEAN DEFAULT true,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP,
    CONSTRAINT valid_expiration CHECK (
        (expires_at IS NULL) OR (expires_at > created_at)
    )
);

COMMENT ON TABLE stream_keys IS 'Manages rotatable stream keys for channels';
COMMENT ON COLUMN stream_keys.key_hash IS 'Bcrypt hash of the stream key';
COMMENT ON COLUMN stream_keys.is_active IS 'Whether this key is currently valid for streaming';

-- Create viewer_sessions table for real-time viewer tracking
CREATE TABLE IF NOT EXISTS viewer_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    live_stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    ip_address INET,
    user_agent TEXT,
    country_code TEXT,
    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at TIMESTAMP,
    last_heartbeat_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT valid_session_times CHECK (
        (left_at IS NULL) OR (left_at >= joined_at)
    )
);

COMMENT ON TABLE viewer_sessions IS 'Tracks individual viewer sessions for live streams';
COMMENT ON COLUMN viewer_sessions.session_id IS 'Unique session identifier (UUID or random string)';
COMMENT ON COLUMN viewer_sessions.last_heartbeat_at IS 'Last time viewer sent heartbeat (for active viewer count)';

-- Create indexes for efficient queries
CREATE INDEX idx_live_streams_channel_id ON live_streams(channel_id);
CREATE INDEX idx_live_streams_user_id ON live_streams(user_id);
CREATE INDEX idx_live_streams_status ON live_streams(status);
CREATE INDEX idx_live_streams_created_at ON live_streams(created_at DESC);
CREATE INDEX idx_live_streams_started_at ON live_streams(started_at DESC) WHERE started_at IS NOT NULL;

CREATE INDEX idx_stream_keys_channel_id ON stream_keys(channel_id);
CREATE INDEX idx_stream_keys_is_active ON stream_keys(is_active) WHERE is_active = true;
CREATE INDEX idx_stream_keys_expires_at ON stream_keys(expires_at) WHERE expires_at IS NOT NULL;

CREATE INDEX idx_viewer_sessions_live_stream_id ON viewer_sessions(live_stream_id);
CREATE INDEX idx_viewer_sessions_session_id ON viewer_sessions(session_id);
CREATE INDEX idx_viewer_sessions_user_id ON viewer_sessions(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_viewer_sessions_active ON viewer_sessions(live_stream_id, left_at) WHERE left_at IS NULL;
CREATE INDEX idx_viewer_sessions_heartbeat ON viewer_sessions(last_heartbeat_at) WHERE left_at IS NULL;

-- Create trigger to update updated_at timestamp for live_streams
CREATE TRIGGER trigger_update_live_streams_updated_at
    BEFORE UPDATE ON live_streams
    FOR EACH ROW
    EXECUTE FUNCTION update_updated_at_column();

-- Function to get current viewer count for a stream
CREATE OR REPLACE FUNCTION get_live_viewer_count(p_stream_id UUID)
RETURNS INTEGER AS $$
BEGIN
    -- Count viewers with heartbeat in last 30 seconds
    RETURN (
        SELECT COUNT(*)
        FROM viewer_sessions
        WHERE live_stream_id = p_stream_id
          AND left_at IS NULL
          AND last_heartbeat_at > NOW() - INTERVAL '30 seconds'
    );
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION get_live_viewer_count IS 'Returns current active viewer count for a live stream (based on heartbeats)';

-- Function to cleanup stale viewer sessions
CREATE OR REPLACE FUNCTION cleanup_stale_viewer_sessions()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    -- Mark sessions as left if no heartbeat for 60 seconds
    UPDATE viewer_sessions
    SET left_at = last_heartbeat_at + INTERVAL '60 seconds'
    WHERE left_at IS NULL
      AND last_heartbeat_at < NOW() - INTERVAL '60 seconds';

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION cleanup_stale_viewer_sessions IS 'Marks viewer sessions as ended if no heartbeat received for 60 seconds';

-- Function to end a live stream and update statistics
CREATE OR REPLACE FUNCTION end_live_stream(p_stream_id UUID)
RETURNS VOID AS $$
BEGIN
    UPDATE live_streams
    SET
        status = 'ended',
        ended_at = COALESCE(ended_at, NOW()),
        viewer_count = 0
    WHERE id = p_stream_id
      AND status = 'live';

    -- Mark all active viewer sessions as ended
    UPDATE viewer_sessions
    SET left_at = COALESCE(left_at, NOW())
    WHERE live_stream_id = p_stream_id
      AND left_at IS NULL;
END;
$$ LANGUAGE plpgsql;

COMMENT ON FUNCTION end_live_stream IS 'Ends a live stream and cleans up viewer sessions';

-- View for active streams with viewer counts
CREATE OR REPLACE VIEW active_live_streams AS
SELECT
    ls.*,
    get_live_viewer_count(ls.id) as current_viewers,
    c.display_name as channel_name,
    u.username as streamer_username
FROM live_streams ls
JOIN channels c ON ls.channel_id = c.id
JOIN users u ON ls.user_id = u.id
WHERE ls.status = 'live';

COMMENT ON VIEW active_live_streams IS 'Shows all currently active live streams with real-time viewer counts';
