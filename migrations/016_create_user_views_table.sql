-- Create comprehensive user views table for efficient analytics and metrics
-- Designed for high-frequency inserts with optimal performance and rich analytics

-- Main views table for tracking individual view sessions
CREATE TABLE user_views (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    user_id UUID REFERENCES users(id) ON DELETE SET NULL, -- NULL for anonymous views
    
    -- Session and deduplication tracking
    session_id UUID NOT NULL, -- Generated client-side for session deduplication
    fingerprint_hash TEXT NOT NULL, -- Hash of IP + User-Agent for anonymous deduplication
    
    -- Engagement metrics
    watch_duration INTEGER NOT NULL DEFAULT 0, -- Seconds watched
    video_duration INTEGER NOT NULL DEFAULT 0, -- Total video duration at time of view
    completion_percentage DECIMAL(5,2) NOT NULL DEFAULT 0.0, -- 0.00-100.00
    is_completed BOOLEAN NOT NULL DEFAULT false, -- Watched >= 95%
    
    -- Interaction metrics
    seek_count INTEGER NOT NULL DEFAULT 0, -- Number of seeks/jumps
    pause_count INTEGER NOT NULL DEFAULT 0, -- Number of pauses
    replay_count INTEGER NOT NULL DEFAULT 0, -- Number of replays from start
    quality_changes INTEGER NOT NULL DEFAULT 0, -- Quality setting changes
    
    -- Technical metrics
    initial_load_time INTEGER, -- Milliseconds to first frame
    buffer_events INTEGER NOT NULL DEFAULT 0, -- Number of buffering events
    connection_type VARCHAR(20), -- 'wifi', 'cellular', 'ethernet', 'unknown'
    video_quality VARCHAR(10), -- '360p', '720p', '1080p', etc.
    
    -- Context and attribution
    referrer_url TEXT, -- Where the user came from (truncated for privacy)
    referrer_type VARCHAR(20), -- 'search', 'social', 'direct', 'internal', 'external'
    utm_source VARCHAR(50), -- Marketing attribution
    utm_medium VARCHAR(50),
    utm_campaign VARCHAR(100),
    
    -- Device and environment
    device_type VARCHAR(20), -- 'desktop', 'mobile', 'tablet', 'tv', 'unknown'
    os_name VARCHAR(50), -- 'Windows', 'macOS', 'iOS', 'Android', etc.
    browser_name VARCHAR(50), -- 'Chrome', 'Firefox', 'Safari', etc.
    screen_resolution VARCHAR(20), -- '1920x1080', '375x667', etc.
    is_mobile BOOLEAN NOT NULL DEFAULT false,
    
    -- Geographic data (privacy-compliant)
    country_code CHAR(2), -- ISO 3166-1 alpha-2
    region_code VARCHAR(10), -- State/province code
    city_name VARCHAR(100), -- City name (optional for privacy)
    timezone VARCHAR(50), -- IANA timezone identifier
    
    -- Privacy and consent
    is_anonymous BOOLEAN NOT NULL DEFAULT false, -- User opted for anonymous tracking
    tracking_consent BOOLEAN NOT NULL DEFAULT true, -- User consented to detailed tracking
    gdpr_consent BOOLEAN, -- Specific GDPR consent where applicable
    
    -- Temporal data for analytics
    view_date DATE NOT NULL DEFAULT CURRENT_DATE, -- Partitioning key
    view_hour INTEGER NOT NULL DEFAULT EXTRACT(HOUR FROM NOW()), -- 0-23 for hourly analytics
    weekday INTEGER NOT NULL DEFAULT EXTRACT(DOW FROM NOW()), -- 0-6 (Sunday-Saturday)
    
    -- Metadata
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(), -- For session updates
    
    -- Constraints for data integrity
    CONSTRAINT check_completion_percentage CHECK (completion_percentage >= 0.0 AND completion_percentage <= 100.0),
    CONSTRAINT check_watch_duration CHECK (watch_duration >= 0),
    CONSTRAINT check_positive_counts CHECK (
        seek_count >= 0 AND 
        pause_count >= 0 AND 
        replay_count >= 0 AND 
        quality_changes >= 0 AND
        buffer_events >= 0
    )
);

