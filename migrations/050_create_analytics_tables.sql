-- Migration: Create Analytics Tables
-- Description: Comprehensive video analytics system with event tracking, daily aggregates, and retention curves
-- Created: 2025-10-23

-- Raw analytics events table (high-volume inserts)
CREATE TABLE IF NOT EXISTS video_analytics_events (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('view', 'play', 'pause', 'seek', 'complete', 'buffer', 'error')),
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    session_id TEXT NOT NULL,
    timestamp_seconds INTEGER, -- Position in video when event occurred
    watch_duration_seconds INTEGER, -- How long the user watched (for view/complete events)
    ip_address INET,
    user_agent TEXT,
    country_code TEXT,
    region TEXT,
    city TEXT,
    device_type TEXT CHECK (device_type IN ('desktop', 'mobile', 'tablet', 'tv', 'unknown')),
    browser TEXT,
    os TEXT,
    referrer TEXT,
    quality TEXT, -- Video quality selected (360p, 720p, 1080p, etc.)
    player_version TEXT,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Daily aggregated analytics (for fast querying)
CREATE TABLE IF NOT EXISTS video_analytics_daily (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    views INTEGER DEFAULT 0,
    unique_viewers INTEGER DEFAULT 0,
    watch_time_seconds BIGINT DEFAULT 0,
    avg_watch_percentage DECIMAL(5,2), -- Average % of video watched
    completion_rate DECIMAL(5,2), -- % of viewers who completed the video
    likes INTEGER DEFAULT 0,
    dislikes INTEGER DEFAULT 0,
    comments INTEGER DEFAULT 0,
    shares INTEGER DEFAULT 0,
    downloads INTEGER DEFAULT 0,
    countries JSONB DEFAULT '{}', -- {"US": 100, "CA": 50, ...}
    devices JSONB DEFAULT '{}', -- {"desktop": 150, "mobile": 100, ...}
    browsers JSONB DEFAULT '{}', -- {"chrome": 120, "firefox": 80, ...}
    traffic_sources JSONB DEFAULT '{}', -- {"direct": 100, "search": 50, ...}
    qualities JSONB DEFAULT '{}', -- {"1080p": 80, "720p": 120, ...}
    peak_concurrent_viewers INTEGER DEFAULT 0,
    errors INTEGER DEFAULT 0,
    buffering_events INTEGER DEFAULT 0,
    avg_buffering_duration_seconds DECIMAL(8,2),
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(video_id, date)
);

-- Video retention curve (viewer drop-off over time)
CREATE TABLE IF NOT EXISTS video_analytics_retention (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    timestamp_seconds INTEGER NOT NULL CHECK (timestamp_seconds >= 0),
    viewer_count INTEGER DEFAULT 0,
    date DATE NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(video_id, date, timestamp_seconds)
);

-- Channel-level aggregated analytics
CREATE TABLE IF NOT EXISTS channel_analytics_daily (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    channel_id UUID NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    views INTEGER DEFAULT 0,
    unique_viewers INTEGER DEFAULT 0,
    watch_time_seconds BIGINT DEFAULT 0,
    subscribers_gained INTEGER DEFAULT 0,
    subscribers_lost INTEGER DEFAULT 0,
    total_subscribers INTEGER DEFAULT 0,
    likes INTEGER DEFAULT 0,
    comments INTEGER DEFAULT 0,
    shares INTEGER DEFAULT 0,
    videos_published INTEGER DEFAULT 0,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(channel_id, date)
);

-- Real-time active viewers tracking (frequently updated, short TTL)
CREATE TABLE IF NOT EXISTS video_active_viewers (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    last_heartbeat TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE(video_id, session_id)
);

-- Indexes for performance

