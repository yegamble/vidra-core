-- Add stream analytics and metrics collection
-- Migration: 048_create_stream_analytics.sql
-- Sprint 7 - Phase 3: Analytics & Metrics

-- Create table for storing stream analytics time-series data
CREATE TABLE IF NOT EXISTS stream_analytics (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    collected_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,

    -- Viewer metrics
    viewer_count INTEGER NOT NULL DEFAULT 0,
    peak_viewer_count INTEGER NOT NULL DEFAULT 0,
    unique_viewers INTEGER NOT NULL DEFAULT 0,
    average_watch_time INTEGER DEFAULT 0, -- in seconds

    -- Engagement metrics
    chat_messages_count INTEGER NOT NULL DEFAULT 0,
    chat_participants INTEGER NOT NULL DEFAULT 0,
    likes_count INTEGER NOT NULL DEFAULT 0,
    shares_count INTEGER NOT NULL DEFAULT 0,

    -- Technical metrics
    bitrate INTEGER, -- in kbps
    framerate DECIMAL(5,2), -- fps
    resolution VARCHAR(20), -- e.g., "1920x1080"
    buffering_ratio DECIMAL(5,4), -- ratio of buffering time to watch time
    avg_latency INTEGER, -- in milliseconds

    -- Geographic distribution (JSONB for flexibility)
    viewer_countries JSONB DEFAULT '{}', -- {"US": 45, "UK": 20, ...}

    -- Device/Platform breakdown
    viewer_devices JSONB DEFAULT '{}', -- {"desktop": 60, "mobile": 30, "tablet": 10}
    viewer_browsers JSONB DEFAULT '{}', -- {"chrome": 50, "firefox": 25, ...}

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for efficient querying
CREATE INDEX IF NOT EXISTS idx_stream_analytics_stream_id_collected
ON stream_analytics(stream_id, collected_at DESC);

CREATE INDEX IF NOT EXISTS idx_stream_analytics_collected_at
ON stream_analytics(collected_at DESC);

CREATE INDEX IF NOT EXISTS idx_stream_analytics_stream_id_viewer_count
ON stream_analytics(stream_id, viewer_count DESC);

-- Create table for aggregated stream statistics (summary view)
CREATE TABLE IF NOT EXISTS stream_stats_summary (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL UNIQUE REFERENCES live_streams(id) ON DELETE CASCADE,

    -- Aggregate metrics
    total_viewers INTEGER NOT NULL DEFAULT 0,
    peak_concurrent_viewers INTEGER NOT NULL DEFAULT 0,
    average_viewers INTEGER NOT NULL DEFAULT 0,
    total_watch_time BIGINT NOT NULL DEFAULT 0, -- in seconds
    average_watch_duration INTEGER NOT NULL DEFAULT 0, -- in seconds

    -- Engagement totals
    total_chat_messages INTEGER NOT NULL DEFAULT 0,
    total_unique_chatters INTEGER NOT NULL DEFAULT 0,
    total_likes INTEGER NOT NULL DEFAULT 0,
    total_shares INTEGER NOT NULL DEFAULT 0,
    engagement_rate DECIMAL(5,2) DEFAULT 0, -- percentage

    -- Stream quality metrics
    average_bitrate INTEGER,
    average_framerate DECIMAL(5,2),
    quality_score DECIMAL(3,2) DEFAULT 0, -- 0-100 score

    -- Time-based metrics
    stream_duration INTEGER DEFAULT 0, -- in seconds
    first_viewer_joined_at TIMESTAMP,
    peak_time TIMESTAMP,

    -- Geographic summary
    top_countries JSONB DEFAULT '[]', -- [{"country": "US", "viewers": 100}, ...]
    countries_count INTEGER DEFAULT 0,

    -- Platform summary
    top_devices JSONB DEFAULT '{}',
    top_browsers JSONB DEFAULT '{}',

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Create table for viewer session tracking
CREATE TABLE IF NOT EXISTS viewer_sessions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    stream_id UUID NOT NULL REFERENCES live_streams(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL,
    session_id VARCHAR(255) NOT NULL, -- for anonymous users

    joined_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    left_at TIMESTAMP,
    watch_duration INTEGER GENERATED ALWAYS AS (
        CASE
            WHEN left_at IS NOT NULL THEN EXTRACT(EPOCH FROM (left_at - joined_at))::INTEGER
            ELSE NULL
        END
    ) STORED,

    -- Session details
    ip_address INET,
    country_code VARCHAR(2),
    city VARCHAR(100),
    device_type VARCHAR(50),
    browser VARCHAR(50),
    operating_system VARCHAR(50),

    -- Engagement during session
    messages_sent INTEGER DEFAULT 0,
    liked BOOLEAN DEFAULT FALSE,
    shared BOOLEAN DEFAULT FALSE,

    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Create indexes for viewer sessions
CREATE INDEX IF NOT EXISTS idx_viewer_sessions_stream_id
ON viewer_sessions(stream_id, joined_at DESC);

CREATE INDEX IF NOT EXISTS idx_viewer_sessions_user_id
ON viewer_sessions(user_id) WHERE user_id IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_viewer_sessions_session_id
ON viewer_sessions(session_id);

CREATE INDEX IF NOT EXISTS idx_viewer_sessions_active
ON viewer_sessions(stream_id) WHERE left_at IS NULL;

-- Function to calculate current viewer count for a stream
CREATE OR REPLACE FUNCTION get_current_viewer_count(p_stream_id UUID)
RETURNS INTEGER AS $$
DECLARE
    v_count INTEGER;
BEGIN
    SELECT COUNT(*)
    INTO v_count
    FROM viewer_sessions
    WHERE stream_id = p_stream_id
    AND left_at IS NULL;

    RETURN COALESCE(v_count, 0);
END;
$$ LANGUAGE plpgsql;

-- Function to get stream analytics for a time range
CREATE OR REPLACE FUNCTION get_stream_analytics_range(
    p_stream_id UUID,
    p_start_time TIMESTAMP,
    p_end_time TIMESTAMP,
    p_interval_minutes INTEGER DEFAULT 5
)
RETURNS TABLE (
    time_bucket TIMESTAMP,
    avg_viewers INTEGER,
    max_viewers INTEGER,
    messages INTEGER,
    avg_bitrate INTEGER
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        date_trunc('minute', collected_at) -
        (EXTRACT(MINUTE FROM collected_at)::INTEGER % p_interval_minutes) * INTERVAL '1 minute' AS time_bucket,
        AVG(viewer_count)::INTEGER AS avg_viewers,
        MAX(viewer_count) AS max_viewers,
        SUM(chat_messages_count) AS messages,
        AVG(bitrate)::INTEGER AS avg_bitrate
    FROM stream_analytics
    WHERE stream_id = p_stream_id
    AND collected_at BETWEEN p_start_time AND p_end_time
    GROUP BY time_bucket
    ORDER BY time_bucket ASC;
END;
$$ LANGUAGE plpgsql;

-- Function to update stream stats summary
CREATE OR REPLACE FUNCTION update_stream_stats_summary(p_stream_id UUID)
RETURNS VOID AS $$
DECLARE
    v_record RECORD;
BEGIN
    -- Calculate aggregate statistics
    WITH stats AS (
        SELECT
            COUNT(DISTINCT vs.user_id) + COUNT(DISTINCT vs.session_id) AS total_viewers,
            MAX(sa.viewer_count) AS peak_viewers,
            AVG(sa.viewer_count)::INTEGER AS avg_viewers,
            SUM(COALESCE(vs.watch_duration, 0)) AS total_watch_time,
            AVG(COALESCE(vs.watch_duration, 0))::INTEGER AS avg_watch_duration,
            SUM(sa.chat_messages_count) AS total_messages,
            COUNT(DISTINCT CASE WHEN vs.messages_sent > 0 THEN COALESCE(vs.user_id::TEXT, vs.session_id) END) AS unique_chatters,
            SUM(sa.likes_count) AS total_likes,
            SUM(sa.shares_count) AS total_shares,
            AVG(sa.bitrate)::INTEGER AS avg_bitrate,
            AVG(sa.framerate) AS avg_framerate,
            MIN(vs.joined_at) AS first_viewer_at,
            (
                SELECT sa2.collected_at
                FROM stream_analytics sa2
                WHERE sa2.stream_id = p_stream_id
                ORDER BY sa2.viewer_count DESC
                LIMIT 1
            ) AS peak_time
        FROM stream_analytics sa
        LEFT JOIN viewer_sessions vs ON vs.stream_id = sa.stream_id
        WHERE sa.stream_id = p_stream_id
    ),
    geo_stats AS (
        SELECT
            COUNT(DISTINCT country_code) AS countries_count,
            jsonb_agg(
                jsonb_build_object(
                    'country', country_code,
                    'viewers', COUNT(*)
                ) ORDER BY COUNT(*) DESC
            ) FILTER (WHERE country_code IS NOT NULL) AS top_countries
        FROM viewer_sessions
        WHERE stream_id = p_stream_id
        GROUP BY stream_id
    )
    SELECT * INTO v_record FROM stats, geo_stats;

    -- Upsert the summary
    INSERT INTO stream_stats_summary (
        stream_id,
        total_viewers,
        peak_concurrent_viewers,
        average_viewers,
        total_watch_time,
        average_watch_duration,
        total_chat_messages,
        total_unique_chatters,
        total_likes,
        total_shares,
        average_bitrate,
        average_framerate,
        first_viewer_joined_at,
        peak_time,
        countries_count,
        top_countries,
        updated_at
    ) VALUES (
        p_stream_id,
        COALESCE(v_record.total_viewers, 0),
        COALESCE(v_record.peak_viewers, 0),
        COALESCE(v_record.avg_viewers, 0),
        COALESCE(v_record.total_watch_time, 0),
        COALESCE(v_record.avg_watch_duration, 0),
        COALESCE(v_record.total_messages, 0),
        COALESCE(v_record.unique_chatters, 0),
        COALESCE(v_record.total_likes, 0),
        COALESCE(v_record.total_shares, 0),
        COALESCE(v_record.avg_bitrate, 0),
        COALESCE(v_record.avg_framerate, 0),
        v_record.first_viewer_at,
        v_record.peak_time,
        COALESCE(v_record.countries_count, 0),
        COALESCE(v_record.top_countries, '[]'::jsonb),
        NOW()
    )
    ON CONFLICT (stream_id) DO UPDATE SET
        total_viewers = EXCLUDED.total_viewers,
        peak_concurrent_viewers = EXCLUDED.peak_concurrent_viewers,
        average_viewers = EXCLUDED.average_viewers,
        total_watch_time = EXCLUDED.total_watch_time,
        average_watch_duration = EXCLUDED.average_watch_duration,
        total_chat_messages = EXCLUDED.total_chat_messages,
        total_unique_chatters = EXCLUDED.total_unique_chatters,
        total_likes = EXCLUDED.total_likes,
        total_shares = EXCLUDED.total_shares,
        average_bitrate = EXCLUDED.average_bitrate,
        average_framerate = EXCLUDED.average_framerate,
        first_viewer_joined_at = EXCLUDED.first_viewer_joined_at,
        peak_time = EXCLUDED.peak_time,
        countries_count = EXCLUDED.countries_count,
        top_countries = EXCLUDED.top_countries,
        updated_at = NOW();
END;
$$ LANGUAGE plpgsql;

-- Trigger to update timestamps
CREATE OR REPLACE FUNCTION update_analytics_timestamp()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = NOW();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER update_stream_analytics_timestamp
    BEFORE UPDATE ON stream_analytics
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_timestamp();

CREATE TRIGGER update_stream_stats_summary_timestamp
    BEFORE UPDATE ON stream_stats_summary
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_timestamp();

CREATE TRIGGER update_viewer_sessions_timestamp
    BEFORE UPDATE ON viewer_sessions
    FOR EACH ROW
    EXECUTE FUNCTION update_analytics_timestamp();

-- Add comments
COMMENT ON TABLE stream_analytics IS 'Time-series analytics data collected periodically during streams';
COMMENT ON TABLE stream_stats_summary IS 'Aggregated statistics summary for each stream';
COMMENT ON TABLE viewer_sessions IS 'Individual viewer session tracking for analytics';
COMMENT ON FUNCTION get_current_viewer_count IS 'Returns the current number of active viewers for a stream';
COMMENT ON FUNCTION get_stream_analytics_range IS 'Returns analytics data aggregated by time buckets';
COMMENT ON FUNCTION update_stream_stats_summary IS 'Recalculates and updates the summary statistics for a stream';