-- Create partitioning for high-performance queries (partition by month)
-- Note: This would be set up in production with actual partitioning
-- CREATE TABLE user_views_y2024m01 PARTITION OF user_views 
--     FOR VALUES FROM ('2024-01-01') TO ('2024-02-01');

-- High-performance indexes for common query patterns
CREATE INDEX idx_user_views_video_id ON user_views(video_id);
CREATE INDEX idx_user_views_user_id ON user_views(user_id) WHERE user_id IS NOT NULL;
CREATE INDEX idx_user_views_session_fingerprint ON user_views(session_id, fingerprint_hash);
CREATE INDEX idx_user_views_view_date ON user_views(view_date);
CREATE INDEX idx_user_views_created_at ON user_views(created_at);

-- Composite indexes for analytics queries
CREATE INDEX idx_user_views_video_date_completed ON user_views(video_id, view_date, is_completed);
CREATE INDEX idx_user_views_analytics ON user_views(video_id, view_date, completion_percentage, watch_duration);
CREATE INDEX idx_user_views_engagement ON user_views(video_id, is_completed, completion_percentage) 
    WHERE completion_percentage > 0;

-- Geographic analytics
CREATE INDEX idx_user_views_geo ON user_views(country_code, region_code) WHERE country_code IS NOT NULL;

-- Device analytics
CREATE INDEX idx_user_views_device ON user_views(device_type, os_name, browser_name) WHERE device_type IS NOT NULL;

-- Time-based analytics
CREATE INDEX idx_user_views_temporal ON user_views(view_date, view_hour, weekday);

-- Privacy-compliant index (excludes personal data)
CREATE INDEX idx_user_views_anonymous ON user_views(video_id, view_date, device_type, country_code) 
    WHERE is_anonymous = true;

-- Aggregated daily view statistics for faster queries
CREATE TABLE daily_video_stats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    video_id UUID NOT NULL REFERENCES videos(id) ON DELETE CASCADE,
    stat_date DATE NOT NULL,
    
    -- Core metrics
    total_views BIGINT NOT NULL DEFAULT 0,
    unique_views BIGINT NOT NULL DEFAULT 0, -- Based on session_id deduplication
    authenticated_views BIGINT NOT NULL DEFAULT 0, -- Views by logged-in users
    anonymous_views BIGINT NOT NULL DEFAULT 0,
    
    -- Engagement metrics
    total_watch_time BIGINT NOT NULL DEFAULT 0, -- Total seconds watched
    avg_watch_duration DECIMAL(10,2) NOT NULL DEFAULT 0.0,
    avg_completion_percentage DECIMAL(5,2) NOT NULL DEFAULT 0.0,
    completed_views BIGINT NOT NULL DEFAULT 0, -- Views with >= 95% completion
    
    -- Quality metrics
    avg_initial_load_time DECIMAL(10,2), -- Average load time in ms
    total_buffer_events BIGINT NOT NULL DEFAULT 0,
    avg_seek_count DECIMAL(5,2) NOT NULL DEFAULT 0.0,
    
    -- Device breakdown
    desktop_views BIGINT NOT NULL DEFAULT 0,
    mobile_views BIGINT NOT NULL DEFAULT 0,
    tablet_views BIGINT NOT NULL DEFAULT 0,
    tv_views BIGINT NOT NULL DEFAULT 0,
    
    -- Geographic breakdown (top countries/regions)
    top_countries JSONB NOT NULL DEFAULT '[]'::jsonb, -- [{"code": "US", "count": 123}, ...]
    top_regions JSONB NOT NULL DEFAULT '[]'::jsonb,
    
    -- Traffic sources
    referrer_breakdown JSONB NOT NULL DEFAULT '{}'::jsonb, -- {"search": 45, "social": 30, ...}
    
    -- Temporal metadata
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(video_id, stat_date)
);