-- Events table indexes (high-volume)
CREATE INDEX idx_analytics_events_video_id ON video_analytics_events(video_id);
CREATE INDEX idx_analytics_events_created_at ON video_analytics_events(created_at DESC);
CREATE INDEX idx_analytics_events_session_id ON video_analytics_events(session_id);
CREATE INDEX idx_analytics_events_event_type ON video_analytics_events(event_type);
CREATE INDEX idx_analytics_events_video_date ON video_analytics_events(video_id, created_at DESC);
CREATE INDEX idx_analytics_events_user_id ON video_analytics_events(user_id) WHERE user_id IS NOT NULL;

-- Daily aggregates indexes
CREATE INDEX idx_analytics_daily_video_id ON video_analytics_daily(video_id);
CREATE INDEX idx_analytics_daily_date ON video_analytics_daily(date DESC);
CREATE INDEX idx_analytics_daily_video_date ON video_analytics_daily(video_id, date DESC);

-- Retention indexes
CREATE INDEX idx_analytics_retention_video_id ON video_analytics_retention(video_id);
CREATE INDEX idx_analytics_retention_date ON video_analytics_retention(date);
CREATE INDEX idx_analytics_retention_video_date ON video_analytics_retention(video_id, date, timestamp_seconds);

-- Channel analytics indexes
CREATE INDEX idx_channel_analytics_channel_id ON channel_analytics_daily(channel_id);
CREATE INDEX idx_channel_analytics_date ON channel_analytics_daily(date DESC);
CREATE INDEX idx_channel_analytics_channel_date ON channel_analytics_daily(channel_id, date DESC);

-- Active viewers indexes
CREATE INDEX idx_active_viewers_video_id ON video_active_viewers(video_id);
CREATE INDEX idx_active_viewers_last_heartbeat ON video_active_viewers(last_heartbeat);
CREATE INDEX idx_active_viewers_video_heartbeat ON video_active_viewers(video_id, last_heartbeat);

-- PostgreSQL function to automatically update updated_at timestamp
CREATE OR REPLACE FUNCTION update_analytics_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

-- Triggers for updated_at
CREATE TRIGGER trigger_analytics_daily_updated_at
    BEFORE UPDATE ON video_analytics_daily
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_updated_at();

CREATE TRIGGER trigger_analytics_retention_updated_at
    BEFORE UPDATE ON video_analytics_retention
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_updated_at();

CREATE TRIGGER trigger_channel_analytics_updated_at
    BEFORE UPDATE ON channel_analytics_daily
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_updated_at();

-- Function to cleanup old raw events (retention policy)
CREATE OR REPLACE FUNCTION cleanup_old_analytics_events()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    -- Delete events older than 90 days (after aggregation)
    DELETE FROM video_analytics_events
    WHERE created_at < CURRENT_DATE - INTERVAL '90 days';

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Function to cleanup inactive viewer sessions
CREATE OR REPLACE FUNCTION cleanup_inactive_viewers()
RETURNS INTEGER AS $$
DECLARE
    deleted_count INTEGER;
BEGIN
    -- Remove viewers with no heartbeat in the last 30 seconds
    DELETE FROM video_active_viewers
    WHERE last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '30 seconds';

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Comments for documentation
COMMENT ON TABLE video_analytics_events IS 'Raw analytics events for detailed analysis (90-day retention)';
COMMENT ON TABLE video_analytics_daily IS 'Daily aggregated video analytics for fast querying';
COMMENT ON TABLE video_analytics_retention IS 'Viewer retention data showing drop-off over video timeline';
COMMENT ON TABLE channel_analytics_daily IS 'Daily aggregated channel-level analytics';
COMMENT ON TABLE video_active_viewers IS 'Real-time tracking of active video viewers';

COMMENT ON COLUMN video_analytics_events.watch_duration_seconds IS 'Total seconds watched in this session';
COMMENT ON COLUMN video_analytics_daily.avg_watch_percentage IS 'Average percentage of video watched by viewers';
COMMENT ON COLUMN video_analytics_daily.completion_rate IS 'Percentage of viewers who watched to completion';
COMMENT ON COLUMN video_analytics_retention.timestamp_seconds IS 'Second in the video timeline';
COMMENT ON COLUMN video_analytics_retention.viewer_count IS 'Number of viewers at this timestamp';