-- Indexes for daily stats
CREATE INDEX idx_daily_video_stats_video_date ON daily_video_stats(video_id, stat_date);
CREATE INDEX idx_daily_video_stats_date ON daily_video_stats(stat_date);
CREATE INDEX idx_daily_video_stats_views ON daily_video_stats(total_views DESC);
CREATE INDEX idx_daily_video_stats_engagement ON daily_video_stats(avg_completion_percentage DESC);

-- User engagement summary for user analytics
CREATE TABLE user_engagement_stats (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    stat_date DATE NOT NULL,
    
    -- Viewing behavior
    videos_watched BIGINT NOT NULL DEFAULT 0,
    total_watch_time BIGINT NOT NULL DEFAULT 0, -- Seconds
    avg_session_duration DECIMAL(10,2) NOT NULL DEFAULT 0.0,
    unique_videos_watched BIGINT NOT NULL DEFAULT 0,
    
    -- Engagement patterns
    avg_completion_rate DECIMAL(5,2) NOT NULL DEFAULT 0.0,
    completed_videos BIGINT NOT NULL DEFAULT 0,
    sessions_count BIGINT NOT NULL DEFAULT 0,
    
    -- Behavioral metrics
    total_seeks BIGINT NOT NULL DEFAULT 0,
    total_pauses BIGINT NOT NULL DEFAULT 0,
    total_replays BIGINT NOT NULL DEFAULT 0,
    
    -- Device preferences
    preferred_device VARCHAR(20), -- Most used device type
    device_diversity INTEGER NOT NULL DEFAULT 0, -- Number of different device types used
    
    -- Content preferences
    top_categories JSONB NOT NULL DEFAULT '[]'::jsonb,
    avg_video_duration_preference DECIMAL(10,2), -- Average duration of watched videos
    
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    UNIQUE(user_id, stat_date)
);

-- Indexes for user engagement stats
CREATE INDEX idx_user_engagement_user_date ON user_engagement_stats(user_id, stat_date);
CREATE INDEX idx_user_engagement_date ON user_engagement_stats(stat_date);
CREATE INDEX idx_user_engagement_activity ON user_engagement_stats(videos_watched DESC, total_watch_time DESC);

-- Real-time trending data (for "trending now" features)
CREATE TABLE trending_videos (
    video_id UUID PRIMARY KEY REFERENCES videos(id) ON DELETE CASCADE,
    
    -- Trending metrics (updated frequently)
    views_last_hour BIGINT NOT NULL DEFAULT 0,
    views_last_24h BIGINT NOT NULL DEFAULT 0,
    views_last_7d BIGINT NOT NULL DEFAULT 0,
    
    -- Engagement velocity
    engagement_score DECIMAL(10,4) NOT NULL DEFAULT 0.0, -- Weighted score for trending
    velocity_score DECIMAL(10,4) NOT NULL DEFAULT 0.0, -- Rate of growth
    
    -- Rankings
    hourly_rank INTEGER,
    daily_rank INTEGER,
    weekly_rank INTEGER,
    
    -- Metadata
    last_updated TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    
    -- Performance optimization
    is_trending BOOLEAN NOT NULL DEFAULT false -- Cached trending status
);

-- Indexes for trending
CREATE INDEX idx_trending_videos_scores ON trending_videos(engagement_score DESC, velocity_score DESC);
CREATE INDEX idx_trending_videos_ranks ON trending_videos(hourly_rank, daily_rank, weekly_rank) 
    WHERE is_trending = true;
CREATE INDEX idx_trending_videos_updated ON trending_videos(last_updated);

-- Update triggers for timestamp management
CREATE TRIGGER update_user_views_updated_at 
    BEFORE UPDATE ON user_views 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_daily_video_stats_updated_at 
    BEFORE UPDATE ON daily_video_stats 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

CREATE TRIGGER update_user_engagement_stats_updated_at 
    BEFORE UPDATE ON user_engagement_stats 
    FOR EACH ROW 
    EXECUTE FUNCTION update_updated_at_column();

-- Function to efficiently increment video view count
CREATE OR REPLACE FUNCTION increment_video_views(p_video_id UUID)
RETURNS void AS $$
BEGIN
    UPDATE videos 
    SET views = views + 1, updated_at = NOW()
    WHERE id = p_video_id;
END;
$$ LANGUAGE plpgsql;

-- Function to get deduplicated view count for a video in a time period
CREATE OR REPLACE FUNCTION get_unique_views(
    p_video_id UUID,
    p_start_date TIMESTAMP WITH TIME ZONE DEFAULT NOW() - INTERVAL '30 days',
    p_end_date TIMESTAMP WITH TIME ZONE DEFAULT NOW()
)
RETURNS BIGINT AS $$
DECLARE
    unique_count BIGINT;
BEGIN
    SELECT COUNT(DISTINCT session_id)
    INTO unique_count
    FROM user_views 
    WHERE video_id = p_video_id 
    AND created_at BETWEEN p_start_date AND p_end_date;
    
    RETURN COALESCE(unique_count, 0);
END;
$$ LANGUAGE plpgsql;

-- Function to calculate engagement score for trending algorithm
CREATE OR REPLACE FUNCTION calculate_engagement_score(
    p_video_id UUID,
    p_hours_back INTEGER DEFAULT 24
)
RETURNS DECIMAL(10,4) AS $$
DECLARE
    score DECIMAL(10,4) := 0.0;
    view_count BIGINT;
    avg_completion DECIMAL(5,2);
    unique_viewers BIGINT;
    recency_weight DECIMAL(4,2);
BEGIN
    -- Get metrics for the specified time period
    SELECT 
        COUNT(*),
        AVG(completion_percentage),
        COUNT(DISTINCT session_id)
    INTO view_count, avg_completion, unique_viewers
    FROM user_views 
    WHERE video_id = p_video_id 
    AND created_at >= NOW() - (p_hours_back || ' hours')::INTERVAL;
    
    -- Calculate recency weight (more recent = higher weight)
    recency_weight := CASE 
        WHEN p_hours_back <= 1 THEN 2.0
        WHEN p_hours_back <= 6 THEN 1.5
        WHEN p_hours_back <= 24 THEN 1.2
        ELSE 1.0
    END;
    
    -- Weighted scoring algorithm
    score := (
        (COALESCE(view_count, 0) * 1.0) +                    -- Raw views
        (COALESCE(unique_viewers, 0) * 1.5) +                -- Unique viewers (weighted higher)
        (COALESCE(avg_completion, 0) / 100.0 * view_count * 2.0) -- Engagement quality
    ) * recency_weight;
    
    RETURN score;
END;
$$ LANGUAGE plpgsql;

-- Function to clean up old view data (for GDPR compliance and performance)
CREATE OR REPLACE FUNCTION cleanup_old_views(p_days_to_keep INTEGER DEFAULT 365)
RETURNS void AS $$
BEGIN
    -- Archive old detailed views to a separate table or delete
    DELETE FROM user_views 
    WHERE created_at < NOW() - (p_days_to_keep || ' days')::INTERVAL
    AND is_anonymous = true; -- Keep authenticated user data longer
    
    -- Clean up old daily stats (keep longer than raw views)
    DELETE FROM daily_video_stats 
    WHERE stat_date < CURRENT_DATE - (p_days_to_keep * 2 || ' days')::INTERVAL;
    
    DELETE FROM user_engagement_stats 
    WHERE stat_date < CURRENT_DATE - (p_days_to_keep * 2 || ' days')::INTERVAL;
END;
$$ LANGUAGE plpgsql;

-- Create a scheduled job function for aggregating daily stats
-- This would be called by a cron job or background worker
CREATE OR REPLACE FUNCTION aggregate_daily_stats(p_date DATE DEFAULT CURRENT_DATE - 1)
RETURNS void AS $$
BEGIN
    -- Aggregate video stats for the specified date
    INSERT INTO daily_video_stats (
        video_id, stat_date, total_views, unique_views, authenticated_views, anonymous_views,
        total_watch_time, avg_watch_duration, avg_completion_percentage, completed_views,
        avg_initial_load_time, total_buffer_events, avg_seek_count,
        desktop_views, mobile_views, tablet_views, tv_views,
        top_countries, referrer_breakdown
    )
    SELECT 
        video_id,
        p_date,
        COUNT(*) as total_views,
        COUNT(DISTINCT session_id) as unique_views,
        COUNT(*) FILTER (WHERE user_id IS NOT NULL) as authenticated_views,
        COUNT(*) FILTER (WHERE user_id IS NULL) as anonymous_views,
        SUM(watch_duration) as total_watch_time,
        AVG(watch_duration) as avg_watch_duration,
        AVG(completion_percentage) as avg_completion_percentage,
        COUNT(*) FILTER (WHERE is_completed = true) as completed_views,
        AVG(initial_load_time) as avg_initial_load_time,
        SUM(buffer_events) as total_buffer_events,
        AVG(seek_count) as avg_seek_count,
        COUNT(*) FILTER (WHERE device_type = 'desktop') as desktop_views,
        COUNT(*) FILTER (WHERE device_type = 'mobile') as mobile_views,
        COUNT(*) FILTER (WHERE device_type = 'tablet') as tablet_views,
        COUNT(*) FILTER (WHERE device_type = 'tv') as tv_views,
        jsonb_agg(DISTINCT jsonb_build_object('code', country_code, 'count', 1)) 
            FILTER (WHERE country_code IS NOT NULL) as top_countries,
        jsonb_object_agg(COALESCE(referrer_type, 'unknown'), COUNT(*)) as referrer_breakdown
    FROM user_views 
    WHERE view_date = p_date
    GROUP BY video_id
    ON CONFLICT (video_id, stat_date) 
    DO UPDATE SET
        total_views = EXCLUDED.total_views,
        unique_views = EXCLUDED.unique_views,
        authenticated_views = EXCLUDED.authenticated_views,
        anonymous_views = EXCLUDED.anonymous_views,
        total_watch_time = EXCLUDED.total_watch_time,
        avg_watch_duration = EXCLUDED.avg_watch_duration,
        avg_completion_percentage = EXCLUDED.avg_completion_percentage,
        completed_views = EXCLUDED.completed_views,
        avg_initial_load_time = EXCLUDED.avg_initial_load_time,
        total_buffer_events = EXCLUDED.total_buffer_events,
        avg_seek_count = EXCLUDED.avg_seek_count,
        desktop_views = EXCLUDED.desktop_views,
        mobile_views = EXCLUDED.mobile_views,
        tablet_views = EXCLUDED.tablet_views,
        tv_views = EXCLUDED.tv_views,
        top_countries = EXCLUDED.top_countries,
        referrer_breakdown = EXCLUDED.referrer_breakdown,
        updated_at = NOW();
END;
$$ LANGUAGE plpgsql;

-- Comments for maintenance and understanding
COMMENT ON TABLE user_views IS 'Detailed view tracking with rich analytics data for video engagement metrics';
COMMENT ON TABLE daily_video_stats IS 'Pre-aggregated daily statistics for fast analytics queries';
COMMENT ON TABLE user_engagement_stats IS 'User-level engagement metrics for personalization and user analytics';
COMMENT ON TABLE trending_videos IS 'Real-time trending calculation data for discovery features';

COMMENT ON COLUMN user_views.session_id IS 'Client-generated UUID for deduplicating views within the same session';
COMMENT ON COLUMN user_views.fingerprint_hash IS 'Privacy-compliant hash of IP+UA for anonymous user deduplication';
COMMENT ON COLUMN user_views.completion_percentage IS 'Percentage of video watched (0.00-100.00)';
COMMENT ON COLUMN user_views.is_anonymous IS 'User opted for anonymous tracking - limits data collection';
COMMENT ON COLUMN user_views.tracking_consent IS 'User consented to detailed behavioral tracking';